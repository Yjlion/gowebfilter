package tun2socks

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yjlion/gowebfilter/internal/models"
)

// Supervisor runs the external tun2socks process for the lifetime of a context
// and reports what it is doing. See binary.go for why this is a child process
// rather than a linked-in library.
type Supervisor struct {
	settings models.GlobalSettings
	// socksAddr is the dedicated SOCKS5 listener the engine bound for TUN
	// capture (host:port). It is never user-configurable: tun2socks needs a
	// SOCKS5 endpoint that carries UDP, and pointing it anywhere else - an HTTP
	// listener, an unbound port, or the proxy's own upstream - only ever
	// produced silent breakage.
	socksAddr string
	run       commandRunner

	mu        sync.Mutex
	running   bool
	pid       int
	restarts  int
	startedAt time.Time
	lastError string
	// lastLogError is the child's own most recent error line, kept apart from
	// lastError so an exit message can quote it rather than replace it.
	lastLogError string
}

// StartupSkippedError means TUN capture could not start for an expected,
// user-fixable reason (wrong OS, no privileges, missing binary). Callers keep
// serving the proxy rather than failing the whole run.
type StartupSkippedError struct {
	Reason string
}

func (e StartupSkippedError) Error() string {
	return e.Reason
}

func IsStartupSkipped(err error) bool {
	var skipped StartupSkippedError
	return errors.As(err, &skipped)
}

// NewSupervisor returns a Supervisor for these settings, funnelling captured
// traffic into socksAddr.
func NewSupervisor(settings models.GlobalSettings, socksAddr string) *Supervisor {
	return &Supervisor{settings: settings, socksAddr: socksAddr, run: osCommandRunner{}}
}

// NewSupervisorWithRunner is NewSupervisor with an injectable runner for the
// platform route commands, so tests can assert on them without touching the
// host's routing table.
func NewSupervisorWithRunner(settings models.GlobalSettings, socksAddr string, runner commandRunner) *Supervisor {
	if runner == nil {
		runner = osCommandRunner{}
	}
	return &Supervisor{settings: settings, socksAddr: socksAddr, run: runner}
}

// restart backoff bounds. tun2socks exiting is usually terminal (no privileges,
// device name taken), so back off quickly to avoid a hot loop, but keep
// retrying so a transient failure - an interface flapping during suspend/resume
// - recovers without user action.
const (
	restartBackoffMin = 1 * time.Second
	restartBackoffMax = 30 * time.Second
)

// Start brings up TUN capture and returns once the process is running (or once
// it is clear it cannot). Supervision continues in the background until ctx is
// cancelled. A disabled config is a no-op.
func (s *Supervisor) Start(ctx context.Context) error {
	cfg := s.settings.Tun2Socks
	if !cfg.Enabled {
		return nil
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return StartupSkippedError{Reason: fmt.Sprintf("tun2socks is not supported on %s yet", runtime.GOOS)}
	}
	if ok, detail := hasRoutePrivileges(); !ok {
		return StartupSkippedError{Reason: fmt.Sprintf("tun2socks disabled for this run because the privileges it needs are unavailable: %s", detail)}
	}
	if err := checkPlatformPrerequisites(); err != nil {
		return StartupSkippedError{Reason: fmt.Sprintf("tun2socks disabled for this run because a platform prerequisite is missing: %v", err)}
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	if s.socksAddr == "" {
		return errors.New("tun2socks: no SOCKS5 listener address was provided for TUN capture")
	}
	bin, _, err := Resolve()
	if err != nil {
		return StartupSkippedError{Reason: "tun2socks disabled for this run because its binary is not installed: " +
			"use the Download button in Settings, or run `webfilter tun2socks download`"}
	}

	// Linux can create and address the TUN device up front; on Windows the
	// adapter only exists once tun2socks has started, so routes are configured
	// after launch (waitForWindowsInterface handles the race).
	if cfg.AutoRoutes && runtime.GOOS == "linux" {
		if err := configurePlatform(ctx, cfg, s.run); err != nil {
			return err
		}
	}

	started := make(chan error, 1)
	go s.supervise(ctx, bin, started)
	return <-started
}

// supervise runs the process, restarting it with capped backoff until ctx is
// cancelled. It reports only the *first* launch outcome on started; later
// restarts surface through Status instead of failing an already-running proxy.
func (s *Supervisor) supervise(ctx context.Context, bin string, started chan<- error) {
	cfg := s.settings.Tun2Socks
	backoff := restartBackoffMin

	for attempt := 0; ; attempt++ {
		cmd := exec.CommandContext(ctx, bin, s.args()...)
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()

		slog.Info("starting tun2socks", "bin", bin, "device", cfg.DeviceName, "proxy", s.proxyURL())
		err := cmd.Start()
		if attempt == 0 {
			if err != nil {
				s.setError(fmt.Sprintf("tun2socks failed to start: %v", err))
				started <- fmt.Errorf("start tun2socks: %w", err)
				return
			}
			started <- nil
		}

		if err == nil {
			s.markRunning(cmd.Process.Pid, attempt)

			// Both streams are drained, even though tun2socks writes
			// everything to stderr: an unread pipe eventually blocks the child.
			var logWG sync.WaitGroup
			logWG.Add(2)
			go func() { defer logWG.Done(); s.pipeLogs(stdout) }()
			go func() { defer logWG.Done(); s.pipeLogs(stderr) }()

			waitErr := cmd.Wait()
			logWG.Wait()
			s.markStopped()

			if ctx.Err() != nil {
				return // shutting down: an exit here is expected
			}
			slog.Warn("tun2socks exited, restarting", "err", waitErr, "backoff", backoff)
			// The child's own last error says *why* ("create tun: operation not
			// permitted"); the exit status alone doesn't. Keep both, child first.
			s.setExitError(waitErr)
			// A run that lasted a while is not a crash loop, so start over
			// from the short delay.
			if time.Since(s.startTime()) > restartBackoffMax {
				backoff = restartBackoffMin
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < restartBackoffMax {
			backoff *= 2
			if backoff > restartBackoffMax {
				backoff = restartBackoffMax
			}
		}
	}
}

// args builds the tun2socks command line. Flags are the upstream v2 CLI's.
func (s *Supervisor) args() []string {
	cfg := s.settings.Tun2Socks
	args := []string{
		"--device", deviceSpec(cfg),
		"--proxy", s.proxyURL(),
		"--loglevel", "info",
	}
	if cfg.InterfaceName != "" {
		// Binds tun2socks' own upstream sockets to this interface, which is
		// what stops its traffic from being routed back into the TUN it just
		// installed a default route for.
		args = append(args, "--interface", cfg.InterfaceName)
	}
	return args
}

// proxyURL is always the dedicated SOCKS5 listener; see Supervisor.socksAddr.
func (s *Supervisor) proxyURL() string {
	return "socks5://" + s.socksAddr
}

// deviceSpec names the TUN device for --device. Windows needs the driver
// prefix; on Linux the bare name is the device configurePlatform created.
func deviceSpec(cfg models.Tun2SocksConfig) string {
	if runtime.GOOS == "windows" {
		return "tun://" + cfg.DeviceName
	}
	return cfg.DeviceName
}

// pipeLogs forwards the child's output into slog, one record per line, so
// tun2socks diagnostics land in the same place as everything else instead of an
// inherited stderr. Error-level lines are also kept as LastError, so the UI can
// say why capture is unhealthy.
//
// The severity comes from the line itself, not from which stream it arrived on:
// tun2socks writes *everything* - including routine info - to stderr, so
// treating stderr as errors would flag a healthy run as broken.
func (s *Supervisor) pipeLogs(r io.Reader) {
	if r == nil {
		return
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		level, msg := parseTun2SocksLog(line)
		slog.Log(context.Background(), level, "tun2socks: "+msg)
		if level >= slog.LevelError {
			s.setError(msg)
		}
	}
}

// tun2socksLogLine is the zap-style JSON record tun2socks emits.
type tun2socksLogLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// parseTun2SocksLog maps one output line to a slog level and a message. Lines
// that aren't the expected JSON (an early panic, a wrapper script's output) are
// passed through verbatim at info rather than dropped.
func parseTun2SocksLog(line string) (slog.Level, string) {
	var rec tun2socksLogLine
	if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Level == "" {
		return slog.LevelInfo, line
	}
	msg := rec.Msg
	if msg == "" {
		msg = line
	}
	switch strings.ToLower(rec.Level) {
	case "debug":
		return slog.LevelDebug, msg
	case "warn", "warning":
		return slog.LevelWarn, msg
	case "error", "fatal", "panic", "dpanic":
		return slog.LevelError, msg
	default: // info and anything unrecognized
		return slog.LevelInfo, msg
	}
}

func (s *Supervisor) markRunning(pid, attempt int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = true
	s.pid = pid
	s.restarts = attempt
	s.startedAt = time.Now()
	if attempt == 0 {
		s.lastError = ""
		s.lastLogError = ""
	}
}

func (s *Supervisor) markStopped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.pid = 0
}

func (s *Supervisor) setError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = msg
	s.lastLogError = msg
}

// setExitError records an unexpected exit, prefixed with whatever the child
// last complained about. "exit status 1" on its own tells a user nothing;
// "create tun: operation not permitted" tells them exactly what to fix.
func (s *Supervisor) setExitError(waitErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := fmt.Sprintf("tun2socks exited unexpectedly (%v); restarting", waitErr)
	if s.lastLogError != "" {
		msg = fmt.Sprintf("%s (tun2socks exited: %v; restarting)", s.lastLogError, waitErr)
	}
	s.lastError = msg
}

func (s *Supervisor) startTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedAt
}

// Status reports the configuration and binary state (as Inspect does) with the
// live process state layered on top. Safe on a nil Supervisor, which is how the
// standalone `webfilter mgmt` process - which supervises nothing - reports.
func (s *Supervisor) Status() Status {
	if s == nil {
		return Status{}
	}
	st := Inspect(s.settings)
	st.SocksAddr = s.socksAddr

	s.mu.Lock()
	defer s.mu.Unlock()
	st.Running = s.running
	st.PID = s.pid
	st.Restarts = s.restarts
	if s.lastError != "" {
		st.LastError = s.lastError
	}
	return st
}

// Ref is a shareable slot for the live Supervisor. The proxy engine only
// creates one after its listeners are bound, which happens concurrently with
// the management server coming up, so the two are wired together through this
// rather than by passing the Supervisor itself.
//
// The zero Ref is usable and reports the settings/filesystem view, which is
// what standalone `webfilter mgmt` - a process that supervises nothing - wants.
type Ref struct {
	p atomic.Pointer[Supervisor]
}

// Set publishes the supervisor this process is running.
func (r *Ref) Set(s *Supervisor) { r.p.Store(s) }

// Status reports live process state when a supervisor is published, and the
// static settings/filesystem view otherwise.
func (r *Ref) Status(settings models.GlobalSettings) Status {
	if r != nil {
		if s := r.p.Load(); s != nil {
			return s.Status()
		}
	}
	return Inspect(settings)
}

// ValidateConfig rejects a tun2socks block that could not possibly work, so a
// bad setting is caught at PUT /api/settings rather than at next startup.
func ValidateConfig(cfg models.Tun2SocksConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.DeviceName) == "" {
		return errors.New("tun2socks device_name is required")
	}
	if net.ParseIP(cfg.TunAddress) == nil {
		return fmt.Errorf("tun2socks tun_address must be an IP address")
	}
	if net.ParseIP(cfg.TunGateway) == nil {
		return fmt.Errorf("tun2socks tun_gateway must be an IP address")
	}
	for _, dns := range cfg.DNSServers {
		if net.ParseIP(dns) == nil {
			return fmt.Errorf("tun2socks dns_servers contains invalid IP %q", dns)
		}
	}
	for _, cidr := range cfg.BypassCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("tun2socks bypass_cidrs contains invalid CIDR %q", cidr)
		}
	}
	return nil
}
