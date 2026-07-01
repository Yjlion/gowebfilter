// Package state holds the proxy engine's shared, hot-reloaded runtime
// state: the current settings.json snapshot, the current policies/*.json
// snapshot, and the CA/log-store/category-store instances every addon
// reads from. It replaces the several independent module-level globals
// each proxy/addons/*.py file keeps in the Python original with one
// consistently-updated object.
//
// Only policies/*.json hot-reloads (via an fsnotify watcher, mirroring
// policy_router.py's watchfiles-based loop): settings.json is loaded once
// at startup, matching the Python original, where a settings.json change
// only ever takes effect on the next proxy restart.
package state

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yjlion/gowebfilter/internal/categories"
	"github.com/yjlion/gowebfilter/internal/certs"
	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/neighbors"
)

// Runtime is the shared state passed to every addon.
type Runtime struct {
	SettingsPath string
	Settings     models.GlobalSettings

	CA         *certs.CA
	LeafIssuer *certs.LeafIssuer
	Logs       *logstore.Store
	Categories *categories.Store

	policyStore *config.PolicyStore
	policies    atomic.Pointer[[]models.Policy]
	mitmBypass  atomic.Pointer[[]string] // aggregated exclude-mode mitm domains, lowercased
}

// New loads settings.json once and wires up the CA, log store, category
// store, and an initial policies load.
func New(settingsPath string) (*Runtime, error) {
	s, err := config.LoadSettings(settingsPath)
	if err != nil {
		return nil, err
	}

	ca, err := certs.LoadOrCreateCA(s.CertDir)
	if err != nil {
		return nil, err
	}
	leafIssuer, err := certs.NewLeafIssuer(ca)
	if err != nil {
		return nil, err
	}
	logs, err := logstore.Configure(s.DBPath(), s.LogRetentionDays, s.LogRequests, s.LogBlocks)
	if err != nil {
		return nil, err
	}

	rt := &Runtime{
		SettingsPath: settingsPath,
		Settings:     s,
		CA:           ca,
		LeafIssuer:   leafIssuer,
		Logs:         logs,
		Categories:   categories.NewStore(s.CategoriesDir),
		policyStore:  config.NewPolicyStore(s.PoliciesDir),
	}
	rt.ReloadPolicies()
	return rt, nil
}

// Start begins watching policies_dir for changes, hot-reloading until ctx
// is cancelled.
func (rt *Runtime) Start(ctx context.Context) {
	go config.WatchDir(ctx, rt.policyStore.Dir, 300*time.Millisecond, rt.ReloadPolicies)
}

// ReloadPolicies re-reads every policies/*.json file immediately (also
// invoked automatically by the fsnotify watcher Start begins).
func (rt *Runtime) ReloadPolicies() {
	policies, err := rt.policyStore.List()
	if err != nil {
		slog.Warn("policy_router: failed to load policies", "err", err)
		return
	}
	rt.policies.Store(&policies)
	rt.rebuildMitmBypass(policies)
	slog.Info("policy_router: loaded policies", "count", len(policies), "dir", rt.policyStore.Dir)
}

// Policies returns the current policy snapshot, in file-sort order
// (matches the tie-breaking PolicyStore.List documents).
func (rt *Runtime) Policies() []models.Policy {
	p := rt.policies.Load()
	if p == nil {
		return nil
	}
	return *p
}

func (rt *Runtime) rebuildMitmBypass(policies []models.Policy) {
	seen := make(map[string]struct{})
	domains := make([]string, 0)
	for _, p := range policies {
		if p.Mitm.Mode != models.MitmModeExclude {
			continue
		}
		for _, site := range p.Mitm.Sites {
			d := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(site), "*."))
			if d == "" {
				continue
			}
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				domains = append(domains, d)
			}
		}
	}
	rt.mitmBypass.Store(&domains)
}

// ShouldBypassMitm reports whether host (or a parent domain) is in the
// aggregated MITM-exclude list from every loaded policy, mirroring
// policy_router.py's _sync_ignore_hosts/ctx.options.ignore_hosts. Unlike
// mitmproxy, this can't be scoped per source IP - a bypass here applies to
// every client, exactly matching the Python original's documented
// limitation ("Per-source-IP TLS bypass is architecturally impossible").
func (rt *Runtime) ShouldBypassMitm(host string) bool {
	p := rt.mitmBypass.Load()
	if p == nil {
		return false
	}
	host = strings.ToLower(host)
	for _, d := range *p {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// GetPolicy matches a client IP to a policy by specificity, most specific
// first: (0) MAC match, (1) exact single-IP match, (2) CIDR block match
// (narrowest/longest-prefix wins), (3) catch-all (empty source_ips).
// Within a tier, policies are considered in file-sort order (first wins).
// A policy is skipped if its schedule is not currently active. Ported from
// policy_router.py's get_policy.
func (rt *Runtime) GetPolicy(clientIP string) *models.Policy {
	if idx := strings.IndexByte(clientIP, '%'); idx != -1 {
		clientIP = clientIP[:idx]
	}
	addr := net.ParseIP(strings.TrimSpace(clientIP))
	if addr == nil {
		return nil
	}
	policies := rt.Policies()

	// Tier 0: MAC match (best-effort; only resolves for devices on the
	// proxy's own L2 segment). Only bother resolving if some policy
	// actually uses source_macs.
	hasMacPolicy := false
	for i := range policies {
		if len(policies[i].SourceMACs) > 0 {
			hasMacPolicy = true
			break
		}
	}
	if hasMacPolicy {
		if mac := neighbors.Lookup(clientIP); mac != "" {
			for i := range policies {
				p := &policies[i]
				if !p.Schedule.IsActiveNow() {
					continue
				}
				if containsString(p.SourceMACs, mac) {
					return p
				}
			}
		}
	}

	// Tier 1: exact single-IP match.
	for i := range policies {
		p := &policies[i]
		if !p.Schedule.IsActiveNow() {
			continue
		}
		for _, src := range p.SourceIPs {
			if strings.Contains(src, "/") {
				continue
			}
			target := net.ParseIP(strings.TrimSpace(src))
			if target == nil {
				continue
			}
			if addr.Equal(target) {
				return p
			}
		}
	}

	// Tier 2: CIDR block match - narrowest (longest-prefix) wins.
	var best *models.Policy
	bestPrefix := -1
	for i := range policies {
		p := &policies[i]
		if !p.Schedule.IsActiveNow() {
			continue
		}
		for _, src := range p.SourceIPs {
			if !strings.Contains(src, "/") {
				continue
			}
			_, ipnet, err := net.ParseCIDR(strings.TrimSpace(src))
			if err != nil {
				continue
			}
			if !ipnet.Contains(addr) {
				continue
			}
			if ones, _ := ipnet.Mask.Size(); ones > bestPrefix {
				best = p
				bestPrefix = ones
			}
		}
	}
	if best != nil {
		return best
	}

	// Tier 3: catch-all.
	for i := range policies {
		p := &policies[i]
		if len(p.SourceIPs) == 0 && p.Schedule.IsActiveNow() {
			return p
		}
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
