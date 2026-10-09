package addons_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/categories"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
)

func TestUrlFilterAllowShortCircuits(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://blocked.example.com/ok")
	policy := models.NewPolicy()
	policy.UrlFilter = models.UrlFilterConfig{
		Enabled: true,
		Allow:   []string{"blocked.example.com/ok"},
		Block:   []string{"*.example.com"},
	}
	fc.Policy = &policy

	addons.UrlFilter{}.HandleRequest(fc)

	if !fc.URLAllowed {
		t.Error("expected URLAllowed to short-circuit block list")
	}
	if fc.Response != nil {
		t.Error("did not expect a block response")
	}
}

func TestUrlFilterBlocksListedPattern(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://ads.example.com/track")
	policy := models.NewPolicy()
	policy.Name = "kids"
	policy.UrlFilter = models.UrlFilterConfig{Enabled: true, Block: []string{"*.example.com"}}
	fc.Policy = &policy

	addons.UrlFilter{}.HandleRequest(fc)

	if fc.Response == nil {
		t.Fatal("expected a block response")
	}
	if fc.WFComponent != "url_filter" || fc.WFAction != "blocked" {
		t.Errorf("WFAction/WFComponent = %q/%q", fc.WFAction, fc.WFComponent)
	}
}

func TestUrlFilterMitmPassthroughSkipsFiltering(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://ads.example.com/track")
	fc.MitmPassthrough = true
	policy := models.NewPolicy()
	policy.UrlFilter = models.UrlFilterConfig{Enabled: true, Block: []string{"*.example.com"}}
	fc.Policy = &policy

	addons.UrlFilter{}.HandleRequest(fc)

	if fc.Response != nil {
		t.Error("expected mitm_passthrough to skip url_filter entirely")
	}
}

func TestUrlFilterCategoryBlacklist(t *testing.T) {
	rt := newTestRuntime(t)
	catDir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(catDir, "ads"), 0o755))
	must(t, os.WriteFile(filepath.Join(catDir, "ads", "domains"), []byte("ads.net\n"), 0o644))
	rt.Categories = categories.NewStore(catDir)

	fc := newFlow(t, rt, "http://sub.ads.net/x")
	policy := models.NewPolicy()
	policy.UrlFilter = models.UrlFilterConfig{Enabled: true, Mode: "blacklist", Categories: []string{"ads"}}
	fc.Policy = &policy

	addons.UrlFilter{}.HandleRequest(fc)

	if fc.Response == nil {
		t.Fatal("expected category blacklist block")
	}
	if !strings.Contains(string(fc.ResponseBody), "ads") {
		t.Error("expected category name in block reason")
	}
}

func TestUrlFilterCategoryWhitelistBlocksUnlisted(t *testing.T) {
	rt := newTestRuntime(t)
	catDir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(catDir, "kids"), 0o755))
	must(t, os.WriteFile(filepath.Join(catDir, "kids", "domains"), []byte("kidsite.com\n"), 0o644))
	rt.Categories = categories.NewStore(catDir)

	fc := newFlow(t, rt, "http://random.com/x")
	policy := models.NewPolicy()
	policy.UrlFilter = models.UrlFilterConfig{Enabled: true, Mode: "whitelist", Categories: []string{"kids"}}
	fc.Policy = &policy

	addons.UrlFilter{}.HandleRequest(fc)

	if fc.Response == nil {
		t.Fatal("expected whitelist mode to block a site not in the allowed category")
	}
}

func TestUrlFilterCategoryWhitelistAllowsListed(t *testing.T) {
	rt := newTestRuntime(t)
	catDir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(catDir, "kids"), 0o755))
	must(t, os.WriteFile(filepath.Join(catDir, "kids", "domains"), []byte("kidsite.com\n"), 0o644))
	rt.Categories = categories.NewStore(catDir)

	fc := newFlow(t, rt, "http://kidsite.com/x")
	policy := models.NewPolicy()
	policy.UrlFilter = models.UrlFilterConfig{Enabled: true, Mode: "whitelist", Categories: []string{"kids"}}
	fc.Policy = &policy

	addons.UrlFilter{}.HandleRequest(fc)

	if fc.Response != nil {
		t.Error("did not expect a block for a site in the allowed category")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// categoryRuntime seeds a categories store with "adult" (which, as in the
// real IPFire lists, overlaps a bank-ish domain) and "banking".
func categoryRuntime(t *testing.T) *state.Runtime {
	t.Helper()
	rt := newTestRuntime(t)
	catDir := t.TempDir()
	for name, domains := range map[string]string{
		"adult":   "adult.example\nshared.example\n",
		"banking": "bank.example\nshared.example\n",
	} {
		must(t, os.MkdirAll(filepath.Join(catDir, name), 0o755))
		must(t, os.WriteFile(filepath.Join(catDir, name, "domains"), []byte(domains), 0o644))
	}
	rt.Categories = categories.NewStore(catDir)
	return rt
}

func TestUrlFilterPerCategoryActions(t *testing.T) {
	actions := map[string]models.CategoryAction{"adult": models.CategoryActionBlock, "banking": models.CategoryActionAllow}
	cases := []struct {
		mode        models.UrlFilterMode
		url         string
		wantBlocked bool
		wantAllowed bool
	}{
		{models.UrlFilterModeBlacklist, "http://adult.example/", true, false},
		{models.UrlFilterModeBlacklist, "http://bank.example/", false, true},
		{models.UrlFilterModeBlacklist, "http://shared.example/", false, true}, // allow wins
		{models.UrlFilterModeBlacklist, "http://other.example/", false, false},
		{models.UrlFilterModeWhitelist, "http://other.example/", true, false}, // unlisted sites blocked
		{models.UrlFilterModeWhitelist, "http://bank.example/", false, true},
		{models.UrlFilterModeWhitelist, "http://adult.example/", true, false},
	}
	for _, c := range cases {
		rt := categoryRuntime(t)
		fc := newFlow(t, rt, c.url)
		policy := models.NewPolicy()
		policy.UrlFilter = models.NewUrlFilterConfig()
		policy.UrlFilter.Enabled = true
		policy.UrlFilter.Mode = c.mode
		policy.UrlFilter.CategoryActions = actions
		fc.Policy = &policy

		addons.UrlFilter{}.HandleRequest(fc)

		if blocked := fc.Response != nil; blocked != c.wantBlocked {
			t.Errorf("%s %s: blocked=%v, want %v", c.mode, c.url, blocked, c.wantBlocked)
		}
		if fc.URLAllowed != c.wantAllowed {
			t.Errorf("%s %s: URLAllowed=%v, want %v", c.mode, c.url, fc.URLAllowed, c.wantAllowed)
		}
	}
}

func TestUrlFilterCustomRulesBeatCategories(t *testing.T) {
	rt := categoryRuntime(t)
	fc := newFlow(t, rt, "http://bank.example/")
	policy := models.NewPolicy()
	policy.UrlFilter = models.NewUrlFilterConfig()
	policy.UrlFilter.Enabled = true
	policy.UrlFilter.Block = []string{"bank.example"}
	policy.UrlFilter.CategoryActions = map[string]models.CategoryAction{"banking": models.CategoryActionAllow}
	fc.Policy = &policy
	addons.UrlFilter{}.HandleRequest(fc)
	if fc.Response == nil {
		t.Fatal("custom block should beat an allow-category")
	}
}

func TestHostGateHonoursCategoryActions(t *testing.T) {
	rt := categoryRuntime(t)
	policy := models.NewPolicy()
	policy.UrlFilter = models.NewUrlFilterConfig()
	policy.UrlFilter.Enabled = true
	policy.UrlFilter.Mode = models.UrlFilterModeWhitelist
	policy.UrlFilter.CategoryActions = map[string]models.CategoryAction{"banking": models.CategoryActionAllow}
	if v := proxy.HostFilterVerdict(rt, &policy, "bank.example"); v.Blocked {
		t.Errorf("allow-category host blocked at the host gate: %+v", v)
	}
	if v := proxy.HostFilterVerdict(rt, &policy, "other.example"); !v.Blocked {
		t.Error("unlisted host not blocked at the host gate in whitelist mode")
	}
}
