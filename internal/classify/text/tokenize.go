// Package text implements the optional ML stage of
// internal/proxy/addons.TextClassifier (project plan Phase 8): a small
// TF-IDF + logistic-regression scorer, trained offline (see Train/TrainModel)
// and loaded at runtime from a JSON sidecar (see Load) as a pure-Go
// implementation of addons.MLScorer - no CGO/ONNX needed, unlike the image
// classifier's Phase 7 backend.
package text

import (
	"regexp"
	"strings"
)

// tokenRe extracts alphabetic runs (including internal apostrophes, so
// "don't" stays one token) - the same conservative tokenization on both the
// training and inference paths matters more than its exact shape, since the
// vocabulary/IDF table is only meaningful if both sides agree on it.
var tokenRe = regexp.MustCompile(`[a-zA-Z']+`)

// Tokenize lowercases s and splits it into word tokens. Exported so the
// trainer (building a vocabulary/document-frequency table from a corpus) and
// Model.Vectorize (scoring one document) can never drift apart.
func Tokenize(s string) []string {
	return tokenRe.FindAllString(strings.ToLower(s), -1)
}
