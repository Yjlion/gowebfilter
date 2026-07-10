package mobile

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjlion/gowebfilter/internal/config"
)

// freePort grabs an OS-assigned loopback port and releases it for the
// engine to re-bind. Small race window, fine for a test.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// TestStartProxyOnlyLifecycle exercises the TUN-less bring-up end to end:
// engine + mgmt server come up without a TUN fd, Status reports proxy mode
// with a PAC URL, the PAC advertises the bound regular listener, and
// Stop/Start cycles cleanly. Settings are pre-written with OS-assigned
// ports so CI runs can't collide on the 1080/8000/8080 defaults.
func TestStartProxyOnlyLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	settingsPath := filepath.Join(dataDir, "config", "settings.json")
	mgmtPort := freePort(t)
	proxyPort := freePort(t)

	s := config.NewBootstrapSettings(settingsPath)
	s.MgmtHost = "127.0.0.1"
	s.MgmtPort = mgmtPort
	s.ProxyListen = []string{fmt.Sprintf("regular@127.0.0.1:%d", proxyPort)}
	s.Tun2Socks.Enabled = false // keep EnsureTunSocksListener from binding the fixed 1080
	if err := config.SaveSettings(settingsPath, s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if err := StartProxyOnly(dataDir); err != nil {
		t.Fatalf("StartProxyOnly() error = %v", err)
	}
	defer Stop()

	if !IsRunning() {
		t.Fatal("IsRunning() = false after StartProxyOnly")
	}

	var st struct {
		Running   bool     `json:"running"`
		Mode      string   `json:"mode"`
		PacURL    string   `json:"pacUrl"`
		ProxyPort int      `json:"proxyPort"`
		Listeners []string `json:"listeners"`
	}
	if err := json.Unmarshal([]byte(Status()), &st); err != nil {
		t.Fatalf("Status() is not valid JSON: %v", err)
	}
	if st.Mode != "proxy" {
		t.Errorf("Status().mode = %q, want \"proxy\"", st.Mode)
	}
	wantPac := fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", mgmtPort)
	if st.PacURL != wantPac {
		t.Errorf("Status().pacUrl = %q, want %q", st.PacURL, wantPac)
	}
	if st.ProxyPort != proxyPort {
		t.Errorf("Status().proxyPort = %d, want %d", st.ProxyPort, proxyPort)
	}

	// The mgmt server starts asynchronously; poll the PAC endpoint briefly.
	var body string
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(wantPac)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body = string(b)
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("PAC endpoint never came up at %s (last err %v)", wantPac, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	wantDirective := fmt.Sprintf("PROXY 127.0.0.1:%d", proxyPort)
	if !strings.Contains(body, wantDirective) {
		t.Errorf("PAC missing %q:\n%s", wantDirective, body)
	}

	// Clean stop and a clean second cycle on the same ports (leaked
	// listeners or sqlite handles would make the re-Start fail).
	Stop()
	if IsRunning() {
		t.Fatal("IsRunning() = true after Stop")
	}
	if err := StartProxyOnly(dataDir); err != nil {
		t.Fatalf("second StartProxyOnly() error = %v", err)
	}
	Stop()
}

// TestStartProxyOnlyRejectsEmptyDataDir mirrors Start's arg validation.
func TestStartProxyOnlyRejectsEmptyDataDir(t *testing.T) {
	if err := StartProxyOnly(""); err == nil {
		t.Error("StartProxyOnly(\"\") = nil, want error")
	}
}
