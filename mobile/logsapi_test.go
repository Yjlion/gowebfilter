package mobile

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/logstore"
)

// seedLogs bootstraps a dataDir and writes one request + one block through
// a temporary Store, then closes it (the exports must work engine-stopped).
func seedLogs(t *testing.T, dataDir string) {
	t.Helper()
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	s, err := logstore.Configure(settings.DBPath(), 30, true, true)
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	defer s.Close()
	now := time.Now().Unix()
	if err := s.LogRequest(logstore.RequestEntry{TS: now, Method: "GET", Host: "blocked.example", Action: "blocked", Policy: "default"}); err != nil {
		t.Fatalf("LogRequest: %v", err)
	}
	if err := s.LogBlock(logstore.BlockEntry{TS: now, Domain: "blocked.example", Reason: "category: porn", Component: "url_filter"}); err != nil {
		t.Fatalf("LogBlock: %v", err)
	}
}

func TestQueryLogsJsonReadsSeededRows(t *testing.T) {
	dataDir := t.TempDir()
	seedLogs(t, dataDir)

	out, err := QueryLogsJson(dataDir, "", 0) // defaults: blocks, 500
	if err != nil {
		t.Fatalf("QueryLogsJson() error = %v", err)
	}
	var parsed struct {
		Kind    string           `json:"kind"`
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if parsed.Kind != "blocks" || len(parsed.Entries) != 1 {
		t.Fatalf("QueryLogsJson = %+v, want 1 blocks entry", parsed)
	}
	if parsed.Entries[0]["domain"] != "blocked.example" || parsed.Entries[0]["component"] != "url_filter" {
		t.Errorf("entry = %v", parsed.Entries[0])
	}

	out, err = QueryLogsJson(dataDir, "requests", 10)
	if err != nil {
		t.Fatalf("QueryLogsJson(requests) error = %v", err)
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(parsed.Entries) != 1 || parsed.Entries[0]["action"] != "blocked" {
		t.Errorf("requests entries = %v", parsed.Entries)
	}
}

func TestAnalyticsJsonAggregatesSeededRows(t *testing.T) {
	dataDir := t.TempDir()
	seedLogs(t, dataDir)

	out, err := AnalyticsJson(dataDir, 0) // default 24h
	if err != nil {
		t.Fatalf("AnalyticsJson() error = %v", err)
	}
	var a logstore.Analytics
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if a.WindowHours != 24 || a.TotalRequests != 1 || a.TotalBlocks != 1 {
		t.Errorf("analytics = %+v, want the seeded totals over 24h", a)
	}
	if len(a.TopBlockedDomains) != 1 || a.TopBlockedDomains[0].Domain != "blocked.example" {
		t.Errorf("top blocked domains = %v", a.TopBlockedDomains)
	}
}

func TestLogsExportsOnFreshDataDir(t *testing.T) {
	dataDir := t.TempDir()
	out, err := QueryLogsJson(dataDir, "blocks", 10)
	if err != nil {
		t.Fatalf("QueryLogsJson on fresh dataDir: %v", err)
	}
	if out == "" {
		t.Fatal("empty response")
	}
	if _, err := AnalyticsJson(dataDir, 24); err != nil {
		t.Fatalf("AnalyticsJson on fresh dataDir: %v", err)
	}
}
