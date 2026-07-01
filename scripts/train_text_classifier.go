//go:build ignore

// Command train_text_classifier trains internal/proxy/addons.TextClassifier's
// optional ML stage (project plan Phase 8) from a labeled CSV corpus and
// writes the result as a JSON sidecar that internal/classify/text.Load reads
// at startup (GlobalSettings.TextClassifierModelPath).
//
// The corpus CSV needs a header row "text,label" where label is "1" (adult
// content) or "0" (not). Sourcing/curating that corpus is a real,
// use-case-specific decision this script deliberately does not make for
// you - see internal/classify/text's demoCorpus (in train_test.go) for the
// tiny, intentionally-tame smoke-test example this pipeline was verified
// against, not a shippable dataset.
//
// Usage: go run scripts/train_text_classifier.go [flags] <corpus.csv> <model.json>
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/yjlion/gowebfilter/internal/classify/text"
)

func main() {
	maxVocab := flag.Int("max-vocab", 2000, "maximum vocabulary size")
	minDF := flag.Int("min-df", 2, "minimum document frequency to keep a token")
	epochs := flag.Int("epochs", 300, "gradient descent epochs")
	lr := flag.Float64("lr", 0.5, "learning rate")
	l2 := flag.Float64("l2", 0.001, "L2 regularization strength")
	flag.Parse()

	args := flag.Args()
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/train_text_classifier.go [flags] <corpus.csv> <model.json>")
		os.Exit(2)
	}
	corpusPath, modelPath := args[0], args[1]

	texts, labels, err := readCorpus(corpusPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("[train] loaded %d rows from %s\n", len(texts), corpusPath)

	opts := text.TrainOptions{MaxVocab: *maxVocab, MinDF: *minDF, Epochs: *epochs, LR: *lr, L2: *l2}
	m, err := text.TrainModel(texts, labels, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("[train] vocabulary size: %d\n", len(m.Vocab))

	correct := 0
	for i, t := range texts {
		score, _ := m.Score(t)
		if (score >= 0.5) == (labels[i] == 1) {
			correct++
		}
	}
	fmt.Printf("[train] training-set accuracy: %d/%d (%.1f%%) - this is NOT a held-out estimate\n",
		correct, len(texts), 100*float64(correct)/float64(len(texts)))

	if err := m.Save(modelPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("[train] wrote %s\n", modelPath)
}

// readCorpus reads a "text,label" CSV (header row required, label "0"/"1").
func readCorpus(path string) (texts []string, labels []float64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open corpus: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("read corpus: %w", err)
	}
	if len(rows) < 2 {
		return nil, nil, fmt.Errorf("corpus has no data rows (need a header plus at least one row)")
	}
	for i, row := range rows[1:] {
		if len(row) < 2 {
			return nil, nil, fmt.Errorf("row %d: expected 2 columns (text,label), got %d", i+2, len(row))
		}
		label, err := strconv.ParseFloat(row[1], 64)
		if err != nil || (label != 0 && label != 1) {
			return nil, nil, fmt.Errorf("row %d: label %q must be \"0\" or \"1\"", i+2, row[1])
		}
		texts = append(texts, row[0])
		labels = append(labels, label)
	}
	return texts, labels, nil
}
