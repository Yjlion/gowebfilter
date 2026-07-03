package main

import "testing"

func TestLoadTextScorerLoadsEmbeddedBayesModel(t *testing.T) {
	got := loadTextScorer()
	if got != nil {
		score, ok := got.Score("adult video and xxx content")
		if !ok || score <= 0 {
			t.Fatalf("embedded text scorer Score() = (%.6f, %v), want positive ok score", score, ok)
		}
		return
	}
	t.Fatal("loadTextScorer() = nil, want embedded Bayesian scorer")
}

func TestLoadImageDetectorAlwaysLoads(t *testing.T) {
	// The image classifier's model is embedded in the binary (no download,
	// no config path) - it should always load successfully.
	if got := loadImageDetector(); got == nil {
		t.Fatal("loadImageDetector() = nil, want a loaded detector (model is embedded)")
	}
}
