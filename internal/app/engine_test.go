package app

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadTextScorerLoadsEmbeddedBayesModel(t *testing.T) {
	got := LoadTextScorer()
	if got != nil {
		score, ok := got.Score("adult video and xxx content")
		if !ok || score <= 0 {
			t.Fatalf("embedded text scorer Score() = (%.6f, %v), want positive ok score", score, ok)
		}
		return
	}
	t.Fatal("LoadTextScorer() = nil, want embedded Bayesian scorer")
}

func TestLoadImageDetectorAlwaysLoads(t *testing.T) {
	// The image classifier's model is embedded in the binary (no download,
	// no config path) - it should always load successfully.
	if got := LoadImageDetector(); got == nil {
		t.Fatal("LoadImageDetector() = nil, want a loaded detector (model is embedded)")
	}
}

func TestBuildProxyEngineBootstrapsAndWiresPipeline(t *testing.T) {
	// Absolute temp settings path: config.NewBootstrapSettings roots
	// cert/policies/categories/logs dirs from it (per the repo gotcha about
	// relative defaults resolving against the test CWD).
	settingsPath := filepath.Join(t.TempDir(), "config", "settings.json")

	eng, rt, err := BuildProxyEngine(settingsPath)
	if err != nil {
		t.Fatalf("BuildProxyEngine() error = %v", err)
	}
	defer rt.Logs.Close()

	if eng.Pipeline == nil {
		t.Fatal("engine has no pipeline")
	}
	if eng.Runtime != rt {
		t.Fatal("engine.Runtime not wired to the returned state.Runtime")
	}
	if eng.Transport == nil {
		t.Fatal("engine has no transport")
	}
}

func TestEnsureTunSocksListenerRegistersDedicatedListener(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "config", "settings.json")
	eng, rt, err := BuildProxyEngine(settingsPath)
	if err != nil {
		t.Fatalf("BuildProxyEngine() error = %v", err)
	}
	defer rt.Logs.Close()

	eng.Settings.Tun2Socks.Enabled = true
	eng.Settings.ProxyListen = []string{"0.0.0.0:8080"}
	EnsureTunSocksListener(eng)

	if len(eng.InternalListen) != 1 {
		t.Fatalf("InternalListen = %v, want exactly one entry", eng.InternalListen)
	}
	got := eng.InternalListen[0]
	if got.Purpose != TunSocksListenerPurpose {
		t.Errorf("purpose = %q, want %q", got.Purpose, TunSocksListenerPurpose)
	}
	// Port 0 lets the OS assign, so the dedicated listener can never collide
	// with a user-configured one.
	if got.Spec != "socks5@127.0.0.1:0" {
		t.Errorf("spec = %q, want socks5@127.0.0.1:0", got.Spec)
	}

	// The listener must stay out of proxy_listen: it is not user-editable and
	// must never be persisted to settings.json.
	if !slices.Equal(eng.Settings.ProxyListen, []string{"0.0.0.0:8080"}) {
		t.Errorf("proxy_listen = %v, want it untouched", eng.Settings.ProxyListen)
	}

	// Idempotent: a second call must not register a duplicate.
	EnsureTunSocksListener(eng)
	if len(eng.InternalListen) != 1 {
		t.Fatalf("EnsureTunSocksListener registered a duplicate: %v", eng.InternalListen)
	}
}

// TestEnsureTunSocksListenerSkippedWhenDisabled: the dedicated listener exists
// only to feed TUN capture, so it must not open a port when capture is off.
func TestEnsureTunSocksListenerSkippedWhenDisabled(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "config", "settings.json")
	eng, rt, err := BuildProxyEngine(settingsPath)
	if err != nil {
		t.Fatalf("BuildProxyEngine() error = %v", err)
	}
	defer rt.Logs.Close()

	eng.Settings.Tun2Socks.Enabled = false
	EnsureTunSocksListener(eng)

	if len(eng.InternalListen) != 0 {
		t.Errorf("InternalListen = %v, want none when tun2socks is disabled", eng.InternalListen)
	}
}

func TestEnsureLocalHTTPProxyListenerAddsRegularWhenMissing(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "config", "settings.json")
	eng, rt, err := BuildProxyEngine(settingsPath)
	if err != nil {
		t.Fatalf("BuildProxyEngine() error = %v", err)
	}
	defer rt.Logs.Close()

	// The Android default: socks5-only. A regular listener must be injected
	// on the same port PrimaryRegularProxyPort falls back to.
	eng.Settings.ProxyListen = []string{"socks5@127.0.0.1:1080"}
	EnsureLocalHTTPProxyListener(eng)
	if got := eng.Settings.PrimaryRegularProxyPort(); got != 8080 {
		t.Fatalf("PrimaryRegularProxyPort() = %d after EnsureLocalHTTPProxyListener, want 8080", got)
	}
	found := false
	for _, entry := range eng.Settings.ProxyListen {
		if entry == "regular@127.0.0.1:8080" {
			found = true
		}
	}
	if !found {
		t.Fatalf("regular@127.0.0.1:8080 not appended: %v", eng.Settings.ProxyListen)
	}

	// Idempotent.
	n := len(eng.Settings.ProxyListen)
	EnsureLocalHTTPProxyListener(eng)
	if len(eng.Settings.ProxyListen) != n {
		t.Fatalf("EnsureLocalHTTPProxyListener appended a duplicate: %v", eng.Settings.ProxyListen)
	}

	// A user-configured regular listener is respected, not shadowed.
	eng.Settings.ProxyListen = []string{"regular@127.0.0.1:9090"}
	EnsureLocalHTTPProxyListener(eng)
	if len(eng.Settings.ProxyListen) != 1 {
		t.Fatalf("EnsureLocalHTTPProxyListener must not add a listener when one is configured: %v", eng.Settings.ProxyListen)
	}
}
