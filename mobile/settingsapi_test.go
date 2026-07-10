package mobile

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/config"
)

// All tests here run against a temp dataDir WITHOUT Start() - the native
// settings API must be fully disk-backed so the Kotlin settings screens
// work while the VPN is stopped. ensureMobileSettings roots every dir at
// absolute paths under dataDir, so the repo's relative-default gotcha does
// not apply.

func TestGetSettingsJsonBootstrapsAndRedacts(t *testing.T) {
	dataDir := t.TempDir()

	out, err := GetSettingsJson(dataDir)
	if err != nil {
		t.Fatalf("GetSettingsJson: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	for _, secret := range []string{"password_hash", "secret_key", "proxy_auth_password_hash"} {
		if _, ok := m[secret]; ok {
			t.Errorf("%s leaked into GetSettingsJson output", secret)
		}
	}
	if m["mgmt_host"] != "127.0.0.1" {
		t.Errorf("mobile bootstrap mgmt_host = %v, want 127.0.0.1", m["mgmt_host"])
	}
	if m["has_password"] != false {
		t.Errorf("has_password = %v, want false", m["has_password"])
	}
}

func TestUpdateSettingsJsonPartialMergeRoundTrip(t *testing.T) {
	dataDir := t.TempDir()

	out, err := UpdateSettingsJson(dataDir, `{"log_retention_days": 7}`)
	if err != nil {
		t.Fatalf("UpdateSettingsJson: %v", err)
	}
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	if m["log_retention_days"] != float64(7) {
		t.Errorf("log_retention_days = %v, want 7", m["log_retention_days"])
	}
	// Unmentioned mobile-override fields survive the partial merge.
	if m["mgmt_host"] != "127.0.0.1" {
		t.Errorf("mgmt_host clobbered: %v", m["mgmt_host"])
	}

	// And it persisted to disk.
	settings, err := config.LoadSettings(settingsPathFor(dataDir))
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.LogRetentionDays != 7 {
		t.Errorf("disk log_retention_days = %d, want 7", settings.LogRetentionDays)
	}
}

func TestUpdateSettingsJsonValidation(t *testing.T) {
	dataDir := t.TempDir()
	_, err := UpdateSettingsJson(dataDir, `{"auth_enabled": true}`)
	if err == nil || !strings.Contains(err.Error(), "Set a password") {
		t.Fatalf("expected auth-without-password validation error, got %v", err)
	}
}

func TestPolicyJsonRoundTrip(t *testing.T) {
	dataDir := t.TempDir()

	out, err := GetPolicyJson(dataDir, "default")
	if err != nil {
		t.Fatalf("GetPolicyJson: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("policy not JSON: %v", err)
	}
	if p["name"] != "default" {
		t.Fatalf("bootstrapped policy name = %v", p["name"])
	}

	// Full-document write (the only write shape - see UpdatePolicyJson doc).
	sub := p["text_classifier"].(map[string]any)
	sub["enabled"] = true
	sub["threshold"] = 0.9
	body, _ := json.Marshal(p)
	updated, err := UpdatePolicyJson(dataDir, "default", string(body))
	if err != nil {
		t.Fatalf("UpdatePolicyJson: %v", err)
	}
	var got map[string]any
	json.Unmarshal([]byte(updated), &got)
	tc := got["text_classifier"].(map[string]any)
	if tc["enabled"] != true || tc["threshold"] != float64(0.9) {
		t.Errorf("text_classifier not updated: %v", tc)
	}

	// Re-read from disk confirms persistence.
	again, err := GetPolicyJson(dataDir, "default")
	if err != nil {
		t.Fatalf("GetPolicyJson after update: %v", err)
	}
	if !strings.Contains(again, `"threshold": 0.9`) && !strings.Contains(again, `"threshold":0.9`) {
		t.Errorf("updated threshold not persisted: %s", again)
	}
}

func TestLockedBlocksNativeWritesButNotManagedApply(t *testing.T) {
	dataDir := t.TempDir()

	changed, err := ApplyManagedConfigJson(dataDir, `{"settings_locked":true,"policy":{"safesearch":{"enabled":true}}}`)
	if err != nil {
		t.Fatalf("ApplyManagedConfigJson: %v", err)
	}
	if !changed {
		t.Fatal("first apply should report changed")
	}

	if _, err := UpdateSettingsJson(dataDir, `{"log_blocks": false}`); err == nil {
		t.Error("UpdateSettingsJson must be rejected when locked")
	} else if !strings.Contains(err.Error(), "managed by your organization") {
		t.Errorf("unexpected lock error: %v", err)
	}
	if _, err := UpdatePolicyJson(dataDir, "default", `{"name":"default"}`); err == nil {
		t.Error("UpdatePolicyJson must be rejected when locked")
	}

	// Reads stay open.
	if _, err := GetSettingsJson(dataDir); err != nil {
		t.Errorf("GetSettingsJson under lock: %v", err)
	}
	if _, err := GetPolicyJson(dataDir, "default"); err != nil {
		t.Errorf("GetPolicyJson under lock: %v", err)
	}

	// The manager itself is never locked out.
	changed, err = ApplyManagedConfigJson(dataDir, `{"settings_locked":true,"policy":{"doh":{"enabled":true}}}`)
	if err != nil {
		t.Fatalf("second managed apply under lock: %v", err)
	}
	if !changed {
		t.Error("different doc should report changed")
	}

	var state map[string]bool
	json.Unmarshal([]byte(GetManagedStateJson(dataDir)), &state)
	if !state["managed"] || !state["settings_locked"] {
		t.Errorf("GetManagedStateJson = %v, want managed+locked", state)
	}
}

func TestManagedApplyIdempotentAndClearable(t *testing.T) {
	dataDir := t.TempDir()
	doc := `{"settings_locked":true,"settings":{"log_retention_days":3}}`

	if _, err := ApplyManagedConfigJson(dataDir, doc); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	before, _ := os.ReadFile(settingsPathFor(dataDir))

	changed, err := ApplyManagedConfigJson(dataDir, doc)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if changed {
		t.Error("identical re-apply must report changed=false")
	}
	after, _ := os.ReadFile(settingsPathFor(dataDir))
	if string(before) != string(after) {
		t.Error("settings.json rewritten by idempotent re-apply")
	}

	// Un-enroll.
	changed, err = ApplyManagedConfigJson(dataDir, "")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !changed {
		t.Error("clearing enrollment should report changed")
	}
	var state map[string]bool
	json.Unmarshal([]byte(GetManagedStateJson(dataDir)), &state)
	if state["managed"] || state["settings_locked"] {
		t.Errorf("state after clear = %v, want unmanaged", state)
	}
	// Native writes work again.
	if _, err := UpdateSettingsJson(dataDir, `{"log_blocks": false}`); err != nil {
		t.Errorf("UpdateSettingsJson after un-enroll: %v", err)
	}
}
