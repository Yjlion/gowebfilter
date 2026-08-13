package mgmtclient_test

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/mgmtclient"
	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/models"
)

// newTestServer stands up a real mgmtapi.Server in a temp dir (absolute
// cert_dir/policies_dir/logs_dir - the documented relative defaults resolve
// against the test working directory otherwise) and returns a client wired
// to an httptest server in front of its router.
func newTestServer(t *testing.T) (*mgmtapi.Server, *mgmtclient.Client, string) {
	t.Helper()
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "config", "settings.json")

	seed := map[string]any{
		"cert_dir":     filepath.Join(dir, "certs"),
		"policies_dir": filepath.Join(dir, "policies"),
		"logs_dir":     filepath.Join(dir, "logs"),
	}
	data, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed settings: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatalf("write seed settings: %v", err)
	}

	srv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { srv.Logs.Close() })
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	c, err := mgmtclient.New(ts.URL)
	if err != nil {
		t.Fatalf("mgmtclient.New: %v", err)
	}
	return srv, c, settingsPath
}

func TestStatusAndLogs(t *testing.T) {
	srv, c, _ := newTestServer(t)

	st, err := c.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.MgmtPort == 0 || len(st.ProxyListen) == 0 {
		t.Errorf("Status looks empty: %+v", st)
	}

	if err := srv.Logs.LogBlock(logstore.BlockEntry{
		TS: 1_700_000_000, ClientIP: "10.0.0.9", Domain: "blocked.example", Reason: "url_filter",
	}); err != nil {
		t.Fatalf("LogBlock: %v", err)
	}
	entries, err := c.Logs("blocks", 10)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Logs returned %d entries, want 1", len(entries))
	}
	if got := entries[0]["domain"]; got != "blocked.example" {
		t.Errorf("block entry domain = %v, want blocked.example", got)
	}

	for _, kind := range []string{"requests", "policy_changes"} {
		if _, err := c.Logs(kind, 10); err != nil {
			t.Errorf("Logs(%q): %v", kind, err)
		}
	}
}

// TestSettingsRoundTripPreservesUntouchedFields is the core safety contract
// of the settings screen: GET -> edit one field -> PUT full doc must not
// disturb anything else, including server-held secrets.
func TestSettingsRoundTripPreservesUntouchedFields(t *testing.T) {
	_, c, _ := newTestServer(t)

	// Establish a password (secret state the round trip must not clobber).
	orig, err := c.Settings()
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	orig.AuthEnabled = true
	if _, err := c.UpdateSettings(orig, "hunter2hunter2", ""); err != nil {
		t.Fatalf("UpdateSettings (set password): %v", err)
	}
	// Auth is live from this point; pick up a session cookie.
	if err := c.Login("hunter2hunter2"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	// GET -> change one unrelated field -> PUT the full document back.
	cur, err := c.Settings()
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !cur.AuthEnabled {
		t.Fatalf("auth_enabled did not persist")
	}
	cur.LogRetentionDays = 7
	updated, err := c.UpdateSettings(cur, "", "")
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if updated.LogRetentionDays != 7 {
		t.Errorf("log_retention_days = %d, want 7", updated.LogRetentionDays)
	}
	if !updated.AuthEnabled {
		t.Errorf("auth_enabled was clobbered by the round trip")
	}
	// auth_enabled surviving with no ValidationError proves password_hash
	// survived server-side too (MergeSettings rejects auth without a hash),
	// and the session cookie must remain valid after the settings write.
	if _, err := c.Settings(); err != nil {
		t.Fatalf("Settings after round trip: %v", err)
	}
}

// TestProxyAuthPasswordTravelsSeparately: the Advanced tab's proxy-auth
// password must reach the server as new_proxy_auth_password (which it
// hashes), never as a raw hash field, and enabling proxy auth with a
// password in the same PUT must validate server-side.
func TestProxyAuthPasswordTravelsSeparately(t *testing.T) {
	_, c, _ := newTestServer(t)

	cur, err := c.Settings()
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	cur.ProxyAuthEnabled = true
	cur.ProxyAuthUsername = "proxyuser"
	updated, err := c.UpdateSettings(cur, "", "swordfish-swordfish")
	if err != nil {
		t.Fatalf("UpdateSettings (proxy auth): %v", err)
	}
	if !updated.ProxyAuthEnabled || updated.ProxyAuthUsername != "proxyuser" {
		t.Errorf("proxy auth fields not persisted: enabled=%v username=%q",
			updated.ProxyAuthEnabled, updated.ProxyAuthUsername)
	}
	// The GET DTO must never leak the hash but must report a password is set.
	// (models.GlobalSettings has no has_proxy_auth field, so re-fetch proves
	// only that the enabled state persisted; enabling without a hash would
	// have failed validation above, which is the real contract.)
	refetched, err := c.Settings()
	if err != nil {
		t.Fatalf("Settings refetch: %v", err)
	}
	if refetched.ProxyAuthPasswordHash != "" {
		t.Errorf("proxy_auth_password_hash leaked through GET /api/settings")
	}

	// A later unrelated round trip must not clobber the stored hash: enabled
	// stays on, and the server would reject the PUT if the hash were lost.
	refetched.LogRetentionDays = 9
	if _, err := c.UpdateSettings(refetched, "", ""); err != nil {
		t.Fatalf("UpdateSettings (round trip after proxy auth): %v", err)
	}
}

// TestPolicyFullDocumentPreservesThreshold guards the partial-unmarshal
// footgun: a GET -> toggle one flag -> PUT round trip must keep a custom
// classifier threshold.
func TestPolicyFullDocumentPreservesThreshold(t *testing.T) {
	_, c, _ := newTestServer(t)

	p, err := c.Policy("default")
	if err != nil {
		t.Fatalf("Policy(default): %v", err)
	}
	p.TextClassifier.Enabled = true
	p.TextClassifier.Threshold = 0.93
	if _, err := c.UpdatePolicy("default", p); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}

	p2, err := c.Policy("default")
	if err != nil {
		t.Fatalf("Policy(default) refetch: %v", err)
	}
	p2.Inactive = !p2.Inactive
	saved, err := c.UpdatePolicy("default", p2)
	if err != nil {
		t.Fatalf("UpdatePolicy round trip: %v", err)
	}
	if saved.TextClassifier.Threshold != 0.93 {
		t.Errorf("threshold = %v after round trip, want 0.93", saved.TextClassifier.Threshold)
	}
}

func TestPolicyCRUD(t *testing.T) {
	_, c, _ := newTestServer(t)

	np := models.NewPolicy()
	np.Name = "kids"
	np.SourceIPs = []string{"192.168.1.50"}
	created, err := c.CreatePolicy(np)
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if created.Name != "kids" {
		t.Errorf("created name = %q", created.Name)
	}

	list, err := c.Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	if len(list) != 2 { // default + kids
		t.Fatalf("Policies returned %d, want 2", len(list))
	}

	// Duplicate create maps to a plain APIError with the server's detail.
	if _, err := c.CreatePolicy(np); err == nil {
		t.Fatalf("duplicate CreatePolicy succeeded, want conflict")
	} else {
		var apiErr *mgmtclient.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 409 {
			t.Errorf("duplicate create error = %v, want APIError 409", err)
		}
	}

	if err := c.DeletePolicy("kids"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if _, err := c.Policy("kids"); err == nil {
		t.Fatalf("Policy(kids) still exists after delete")
	}
}

func TestManagedLockMapsToErrManagedLocked(t *testing.T) {
	_, c, settingsPath := newTestServer(t)

	locked := []byte(`{"managed": true, "settings_locked": true}`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(settingsPath), "managed.json"), locked, 0o644); err != nil {
		t.Fatalf("seed managed.json: %v", err)
	}

	cur, err := c.Settings() // reads stay allowed
	if err != nil {
		t.Fatalf("Settings under lock: %v", err)
	}
	if _, err := c.UpdateSettings(cur, "", ""); !errors.Is(err, mgmtclient.ErrManagedLocked) {
		t.Errorf("UpdateSettings under lock = %v, want ErrManagedLocked", err)
	}
	p, err := c.Policy("default")
	if err != nil {
		t.Fatalf("Policy under lock: %v", err)
	}
	if _, err := c.UpdatePolicy("default", p); !errors.Is(err, mgmtclient.ErrManagedLocked) {
		t.Errorf("UpdatePolicy under lock = %v, want ErrManagedLocked", err)
	}
}

func TestAuthFlow(t *testing.T) {
	srv, c, _ := newTestServer(t)

	st, err := c.AuthStatus()
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if st.Enabled {
		t.Fatalf("auth enabled on fresh server")
	}

	cur, err := c.Settings()
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	cur.AuthEnabled = true
	if _, err := c.UpdateSettings(cur, "correcthorse", ""); err != nil {
		t.Fatalf("enable auth: %v", err)
	}

	// A fresh client with no cookie gets 401 mapped to ErrUnauthorized...
	fresh, err := mgmtclient.New(c.BaseURL())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fresh.Settings(); !errors.Is(err, mgmtclient.ErrUnauthorized) {
		t.Fatalf("unauthenticated Settings = %v, want ErrUnauthorized", err)
	}
	if err := fresh.Login("wrong"); !errors.Is(err, mgmtclient.ErrUnauthorized) {
		t.Fatalf("bad Login = %v, want ErrUnauthorized", err)
	}
	if err := fresh.Login("correcthorse"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := fresh.Settings(); err != nil {
		t.Fatalf("Settings after login: %v", err)
	}

	// ...and the self-host path authenticates via the pre-minted cookie.
	name, value := srv.SessionCookie()
	seeded, err := mgmtclient.New(c.BaseURL(), mgmtclient.WithSessionCookie(name, value))
	if err != nil {
		t.Fatalf("New with cookie: %v", err)
	}
	if _, err := seeded.Settings(); err != nil {
		t.Fatalf("Settings with seeded cookie: %v", err)
	}
}
