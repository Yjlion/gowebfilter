package text

import (
	"fmt"
	"math"
	"sort"
)

// TrainOptions controls BuildVocab/Train. Defaults (via TrainModel) match
// scikit-learn's TfidfVectorizer/LogisticRegression common defaults closely
// enough for a small, dependency-free reimplementation.
type TrainOptions struct {
	MaxVocab int     // 0 means unlimited
	MinDF    int     // minimum document frequency to keep a token; <1 treated as 1
	Epochs   int     // gradient descent epochs
	LR       float64 // learning rate
	L2       float64 // L2 regularization strength
}

// DefaultTrainOptions returns reasonable defaults for a small corpus (low
// hundreds to low thousands of documents).
func DefaultTrainOptions() TrainOptions {
	return TrainOptions{MaxVocab: 2000, MinDF: 2, Epochs: 300, LR: 0.5, L2: 0.001}
}

// BuildVocab picks the vocabulary and IDF table from a corpus of documents:
// tokens with document frequency below minDF are dropped, then the
// maxVocab tokens with the highest document frequency are kept (ties broken
// alphabetically for determinism), and finally re-sorted alphabetically for
// the actual index assignment - so the resulting Vocab/IDF is independent of
// input document order.
func BuildVocab(docs []string, minDF, maxVocab int) (vocab map[string]int, idf []float64) {
	if minDF < 1 {
		minDF = 1
	}
	df := make(map[string]int)
	for _, doc := range docs {
		seen := make(map[string]struct{})
		for _, tok := range Tokenize(doc) {
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			df[tok]++
		}
	}

	candidates := make([]string, 0, len(df))
	for tok, count := range df {
		if count >= minDF {
			candidates = append(candidates, tok)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if df[candidates[i]] != df[candidates[j]] {
			return df[candidates[i]] > df[candidates[j]]
		}
		return candidates[i] < candidates[j]
	})
	if maxVocab > 0 && len(candidates) > maxVocab {
		candidates = candidates[:maxVocab]
	}
	sort.Strings(candidates)

	n := len(docs)
	vocab = make(map[string]int, len(candidates))
	idf = make([]float64, len(candidates))
	for i, tok := range candidates {
		vocab[tok] = i
		// Smooth IDF (as sklearn's default smooth_idf=True):
		// ln((1+n)/(1+df)) + 1, so a token in every document still gets a
		// small positive weight instead of 0.
		idf[i] = math.Log(float64(1+n)/float64(1+df[tok])) + 1.0
	}
	return vocab, idf
}

// Train fits logistic-regression weights via full-batch gradient descent
// with L2 regularization on the intercept-free weights (bias is not
// regularized, matching sklearn's default).
func Train(vectors [][]float64, labels []float64, dim int, opts TrainOptions) (coef []float64, intercept float64) {
	coef = make([]float64, dim)
	n := float64(len(vectors))
	if n == 0 {
		return coef, 0
	}
	gradCoef := make([]float64, dim)
	for epoch := 0; epoch < opts.Epochs; epoch++ {
		for i := range gradCoef {
			gradCoef[i] = 0
		}
		var gradIntercept float64
		for i, vec := range vectors {
			z := intercept
			for j, v := range vec {
				z += v * coef[j]
			}
			p := sigmoid(z)
			diff := p - labels[i]
			for j, v := range vec {
				if v != 0 {
					gradCoef[j] += diff * v
				}
			}
			gradIntercept += diff
		}
		for j := range coef {
			g := gradCoef[j]/n + opts.L2*coef[j]
			coef[j] -= opts.LR * g
		}
		intercept -= opts.LR * (gradIntercept / n)
	}
	return coef, intercept
}

// TrainModel builds a vocabulary from texts, vectorizes every document, and
// fits logistic-regression weights against labels (1.0 = adult content, 0.0
// = not), returning a ready-to-Save Model. len(texts) must equal
// len(labels) and be non-zero.
func TrainModel(texts []string, labels []float64, opts TrainOptions) (*Model, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("train text classifier: empty corpus")
	}
	if len(texts) != len(labels) {
		return nil, fmt.Errorf("train text classifier: %d texts but %d labels", len(texts), len(labels))
	}

	vocab, idf := BuildVocab(texts, opts.MinDF, opts.MaxVocab)
	if len(vocab) == 0 {
		return nil, fmt.Errorf("train text classifier: vocabulary is empty (corpus too small, or min-df %d too strict)", opts.MinDF)
	}

	m := &Model{Vocab: vocab, IDF: idf}
	vectors := make([][]float64, len(texts))
	for i, t := range texts {
		vectors[i] = m.Vectorize(t)
	}

	coef, intercept := Train(vectors, labels, len(vocab), opts)
	m.Coef = coef
	m.Intercept = intercept
	return m, nil
}
