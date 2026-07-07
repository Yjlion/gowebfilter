package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/config"
)

func TestMobileDefaultsApplyOverrides(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "config", "settings.json")
	s := mobileDefaults(settingsPath)

	if s.MgmtHost != "127.0.0.1" {
		t.Errorf("MgmtHost = %q, want 127.0.0.1 (loopback-only on device)", s.MgmtHost)
	}
	if !s.Tun2Socks.Enabled {
		t.Error("Tun2Socks.Enabled = false, want true for the VpnService TUN path")
	}
	if len(s.ProxyListen) != 1 || !strings.HasPrefix(s.ProxyListen[0], "socks5@127.0.0.1") {
		t.Errorf("ProxyListen = %v, want a single loopback socks5 listener", s.ProxyListen)
	}
	// Storage dirs must be absolute (rooted at the data dir), per the repo's
	// relative-default gotcha.
	if !filepath.IsAbs(s.CertDir) || !filepath.IsAbs(s.LogsDir) {
		t.Errorf("cert/logs dirs must be absolute, got CertDir=%q LogsDir=%q", s.CertDir, s.LogsDir)
	}
}

func TestEnsureMobileSettingsCreatesAndIsNonDestructive(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "config", "settings.json")

	if err := ensureMobileSettings(settingsPath); err != nil {
		t.Fatalf("ensureMobileSettings() error = %v", err)
	}
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
	// Default policy should be bootstrapped too.
	s, err := config.LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.PoliciesDir, "default.json")); err != nil {
		t.Fatalf("default policy not bootstrapped: %v", err)
	}

	// Simulate a user edit through the WebView mgmt UI, then re-run:
	// ensureMobileSettings must NOT clobber it.
	s.MgmtPort = 9999
	if err := config.SaveSettings(settingsPath, s); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	if err := ensureMobileSettings(settingsPath); err != nil {
		t.Fatalf("ensureMobileSettings() second call error = %v", err)
	}
	s2, err := config.LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	if s2.MgmtPort != 9999 {
		t.Errorf("MgmtPort = %d after re-run, want 9999 preserved (must not overwrite user edits)", s2.MgmtPort)
	}
}

func TestLifecycleGettersWhenStopped(t *testing.T) {
	if IsRunning() {
		t.Fatal("IsRunning() = true before any Start()")
	}
	if MgmtUrl() != "" {
		t.Errorf("MgmtUrl() = %q when stopped, want empty", MgmtUrl())
	}
	Stop() // must be a safe no-op

	var st struct {
		Running   bool     `json:"running"`
		Listeners []string `json:"listeners"`
	}
	if err := json.Unmarshal([]byte(Status()), &st); err != nil {
		t.Fatalf("Status() is not valid JSON: %v", err)
	}
	if st.Running {
		t.Error("Status().running = true when stopped")
	}
}

func TestStartRejectsBadArgs(t *testing.T) {
	if err := Start("", 3); err == nil {
		t.Error("Start(\"\", ...) = nil, want error for empty dataDir")
	}
	if err := Start(t.TempDir(), -1); err == nil {
		t.Error("Start(dir, -1) = nil, want error for negative fd")
	}
}

func TestCACertPEMProducesCertificate(t *testing.T) {
	pem := CaCertPem(t.TempDir())
	if !strings.Contains(pem, "BEGIN CERTIFICATE") {
		t.Fatalf("CaCertPem() did not return a PEM certificate, got %q", truncate(pem, 60))
	}
	if strings.Contains(pem, "PRIVATE KEY") {
		t.Fatal("CaCertPem() leaked a private key — must return the public cert only")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
