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
// policy_router.py's get_policy. Detailed matching logic lives in
// MatchPolicy so the management API can simulate the same decision.
func (rt *Runtime) GetPolicy(clientIP string) *models.Policy {
	policies := rt.Policies()
	match := MatchPolicy(policies, clientIP, time.Now(), neighbors.Lookup)
	if match.PolicyIndex < 0 {
		return nil
	}
	return &policies[match.PolicyIndex]
}
