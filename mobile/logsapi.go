package mobile

// Read-only logs/analytics accessors for the Android native UI. They read
// the SQLite log database directly through logstore.Reader (write-free —
// never Configure() a second Store here, that would open a competing
// writer and run schema/prune against the engine's own connection), so
// they work identically whether the engine is running or stopped. Clamping
// mirrors the mgmt API routes so both surfaces behave the same.

import (
	"encoding/json"
	"time"

	"github.com/yjlion/gowebfilter/internal/logstore"
)

// QueryLogsJson returns {"kind":..., "entries":[...]} — the same shape as
// GET /api/logs?kind=&limit=. kind defaults to "blocks"; valid kinds are
// requests, blocks, and policy_changes (anything else yields an empty
// entries list, matching the read path's fail-open convention). limit
// defaults to 500 and is capped at 5000.
func QueryLogsJson(dataDir string, kind string, limit int) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}

	if kind == "" {
		kind = "blocks"
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}

	entries := logstore.NewReader(settings.DBPath()).Tail(kind, limit)
	out := struct {
		Kind    string           `json:"kind"`
		Entries []map[string]any `json:"entries"`
	}{Kind: kind, Entries: entries}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// AnalyticsJson returns the logstore.Analytics aggregate as JSON — the
// same shape as GET /api/analytics?hours=. hours defaults to 24 and is
// clamped to [1, 720].
func AnalyticsJson(dataDir string, hours int) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}

	if hours <= 0 {
		hours = 24
	}
	if hours > 720 {
		hours = 720
	}
	cutoff := time.Now().Unix() - int64(hours)*3600

	analytics := logstore.NewReader(settings.DBPath()).Analytics(cutoff, hours)
	data, err := json.Marshal(analytics)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
