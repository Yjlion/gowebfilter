package tun2socks

import (
	"context"
	"log/slog"
	"runtime"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

func enabledSettings() models.GlobalSettings {
	s := models.NewGlobalSettings()
	s.Tun2Socks = models.NewTun2SocksConfig()
	s.Tun2Socks.Enabled = true
	return s
}

// A disabled config must not probe privileges, look for a binary, or touch the
// routing table - `webfilter run` is the common case and TUN capture is off by
// default.
func TestStartIsNoOpWhenDisabled(t *testing.T) {
	s := models.NewGlobalSettings()
	s.Tun2Socks.Enabled = false

	sup := NewSupervisor(s, "127.0.0.1:1080")
	if err := sup.Start(context.Background()); err != nil {
		t.Fatalf("Start() with tun2socks disabled = %v, want nil", err)
	}
	if st := sup.Status(); st.Running {
		t.Error("Status().Running = true for a disabled config")
	}
}

// The skip errors are what keep an unprivileged or binary-less run from taking
// the whole proxy down with it, so the classification matters as much as the
// message.
func TestStartSkipsWithoutPrivileges(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("unsupported platform reports a different skip reason")
	}
	if ok, _ := hasRoutePrivileges(); ok {
		t.Skip("test must run unprivileged to exercise the privilege gate")
	}

	sup := NewSupervisor(enabledSettings(), "127.0.0.1:1080")
	err := sup.Start(context.Background())
	if err == nil {
		t.Fatal("Start() succeeded without privileges")
	}
	if !IsStartupSkipped(err) {
		t.Errorf("Start() error = %v, want a StartupSkippedError so the proxy keeps serving", err)
	}
}

// A missing SOCKS address means the engine never bound the dedicated listener.
// That is a wiring bug, not a user-fixable condition, so it must NOT be
// classified as skippable - it should surface loudly.
func TestStartRequiresSocksAddress(t *testing.T) {
	if ok, _ := hasRoutePrivileges(); !ok {
		t.Skip("the privilege gate runs first; needs a privileged process to reach this check")
	}
	sup := NewSupervisor(enabledSettings(), "")
	err := sup.Start(context.Background())
	if err == nil || IsStartupSkipped(err) {
		t.Errorf("Start() with no SOCKS address = %v, want a hard error", err)
	}
}

func TestValidateConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*models.Tun2SocksConfig)
		wantErr bool
	}{
		{"defaults are valid", func(*models.Tun2SocksConfig) {}, false},
		{"blank device name", func(c *models.Tun2SocksConfig) { c.DeviceName = "" }, true},
		{"bad tun address", func(c *models.Tun2SocksConfig) { c.TunAddress = "not-an-ip" }, true},
		{"bad gateway", func(c *models.Tun2SocksConfig) { c.TunGateway = "999.1.1.1" }, true},
		{"bad dns server", func(c *models.Tun2SocksConfig) { c.DNSServers = []string{"nope"} }, true},
		{"bad bypass cidr", func(c *models.Tun2SocksConfig) { c.BypassCIDRs = []string{"192.168.0.0"} }, true},
		{"valid dns and cidrs", func(c *models.Tun2SocksConfig) {
			c.DNSServers = []string{"1.1.1.3"}
			c.BypassCIDRs = []string{"10.0.0.0/8"}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := models.NewTun2SocksConfig()
			cfg.Enabled = true
			tc.mutate(&cfg)
			if err := ValidateConfig(cfg); (err != nil) != tc.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}

	// A disabled block is never validated, so a half-filled config a user is
	// still editing can be saved.
	cfg := models.NewTun2SocksConfig()
	cfg.TunAddress = "garbage"
	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("ValidateConfig() on a disabled config = %v, want nil", err)
	}
}

// args is the contract with the upstream CLI; a wrong flag name means capture
// silently fails at runtime on a machine we can't test here.
func TestArgsUsesDedicatedSocksListener(t *testing.T) {
	s := enabledSettings()
	s.Tun2Socks.DeviceName = "webfilter-tun"
	sup := NewSupervisor(s, "127.0.0.1:54321")

	args := sup.args()
	want := map[string]string{
		"--device":   deviceSpec(s.Tun2Socks),
		"--proxy":    "socks5://127.0.0.1:54321",
		"--loglevel": "info",
	}
	for flag, value := range want {
		if got := flagValue(args, flag); got != value {
			t.Errorf("%s = %q, want %q", flag, got, value)
		}
	}
	// Omitted rather than passed empty: tun2socks treats "" as a real value.
	if flagValue(args, "--interface") != "" {
		t.Errorf("--interface should be omitted when unset, got %v", args)
	}

	s.Tun2Socks.InterfaceName = "eth0"
	if got := flagValue(NewSupervisor(s, "127.0.0.1:1").args(), "--interface"); got != "eth0" {
		t.Errorf("--interface = %q, want eth0", got)
	}
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestParseTun2SocksLog: severity has to come from the line, not the stream.
// tun2socks writes *everything* - routine info included - to stderr, so a
// stream-based mapping would report a perfectly healthy run as broken.
func TestParseTun2SocksLog(t *testing.T) {
	// Verbatim from tun2socks v2.7.0 (zap JSON on stderr).
	const fatal = `{"level":"fatal","ts":1786651592.8,"caller":"engine/engine.go:48","msg":"[ENGINE] failed to start: create tun: operation not permitted"}`
	level, msg := parseTun2SocksLog(fatal)
	if level != slog.LevelError {
		t.Errorf("fatal line level = %v, want Error", level)
	}
	if msg != "[ENGINE] failed to start: create tun: operation not permitted" {
		t.Errorf("fatal line msg = %q, want the msg field alone", msg)
	}

	for _, tc := range []struct {
		raw   string
		level slog.Level
		msg   string
	}{
		{`{"level":"info","msg":"[STACK] tun://x <-> socks5://y"}`, slog.LevelInfo, "[STACK] tun://x <-> socks5://y"},
		{`{"level":"debug","msg":"d"}`, slog.LevelDebug, "d"},
		{`{"level":"warn","msg":"w"}`, slog.LevelWarn, "w"},
		{`{"level":"error","msg":"e"}`, slog.LevelError, "e"},
		// Not JSON (an early panic, a wrapper script): passed through, not dropped.
		{"panic: runtime error", slog.LevelInfo, "panic: runtime error"},
		{`{"no_level":true}`, slog.LevelInfo, `{"no_level":true}`},
	} {
		gotLevel, gotMsg := parseTun2SocksLog(tc.raw)
		if gotLevel != tc.level || gotMsg != tc.msg {
			t.Errorf("parseTun2SocksLog(%q) = (%v, %q), want (%v, %q)", tc.raw, gotLevel, gotMsg, tc.level, tc.msg)
		}
	}
}

// A nil Ref is the standalone-`webfilter mgmt` case: no supervisor, but status
// must still answer from settings rather than panic.
func TestRefStatusFallsBackToInspect(t *testing.T) {
	var ref *Ref
	st := ref.Status(enabledSettings())
	if !st.Configured {
		t.Error("nil Ref produced an unconfigured status")
	}
	if st.Running {
		t.Error("nil Ref reported a running process")
	}

	var real Ref
	if st := real.Status(enabledSettings()); st.Running {
		t.Error("empty Ref reported a running process")
	}
}
