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

func TestLoadImageDetectorEmptyPathIsPassthrough(t *testing.T) {
	if got := loadImageDetector(""); got != nil {
		t.Fatalf("loadImageDetector(\"\") = %v, want nil", got)
	}
}

func TestLoadImageDetectorMisconfiguredPathFallsBackToNil(t *testing.T) {
	// A configured path that doesn't exist (no model has been provisioned
	// via `webfilter models download`) must still fall back to nil
	// (passthrough) rather than propagating the error, matching
	// loadTextScorer's fail-open contract.
	got := loadImageDetector(filepath.Join(t.TempDir(), "model.onnx"))
	if got != nil {
		t.Fatalf("loadImageDetector() on a missing model = %v, want nil", got)
	}
}
