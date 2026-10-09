package adblock

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yjlion/gowebfilter/internal/models"
)

// ListSource is a downloadable filter list.
type ListSource struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	License string `json:"license,omitempty"`
	Builtin bool   `json:"builtin"`
}

// Presets are the built-in lists offered in the UI. None is embedded in
// the binary: their licences (GPLv3 / CC-BY-SA) and their weekly churn
// both argue for downloading them at runtime.
var Presets = []ListSource{
	{Name: "easylist", Title: "EasyList (ads)", URL: "https://easylist.to/easylist/easylist.txt", License: "GPLv3 / CC-BY-SA 3.0"},
	{Name: "easyprivacy", Title: "EasyPrivacy (trackers)", URL: "https://easylist.to/easylist/easyprivacy.txt", License: "GPLv3 / CC-BY-SA 3.0"},
	{Name: "fanboy-annoyance", Title: "Fanboy's Annoyance (cookie banners, pop-ups)", URL: "https://secure.fanboy.co.nz/fanboy-annoyance.txt", License: "GPLv3 / CC-BY-SA 3.0"},
	{Name: "peter-lowe", Title: "Peter Lowe's ad and tracking server list", URL: "https://pgl.yoyo.org/adservers/serverlist.php?hostformat=adblockplus&showintro=1&mimetype=plaintext", License: "custom, free for non-commercial use"},
	{Name: "adguard-base", Title: "AdGuard Base", URL: "https://filters.adtidy.org/extension/ublock/filters/2.txt", License: "GPLv3"},
	{Name: "ublock-filters", Title: "uBlock Origin filters", URL: "https://ublockorigin.github.io/uAssets/filters/filters.txt", License: "GPLv3"},
	{Name: "ublock-privacy", Title: "uBlock Origin privacy", URL: "https://ublockorigin.github.io/uAssets/filters/privacy.txt", License: "GPLv3"},
}

// DefaultLists are the lists a policy uses when it enables adblock without
// naming any.
var DefaultLists = []string{"easylist", "easyprivacy"}

// ListMeta is the on-disk status of one list.
type ListMeta struct {
	ListSource
	Installed bool   `json:"installed"`
	Rules     int    `json:"rules"`
	Updated   string `json:"updated,omitempty"`
	Error     string `json:"error,omitempty"`
}

var validNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidName reports whether name is usable as a list name (it becomes a
// file name).
func ValidName(name string) bool { return validNameRe.MatchString(name) }

// maxListBytes caps one list download. The largest common lists are a few
// MB; this bounds a misbehaving server.
const maxListBytes = 32 << 20

// checkInterval bounds how often a cached engine re-stats its list files.
const checkInterval = 30 * time.Second

// Store holds downloaded lists under a directory and compiles engines for
// the list combinations policies ask for:
//
//	<dir>/index.json      {"lists": {name: {rules, updated, error, url}}}
//	<dir>/<name>.txt.gz   the list as downloaded, gzipped
//
// Engines are compiled in the background: Engine never blocks a request on
// a compile, it returns the previous engine (or nil) until the new one is
// ready.
type Store struct {
	mu      sync.Mutex
	dir     string
	custom  []ListSource
	maxAge  time.Duration
	engines map[string]*cachedEngine
	wanted  map[string]bool
	wake    chan struct{}

	// Client performs downloads. Set by the engine wiring to a client that
	// dials through the upstream egress path (see proxy/upstream.go) so a
	// capture-mode host never loops list downloads back into itself.
	Client *http.Client
}

type cachedEngine struct {
	engine    *Engine
	mtimes    map[string]time.Time
	checked   time.Time
	building  bool
	buildErrs int
}

// NewStore returns a Store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{
		dir:     dir,
		maxAge:  24 * time.Hour,
		engines: map[string]*cachedEngine{},
		wanted:  map[string]bool{},
		wake:    make(chan struct{}, 1),
	}
}

// NewStoreFromSettings builds a Store from the settings "adblock" block.
func NewStoreFromSettings(cfg models.AdblockSettings) *Store {
	s := NewStore(cfg.Dir)
	s.Apply(cfg)
	return s
}

// Apply re-configures the store from the settings "adblock" block.
func (s *Store) Apply(cfg models.AdblockSettings) {
	custom := make([]ListSource, 0, len(cfg.CustomLists))
	for _, c := range cfg.CustomLists {
		custom = append(custom, ListSource{Name: c.Name, Title: c.Name, URL: c.URL})
	}
	s.Configure(cfg.Dir, custom, time.Duration(cfg.UpdateHours)*time.Hour)
}

// Configure applies settings: the directory, custom list sources, and the
// refresh age. Changing the directory drops compiled engines.
func (s *Store) Configure(dir string, custom []ListSource, maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dir != s.dir {
		s.engines = map[string]*cachedEngine{}
	}
	s.dir = dir
	s.custom = nil
	for _, c := range custom {
		c.Builtin = false
		if ValidName(c.Name) && c.URL != "" && !isPreset(c.Name) {
			s.custom = append(s.custom, c)
		}
	}
	if maxAge > 0 {
		s.maxAge = maxAge
	}
}

func isPreset(name string) bool {
	for _, p := range Presets {
		if p.Name == name {
			return true
		}
	}
	return false
}

// Dir returns the current list directory.
func (s *Store) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// Catalog returns every known list source: presets, then custom lists.
func (s *Store) Catalog() []ListSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ListSource, 0, len(Presets)+len(s.custom))
	for _, p := range Presets {
		p.Builtin = true
		out = append(out, p)
	}
	return append(out, s.custom...)
}

func (s *Store) source(name string) (ListSource, bool) {
	for _, c := range s.Catalog() {
		if c.Name == name {
			return c, true
		}
	}
	return ListSource{}, false
}

type indexDoc struct {
	Lists map[string]indexEntry `json:"lists"`
}

type indexEntry struct {
	URL     string `json:"url"`
	Rules   int    `json:"rules"`
	Updated string `json:"updated,omitempty"`
	Error   string `json:"error,omitempty"`
}

var indexMu sync.Mutex

func readIndex(dir string) indexDoc {
	doc := indexDoc{Lists: map[string]indexEntry{}}
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	if doc.Lists == nil {
		doc.Lists = map[string]indexEntry{}
	}
	return doc
}

func updateIndex(dir, name string, fn func(*indexEntry)) error {
	indexMu.Lock()
	defer indexMu.Unlock()
	doc := readIndex(dir)
	e := doc.Lists[name]
	fn(&e)
	doc.Lists[name] = e
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".index.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "index.json"))
}

func (s *Store) listPath(name string) string {
	return filepath.Join(s.Dir(), name+".txt.gz")
}

// Status returns every catalog list with its on-disk state.
func (s *Store) Status() []ListMeta {
	idx := readIndex(s.Dir())
	var out []ListMeta
	for _, src := range s.Catalog() {
		m := ListMeta{ListSource: src}
		if e, ok := idx.Lists[src.Name]; ok {
			m.Rules, m.Updated, m.Error = e.Rules, e.Updated, e.Error
		}
		if _, err := os.Stat(s.listPath(src.Name)); err == nil {
			m.Installed = true
		}
		out = append(out, m)
	}
	return out
}

// Download fetches one catalog list and installs it atomically. A failed
// download keeps the previously installed copy and records the error.
func (s *Store) Download(ctx context.Context, name string) (ListMeta, error) {
	src, ok := s.source(name)
	if !ok {
		return ListMeta{}, fmt.Errorf("unknown adblock list %q", name)
	}
	dir := s.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ListMeta{}, err
	}
	rules, err := s.download(ctx, dir, src)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	_ = updateIndex(dir, name, func(e *indexEntry) {
		e.URL = src.URL
		if err != nil {
			e.Error = err.Error()
			return
		}
		e.Rules, e.Updated, e.Error = rules, now, ""
	})
	if err != nil {
		return ListMeta{}, err
	}
	s.invalidate()
	return ListMeta{ListSource: src, Installed: true, Rules: rules, Updated: now}, nil
}

func (s *Store) download(ctx context.Context, dir string, src ListSource) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "gowebfilter-adblock/1")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", src.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %s: HTTP %d", src.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", src.URL, err)
	}
	if len(body) > maxListBytes {
		return 0, fmt.Errorf("download %s: larger than %d bytes", src.URL, maxListBytes)
	}
	// Parse before installing: an HTML error page served with 200, or a
	// list we can't use at all, must not replace a good copy.
	if looksLikeHTML(body) {
		return 0, fmt.Errorf("download %s: got an HTML page, not a filter list", src.URL)
	}
	var p parsed
	rules, err := parseList(strings.NewReader(string(body)), &p)
	if err != nil {
		return 0, err
	}
	if rules == 0 {
		return 0, fmt.Errorf("download %s: no usable rules", src.URL)
	}

	tmp, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	zw := gzip.NewWriter(tmp)
	if _, err := zw.Write(body); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, src.Name+".txt.gz")); err != nil {
		return 0, err
	}
	return rules, nil
}

func looksLikeHTML(body []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 1024)])))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html") ||
		strings.Contains(head, "<head>") || strings.Contains(head, "<body")
}

// Delete removes an installed list.
func (s *Store) Delete(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid list name %q", name)
	}
	if err := os.Remove(s.listPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := s.Dir()
	indexMu.Lock()
	doc := readIndex(dir)
	delete(doc.Lists, name)
	data, _ := json.MarshalIndent(doc, "", "  ")
	indexMu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "index.json"), data, 0o644); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.invalidate()
	return nil
}

func (s *Store) invalidate() {
	s.mu.Lock()
	for _, c := range s.engines {
		c.checked = time.Time{}
	}
	s.mu.Unlock()
}

func engineKey(lists []string) (string, []string) {
	names := make([]string, 0, len(lists))
	seen := map[string]bool{}
	for _, l := range lists {
		l = strings.ToLower(strings.TrimSpace(l))
		if ValidName(l) && !seen[l] {
			seen[l] = true
			names = append(names, l)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ","), names
}

// Engine returns the compiled engine for a list combination, or nil when
// none is ready yet. It never blocks on a compile or a download: a missing
// or changed list schedules work in the background (Run performs the
// downloads) and the current engine keeps serving meanwhile.
func (s *Store) Engine(lists []string) *Engine {
	key, names := engineKey(lists)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	c := s.engines[key]
	if c == nil {
		c = &cachedEngine{}
		s.engines[key] = c
	}
	stale := time.Since(c.checked) > checkInterval
	if stale {
		c.checked = time.Now()
	}
	eng := c.engine
	s.mu.Unlock()
	if !stale {
		return eng
	}

	mtimes, missing := s.statLists(names)
	if len(missing) > 0 {
		s.want(missing)
	}
	s.mu.Lock()
	needBuild := !c.building && len(mtimes) > 0 && !sameMtimes(c.mtimes, mtimes)
	if needBuild {
		c.building = true
	}
	s.mu.Unlock()
	if needBuild {
		go s.build(key, names, mtimes)
	}
	return eng
}

// EngineSync is Engine for callers that can wait (tools, tests): it
// compiles in the calling goroutine if needed.
func (s *Store) EngineSync(lists []string) (*Engine, error) {
	key, names := engineKey(lists)
	if key == "" {
		return nil, nil
	}
	mtimes, _ := s.statLists(names)
	s.mu.Lock()
	c := s.engines[key]
	if c != nil && c.engine != nil && sameMtimes(c.mtimes, mtimes) {
		s.mu.Unlock()
		return c.engine, nil
	}
	s.mu.Unlock()
	eng, err := s.compile(names)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.engines[key] = &cachedEngine{engine: eng, mtimes: mtimes, checked: time.Now()}
	s.mu.Unlock()
	return eng, nil
}

func (s *Store) statLists(names []string) (map[string]time.Time, []string) {
	mtimes := map[string]time.Time{}
	var missing []string
	for _, n := range names {
		fi, err := os.Stat(s.listPath(n))
		if err != nil {
			missing = append(missing, n)
			continue
		}
		mtimes[n] = fi.ModTime()
	}
	return mtimes, missing
}

func sameMtimes(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !b[k].Equal(v) {
			return false
		}
	}
	return true
}

func (s *Store) build(key string, names []string, mtimes map[string]time.Time) {
	eng, err := s.compile(names)
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.engines[key]
	if c == nil {
		return
	}
	c.building = false
	if err != nil {
		slog.Warn("adblock: compile failed", "lists", key, "err", err)
		return
	}
	c.engine, c.mtimes = eng, mtimes
	slog.Info("adblock: engine ready", "lists", key, "network_rules", eng.NetworkRules,
		"cosmetic_rules", eng.CosmeticRules, "skipped", eng.Skipped)
}

func (s *Store) compile(names []string) (*Engine, error) {
	var p parsed
	for _, n := range names {
		f, err := os.Open(s.listPath(n))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		zr, err := gzip.NewReader(bufio.NewReader(f))
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		_, err = parseList(zr, &p)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
	}
	return build(&p), nil
}

// want records lists that something asked for but which are not installed,
// and wakes Run to fetch them.
func (s *Store) want(names []string) {
	s.mu.Lock()
	for _, n := range names {
		s.wanted[n] = true
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run keeps lists current until ctx ends: it downloads lists that Engine
// found missing, and refreshes installed lists older than the configured
// age that are still referenced (refs is polled each round for the names
// enabled policies use). Failures are logged and retried next round; a
// stale list keeps serving.
func (s *Store) Run(ctx context.Context, refs func() []string) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	failedAt := map[string]time.Time{}
	round := func() {
		names := map[string]bool{}
		if refs != nil {
			for _, n := range refs() {
				names[n] = true
			}
		}
		s.mu.Lock()
		for n := range s.wanted {
			names[n] = true
		}
		s.wanted = map[string]bool{}
		maxAge := s.maxAge
		s.mu.Unlock()

		idx := readIndex(s.Dir())
		for n := range names {
			if _, ok := s.source(n); !ok {
				continue
			}
			if t, ok := failedAt[n]; ok && time.Since(t) < 15*time.Minute {
				continue
			}
			fi, err := os.Stat(s.listPath(n))
			fresh := err == nil && time.Since(fi.ModTime()) < maxAge
			if e, ok := idx.Lists[n]; ok && err == nil && e.Updated != "" {
				if t, perr := time.Parse("2006-01-02T15:04:05Z", e.Updated); perr == nil {
					fresh = time.Since(t) < maxAge
				}
			}
			if fresh {
				continue
			}
			dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			meta, err := s.Download(dctx, n)
			cancel()
			if err != nil {
				failedAt[n] = time.Now()
				slog.Warn("adblock: list download failed", "list", n, "err", err)
				continue
			}
			delete(failedAt, n)
			slog.Info("adblock: list updated", "list", n, "rules", meta.Rules)
		}
	}
	round()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			round()
		case <-s.wake:
			round()
		}
	}
}
