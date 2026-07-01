package addons

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
)

// YouTubeFilter blocks/allows YouTube channels and optionally strips
// comments/the recommendation sidebar. Ported from
// proxy/addons/youtube_filter.py.
//
// Channels can be listed three ways in a policy's channels list, all
// matched: channel ID (UCxxxx...), @handle, or display name (case
// insensitive). youtube.com is a single-page app, so several interception
// points are needed - a full document GET (/watch, /@channel, ...) only
// happens on a direct load/reload; in-app navigation instead hits the
// InnerTube JSON APIs, covered below.
type YouTubeFilter struct{}

func (YouTubeFilter) Name() string { return "youtube_filter" }

var youtubeHosts = map[string]bool{
	"www.youtube.com": true, "youtube.com": true, "m.youtube.com": true,
	"youtubei.googleapis.com": true,
}

const (
	playerPath   = "/youtubei/v1/player"
	getWatchPath = "/youtubei/v1/get_watch"
	nextPath     = "/youtubei/v1/next"
	browsePath   = "/youtubei/v1/browse"
)

var (
	channelIDRe      = regexp.MustCompile(`"channelId":"(UC[\w-]{22})"`)
	authorRe         = regexp.MustCompile(`"author":"((?:[^"\\]|\\.)*)"`)
	handleRe         = regexp.MustCompile(`"canonicalBaseUrl":"/(@[\w.\-]+)"`)
	ownerURLHandleRe = regexp.MustCompile(`/(@[\w.\-]+)`)
	externalIDRe     = regexp.MustCompile(`"externalId":"(UC[\w-]{22})"`)
	vanityRe         = regexp.MustCompile(`"vanityChannelUrl":"https?://(?:www\.)?youtube\.com/(@[\w.\-]+)"`)
	channelTitleRe   = regexp.MustCompile(`"channelMetadataRenderer":\{"title":"((?:[^"\\]|\\.)*)"`)
	channelURLPathRe = regexp.MustCompile(`^/(?:(channel)/(UC[\w-]{22})|(@[\w.\-]+)|(?:c|user)/[\w.\-]+)/?`)
)

func isYouTube(host string) bool { return youtubeHosts[host] }

func youtubeShouldFilter(host string, cfg models.YouTubeConfig) bool {
	if len(cfg.IncludeOnly) > 0 {
		return hostInList(host, cfg.IncludeOnly)
	}
	if len(cfg.Exclude) > 0 {
		return !hostInList(host, cfg.Exclude)
	}
	return true
}

func normChannelName(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
}

func jsonUnescape(raw string) string {
	var s string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &s); err != nil {
		return raw
	}
	return s
}

// channelListed reports whether this channel appears in the configured
// list, matched by channel ID, display name, or @handle (case-insensitive
// for names/handles). Ported from _channel_listed.
func channelListed(channelID, author, handle string, channels []string) bool {
	names := make(map[string]bool)
	if author != "" {
		names[strings.ToLower(author)] = true
	}
	if handle != "" {
		names[normChannelName(handle)] = true
	}
	for _, entry := range channels {
		e := strings.TrimSpace(entry)
		if e == "" {
			continue
		}
		if channelID != "" && e == channelID {
			return true
		}
		if names[normChannelName(e)] {
			return true
		}
	}
	return false
}

func youtubeIsBlocked(channelID, author, handle string, cfg models.YouTubeConfig) bool {
	listed := channelListed(channelID, author, handle, cfg.Channels)
	if cfg.Mode == models.YouTubeModeWhitelist {
		return !listed
	}
	return listed
}

func isChannelPath(path string) bool { return channelURLPathRe.MatchString(path) }

func isHomePath(path string) bool {
	return path == "/" || path == "" || strings.HasPrefix(path, "/feed")
}

// watchResults navigates to
// data.contents.twoColumnWatchNextResults.results.results.contents.
func watchResults(data map[string]any) ([]any, map[string]any) {
	contents, ok := getMap(data, "contents")
	if !ok {
		return nil, nil
	}
	twoCol, ok := getMap(contents, "twoColumnWatchNextResults")
	if !ok {
		return nil, nil
	}
	results1, ok := getMap(twoCol, "results")
	if !ok {
		return nil, nil
	}
	results2, ok := getMap(results1, "results")
	if !ok {
		return nil, nil
	}
	items, ok := getSlice(results2, "contents")
	if !ok {
		return nil, nil
	}
	return items, results2
}

// stripCommentsFromNext removes the comments section from a watch /next
// response. Ported from _strip_comments_from_next.
func stripCommentsFromNext(data map[string]any) bool {
	items, holder := watchResults(data)
	if items == nil {
		return false
	}
	keep := make([]any, 0, len(items))
	changed := false
	for _, item := range items {
		itemMap, _ := item.(map[string]any)
		if itemMap != nil {
			if isr, ok := getMap(itemMap, "itemSectionRenderer"); ok {
				sid, _ := isr["sectionIdentifier"].(string)
				if sid == "comment-item-section" || sid == "comments-entry-point" {
					changed = true
					continue
				}
			}
		}
		keep = append(keep, item)
	}
	if changed {
		holder["contents"] = keep
	}
	return changed
}

// stripSidebarFromNext removes the related-videos sidebar (and autoplay)
// from a watch /next response. Ported from _strip_sidebar_from_next.
func stripSidebarFromNext(data map[string]any) bool {
	contents, ok := getMap(data, "contents")
	if !ok {
		return false
	}
	twoCol, ok := getMap(contents, "twoColumnWatchNextResults")
	if !ok {
		return false
	}
	if _, exists := twoCol["secondaryResults"]; exists {
		delete(twoCol, "secondaryResults")
		return true
	}
	return false
}

// browseChannelIdentity pulls (channel_id, title, handle) from a /browse
// response's metadata. Ported from _browse_channel_identity.
func browseChannelIdentity(data map[string]any) (channelID, title, handle string) {
	meta, _ := getMap(data, "metadata")
	metaRenderer, ok := getMap(meta, "channelMetadataRenderer")
	if !ok {
		return "", "", ""
	}
	channelID = getString(metaRenderer, "externalId")
	title = getString(metaRenderer, "title")
	vanity := getString(metaRenderer, "vanityChannelUrl")
	if m := ownerURLHandleRe.FindStringSubmatch(vanity); m != nil {
		handle = m[1]
	}
	return channelID, title, handle
}

func (YouTubeFilter) HandleResponse(fc *proxy.FlowContext) {
	if fc.URLAllowed || fc.MitmPassthrough {
		return
	}
	policy := fc.Policy
	if policy == nil || !policy.YouTube.Enabled {
		return
	}
	host := fc.Request.URL.Hostname()
	if !isYouTube(host) || !youtubeShouldFilter(host, policy.YouTube) {
		return
	}
	if fc.Response == nil {
		return
	}

	path := fc.Request.URL.Path

	switch path {
	case playerPath:
		handlePlayer(fc, policy)
		return
	case getWatchPath:
		handleGetWatch(fc, policy)
		return
	case nextPath:
		handleNext(fc, policy)
		return
	case browsePath:
		handleBrowse(fc, policy)
		return
	}

	ct := fc.Response.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		return
	}
	switch {
	case strings.HasPrefix(path, "/watch"):
		handleWatchHTML(fc, policy)
	case isChannelPath(path):
		handleChannelHTML(fc, policy)
	case isHomePath(path):
		handleHomeHTML(fc, policy)
	}
}

// blockPlayerResponse mutates pr in place to be unplayable if its channel
// is blocked, returning the channel label; returns "" if not blocked (or
// no channel identity could be determined). Ported from
// _block_player_response.
func blockPlayerResponse(pr map[string]any, policy *models.Policy) string {
	vd, _ := getMap(pr, "videoDetails")
	micro, _ := getMap(pr, "microformat")
	microRenderer, _ := getMap(micro, "playerMicroformatRenderer")

	channelID := getString(vd, "channelId")
	if channelID == "" {
		channelID = getString(microRenderer, "externalChannelId")
	}
	author := getString(vd, "author")
	if author == "" {
		author = getString(microRenderer, "ownerChannelName")
	}
	handle := ""
	if m := ownerURLHandleRe.FindStringSubmatch(getString(microRenderer, "ownerProfileUrl")); m != nil {
		handle = m[1]
	}

	if channelID == "" && author == "" {
		return ""
	}
	if !youtubeIsBlocked(channelID, author, handle, policy.YouTube) {
		return ""
	}

	label := author
	if label == "" {
		label = channelID
	}
	msg := policy.BlockPage.Message
	if msg == "" {
		msg = "This video is blocked by your network policy."
	}
	pr["playabilityStatus"] = map[string]any{
		"status": "ERROR",
		"reason": msg,
		"errorScreen": map[string]any{
			"playerErrorMessageRenderer": map[string]any{
				"reason":    map[string]any{"simpleText": msg},
				"subreason": map[string]any{"simpleText": "Blocked channel: " + label},
			},
		},
	}
	delete(pr, "streamingData")
	return label
}

func decodeJSONResponse(fc *proxy.FlowContext) (map[string]any, bool) {
	ct := fc.Response.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") {
		return nil, false
	}
	var data map[string]any
	if err := json.Unmarshal(fc.ResponseBody, &data); err != nil {
		return nil, false
	}
	return data, true
}

func encodeJSONResponse(fc *proxy.FlowContext, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		return
	}
	fc.ResponseBody = body
}

func handlePlayer(fc *proxy.FlowContext, policy *models.Policy) {
	data, ok := decodeJSONResponse(fc)
	if !ok {
		return
	}
	label := blockPlayerResponse(data, policy)
	if label != "" {
		encodeJSONResponse(fc, data)
		fc.LogBlock("YouTube channel '"+label+"' blocked by policy", "youtube")
	}
}

// handleGetWatch handles the combined watch call modern YouTube uses for
// in-app navigation. Its body is a JSON array carrying both the player
// response ([0].playerResponse) and the watch-next data
// ([1].watchNextResponse) - but is treated generically as "a list of
// elements" (normalizing a bare object to a one-element list), mirroring
// the Python original exactly, since some deployments have been observed
// returning a single object instead of the usual two-element array.
func handleGetWatch(fc *proxy.FlowContext, policy *models.Policy) {
	ct := fc.Response.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") {
		return
	}
	var raw any
	if err := json.Unmarshal(fc.ResponseBody, &raw); err != nil {
		return
	}
	yt := policy.YouTube

	elements, isArray := raw.([]any)
	if !isArray {
		elements = []any{raw}
	}

	changed := false
	label := ""
	for _, el := range elements {
		elMap, ok := el.(map[string]any)
		if !ok {
			continue
		}
		if pr, ok := getMap(elMap, "playerResponse"); ok {
			if lbl := blockPlayerResponse(pr, policy); lbl != "" {
				changed = true
				label = lbl
			}
		}
		if wn, ok := getMap(elMap, "watchNextResponse"); ok {
			if yt.RemoveComments && stripCommentsFromNext(wn) {
				changed = true
			}
			if yt.RemoveRecommendations && stripSidebarFromNext(wn) {
				changed = true
			}
		}
	}

	if changed {
		if isArray {
			encodeJSONResponse(fc, elements)
		} else {
			encodeJSONResponse(fc, elements[0])
		}
		if label != "" {
			fc.LogBlock("YouTube channel '"+label+"' blocked by policy", "youtube")
		}
	}
}

func handleNext(fc *proxy.FlowContext, policy *models.Policy) {
	data, ok := decodeJSONResponse(fc)
	if !ok {
		return
	}
	yt := policy.YouTube
	changed := false
	if yt.RemoveComments && stripCommentsFromNext(data) {
		changed = true
	}
	if yt.RemoveRecommendations && stripSidebarFromNext(data) {
		changed = true
	}
	if changed {
		encodeJSONResponse(fc, data)
	}
}

func handleBrowse(fc *proxy.FlowContext, policy *models.Policy) {
	data, ok := decodeJSONResponse(fc)
	if !ok {
		return
	}
	yt := policy.YouTube
	channelID, title, handle := browseChannelIdentity(data)

	if channelID != "" || title != "" || handle != "" {
		if youtubeIsBlocked(channelID, title, handle, yt) {
			label := title
			if label == "" {
				label = handle
			}
			if label == "" {
				label = channelID
			}
			blankBrowse(fc, data, "YouTube channel '"+label+"' blocked by policy")
		}
		return
	}

	// No channel metadata -> a feed (home / trending / subscriptions, etc.).
	if yt.Mode == models.YouTubeModeWhitelist && yt.BlockHome {
		blankBrowse(fc, data, "YouTube home/feed blocked (whitelist mode)")
	}
}

// blankBrowse replaces a /browse payload so no channel/feed content
// renders. Ported from _blank_browse.
func blankBrowse(fc *proxy.FlowContext, data map[string]any, reason string) {
	respContext, ok := data["responseContext"]
	if !ok {
		respContext = map[string]any{}
	}
	blanked := map[string]any{"responseContext": respContext}
	encodeJSONResponse(fc, blanked)
	fc.LogBlock(reason, "youtube")
}

func handleWatchHTML(fc *proxy.FlowContext, policy *models.Policy) {
	html := string(fc.ResponseBody)

	channelID := ""
	if m := channelIDRe.FindStringSubmatch(html); m != nil {
		channelID = m[1]
	}
	author := ""
	if m := authorRe.FindStringSubmatch(html); m != nil {
		author = jsonUnescape(m[1])
	}
	handle := ""
	if m := handleRe.FindStringSubmatch(html); m != nil {
		handle = m[1]
	}

	if channelID == "" && author == "" {
		return
	}
	if !youtubeIsBlocked(channelID, author, handle, policy.YouTube) {
		return
	}

	label := author
	if label == "" {
		label = channelID
	}
	fc.Block("YouTube channel '"+label+"' blocked by policy", "youtube")
}

func handleChannelHTML(fc *proxy.FlowContext, policy *models.Policy) {
	html := string(fc.ResponseBody)
	path := fc.Request.URL.Path

	channelID, handle, name := "", "", ""
	if m := channelURLPathRe.FindStringSubmatch(path); m != nil {
		if m[2] != "" { // /channel/UC...
			channelID = m[2]
		} else if m[3] != "" { // /@handle
			handle = m[3]
		}
	}
	if m := externalIDRe.FindStringSubmatch(html); m != nil && channelID == "" {
		channelID = m[1]
	}
	if m := vanityRe.FindStringSubmatch(html); m != nil && handle == "" {
		handle = m[1]
	}
	if m := channelTitleRe.FindStringSubmatch(html); m != nil {
		name = jsonUnescape(m[1])
	}

	if channelID == "" && handle == "" && name == "" {
		return
	}
	if !youtubeIsBlocked(channelID, name, handle, policy.YouTube) {
		return
	}

	label := name
	if label == "" {
		label = handle
	}
	if label == "" {
		label = channelID
	}
	fc.Block("YouTube channel '"+label+"' blocked by policy", "youtube")
}

func handleHomeHTML(fc *proxy.FlowContext, policy *models.Policy) {
	yt := policy.YouTube
	if yt.Mode != models.YouTubeModeWhitelist || !yt.BlockHome {
		return
	}
	fc.Block("YouTube home page blocked (whitelist mode)", "youtube")
}
