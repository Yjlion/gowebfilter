package mobile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

func listPolicies(t *testing.T, dataDir string) []models.Policy {
	t.Helper()
	out, err := ListPoliciesJson(dataDir)
	if err != nil {
		t.Fatalf("ListPoliciesJson() error = %v", err)
	}
	var policies []models.Policy
	if err := json.Unmarshal([]byte(out), &policies); err != nil {
		t.Fatalf("policies list not valid JSON: %v", err)
	}
	return policies
}

func TestPoliciesCreateListDeleteRoundtrip(t *testing.T) {
	dataDir := t.TempDir()

	// Bootstrap gives exactly the default policy.
	policies := listPolicies(t, dataDir)
	if len(policies) != 1 || policies[0].Name != "default" {
		t.Fatalf("fresh dataDir policies = %+v, want just default", policies)
	}

	out, err := CreatePolicyJson(dataDir, `{"name":"bedtime","schedule":{"enabled":true,"active_windows":[{"days":[0,1,2,3,4],"start":"21:00","end":"23:59"}]}}`)
	if err != nil {
		t.Fatalf("CreatePolicyJson() error = %v", err)
	}
	var created models.Policy
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("created policy not valid JSON: %v", err)
	}
	if !created.Inactive {
		t.Error("created policy must default to inactive (an active schedule-less policy would shadow default)")
	}
	if !created.Schedule.Enabled || len(created.Schedule.ActiveWindows) != 1 {
		t.Errorf("schedule not preserved: %+v", created.Schedule)
	}

	// Explicit inactive:false is honored.
	if _, err := CreatePolicyJson(dataDir, `{"name":"weekend","inactive":false}`); err != nil {
		t.Fatalf("CreatePolicyJson(weekend) error = %v", err)
	}
	for _, p := range listPolicies(t, dataDir) {
		if p.Name == "weekend" && p.Inactive {
			t.Error("explicit inactive:false overridden")
		}
	}

	// Duplicate create fails.
	if _, err := CreatePolicyJson(dataDir, `{"name":"bedtime"}`); err == nil {
		t.Error("duplicate CreatePolicyJson must fail")
	}
	// Empty name fails.
	if _, err := CreatePolicyJson(dataDir, `{}`); err == nil {
		t.Error("CreatePolicyJson without a name must fail")
	}

	if err := DeletePolicy(dataDir, "bedtime"); err != nil {
		t.Fatalf("DeletePolicy() error = %v", err)
	}
	for _, p := range listPolicies(t, dataDir) {
		if p.Name == "bedtime" {
			t.Error("bedtime survived delete")
		}
	}
}

func TestDefaultPolicyIsProtected(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := ListPoliciesJson(dataDir); err != nil { // bootstrap
		t.Fatalf("bootstrap: %v", err)
	}

	if err := DeletePolicy(dataDir, "default"); err == nil {
		t.Error("DeletePolicy(default) must be refused")
	}
	if _, err := UpdatePolicyJson(dataDir, "default", `{"name":"renamed"}`); err == nil {
		t.Error("renaming default via UpdatePolicyJson must be refused")
	} else if !strings.Contains(err.Error(), "renamed") && !strings.Contains(err.Error(), "cannot be renamed") {
		t.Errorf("unexpected rename-guard error: %v", err)
	}

	// Same-name update still works.
	if _, err := UpdatePolicyJson(dataDir, "default", `{"name":"default","safesearch":{"enabled":true}}`); err != nil {
		t.Errorf("UpdatePolicyJson(default→default) error = %v", err)
	}
}

func TestPolicyMutationsAreLockGated(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := ApplyManagedConfigJson(dataDir, `{"settings_locked":true}`); err != nil {
		t.Fatalf("apply managed lock: %v", err)
	}

	if _, err := CreatePolicyJson(dataDir, `{"name":"x"}`); err == nil {
		t.Error("CreatePolicyJson must be rejected when locked")
	} else if !strings.Contains(err.Error(), "managed by your organization") {
		t.Errorf("unexpected lock error: %v", err)
	}
	if err := DeletePolicy(dataDir, "x"); err == nil {
		t.Error("DeletePolicy must be rejected when locked")
	}
	// Reads stay available.
	if _, err := ListPoliciesJson(dataDir); err != nil {
		t.Errorf("ListPoliciesJson under lock: %v", err)
	}
}
