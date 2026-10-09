package addons

import (
	"bytes"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/yjlion/gowebfilter/internal/adblock"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
)

// Adblock blocks ad and tracker requests matched by the policy's filter
// lists, and hides ad elements on HTML pages with the lists' cosmetic
// (##) rules. It acts on MITM'd traffic only: a blind-spliced tunnel never
// reaches the pipeline, and the connection-level host gate deliberately
// does not consult adblock lists (see docs/adblock.md).
type Adblock struct{}

func (Adblock) Name() string { return "adblock" }

// adblockComponent is the log component for everything this addon does.
const adblockComponent = "adblock"

func adblockActive(fc *proxy.FlowContext) (models.AdblockConfig, bool) {
	p := fc.Policy
	if p == nil || !p.Adblock.Enabled || fc.URLAllowed || fc.MitmPassthrough {
		return models.AdblockConfig{}, false
	}
	if fc.Runtime == nil || fc.Runtime.Adblock == nil {
		return models.AdblockConfig{}, false
	}
	return p.Adblock, true
}

func adblockLists(cfg models.AdblockConfig) []string {
	if len(cfg.Lists) == 0 {
		return adblock.DefaultLists
	}
	return cfg.Lists
}

func (Adblock) HandleRequest(fc *proxy.FlowContext) {
	cfg, ok := adblockActive(fc)
	if !ok || fc.Response != nil {
		return
	}
	req := fc.Request
	host := strings.ToLower(req.URL.Hostname())
	sourceURL, sourceHost := requestSource(req)
	typ := requestResourceType(req)
	if adblockSiteAllowed(cfg, host, sourceHost, typ) {
		return
	}
	eng := fc.Runtime.Adblock.Engine(adblockLists(cfg))
	if eng == nil {
		return // lists still downloading/compiling: fail open
	}
	v := eng.Match(adblock.Request{
		URL:        req.URL.String(),
		Host:       host,
		SourceHost: sourceHost,
		SourceURL:  sourceURL,
		Type:       typ,
	})
	if !v.Blocked {
		return
	}
	reason := "Blocked by ad/tracker filter: " + v.Rule
	if typ == "document" {
		fc.Block(reason, adblockComponent)
		return
	}
	fc.BlockEmpty(reason, adblockComponent, typ)
}

// HandleResponse injects element-hiding CSS into HTML pages.
func (Adblock) HandleResponse(fc *proxy.FlowContext) {
	cfg, ok := adblockActive(fc)
	if !ok || !cfg.Cosmetic || fc.Response == nil || fc.WFAction == "blocked" {
		return
	}
	if fc.Response.StatusCode != http.StatusOK ||
		!strings.Contains(strings.ToLower(fc.Response.Header.Get("Content-Type")), "text/html") {
		return
	}
	host := strings.ToLower(fc.Request.URL.Hostname())
	if adblockSiteAllowed(cfg, host, "", "document") {
		return
	}
	eng := fc.Runtime.Adblock.Engine(adblockLists(cfg))
	if eng == nil {
		return
	}
	css := adblock.CosmeticCSS(eng.CosmeticSelectors(host, fc.Request.URL.String(), fc.ResponseBody))
	if css == "" {
		return
	}
	fc.ResponseBody = injectStyle(fc.ResponseBody, css)
	fc.Response.Header.Set("Content-Length", strconv.Itoa(len(fc.ResponseBody)))
	// Not marked "modified": hiding ad slots happens on nearly every page
	// and would drown out the classifier rewrites that flag is for.
}

// adblockSiteAllowed applies the policy's per-site exemptions. They name
// the page a user is on, so subresources are judged by their source page;
// a navigation (or a request with no known source) by its own host.
func adblockSiteAllowed(cfg models.AdblockConfig, host, sourceHost, typ string) bool {
	if len(cfg.Allow) == 0 {
		return false
	}
	site := sourceHost
	if site == "" || typ == "document" {
		site = host
	}
	return proxy.DomainInList(site, cfg.Allow)
}

// requestSource returns the page that issued a request, from Referer or
// Origin.
func requestSource(req *http.Request) (string, string) {
	for _, h := range []string{"Referer", "Origin"} {
		if v := req.Header.Get(h); v != "" {
			if u, err := url.Parse(v); err == nil && u.Hostname() != "" {
				return v, strings.ToLower(u.Hostname())
			}
		}
	}
	return "", ""
}

var secFetchDestTypes = map[string]string{
	"document": "document", "iframe": "subdocument", "frame": "subdocument",
	"script": "script", "worker": "script", "sharedworker": "script", "serviceworker": "script",
	"style": "stylesheet", "image": "image", "font": "font",
	"audio": "media", "video": "media", "track": "media",
	"object": "object", "embed": "object", "empty": "xmlhttprequest",
	"report": "ping",
}

var extensionTypes = map[string]string{
	".js": "script", ".mjs": "script", ".css": "stylesheet",
	".png": "image", ".jpg": "image", ".jpeg": "image", ".gif": "image",
	".webp": "image", ".avif": "image", ".svg": "image", ".ico": "image",
	".woff": "font", ".woff2": "font", ".ttf": "font", ".otf": "font",
	".mp4": "media", ".webm": "media", ".mp3": "media", ".m3u8": "media",
	".html": "document", ".htm": "document",
}

// requestResourceType infers the adblock resource type of a request:
// Sec-Fetch-Dest when the browser sends it, then the Accept header, then
// the URL's extension.
func requestResourceType(req *http.Request) string {
	if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return "websocket"
	}
	if t, ok := secFetchDestTypes[strings.ToLower(req.Header.Get("Sec-Fetch-Dest"))]; ok {
		return t
	}
	accept := strings.ToLower(req.Header.Get("Accept"))
	switch {
	case strings.HasPrefix(accept, "text/html"):
		return "document"
	case strings.HasPrefix(accept, "image/"):
		return "image"
	case strings.HasPrefix(accept, "text/css"):
		return "stylesheet"
	}
	if t, ok := extensionTypes[strings.ToLower(path.Ext(req.URL.Path))]; ok {
		if t == "document" && req.Header.Get("Referer") != "" {
			return "subdocument"
		}
		return t
	}
	return "other"
}

// injectStyle inserts a <style> element before </head>, else right after
// the opening <body> tag, else at the start of the document.
func injectStyle(body []byte, css string) []byte {
	tag := []byte("<style id=\"webfilter-adblock\">\n" + css + "</style>")
	lower := bytes.ToLower(body)
	if i := bytes.Index(lower, []byte("</head>")); i >= 0 {
		return splice(body, i, tag)
	}
	if i := bytes.Index(lower, []byte("<body")); i >= 0 {
		if j := bytes.IndexByte(body[i:], '>'); j >= 0 {
			return splice(body, i+j+1, tag)
		}
	}
	return splice(body, 0, tag)
}

func splice(body []byte, at int, insert []byte) []byte {
	out := make([]byte, 0, len(body)+len(insert))
	out = append(out, body[:at]...)
	out = append(out, insert...)
	return append(out, body[at:]...)
}
