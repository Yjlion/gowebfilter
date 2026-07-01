package addons

import (
	"net/url"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
)

// searchEngine mirrors safesearch.py's per-engine dict entries.
//
//   - safeParamKey/safeParamValue: injected into the request URL's query
//     string (empty key = no param-based enforcement, e.g. YouTube).
//   - safeHeaderKey/safeHeaderValue: injected as a request header (YouTube
//     Restricted Mode).
//   - imageCDNDomains: hostnames that serve image results wholesale for
//     this engine, blocked outright when block_images_tab is on.
type searchEngine struct {
	name            string
	domains         map[string]bool
	domainSuffix    string // e.g. ".google." (catches google.co.uk etc.)
	safeParamKey    string
	safeParamValue  string
	safeHeaderKey   string
	safeHeaderValue string
	pathPrefix      string
	imagesPaths     []string
	videosPaths     []string
	aiDomains       map[string]bool
	imagesParamKey  string
	imagesParamVal  string
	videosParamKey  string
	videosParamVal  string
	imageCDNDomains map[string]bool
}

func set(vals ...string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

var searchEngines = []searchEngine{
	{
		name:            "google",
		domains:         set("www.google.com", "google.com"),
		domainSuffix:    ".google.",
		safeParamKey:    "safe",
		safeParamValue:  "active",
		pathPrefix:      "/search",
		imagesPaths:     []string{"/imghp"},
		videosPaths:     []string{"/videohp"},
		aiDomains:       set("gemini.google.com", "bard.google.com"),
		imagesParamKey:  "tbm",
		imagesParamVal:  "isch",
		videosParamKey:  "tbm",
		videosParamVal:  "vid",
		imageCDNDomains: set("encrypted-tbn0.gstatic.com"),
	},
	{
		name:            "bing",
		domains:         set("www.bing.com", "bing.com"),
		safeParamKey:    "adlt",
		safeParamValue:  "strict",
		pathPrefix:      "/search",
		imagesPaths:     []string{"/images/"},
		videosPaths:     []string{"/videos/"},
		aiDomains:       set("copilot.microsoft.com"),
		imageCDNDomains: set("th.bing.com"),
	},
	{
		name:           "duckduckgo",
		domains:        set("duckduckgo.com", "www.duckduckgo.com", "ddg.gg"),
		safeParamKey:   "kp",
		safeParamValue: "1",
		pathPrefix:     "/",
		aiDomains:      set("duckduckgo.com"),
		imagesParamKey: "iar",
		imagesParamVal: "images",
		videosParamKey: "iar",
		videosParamVal: "videos",
	},
	{
		name:           "yahoo",
		domains:        set("search.yahoo.com"),
		domainSuffix:   ".yahoo.com",
		safeParamKey:   "vm",
		safeParamValue: "r",
		pathPrefix:     "/search",
		imagesPaths:    []string{"/images/search"},
		videosPaths:    []string{"/video/search"},
	},
	{
		name: "youtube",
		domains: set(
			"www.youtube.com", "youtube.com", "m.youtube.com",
			"music.youtube.com", "youtu.be",
		),
		domainSuffix:    ".youtube.com",
		safeHeaderKey:   "YouTube-Restrict",
		safeHeaderValue: "Strict",
		pathPrefix:      "/",
	},
}

func matchEngine(host string) *searchEngine {
	for i := range searchEngines {
		e := &searchEngines[i]
		if e.domains[host] {
			return e
		}
		if e.imageCDNDomains[host] {
			return e
		}
		if e.domainSuffix != "" && strings.Contains(host, e.domainSuffix) {
			return e
		}
		for aiDomain := range e.aiDomains {
			if host == aiDomain || strings.HasSuffix(host, "."+aiDomain) {
				return e
			}
		}
	}
	return nil
}

func safesearchShouldFilter(host string, cfg models.SafeSearchConfig) bool {
	if len(cfg.IncludeOnly) > 0 {
		return hostInList(host, cfg.IncludeOnly)
	}
	if len(cfg.Exclude) > 0 {
		return !hostInList(host, cfg.Exclude)
	}
	return true
}

func hostInList(host string, list []string) bool {
	for _, s := range list {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// SafeSearch injects per-engine safe-search parameters/headers and blocks
// images/videos/AI search tabs, ported from proxy/addons/safesearch.py.
type SafeSearch struct{}

func (SafeSearch) Name() string { return "safesearch" }

func (SafeSearch) HandleRequest(fc *proxy.FlowContext) {
	if fc.URLAllowed || fc.MitmPassthrough {
		return
	}
	policy := fc.Policy
	if policy == nil || !policy.SafeSearch.Enabled {
		return
	}

	host := fc.Request.URL.Hostname()
	cfg := policy.SafeSearch
	if !safesearchShouldFilter(host, cfg) {
		return
	}

	engine := matchEngine(host)
	if engine == nil {
		return
	}

	path := fc.Request.URL.Path

	engCfg, hasEngCfg := cfg.Engines[engine.name]
	if hasEngCfg && !engCfg.Enabled {
		return
	}
	blockImages := hasEngCfg && engCfg.BlockImagesTab
	blockVideos := hasEngCfg && engCfg.BlockVideosTab
	blockAI := hasEngCfg && engCfg.BlockAiTab

	// Image CDN domains: block wholesale when image-tab blocking is active
	// for the parent engine (every path on these hosts serves image content).
	if engine.imageCDNDomains[host] {
		if blockImages {
			fc.Block("Image search blocked by policy", "safesearch")
		}
		return
	}

	// Block AI search engines/tabs.
	if blockAI {
		for aiDomain := range engine.aiDomains {
			if host == aiDomain || strings.HasSuffix(host, "."+aiDomain) {
				fc.Block("AI search blocked by policy", "safesearch")
				return
			}
		}
	}

	// Block image search tab.
	if blockImages {
		for _, p := range engine.imagesPaths {
			if strings.HasPrefix(path, p) {
				fc.Block("Image search blocked by policy", "safesearch")
				return
			}
		}
		if engine.imagesParamKey != "" && fc.Request.URL.Query().Get(engine.imagesParamKey) == engine.imagesParamVal {
			fc.Block("Image search blocked by policy", "safesearch")
			return
		}
	}

	// Block video search tab.
	if blockVideos {
		for _, p := range engine.videosPaths {
			if strings.HasPrefix(path, p) {
				fc.Block("Video search blocked by policy", "safesearch")
				return
			}
		}
		if engine.videosParamKey != "" && fc.Request.URL.Query().Get(engine.videosParamKey) == engine.videosParamVal {
			fc.Block("Video search blocked by policy", "safesearch")
			return
		}
	}

	// Header-based enforcement (YouTube Restricted Mode) - all paths.
	if engine.safeHeaderKey != "" {
		fc.Request.Header.Set(engine.safeHeaderKey, engine.safeHeaderValue)
		fc.WFAction = "modified"
		fc.WFComponent = "safesearch"
	}

	// URL param enforcement - paths under pathPrefix.
	if engine.safeParamKey != "" && strings.HasPrefix(path, engine.pathPrefix) {
		if injectParam(fc.Request.URL, engine.safeParamKey, engine.safeParamValue) {
			fc.WFAction = "modified"
			fc.WFComponent = "safesearch"
		}
	}
}

// injectParam sets key=value in u's query string, reports whether it
// changed anything. Mirrors safesearch.py's _inject_param.
func injectParam(u *url.URL, key, value string) bool {
	q := u.Query()
	if q.Get(key) == value {
		q.Set(key, value)
		u.RawQuery = q.Encode()
		return false
	}
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return true
}
