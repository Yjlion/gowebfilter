// Package categories implements shared site-category domain blocklists,
// mirroring shared/categories.py. Categories are domain lists shared
// across all policies, populated on disk by scripts/update_categories.sh
// (from the IPFire squidGuard list) under:
//
//	categories/index.json       [{name, count, updated}, ...] + metadata
//	categories/<name>/domains    one domain per line ('#' comments allowed)
//
// A policy references categories by name via url_filter.categories. Domain
// sets are loaded lazily and cached, with the file mtime re-checked at most
// once per checkInterval so an update via the script is picked up without a
// restart but without a stat() on every request.
package categories

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const checkInterval = 60 * time.Second

// Meta is one entry in index.json's "categories" array.
type Meta struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Updated string `json:"updated"`
}

type cacheEntry struct {
	mtime   time.Time
	domains map[string]struct{}
	checked time.Time
}

// Store is a lazily-loaded, mtime-cached reader over a categories
// directory. The zero value is not usable; construct with NewStore.
type Store struct {
	mu   sync.Mutex
	base string
	// cache is guarded by mu; entries persist across Configure/base changes
	// only implicitly (a base change simply means old cached names retain
	// stale data until re-requested, mirroring the Python singleton's own
	// behavior of never clearing _cache on configure()).
	cache map[string]*cacheEntry
}

// NewStore constructs a Store rooted at base (normally settings.CategoriesDir).
func NewStore(base string) *Store {
	return &Store{base: base, cache: make(map[string]*cacheEntry)}
}

// Configure repoints the store at a different categories directory -
// used when settings.json's categories_dir changes underneath a running
// process (mirrors categories.configure()).
func (s *Store) Configure(base string) {
	s.mu.Lock()
	s.base = base
	s.mu.Unlock()
}

func (s *Store) indexPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return filepath.Join(s.base, "index.json")
}

// List returns category metadata from the on-disk index (empty if not
// populated yet).
func (s *Store) List() []Meta {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		return []Meta{}
	}
	var parsed struct {
		Categories []Meta `json:"categories"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return []Meta{}
	}
	if parsed.Categories == nil {
		return []Meta{}
	}
	return parsed.Categories
}

// IndexMeta returns every top-level key of index.json except "categories"
// (e.g. a last-updated timestamp for the whole index).
func (s *Store) IndexMeta() map[string]any {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		return map[string]any{}
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return map[string]any{}
	}
	delete(raw, "categories")
	return raw
}

// domains returns the cached (or freshly loaded) domain set for name.
func (s *Store) domains(name string) map[string]struct{} {
	s.mu.Lock()
	base := s.base
	entry := s.cache[name]
	now := time.Now()
	if entry != nil && now.Sub(entry.checked) < checkInterval {
		s.mu.Unlock()
		return entry.domains
	}
	s.mu.Unlock()

	path := filepath.Join(base, name, "domains")
	info, err := os.Stat(path)
	if err != nil {
		s.mu.Lock()
		s.cache[name] = &cacheEntry{domains: map[string]struct{}{}, checked: now}
		s.mu.Unlock()
		return map[string]struct{}{}
	}
	mtime := info.ModTime()

	s.mu.Lock()
	entry = s.cache[name]
	if entry != nil && entry.mtime.Equal(mtime) {
		entry.checked = now
		domains := entry.domains
		s.mu.Unlock()
		return domains
	}
	s.mu.Unlock()

	loaded := loadDomains(path)
	s.mu.Lock()
	s.cache[name] = &cacheEntry{mtime: mtime, domains: loaded, checked: now}
	s.mu.Unlock()
	return loaded
}

func loadDomains(path string) map[string]struct{} {
	f, err := os.Open(path)
	if err != nil {
		return map[string]struct{}{}
	}
	defer f.Close()

	out := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if line != "" && !strings.HasPrefix(line, "#") {
			out[line] = struct{}{}
		}
	}
	return out
}

// hostInSet checks host and each of its parent domains (stopping before
// the bare TLD) against domains - a registrable-suffix match.
func hostInSet(host string, domains map[string]struct{}) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	labels := strings.Split(host, ".")
	for i := 0; i < len(labels)-1; i++ {
		if _, ok := domains[strings.Join(labels[i:], ".")]; ok {
			return true
		}
	}
	return false
}

// HostMatches reports whether host (or a parent domain) is listed under
// category name.
func (s *Store) HostMatches(host, name string) bool {
	return hostInSet(host, s.domains(name))
}

// MatchAny returns the first category in names whose set contains host (or
// a parent domain), or "" if none match.
func (s *Store) MatchAny(host string, names []string) string {
	for _, name := range names {
		if name == "" {
			continue
		}
		if s.HostMatches(host, name) {
			return name
		}
	}
	return ""
}
