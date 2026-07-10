package mobile

// Policy management for the Android native UI's multi-policy manager. On
// device every flow arrives from 127.0.0.1, so all policies live in the
// catch-all matching tier: "default" is the schedule-less always-on
// fallback, and additional policies with active schedules outrank it
// during their windows (internal/proxy/state/policy_match.go). Same
// conventions as settingsapi.go: JSON strings in/out, disk-backed,
// MDM-lock-gated mutations, audit + hot-reload when the engine is running.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/models"
)

// ListPoliciesJson returns every policy as a JSON array — the same shape
// as GET /api/policies.
func ListPoliciesJson(dataDir string) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}
	policies, err := config.NewPolicyStore(settings.PoliciesDir).List()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(policies)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// CreatePolicyJson creates a new policy from the (possibly partial) body
// and returns the stored document. Unless the body says otherwise the
// policy is created inactive: an ACTIVE schedule-less policy would compete
// with "default" in the same catch-all tier and win or lose by filename
// sort — the caller should enable it once its schedule is configured.
func CreatePolicyJson(dataDir string, body string) (string, error) {
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
	if p.Name == "" {
		return "", fmt.Errorf("policy name must not be empty")
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(body), &raw)
	if _, explicit := raw["inactive"]; !explicit {
		p.Inactive = true
	}

	if err := config.NewPolicyStore(settings.PoliciesDir).Create(p); err != nil {
		return "", err
	}
	auditAndReload("created", p.Name, "")

	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DeletePolicy removes the named policy. "default" is the engine's
// always-on fallback (and the target of the MDM policy_json restriction),
// so deleting it is refused here, not just hidden in the UI.
func DeletePolicy(dataDir string, name string) error {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return err
	}
	if err := checkUnlocked(settingsPath); err != nil {
		return err
	}
	if name == "default" {
		return fmt.Errorf("the default policy cannot be deleted")
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return err
	}
	if err := config.NewPolicyStore(settings.PoliciesDir).Delete(name); err != nil {
		return err
	}
	auditAndReload("deleted", name, "")
	return nil
}

// auditAndReload records a policy change in the audit log and hot-reloads
// the runtime when the engine is up (same belt-and-braces as
// UpdatePolicyJson: Android's scoped storage can make inotify unreliable).
func auditAndReload(action, policyName, oldName string) {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if !ctl.running {
		return
	}
	if ctl.mgmtSrv != nil {
		_ = ctl.mgmtSrv.Logs.LogPolicyChange(logstore.PolicyChangeEntry{
			TS: time.Now().Unix(), Action: action, PolicyName: policyName, OldName: oldName, ClientIP: nativeClientID,
		})
	}
	if ctl.rt != nil {
		ctl.rt.ReloadPolicies()
	}
}
