// Package gateway turns this host into a filtering router for other machines.
//
// Where internal/tun2socks captures the traffic of the machine it runs on,
// gateway mode captures traffic that other clients route *through* this
// machine: an nftables prerouting rule redirects their TCP connections into a
// transparent listener, which recovers the original destination with
// SO_ORIGINAL_DST (see proxy.OriginalDestination) and hands them to the same
// MITM pipeline everything else uses.
//
// The client's source address survives the redirect - only the destination is
// rewritten - so per-client policy tiers work here in full. That makes this the
// best fit for the existing policy model of any capture mode: one box, many
// clients, different rules per client, and nothing to configure on the clients.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/yjlion/gowebfilter/internal/models"
)

// StartupSkippedError means gateway mode could not start for an expected,
// user-fixable reason (wrong OS, no privileges, nft missing). Callers keep
// serving the proxy rather than failing the whole run - like TUN capture,
// routing other people's traffic is an add-on, not a prerequisite.
type StartupSkippedError struct{ Reason string }

func (e StartupSkippedError) Error() string { return e.Reason }

func IsStartupSkipped(err error) bool {
	var skipped StartupSkippedError
	return errors.As(err, &skipped)
}

// commandRunner is the seam that keeps the nft invocations testable without a
// firewall to wreck.
type commandRunner interface {
	Run(ctx context.Context, stdin string, name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, stdin, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		line := name + " " + strings.Join(args, " ")
		if msg == "" {
			return fmt.Errorf("%s: %w", line, err)
		}
		return fmt.Errorf("%s: %w: %s", line, err, msg)
	}
	return nil
}

// Manager owns the firewall and sysctl state for gateway mode, for the
// lifetime of a context.
type Manager struct {
	settings models.GlobalSettings
	// transparentAddr is the engine-owned transparent listener the redirect
	// points at, read back after binding so the rules can never name a port
	// nothing is serving.
	transparentAddr string
	run             commandRunner
	sysctl          *sysctlSaver

	mu        sync.Mutex
	active    bool
	port      int
	sysctls   []string
	lastError string
}

func NewManager(settings models.GlobalSettings, transparentAddr string) *Manager {
	return &Manager{
		settings:        settings,
		transparentAddr: transparentAddr,
		run:             execRunner{},
		sysctl:          newSysctlSaver(procSys),
	}
}

// NewManagerWithRunner is NewManager with the command runner and /proc/sys root
// injected, so tests can assert on the ruleset and the sysctl writes without
// touching the host.
func NewManagerWithRunner(settings models.GlobalSettings, transparentAddr string, runner commandRunner, root string) *Manager {
	if runner == nil {
		runner = execRunner{}
	}
	return &Manager{
		settings:        settings,
		transparentAddr: transparentAddr,
		run:             runner,
		sysctl:          newSysctlSaver(sysctlRoot(root)),
	}
}

// Start installs the ruleset and sysctls. A disabled config is a no-op.
func (m *Manager) Start(ctx context.Context) error {
	cfg := m.settings.Gateway
	if !cfg.Enabled {
		return nil
	}
	if runtime.GOOS != "linux" {
		return StartupSkippedError{Reason: fmt.Sprintf(
			"gateway mode needs netfilter and is Linux-only; this is %s", runtime.GOOS)}
	}
	if ok, detail := HasNetAdmin(); !ok {
		return StartupSkippedError{Reason: "gateway mode disabled for this run because " +
			"the privileges it needs are unavailable: " + detail}
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return StartupSkippedError{Reason: "gateway mode disabled for this run because the " +
			"nftables CLI (nft) is not installed; on Debian/Ubuntu: apt install nftables"}
	}
	if err := ValidateConfig(cfg, m.settings.MgmtPort); err != nil {
		return err
	}
	port, err := portOfAddr(m.transparentAddr)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	// Order matters: forwarding first, so that the moment the redirect exists
	// there is already a working path for everything it does not catch.
	if err := m.applySysctls(cfg); err != nil {
		m.Shutdown()
		return err
	}
	ruleset := BuildRuleset(cfg, port)
	if err := m.run.Run(ctx, ruleset, "nft", "-f", "-"); err != nil {
		m.Shutdown()
		return fmt.Errorf("gateway: install nftables ruleset: %w", err)
	}

	m.mu.Lock()
	m.active, m.port, m.sysctls, m.lastError = true, port, m.sysctl.changed(), ""
	m.mu.Unlock()

	slog.Info("gateway mode active", "transparent", m.transparentAddr,
		"intercept_ports", cfg.InterceptPorts, "interface", cfg.Interface,
		"drop_quic", cfg.DropQUIC)
	return nil
}

func (m *Manager) applySysctls(cfg models.GatewayConfig) error {
	if cfg.IPForward {
		if err := m.sysctl.set("net.ipv4.ip_forward", "1"); err != nil {
			return err
		}
	}
	// Without this the kernel helpfully tells each client "you can reach that
	// destination directly, stop sending it to me" - and the client obeys,
	// silently leaving the filter. It is the single most common reason a
	// transparent gateway appears to work and then stops.
	for _, iface := range []string{"all", cfg.Interface} {
		if iface == "" {
			continue
		}
		key := "net.ipv4.conf." + iface + ".send_redirects"
		if err := m.sysctl.set(key, "0"); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown removes the ruleset and restores every sysctl. Safe to call when
// nothing was installed, and safe to call twice.
//
// It uses its own context: shutdown runs after the caller's has been cancelled,
// and exec.CommandContext on a cancelled context would kill nft before it ran.
func (m *Manager) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Best-effort and deliberately unchecked: on the shutdown path a missing
	// table is the ordinary case, not a failure.
	_ = m.run.Run(ctx, "", "nft", "delete", "table", "ip", TableName)
	if err := m.sysctl.restore(); err != nil {
		slog.Warn("gateway: could not restore a sysctl", "err", err)
	}

	m.mu.Lock()
	m.active, m.port, m.sysctls = false, 0, nil
	m.mu.Unlock()
}

// Status reports what gateway mode is doing, for GET /api/status.
func (m *Manager) Status() Status {
	if m == nil {
		return Status{}
	}
	st := Inspect(m.settings)
	m.mu.Lock()
	defer m.mu.Unlock()
	st.Active = m.active
	st.TransparentAddr = m.transparentAddr
	st.TransparentPort = m.port
	st.SysctlsChanged = m.sysctls
	if m.lastError != "" {
		st.LastError = m.lastError
	}
	return st
}

// Cleanup removes the gateway ruleset without needing a Manager, for
// `webfilter gateway cleanup` and anything else that has to undo a previous
// run's state. Sysctls are not touched: their prior values are only known to
// the process that changed them, and both keys are safe to leave as they are
// (an operator can reset ip_forward themselves; guessing would be worse).
func Cleanup(ctx context.Context) {
	_ = execRunner{}.Run(ctx, "", "nft", "delete", "table", "ip", TableName)
}
