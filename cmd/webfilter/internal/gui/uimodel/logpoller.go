package uimodel

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// LogPoller holds the log screen's rows between polls and answers the one
// question the redraw path cares about: did anything change? Reads and
// writes may come from the poll goroutine and the UI thread, so all state is
// mutex-guarded.
type LogPoller struct {
	mu     sync.Mutex
	kind   string
	limit  int
	paused bool
	rows   []LogRow
	sig    string
}

// LogRow is one normalized display row (the API returns loosely-typed maps
// whose columns differ per kind).
type LogRow struct {
	Time   string
	Client string
	Target string
	Action string
	Detail string
}

// NewLogPoller starts on the given kind ("blocks", "requests",
// "policy_changes") with the given tail limit.
func NewLogPoller(kind string, limit int) *LogPoller {
	return &LogPoller{kind: kind, limit: limit}
}

// Kind returns the current log kind.
func (p *LogPoller) Kind() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kind
}

// Limit returns the current tail size.
func (p *LogPoller) Limit() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.limit
}

// SetKind switches log kind and clears rows so the next poll repopulates.
func (p *LogPoller) SetKind(kind string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kind != kind {
		p.kind = kind
		p.rows = nil
		p.sig = ""
	}
}

// SetLimit changes the tail size and forces the next poll to refresh.
func (p *LogPoller) SetLimit(limit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.limit != limit {
		p.limit = limit
		p.sig = ""
	}
}

// SetPaused pauses/resumes polling (the ticker keeps running; Paused gates
// the fetch).
func (p *LogPoller) SetPaused(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = v
}

// Paused reports whether polling is paused.
func (p *LogPoller) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// Apply normalizes freshly fetched entries and stores them, returning true
// when the visible rows actually changed - the caller only invalidates the
// list widget and requests a GPU redraw on true, keeping idle polling quiet.
func (p *LogPoller) Apply(entries []map[string]any) bool {
	p.mu.Lock()
	kind := p.kind
	p.mu.Unlock()

	sig := signature(entries)
	rows := make([]LogRow, len(entries))
	for i, e := range entries {
		rows[i] = FormatLogRow(kind, e)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if sig == p.sig && len(rows) == len(p.rows) {
		return false
	}
	p.sig = sig
	p.rows = rows
	return true
}

// Rows returns the current rows (shared slice; treat as read-only).
func (p *LogPoller) Rows() []LogRow {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rows
}

// signature cheaply identifies a result set: length plus first/last entry
// contents. Tail results only ever change at the edges, so this is enough to
// detect both new rows and retention-pruned rows.
func signature(entries []map[string]any) string {
	if len(entries) == 0 {
		return "empty"
	}
	return fmt.Sprintf("%d|%v|%v", len(entries), entries[0], entries[len(entries)-1])
}

// FormatLogRow maps one API entry to display columns for its kind.
func FormatLogRow(kind string, e map[string]any) LogRow {
	row := LogRow{
		Time:   formatTS(e["ts"]),
		Client: str(e["client_ip"]),
	}
	switch kind {
	case "requests":
		row.Target = strings.TrimSpace(str(e["method"]) + " " + str(e["host"]) + str(e["path"]))
		row.Action = str(e["action"])
		row.Detail = str(e["component"])
	case "blocks":
		row.Target = str(e["domain"])
		if row.Target == "" {
			row.Target = str(e["url"])
		}
		row.Action = "blocked"
		row.Detail = strings.TrimSpace(str(e["reason"]) + " " + parenthesize(str(e["component"])))
	case "policy_changes":
		row.Target = str(e["policy_name"])
		row.Action = str(e["action"])
		if old := str(e["old_name"]); old != "" {
			row.Detail = "renamed from " + old
		}
	}
	return row
}

// MatchesFilter reports whether any display column contains the (case
// insensitive) filter string; an empty filter matches everything.
func (r LogRow) MatchesFilter(filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	for _, col := range []string{r.Time, r.Client, r.Target, r.Action, r.Detail} {
		if strings.Contains(strings.ToLower(col), filter) {
			return true
		}
	}
	return false
}

func parenthesize(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func formatTS(v any) string {
	var sec int64
	switch t := v.(type) {
	case float64:
		sec = int64(t)
	case int64:
		sec = t
	case int:
		sec = int64(t)
	default:
		return str(v)
	}
	if sec == 0 {
		return ""
	}
	return time.Unix(sec, 0).Format("Jan 02 15:04:05")
}
