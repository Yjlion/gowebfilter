package mobile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yjlion/gowebfilter/internal/config"
)

// TestFreshInstallSeedsFamilyDefaultPolicy pins the mobile-only first-run
// policy: protective modules on, classifiers off.
func TestFreshInstallSeedsFamilyDefaultPolicy(t *testing.T) {
	dataDir := t.TempDir()
	out, err := GetPolicyJson(dataDir, "default")
	if err != nil {
		t.Fatalf("GetPolicyJson: %v", err)
	}
	var p struct {
		SafeSearch struct{ Enabled bool } `json:"safesearch"`
		Adblock    struct {
			Enabled bool
			Lists   []string
		} `json:"adblock"`
		Doh struct {
			Enabled bool
			Server  string
		} `json:"doh"`
		UrlFilter struct {
			Enabled   bool
			BlockQuic bool `json:"block_quic"`
		} `json:"url_filter"`
		TextClassifier  struct{ Enabled bool } `json:"text_classifier"`
		ImageClassifier struct{ Enabled bool } `json:"image_classifier"`
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !p.SafeSearch.Enabled || !p.Adblock.Enabled || len(p.Adblock.Lists) == 0 || !p.Doh.Enabled || !p.UrlFilter.BlockQuic {
		t.Errorf("default policy not the family baseline: %s", out)
	}
	if p.Doh.Server != "https://1.1.1.3/dns-query" {
		t.Errorf("doh.server = %q", p.Doh.Server)
	}
	if p.TextClassifier.Enabled || p.ImageClassifier.Enabled || p.UrlFilter.Enabled {
		t.Errorf("classifiers/url_filter must stay off by default: %s", out)
	}
}

// TestExistingDefaultPolicyNotReseeded: the seed is first-provisioning only.
func TestExistingDefaultPolicyNotReseeded(t *testing.T) {
	dataDir := t.TempDir()
	settingsPath := filepath.Join(dataDir, "config", "settings.json")
	s := mobileDefaults(settingsPath)
	if err := config.SaveSettings(settingsPath, s); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.PoliciesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.PoliciesDir, "default.json"), []byte(`{"name":"default"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := GetPolicyJson(dataDir, "default")
	if err != nil {
		t.Fatalf("GetPolicyJson: %v", err)
	}
	var p struct {
		SafeSearch struct{ Enabled bool } `json:"safesearch"`
	}
	json.Unmarshal([]byte(out), &p)
	if p.SafeSearch.Enabled {
		t.Errorf("existing default.json was overwritten: %s", out)
	}
}

// TestWebUiToggleOnRunningEngine: the switch gates the mgmt UI at runtime
// while the PAC file stays served.
func TestWebUiToggleOnRunningEngine(t *testing.T) {
	defer SetWebUiEnabled(false)
	dataDir := t.TempDir()
	settingsPath := filepath.Join(dataDir, "config", "settings.json")
	mgmtPort := freePort(t)
	s := config.NewBootstrapSettings(settingsPath)
	s.MgmtHost = "127.0.0.1"
	s.MgmtPort = mgmtPort
	s.ProxyListen = []string{fmt.Sprintf("regular@127.0.0.1:%d", freePort(t))}
	s.Tun2Socks.Enabled = false
	if err := config.SaveSettings(settingsPath, s); err != nil {
		t.Fatal(err)
	}
	SetWebUiEnabled(false)
	if err := StartProxyOnly(dataDir); err != nil {
		t.Fatalf("StartProxyOnly: %v", err)
	}
	defer Stop()

	base := fmt.Sprintf("http://127.0.0.1:%d", mgmtPort)
	get := func(path string) int {
		deadline := time.Now().Add(5 * time.Second)
		for {
			resp, err := http.Get(base + path)
			if err == nil {
				resp.Body.Close()
				return resp.StatusCode
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s: %v", path, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if got := get("/proxy.pac"); got != http.StatusOK {
		t.Errorf("web UI off: /proxy.pac = %d, want 200", got)
	}
	if got := get("/api/settings"); got != http.StatusNotFound {
		t.Errorf("web UI off: /api/settings = %d, want 404", got)
	}
	var st struct {
		WebUI bool `json:"webUi"`
	}
	json.Unmarshal([]byte(Status()), &st)
	if st.WebUI {
		t.Error("Status().webUi = true while off")
	}

	if got := ActivePolicyName(); got != "default" {
		t.Errorf("ActivePolicyName() = %q, want default", got)
	}

	SetWebUiEnabled(true)
	if got := get("/api/settings"); got != http.StatusOK {
		t.Errorf("web UI on: /api/settings = %d, want 200", got)
	}
}
