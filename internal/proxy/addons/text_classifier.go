package addons

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yjlion/gowebfilter/internal/classify/profanity"
	"github.com/yjlion/gowebfilter/internal/metrics"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
)

// MLScorer scores arbitrary text for adult content, returning a
// probability in [0,1] (ok=false if scoring failed/unavailable). The
// optional ML stage (project plan Phase 8) implements this; a nil Scorer
// on TextClassifier means "keyword-only", matching the Python original's
// behavior when models/text_classifier.joblib is absent.
type MLScorer interface {
	Score(text string) (score float64, ok bool)
}

// TextClassifier detects adult text content via a fast keyword
// pre-filter (always active, zero dependencies) plus an optional ML
// stage. Ported from proxy/addons/text_classifier.py.
type TextClassifier struct {
	// Scorer is the optional ML stage; nil means keyword-only.
	Scorer MLScorer
}

func (TextClassifier) Name() string { return "text_classifier" }

// adultKeywordsRe is a conservative, high-precision keyword pre-filter,
// ported verbatim from text_classifier.py's _ADULT_KEYWORDS.
var adultKeywordsRe = regexp.MustCompile(`(?i)\b(porn|pornography|xxx|hentai|nude|naked|erotic|masturbat|orgasm|` +
	`penis|vagina|anal sex|oral sex|blowjob|handjob|gangbang|threesome|` +
	`escort service|cam girl|onlyfans|nsfw|adult content)\b`)

// minKeywordHits requires multiple hits to reduce false positives.
const minKeywordHits = 3

// KeywordScore exposes the keyword pre-filter to the management API's URL
// scanner, so a Tools verdict and a live proxy verdict can't drift apart.
// 1.0 means "enough hits to block on keywords alone".
func KeywordScore(text string) float64 { return keywordScore(text) }

// StripHTML exposes the same tag-stripping the response path uses.
func StripHTML(html string) string { return stripHTML(html) }

func keywordScore(text string) float64 {
	hits := len(adultKeywordsRe.FindAllString(text, -1))
	score := float64(hits) / float64(minKeywordHits)
	if score > 1.0 {
		return 1.0
	}
	return score
}

func (tc TextClassifier) classify(text string, threshold float64) bool {
	started := time.Now()
	result := metrics.ResultClean
	defer func() { metrics.ObserveClassifier("text", started, result) }()

	if keywordScore(text) >= 1.0 {
		result = metrics.ResultNSFW
		return true
	}
	if tc.Scorer != nil {
		p, ok := tc.Scorer.Score(text)
		if !ok {
			// Unscoreable text passes through. Reported as an error rather
			// than "clean" so a corrupt embedded model is visible as
			// something other than a sudden drop in detections.
			result = metrics.ResultError
			return false
		}
		if p >= threshold {
			result = metrics.ResultNSFW
		}
		return p >= threshold
	}
	return false
}

func textClassifierShouldFilter(host, url string, cfg models.TextClassifierConfig) bool {
	if len(cfg.IncludeOnly) > 0 {
		return proxy.UrlInList(host, url, cfg.IncludeOnly)
	}
	if len(cfg.Exclude) > 0 {
		return !proxy.UrlInList(host, url, cfg.Exclude)
	}
	return true
}

// htmlTagRe strips HTML tags without a full parser - the same
// no-dependency fallback text_classifier.py itself falls back to when
// BeautifulSoup isn't installed. Script and style bodies are dropped
// first (htmlCodeRe): they are code, not prose, and scoring them only adds
// noise - including the CSS the adblock addon injects.
var (
	htmlTagRe  = regexp.MustCompile(`<[^>]+>`)
	htmlCodeRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<style\b[^>]*>.*?</style\s*>`)
)

func stripHTML(html string) string {
	return htmlTagRe.ReplaceAllString(htmlCodeRe.ReplaceAllString(html, " "), " ")
}

func (tc TextClassifier) HandleResponse(fc *proxy.FlowContext) {
	if fc.URLAllowed || fc.MitmPassthrough {
		return
	}
	policy := fc.Policy
	if policy == nil || !policy.TextClassifier.Enabled {
		return
	}
	if fc.Response == nil {
		return
	}
	ct := fc.Response.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		return
	}

	host := fc.Request.URL.Hostname()
	url := fc.Request.URL.String()
	cfg := policy.TextClassifier
	if !textClassifierShouldFilter(host, url, cfg) {
		return
	}

	if cfg.Mode.Blocks() && tc.shouldBlock(stripHTML(string(fc.ResponseBody)), cfg.Threshold) {
		fc.Block("Adult text content detected", "text_classifier")
		return
	}
	if cfg.Mode.Censors() {
		censorResponse(fc, cfg)
	}
}

// shouldBlock is the block-mode decision: the keyword prefilter on any
// page, then the Bayesian scorer for pages with enough text to score.
func (tc TextClassifier) shouldBlock(text string, threshold float64) bool {
	if keywordScore(text) >= 1.0 {
		return true
	}
	if len(text) < 100 { // skip tiny pages
		return false
	}
	return tc.classify(text, threshold)
}

// censorResponse masks offensive words in the page's text in place.
func censorResponse(fc *proxy.FlowContext, cfg models.TextClassifierConfig) {
	langs := censorLanguages(cfg.CensorLanguages, fc.ResponseBody, fc.Response.Header.Get("Content-Language"))
	matcher := profanity.ForLanguages(langs, cfg.CensorWords)
	// Element-level lang attributes refine the automatic choice; a policy
	// that pins censor_languages means exactly those, everywhere.
	var forLang func(string) *profanity.Matcher
	if len(cfg.CensorLanguages) == 0 {
		forLang = func(lang string) *profanity.Matcher { return profanity.ForLanguages([]string{lang}, cfg.CensorWords) }
	}
	body, n := censorHTML(fc.ResponseBody, matcher, forLang)
	if n == 0 {
		return
	}
	fc.ResponseBody = body
	fc.Response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	fc.WFAction = "modified"
	fc.WFComponent = "text_classifier"
}
