package mobile

// Native-settings-UI accessors for the Android app. Every function here is
// disk-backed and self-bootstrapping, so the Kotlin settings screens work
// whether or not the engine is running; when it IS running, writes are
// routed through the live mgmt server / runtime so in-memory caches stay
// coherent with disk (otherwise the next WebView PUT would merge from a
// stale snapshot and silently revert a native edit).
//
// gomobile binds only simple types, so the surface is JSON strings in/out -
// same convention as Status(). Deliberately file NOT named *_android.go: a
// GOOS suffix would exclude it from `go test ./mobile` on a desktop host.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/settingsvc"
)

// nativeClientID is recorded as the client IP in the policy-change audit
// log for edits made through the native settings UI (there is no remote
// address - the call never crosses a socket).
const nativeClientID = "native-ui"

// errManagedLocked mirrors the mgmt API's 403 message so the Kotlin layer
// can show the same string either way.
var errManagedLocked = errors.New("Settings are managed by your organization and cannot be changed here.")

func settingsPathFor(dataDir string) string {
	return filepath.Join(dataDir, "config", "settings.json")
}

// checkUnlocked returns errManagedLocked when managed.json says the device
// configuration is locked by the EMM. Same fail-closed stance as the mgmt
// API's requireUnlocked middleware: an unreadable state file blocks writes.
func checkUnlocked(settingsPath string) error {
	st, err := config.LoadManagedState(settingsPath)
	if err != nil || (st.Managed && st.SettingsLocked) {
		return errManagedLocked
	}
	return nil
}

// GetSettingsJson returns the redacted settings DTO - identical shape to
// GET /api/settings (no password_hash/secret_key, has_* booleans added).
func GetSettingsJson(dataDir string) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(settingsvc.SettingsDTO(settings))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UpdateSettingsJson applies a partial-update body (PUT /api/settings
// semantics, including new_password) and returns the new redacted DTO.
// Settings still need an engine restart to take effect - same contract as
// the WebView path.
func UpdateSettingsJson(dataDir string, body string) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	if err := checkUnlocked(settingsPath); err != nil {
		return "", err
	}

	ctl.mu.Lock()
	defer ctl.mu.Unlock()

	cur, err := currentSettingsLocked(settingsPath)
	if err != nil {
		return "", err
	}
	merged, err := settingsvc.MergeSettings(cur, []byte(body))
	if err != nil {
		return "", err
	}
	if err := saveSettingsLocked(settingsPath, merged); err != nil {
		return "", err
	}
	data, err := json.Marshal(settingsvc.SettingsDTO(merged))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GetPolicyJson returns the named policy as JSON (same shape as
// GET /api/policies/{name}).
func GetPolicyJson(dataDir string, name string) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}
	p, err := config.NewPolicyStore(settings.PoliciesDir).Get(name)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UpdatePolicyJson replaces the named policy with the FULL document in body
// (same contract as PUT /api/policies/{name} - deliberately not a partial
// patch: every sub-config's UnmarshalJSON resets to defaults before
// overlaying, so a partial body would silently reset unmentioned sibling
// fields). Policy changes hot-reload; no restart needed.
func UpdatePolicyJson(dataDir string, name string, body string) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	if err := checkUnlocked(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}

	p := models.NewPolicy()
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return "", fmt.Errorf("invalid policy body: %w", err)
	}
	// "default" is the engine's always-on fallback and the fixed target of
	// the MDM policy_json restriction (settingsvc.ApplyManagedConfig) —
	// renaming it would silently orphan both.
	if name == "default" && p.Name != "default" {
		return "", fmt.Errorf("the default policy cannot be renamed")
	}
	if err := config.NewPolicyStore(settings.PoliciesDir).Update(name, p); err != nil {
		return "", err
	}

	ctl.mu.Lock()
	if ctl.running {
		if ctl.mgmtSrv != nil {
			oldName := ""
			if name != p.Name {
				oldName = name
			}
			_ = ctl.mgmtSrv.Logs.LogPolicyChange(logstore.PolicyChangeEntry{
				TS: time.Now().Unix(), Action: "updated", PolicyName: p.Name, OldName: oldName, ClientIP: nativeClientID,
			})
		}
		if ctl.rt != nil {
			// Belt-and-braces over the fsnotify watcher: Android's scoped
			// storage can make inotify unreliable (see ReloadPolicies).
			ctl.rt.ReloadPolicies()
		}
	}
	ctl.mu.Unlock()

	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GetManagedStateJson returns {"managed":bool,"settings_locked":bool} for
// the Kotlin UI's read-only gating. Errors degrade to locked (fail closed,
// matching checkUnlocked) so the UI never offers edits the engine would
// reject.
func GetManagedStateJson(dataDir string) string {
	st, err := config.LoadManagedState(settingsPathFor(dataDir))
	if err != nil {
		return `{"managed":true,"settings_locked":true}`
	}
	data, err := json.Marshal(map[string]bool{
		"managed":         st.Managed,
		"settings_locked": st.SettingsLocked,
	})
	if err != nil {
		return `{"managed":true,"settings_locked":true}`
	}
	return string(data)
}

// currentSettings returns the live in-memory settings when the engine is
// running (the mgmt server's cache is the intra-process source of truth),
// falling back to disk otherwise.
func currentSettings(settingsPath string) (models.GlobalSettings, error) {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	return currentSettingsLocked(settingsPath)
}

// currentSettingsLocked is currentSettings for callers already holding
// ctl.mu.
func currentSettingsLocked(settingsPath string) (models.GlobalSettings, error) {
	if ctl.running && ctl.mgmtSrv != nil && ctl.settings == settingsPath {
		return ctl.mgmtSrv.Settings(), nil
	}
	return config.LoadSettings(settingsPath)
}

// saveSettingsLocked persists settings through the running mgmt server when
// there is one (keeping its in-memory cache coherent), else straight to
// disk. Caller must hold ctl.mu.
func saveSettingsLocked(settingsPath string, s models.GlobalSettings) error {
	if ctl.running && ctl.mgmtSrv != nil && ctl.settings == settingsPath {
		return ctl.mgmtSrv.SaveSettings(s)
	}
	return config.SaveSettings(settingsPath, s)
}
