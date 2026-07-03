package addons

import (
	"regexp"
	"strings"

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

func keywordScore(text string) float64 {
	hits := len(adultKeywordsRe.FindAllString(text, -1))
	score := float64(hits) / float64(minKeywordHits)
	if score > 1.0 {
		return 1.0
	}
	return score
}

func (tc TextClassifier) classify(text string, threshold float64) bool {
	if keywordScore(text) >= 1.0 {
		return true
	}
	if tc.Scorer != nil {
		if p, ok := tc.Scorer.Score(text); ok {
			return p >= threshold
		}
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
// BeautifulSoup isn't installed.
var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func stripHTML(html string) string {
	return htmlTagRe.ReplaceAllString(html, " ")
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

	text := stripHTML(string(fc.ResponseBody))
	if keywordScore(text) >= 1.0 {
		fc.Block("Adult text content detected", "text_classifier")
		return
	}
	if len(text) < 100 { // skip tiny pages
		return
	}

	if tc.classify(text, cfg.Threshold) {
		fc.Block("Adult text content detected", "text_classifier")
	}
}
