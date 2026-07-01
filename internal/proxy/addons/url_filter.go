package addons

import "github.com/yjlion/gowebfilter/internal/proxy"

// UrlFilter blocks/allows URLs and domains, and blocks by shared category,
// ported from proxy/addons/url_filter.py's request hook.
type UrlFilter struct{}

func (UrlFilter) Name() string { return "url_filter" }

func (UrlFilter) HandleRequest(fc *proxy.FlowContext) {
	policy := fc.Policy
	if policy == nil || !policy.UrlFilter.Enabled {
		return
	}
	// Honor MITM passthrough (include-mode non-listed sites, User-Agent
	// rules): these flows skip all filtering, URL rules included. Without
	// this, matching clients would still be blocked here while every later
	// addon skips them.
	if fc.MitmPassthrough {
		return
	}

	cfg := policy.UrlFilter
	host := fc.Request.URL.Hostname()
	url := fc.Request.URL.String()

	// Custom allow/block lists take precedence over categories.
	for _, pattern := range cfg.Allow {
		if proxy.UrlMatches(host, url, pattern) {
			fc.URLAllowed = true
			return
		}
	}
	for _, pattern := range cfg.Block {
		if proxy.UrlMatches(host, url, pattern) {
			fc.Block("URL blocked by policy", "url_filter")
			return
		}
	}

	// Shared categories, applied per mode.
	if len(cfg.Categories) > 0 {
		cat := fc.Runtime.Categories.MatchAny(host, cfg.Categories)
		if cfg.Mode == "whitelist" {
			// Only listed categories are allowed; block everything else.
			if cat == "" {
				fc.Block("Site not in an allowed category (whitelist)", "url_filter")
			}
			return
		}
		// blacklist: block domains that fall in a listed category.
		if cat != "" {
			fc.Block("Site category '"+cat+"' blocked by policy", "url_filter")
			return
		}
	}
}
