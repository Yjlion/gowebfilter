package mgmtapi_test

import (
	"net/http"
	"sync/atomic"
	"testing"
)

func TestWebUIGate(t *testing.T) {
	s, ts := newTestServer(t)
	var disabled atomic.Bool
	s.WebUIDisabled = disabled.Load

	status := func(path string) int {
		t.Helper()
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Enabled: the UI and API are served as usual.
	for _, p := range []string{"/", "/api/settings", "/proxy.pac"} {
		if got := status(p); got != http.StatusOK {
			t.Errorf("enabled: GET %s = %d, want 200", p, got)
		}
	}

	disabled.Store(true)
	for _, p := range []string{"/", "/index.html", "/login.html", "/api/settings", "/api/policies", "/api/logs"} {
		if got := status(p); got != http.StatusNotFound {
			t.Errorf("disabled: GET %s = %d, want 404", p, got)
		}
	}
	for _, p := range []string{"/proxy.pac", "/wpad.dat", "/api/ca-cert", "/health"} {
		if got := status(p); got != http.StatusOK {
			t.Errorf("disabled: GET %s = %d, want 200 (allowlisted)", p, got)
		}
	}

	// Flipping back takes effect immediately, no restart.
	disabled.Store(false)
	if got := status("/api/settings"); got != http.StatusOK {
		t.Errorf("re-enabled: GET /api/settings = %d, want 200", got)
	}
}
