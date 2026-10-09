// Package textbayes implements an embedded, pure-Go adult-text scorer for
// the proxy text classifier. It deliberately avoids native ML runtimes and
// external model files: the compact Bayesian feature table is embedded in
// the binary and implements addons.MLScorer's Score shape.
package textbayes

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/yjlion/gowebfilter/internal/classify/profanity"
)

//go:embed model_data.json
var embeddedModel []byte

type modelData struct {
	Name       string        `json:"name"`
	Version    int           `json:"version"`
	AdultPrior float64       `json:"adult_prior"`
	SafePrior  float64       `json:"safe_prior"`
	AdultTotal float64       `json:"adult_total"`
	SafeTotal  float64       `json:"safe_total"`
	Features   []featureData `json:"features"`
}

type featureData struct {
	Text  string  `json:"text"`
	Adult float64 `json:"adult"`
	Safe  float64 `json:"safe"`
}

// Model is a small Laplace-smoothed multinomial Naive Bayes scorer.
type Model struct {
	adultPrior float64
	safePrior  float64
	adultTotal float64
	safeTotal  float64
	vocabSize  float64
	features   map[string]featureData
	maxPhrase  int
	// unspaced holds the features written in scripts without spaces
	// between words (Han, Kana, Thai, ...): they can't be token-matched,
	// so they match as substrings, keyed by their lowercased text in
	// features.
	unspaced *profanity.SubstringIndex
}

// New loads the embedded Bayesian feature table.
func New() (*Model, error) {
	var data modelData
	if err := json.Unmarshal(embeddedModel, &data); err != nil {
		return nil, fmt.Errorf("textbayes: parse embedded model: %w", err)
	}
	return newFromData(data)
}

func newFromData(data modelData) (*Model, error) {
	if data.AdultPrior <= 0 || data.SafePrior <= 0 {
		return nil, fmt.Errorf("textbayes: priors must be positive")
	}
	if data.AdultTotal <= 0 || data.SafeTotal <= 0 {
		return nil, fmt.Errorf("textbayes: totals must be positive")
	}
	if len(data.Features) == 0 {
		return nil, fmt.Errorf("textbayes: feature table must not be empty")
	}
	m := &Model{
		adultPrior: data.AdultPrior,
		safePrior:  data.SafePrior,
		adultTotal: data.AdultTotal,
		safeTotal:  data.SafeTotal,
		vocabSize:  float64(len(data.Features)),
		features:   make(map[string]featureData, len(data.Features)),
	}
	var unspaced []string
	for _, f := range data.Features {
		if f.Adult < 0 || f.Safe < 0 {
			return nil, fmt.Errorf("textbayes: feature %q has negative count", f.Text)
		}
		if profanity.HasUnspaced(f.Text) {
			key := strings.ToLower(strings.TrimSpace(f.Text))
			if _, dup := m.features[key]; !dup {
				unspaced = append(unspaced, key)
			}
			f.Text = key
			m.features[key] = f
			continue
		}
		key := normalizePhrase(f.Text)
		if key == "" {
			continue
		}
		f.Text = key
		m.features[key] = f
		if n := strings.Count(key, " ") + 1; n > m.maxPhrase {
			m.maxPhrase = n
		}
	}
	m.unspaced = profanity.NewSubstringIndex(unspaced)
	if len(m.features) == 0 {
		return nil, fmt.Errorf("textbayes: feature table normalized to empty")
	}
	m.vocabSize = float64(len(m.features))
	return m, nil
}

// Score returns a calibrated adult-content probability in [0,1].
func (m *Model) Score(text string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	hits := m.extractFeatures(text)
	if len(hits) == 0 {
		if len(tokenize(text)) == 0 && !profanity.HasUnspaced(text) {
			return 0, false
		}
		return m.adultPrior / (m.adultPrior + m.safePrior), true
	}

	logAdult := math.Log(m.adultPrior)
	logSafe := math.Log(m.safePrior)
	for _, hit := range hits {
		f := m.features[hit]
		logAdult += math.Log((f.Adult + 1) / (m.adultTotal + m.vocabSize))
		logSafe += math.Log((f.Safe + 1) / (m.safeTotal + m.vocabSize))
	}
	if logAdult >= logSafe {
		return 1 / (1 + math.Exp(logSafe-logAdult)), true
	}
	return math.Exp(logAdult-logSafe) / (1 + math.Exp(logAdult-logSafe)), true
}

func (m *Model) extractFeatures(text string) []string {
	var hits []string
	seen := make(map[string]int)
	// Let repeated adult evidence count, but cap repetition so a long spam
	// page cannot drive the score solely by duplication.
	m.unspaced.Scan(text, func(entry string, _, _ int) {
		if seen[entry] < 4 {
			hits = append(hits, entry)
			seen[entry]++
		}
	})
	tokens := tokenize(text)
	for i := 0; i < len(tokens); i++ {
		maxN := m.maxPhrase
		if remaining := len(tokens) - i; remaining < maxN {
			maxN = remaining
		}
		for n := maxN; n >= 1; n-- {
			phrase := strings.Join(tokens[i:i+n], " ")
			if _, ok := m.features[phrase]; !ok {
				continue
			}
			if seen[phrase] < 4 {
				hits = append(hits, phrase)
				seen[phrase]++
			}
			break
		}
	}
	return hits
}

func normalizePhrase(s string) string {
	return strings.Join(tokenize(s), " ")
}

// tokenize splits s into lowercased Unicode words (profanity.Tokenize, so
// the censor and the scorer agree on word boundaries) with light English
// plural folding.
func tokenize(s string) []string {
	raw := profanity.Tokenize(s)
	tokens := make([]string, len(raw))
	for i, tok := range raw {
		tokens[i] = normalizeToken(tok.Text)
	}
	return tokens
}

func normalizeToken(tok string) string {
	switch tok {
	case "cams":
		return "cam"
	case "pics":
		return "pic"
	case "photos":
		return "photo"
	case "videos":
		return "video"
	case "webcams":
		return "webcam"
	}
	if len(tok) > 4 && strings.HasSuffix(tok, "ies") {
		return strings.TrimSuffix(tok, "ies") + "y"
	}
	return tok
}
