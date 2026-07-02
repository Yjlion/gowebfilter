package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTextScorerEmptyPathIsKeywordOnly(t *testing.T) {
	if got := loadTextScorer(""); got != nil {
		t.Fatalf("loadTextScorer(\"\") = %v, want nil", got)
	}
}

func TestLoadTextScorerMissingDirFallsBackToNil(t *testing.T) {
	got := loadTextScorer(filepath.Join(t.TempDir(), "does-not-exist-model-dir"))
	if got != nil {
		t.Fatalf("loadTextScorer() on a missing model dir = %v, want nil (fail open, not a crash)", got)
	}
}

func TestLoadTextScorerStaleJSONPathWarnsAndFallsBackToNil(t *testing.T) {
	// text_classifier_model_path used to point at a single TF-IDF JSON
	// sidecar; it now points at a model directory. A leftover ".json" path
	// from that older config should fail open with a specific, actionable
	// warning rather than a generic load error.
	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got := loadTextScorer(path)
	if got != nil {
		t.Fatalf("loadTextScorer() on a stale .json path = %v, want nil", got)
	}
}

func TestLoadImageDetectorAlwaysLoads(t *testing.T) {
	// The image classifier's model is embedded in the binary (no download,
	// no config path) - it should always load successfully.
	if got := loadImageDetector(); got == nil {
		t.Fatal("loadImageDetector() = nil, want a loaded detector (model is embedded)")
	}
}
