package app

import (
	"path/filepath"
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

func TestEnsureTunSocksListenerAddsSocks5WhenMissing(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "config", "settings.json")
	eng, rt, err := BuildProxyEngine(settingsPath)
	if err != nil {
		t.Fatalf("BuildProxyEngine() error = %v", err)
	}
	defer rt.Logs.Close()

	eng.Settings.Tun2Socks.Enabled = true
	eng.Settings.ProxyListen = []string{"0.0.0.0:8080"}
	EnsureTunSocksListener(eng)

	if got := eng.Settings.PrimarySocks5Port(); got != 1080 {
		t.Fatalf("PrimarySocks5Port() = %d after EnsureTunSocksListener, want 1080", got)
	}

	// Idempotent: a second call must not append a duplicate listener.
	n := len(eng.Settings.ProxyListen)
	EnsureTunSocksListener(eng)
	if len(eng.Settings.ProxyListen) != n {
		t.Fatalf("EnsureTunSocksListener appended a duplicate listener: %v", eng.Settings.ProxyListen)
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
