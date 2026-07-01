package mgmtapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yjlion/gowebfilter/internal/neighbors"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
)

// registerToolsRoutes wires the diagnostic /api/tools/* endpoints that back
// the Tools page and the policy editor's "scan network" MAC picker:
//
//	POST /api/tools/scan       — NSFW scan of a URL (needs the Phase 7/8 ML
//	                             backends; returns 503 until they're built).
//	POST /api/tools/youtube    — parse a YouTube URL + fetch oEmbed metadata.
//	POST /api/tools/doh        — query a DoH resolver and report block status.
//	GET  /api/tools/public-ip  — discover the host's public IP.
//	GET  /api/tools/neighbors  — list the ARP/NDP neighbor table (IP/MAC).
func (s *Server) registerToolsRoutes(r chi.Router) {
	r.Post("/api/tools/scan", s.handleToolsScan)
	r.Post("/api/tools/youtube", s.handleToolsYouTube)
	r.Post("/api/tools/doh", s.handleToolsDoh)
	r.Get("/api/tools/public-ip", s.handleToolsPublicIP)
	r.Get("/api/tools/neighbors", s.handleToolsNeighbors)
}

// toolsHTTPClient deliberately bypasses any configured system proxy (which
// could be this proxy itself) so diagnostic lookups go out directly -
// mirrors the Python original's httpx.AsyncClient(trust_env=False).
var toolsHTTPClient = &http.Client{
	Timeout:   8 * time.Second,
	Transport: &http.Transport{Proxy: nil},
}

// ---------------------------------------------------------------------------
// POST /api/tools/scan
// ---------------------------------------------------------------------------

// handleToolsScan would fetch a URL and classify it for NSFW text/images, but
// that depends on the ML classifier backends (project plan Phases 7 & 8),
// which aren't built in this port yet. Rather than silently return a
// misleading "clean" verdict, it reports 503 with a detail the Tools page
// surfaces verbatim (it reads body.detail on any non-2xx).
func (s *Server) handleToolsScan(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		URL string `json:"url"`
	}
	_ = readJSON(r, &payload)
	if strings.TrimSpace(payload.URL) == "" {
		writeJSONError(w, http.StatusBadRequest, "url is required")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable,
		"NSFW scanning is not available in this build: the image and text ML classifiers are not yet implemented in the Go port")
}

// ---------------------------------------------------------------------------
// POST /api/tools/youtube
// ---------------------------------------------------------------------------

var (
	ytVideoRE     = regexp.MustCompile(`(?:youtube\.com/(?:watch\?(?:.*&)?v=|embed/|shorts/)|youtu\.be/)([\w-]{11})`)
	ytChannelIDRE = regexp.MustCompile(`youtube\.com/channel/(UC[\w-]{22})`)
	ytHandleRE    = regexp.MustCompile(`youtube\.com/(@[\w.\-]+)`)
	ytCustomRE    = regexp.MustCompile(`youtube\.com/(?:c|user)/([\w.\-]+)`)
)

// parseYouTubeURL mirrors tools.py's _parse_youtube_url.
func parseYouTubeURL(u string) (kind, videoID, channel string) {
	if m := ytVideoRE.FindStringSubmatch(u); m != nil {
		return "video", m[1], ""
	}
	if m := ytChannelIDRE.FindStringSubmatch(u); m != nil {
		return "channel", "", m[1]
	}
	if m := ytHandleRE.FindStringSubmatch(u); m != nil {
		return "channel", "", m[1]
	}
	if m := ytCustomRE.FindStringSubmatch(u); m != nil {
		return "channel", "", m[1]
	}
	return "unknown", "", ""
}

func (s *Server) handleToolsYouTube(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		URL string `json:"url"`
	}
	_ = readJSON(r, &payload)
	u := strings.TrimSpace(payload.URL)
	if u == "" {
		writeJSONError(w, http.StatusBadRequest, "url is required")
		return
	}

	kind, videoID, channel := parseYouTubeURL(u)
	result := map[string]any{
		"kind":     kind,
		"video_id": nilIfEmpty(videoID),
		"channel":  nilIfEmpty(channel),
		"url":      u,
	}

	if kind == "video" {
		oembedURL := "https://www.youtube.com/oembed?url=" + url.QueryEscape(u) + "&format=json"
		if oembed, err := fetchJSON(oembedURL); err != nil {
			result["oembed_error"] = err.Error()
		} else {
			result["title"] = oembed["title"]
			result["author_name"] = oembed["author_name"]
			result["author_url"] = oembed["author_url"]
			result["thumbnail_url"] = oembed["thumbnail_url"]
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// POST /api/tools/doh
// ---------------------------------------------------------------------------

func (s *Server) handleToolsDoh(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Domain string `json:"domain"`
		Server string `json:"server"`
	}
	_ = readJSON(r, &payload)
	domain := strings.TrimSpace(payload.Domain)
	if domain == "" {
		writeJSONError(w, http.StatusBadRequest, "domain is required")
		return
	}
	server := strings.TrimSpace(payload.Server)
	if server == "" {
		server = s.defaultDohServer()
	}
	writeJSON(w, http.StatusOK, addons.QueryDohDetailed(domain, server))
}

// defaultDohServer returns the DoH endpoint from the first policy that
// configures one, falling back to Cloudflare's family resolver - mirrors
// tools.py's _default_doh_server.
func (s *Server) defaultDohServer() string {
	policies, err := s.Policies.List()
	if err == nil {
		for _, p := range policies {
			if srv := strings.TrimSpace(p.Doh.Server); srv != "" {
				return srv
			}
		}
	}
	return "https://1.1.1.3/dns-query"
}

// ---------------------------------------------------------------------------
// GET /api/tools/public-ip
// ---------------------------------------------------------------------------

func (s *Server) handleToolsPublicIP(w http.ResponseWriter, r *http.Request) {
	// ipify returns {"ip": "..."}; ifconfig.me returns a bare IP string.
	if body, err := fetchJSON("https://api.ipify.org?format=json"); err == nil {
		writeJSON(w, http.StatusOK, body)
		return
	}
	if ip, err := fetchText("https://ifconfig.me/ip"); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ip": strings.TrimSpace(ip)})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": "all public-ip providers failed"})
}

// ---------------------------------------------------------------------------
// GET /api/tools/neighbors
// ---------------------------------------------------------------------------

func (s *Server) handleToolsNeighbors(w http.ResponseWriter, r *http.Request) {
	entries := neighbors.Scan()
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"ip":     e.IP,
			"mac":    e.MAC,
			"iface":  e.Iface,
			"vendor": e.Vendor,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"neighbors": out})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func fetchText(rawURL string) (string, error) {
	resp, err := toolsHTTPClient.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", &httpStatusError{resp.StatusCode}
	}
	return string(body), nil
}

func fetchJSON(rawURL string) (map[string]any, error) {
	resp, err := toolsHTTPClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string {
	return "unexpected HTTP status " + strconv.Itoa(e.code)
}
