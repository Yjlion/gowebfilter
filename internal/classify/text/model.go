package text

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// Model is a trained TF-IDF + logistic-regression text scorer: Vocab maps a
// token to its column index, IDF/Coef are parallel arrays indexed the same
// way, and Intercept is the logistic-regression bias term. The JSON form is
// the sidecar file GlobalSettings.TextClassifierModelPath points at.
type Model struct {
	Vocab     map[string]int `json:"vocab"`
	IDF       []float64      `json:"idf"`
	Coef      []float64      `json:"coef"`
	Intercept float64        `json:"intercept"`
}

// Load reads a Model from path and sanity-checks that IDF/Coef agree in
// length with Vocab, so a corrupt or hand-edited sidecar fails fast at
// startup rather than silently mis-scoring every request.
func Load(path string) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load text classifier model: %w", err)
	}
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse text classifier model %s: %w", path, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("text classifier model %s: %w", path, err)
	}
	return &m, nil
}

func (m *Model) validate() error {
	n := len(m.Vocab)
	if len(m.IDF) != n {
		return fmt.Errorf("idf length %d does not match vocab size %d", len(m.IDF), n)
	}
	if len(m.Coef) != n {
		return fmt.Errorf("coef length %d does not match vocab size %d", len(m.Coef), n)
	}
	for token, idx := range m.Vocab {
		if idx < 0 || idx >= n {
			return fmt.Errorf("vocab index %d for token %q out of range [0,%d)", idx, token, n)
		}
	}
	return nil
}

// Save writes m as the JSON sidecar Load reads back.
func (m *Model) Save(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal text classifier model: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write text classifier model %s: %w", path, err)
	}
	return nil
}

// Vectorize turns text into an L2-normalized TF-IDF vector over m.Vocab.
// Tokens not present in the vocabulary are ignored (out-of-vocabulary, same
// as sklearn's TfidfVectorizer with a fixed vocabulary). Exported so the
// trainer can build feature vectors identically to how Score will read them
// back at inference time.
func (m *Model) Vectorize(text string) []float64 {
	vec := make([]float64, len(m.Vocab))
	counts := make(map[int]float64, len(vec))
	for _, tok := range Tokenize(text) {
		if idx, ok := m.Vocab[tok]; ok {
			counts[idx]++
		}
	}
	var normSq float64
	for idx, count := range counts {
		v := count * m.IDF[idx]
		vec[idx] = v
		normSq += v * v
	}
	if normSq > 0 {
		norm := math.Sqrt(normSq)
		for idx, v := range vec {
			if v != 0 {
				vec[idx] = v / norm
			}
		}
	}
	return vec
}

// Score implements addons.MLScorer: a logistic-regression probability that
// text is adult content. ok is false only when the model has an empty
// vocabulary (a zero-value or corrupt Model), matching the "unavailable"
// contract addons.TextClassifier expects from a nil/failed Scorer.
func (m *Model) Score(text string) (float64, bool) {
	if m == nil || len(m.Vocab) == 0 {
		return 0, false
	}
	vec := m.Vectorize(text)
	z := m.Intercept
	for idx, v := range vec {
		if v != 0 {
			z += v * m.Coef[idx]
		}
	}
	return sigmoid(z), true
}

func sigmoid(z float64) float64 {
	return 1 / (1 + math.Exp(-z))
}
