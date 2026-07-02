package text

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// These cover the pure-Go pieces of Load (config parsing, label-index
// resolution, softmax) without needing a real ONNX model or onnxruntime
// session - the CGO-backed Load/Score round trip is exercised against the
// real exported model as part of this project's verification steps (see
// HANDOFF.md), not as a package unit test, mirroring how
// internal/classify/image only unit-tests its non-CGO helpers directly.

func writeConfig(t *testing.T, dir string, cfg map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadModelConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, map[string]any{
		"max_position_embeddings": 512,
		"do_lower_case":           true,
		"id2label":                map[string]string{"0": "safe", "1": "nsfw"},
		"pad_token_id":            0,
		"unk_token_id":            100,
		"cls_token_id":            101,
		"sep_token_id":            102,
	})
	cfg, err := loadModelConfig(path)
	if err != nil {
		t.Fatalf("loadModelConfig: %v", err)
	}
	if cfg.MaxPositionEmbeddings != 512 || !cfg.DoLowerCase || cfg.SepTokenID != 102 {
		t.Fatalf("loadModelConfig() = %+v, unexpected values", cfg)
	}
}

func TestLoadModelConfigRejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, map[string]any{"do_lower_case": true})
	if _, err := loadModelConfig(path); err == nil {
		t.Fatal("loadModelConfig() with no max_position_embeddings/id2label should fail, got nil error")
	}
}

func TestNsfwLabelIndexFindsCaseInsensitiveMatch(t *testing.T) {
	idx, err := nsfwLabelIndex(map[string]string{"0": "safe", "1": "NSFW"})
	if err != nil {
		t.Fatalf("nsfwLabelIndex: %v", err)
	}
	if idx != 1 {
		t.Fatalf("nsfwLabelIndex() = %d, want 1", idx)
	}
}

func TestNsfwLabelIndexAtDifferentPosition(t *testing.T) {
	// A re-export with the labels swapped must not silently keep using
	// index 1 - this is exactly the bug nsfwLabelIndex exists to prevent.
	idx, err := nsfwLabelIndex(map[string]string{"0": "nsfw", "1": "safe"})
	if err != nil {
		t.Fatalf("nsfwLabelIndex: %v", err)
	}
	if idx != 0 {
		t.Fatalf("nsfwLabelIndex() = %d, want 0", idx)
	}
}

func TestNsfwLabelIndexMissingLabelErrors(t *testing.T) {
	if _, err := nsfwLabelIndex(map[string]string{"0": "safe", "1": "spam"}); err == nil {
		t.Fatal("nsfwLabelIndex() with no nsfw label should fail, got nil error")
	}
}

func TestSoftmaxAtSumsToOneAndPicksLargerLogit(t *testing.T) {
	logits := []float32{0.1, 3.0}
	pSafe := softmaxAt(logits, 0)
	pNsfw := softmaxAt(logits, 1)
	if math.Abs(pSafe+pNsfw-1.0) > 1e-9 {
		t.Fatalf("softmaxAt probabilities sum to %v, want 1.0", pSafe+pNsfw)
	}
	if pNsfw <= pSafe {
		t.Fatalf("softmaxAt(nsfw)=%v should exceed softmaxAt(safe)=%v given logit 3.0 > 0.1", pNsfw, pSafe)
	}
}

func TestSoftmaxAtEqualLogitsSplitEvenly(t *testing.T) {
	logits := []float32{2.0, 2.0}
	if got := softmaxAt(logits, 0); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("softmaxAt() = %v, want 0.5 for equal logits", got)
	}
}

func TestLoadEmptyDirIsPassthrough(t *testing.T) {
	m, err := Load("")
	if err != nil || m != nil {
		t.Fatalf("Load(\"\") = (%v, %v), want (nil, nil)", m, err)
	}
}

func TestScoreOnNilModelReturnsNotOK(t *testing.T) {
	var m *Model
	if _, ok := m.Score("anything"); ok {
		t.Fatal("Score() on nil *Model should return ok=false")
	}
}
