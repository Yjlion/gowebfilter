package addons_test

import (
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
)

func enabledPolicyWithSafeSearch(engineOverrides map[string]models.SafeSearchEngineConfig) models.Policy {
	p := models.NewPolicy()
	cfg := models.NewSafeSearchConfig()
	cfg.Enabled = true
	for name, e := range engineOverrides {
		cfg.Engines[name] = e
	}
	p.SafeSearch = cfg
	return p
}

func TestSafeSearchGoogleParamInjection(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://www.google.com/search?q=cats")
	policy := enabledPolicyWithSafeSearch(nil)
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if got := fc.Request.URL.Query().Get("safe"); got != "active" {
		t.Errorf("safe param = %q, want active", got)
	}
	if fc.WFAction != "modified" || fc.WFComponent != "safesearch" {
		t.Errorf("WFAction/WFComponent = %q/%q", fc.WFAction, fc.WFComponent)
	}
}

func TestSafeSearchYouTubeHeaderInjection(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://www.youtube.com/watch?v=abc")
	policy := enabledPolicyWithSafeSearch(nil)
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if got := fc.Request.Header.Get("YouTube-Restrict"); got != "Strict" {
		t.Errorf("YouTube-Restrict header = %q, want Strict", got)
	}
}

func TestSafeSearchBlocksImageTab(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://www.google.com/search?tbm=isch&q=cats")
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"google": {Enabled: true, BlockImagesTab: true},
	})
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if fc.Response == nil {
		t.Fatal("expected image tab param to trigger a block")
	}
}

func TestSafeSearchBlocksGoogleAiModeTab(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://www.google.com/search?udm=50&q=cats")
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"google": {Enabled: true, BlockAiTab: true},
	})
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if fc.Response == nil {
		t.Fatal("expected Google AI Mode (udm=50) to trigger a block")
	}
}

func TestSafeSearchBlocksGoogleUdmImagesAndVideosTabs(t *testing.T) {
	rt := newTestRuntime(t)
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"google": {Enabled: true, BlockImagesTab: true, BlockVideosTab: true},
	})

	rt2 := newTestRuntime(t)
	fcImages := newFlow(t, rt, "http://www.google.com/search?udm=2&q=cats")
	fcImages.Policy = &policy
	addons.SafeSearch{}.HandleRequest(fcImages)
	if fcImages.Response == nil {
		t.Error("expected Google Images tab (udm=2) to trigger a block")
	}

	fcVideos := newFlow(t, rt2, "http://www.google.com/search?udm=7&q=cats")
	fcVideos.Policy = &policy
	addons.SafeSearch{}.HandleRequest(fcVideos)
	if fcVideos.Response == nil {
		t.Error("expected Google Videos tab (udm=7) to trigger a block")
	}
}

func TestSafeSearchBlocksImageCDNWholesale(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://encrypted-tbn0.gstatic.com/images?q=x")
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"google": {Enabled: true, BlockImagesTab: true},
	})
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if fc.Response == nil {
		t.Fatal("expected the google image CDN to be blocked wholesale")
	}
}

func TestSafeSearchBlocksImageCDNShardedHosts(t *testing.T) {
	rt := newTestRuntime(t)
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"google": {Enabled: true, BlockImagesTab: true},
	})

	for _, host := range []string{"encrypted-tbn1.gstatic.com", "encrypted-tbn2.gstatic.com", "encrypted-tbn3.gstatic.com"} {
		fc := newFlow(t, rt, "http://"+host+"/images?q=x")
		fc.Policy = &policy

		addons.SafeSearch{}.HandleRequest(fc)

		if fc.Response == nil {
			t.Errorf("expected %s to be blocked wholesale like encrypted-tbn0", host)
		}
	}
}

func TestSafeSearchEngineDisabledSkipsEnforcement(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://www.google.com/search?q=cats")
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"google": {Enabled: false},
	})
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if fc.Request.URL.Query().Get("safe") != "" {
		t.Error("did not expect safe param injection for a disabled engine")
	}
}

func TestSafeSearchDuckDuckGoBlockAiTabDoesNotBlockPlainSearch(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://duckduckgo.com/?q=cats")
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"duckduckgo": {Enabled: true, BlockAiTab: true},
	})
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if fc.Response != nil {
		t.Fatal("plain DuckDuckGo search must not be blocked by block_ai_tab")
	}
	if got := fc.Request.URL.Query().Get("kp"); got != "1" {
		t.Errorf("kp param = %q, want 1", got)
	}
}

func TestSafeSearchDuckDuckGoBlocksAiChatPath(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://duckduckgo.com/duckchat?q=hi")
	policy := enabledPolicyWithSafeSearch(map[string]models.SafeSearchEngineConfig{
		"duckduckgo": {Enabled: true, BlockAiTab: true},
	})
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if fc.Response == nil {
		t.Fatal("expected DuckDuckGo AI chat path to be blocked")
	}
}

func TestSafeSearchUnlistedEngineIsNoop(t *testing.T) {
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://unrelated.com/search?q=cats")
	policy := enabledPolicyWithSafeSearch(nil)
	fc.Policy = &policy

	addons.SafeSearch{}.HandleRequest(fc)

	if fc.Response != nil || fc.WFAction != "" {
		t.Error("expected no effect for a domain matching no known search engine")
	}
}
