package uimodel

import (
	"fmt"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
)

// DefaultPolicyName is the catch-all policy every install has; it can be
// edited but never deleted or renamed (mirrors the mobile front-end's rule -
// the server itself does not enforce it).
const DefaultPolicyName = "default"

// CanDeletePolicy reports whether the GUI should allow deleting name.
func CanDeletePolicy(name string) bool { return name != DefaultPolicyName }

// CanRenamePolicy reports whether the GUI should allow renaming name.
func CanRenamePolicy(name string) bool { return name != DefaultPolicyName }

// PolicySourceSummary describes which clients a policy captures, for the
// policy list ("catch-all", "2 IPs", "1 IP, 1 MAC", ...).
func PolicySourceSummary(p models.Policy) string {
	var parts []string
	if n := len(p.SourceIPs); n == 1 {
		parts = append(parts, "1 IP")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("%d IPs", n))
	}
	if n := len(p.SourceMACs); n == 1 {
		parts = append(parts, "1 MAC")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("%d MACs", n))
	}
	if len(parts) == 0 {
		return "catch-all"
	}
	return strings.Join(parts, ", ")
}

// PolicyChips returns the short status markers for the policy list row.
func PolicyChips(p models.Policy) string {
	var chips []string
	if p.Inactive {
		chips = append(chips, "inactive")
	}
	if p.Schedule.Enabled && len(p.Schedule.ActiveWindows) > 0 {
		chips = append(chips, "scheduled")
	}
	return strings.Join(chips, " · ")
}

// ScheduleSummary renders the schedule read-only (window editing is a
// deliberate v1 cut - the Web UI has the full grid editor).
func ScheduleSummary(p models.Policy) string {
	if !p.Schedule.Enabled {
		return "Schedule disabled (policy always active)."
	}
	if len(p.Schedule.ActiveWindows) == 0 {
		return "Schedule enabled with no windows (always active)."
	}
	return fmt.Sprintf("Schedule enabled with %d active window(s). Edit windows in the Web UI.", len(p.Schedule.ActiveWindows))
}

// ClampThreshold keeps a classifier threshold slider value in [0, 1].
func ClampThreshold(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ValidatePolicyName mirrors what the policy store will accept as a file
// name; empty and path-ish names fail fast with a friendly message.
func ValidatePolicyName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("policy name cannot be empty")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("policy name cannot contain path separators")
	}
	return nil
}
