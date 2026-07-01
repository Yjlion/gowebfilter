package main

import (
	"path/filepath"
	"testing"

	classifytext "github.com/yjlion/gowebfilter/internal/classify/text"
)

func TestLoadTextScorerEmptyPathIsKeywordOnly(t *testing.T) {
	if got := loadTextScorer(""); got != nil {
		t.Fatalf("loadTextScorer(\"\") = %v, want nil", got)
	}
}

func TestLoadTextScorerMissingFileFallsBackToNil(t *testing.T) {
	got := loadTextScorer(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if got != nil {
		t.Fatalf("loadTextScorer() on a missing file = %v, want nil (fail open, not a crash)", got)
	}
}

func TestLoadTextScorerValidModelLoads(t *testing.T) {
	m := &classifytext.Model{
		Vocab:     map[string]int{"porn": 0},
		IDF:       []float64{1},
		Coef:      []float64{10},
		Intercept: -5,
	}
	path := filepath.Join(t.TempDir(), "model.json")
	if err := m.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	scorer := loadTextScorer(path)
	if scorer == nil {
		t.Fatalf("loadTextScorer() = nil, want a loaded scorer")
	}
	if score, ok := scorer.Score("porn"); !ok || score < 0.5 {
		t.Fatalf("loaded scorer.Score(\"porn\") = %v, %v, want >=0.5, true", score, ok)
	}
}

func TestLoadImageDetectorEmptyPathIsPassthrough(t *testing.T) {
	if got := loadImageDetector(""); got != nil {
		t.Fatalf("loadImageDetector(\"\") = %v, want nil", got)
	}
}

func TestLoadImageDetectorMisconfiguredPathFallsBackToNil(t *testing.T) {
	// In the default (non -tags onnx) build, any non-empty path fails to
	// load (image.ErrNotBuilt) - loadImageDetector must still fall back to
	// nil (passthrough) rather than propagating the error, matching
	// loadTextScorer's fail-open contract.
	got := loadImageDetector(filepath.Join(t.TempDir(), "model.onnx"))
	if got != nil {
		t.Fatalf("loadImageDetector() on an unbuilt/missing backend = %v, want nil", got)
	}
}
