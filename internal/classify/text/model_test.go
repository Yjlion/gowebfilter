package text

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenize(t *testing.T) {
	got := Tokenize("Don't PANIC! This is a test-case, 123.")
	want := []string{"don't", "panic", "this", "is", "a", "test", "case"}
	if len(got) != len(want) {
		t.Fatalf("Tokenize() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Tokenize()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func handModel() *Model {
	return &Model{
		Vocab:     map[string]int{"porn": 0, "xxx": 1, "cookie": 2, "recipe": 3},
		IDF:       []float64{1, 1, 1, 1},
		Coef:      []float64{6, 6, -1, -1},
		Intercept: -4,
	}
}

func TestScoreDistinguishesAdultFromSafe(t *testing.T) {
	m := handModel()

	adult, ok := m.Score("free porn xxx videos")
	if !ok {
		t.Fatalf("Score() ok = false, want true")
	}
	safe, ok := m.Score("my favorite cookie recipe")
	if !ok {
		t.Fatalf("Score() ok = false, want true")
	}
	if !(adult > 0.5 && safe < 0.5) {
		t.Fatalf("Score() adult=%v safe=%v, want adult>0.5 and safe<0.5", adult, safe)
	}
}

func TestScoreOutOfVocabTextIsNeutral(t *testing.T) {
	m := handModel()
	score, ok := m.Score("completely unrelated words here")
	if !ok {
		t.Fatalf("Score() ok = false, want true")
	}
	// No vocab hits -> zero vector -> z == intercept == -4 -> sigmoid very low.
	if score > 0.1 {
		t.Fatalf("Score() = %v for out-of-vocab text, want near 0", score)
	}
}

func TestScoreOnNilOrEmptyModel(t *testing.T) {
	var nilModel *Model
	if _, ok := nilModel.Score("anything"); ok {
		t.Fatalf("Score() on nil model: ok = true, want false")
	}
	empty := &Model{}
	if _, ok := empty.Score("anything"); ok {
		t.Fatalf("Score() on empty model: ok = true, want false")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	m := handModel()
	path := filepath.Join(t.TempDir(), "model.json")
	if err := m.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want, _ := m.Score("free porn xxx videos")
	got, ok := loaded.Score("free porn xxx videos")
	if !ok || got != want {
		t.Fatalf("Load()ed model Score() = %v, %v, want %v, true", got, ok, want)
	}
}

func TestLoadRejectsMismatchedLengths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	bad := &Model{Vocab: map[string]int{"a": 0, "b": 1}, IDF: []float64{1}, Coef: []float64{1, 2}}
	if err := bad.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("Load() error = nil, want a length-mismatch error")
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("Load() error = nil, want a parse error")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatalf("Load() error = nil, want a not-found error")
	}
}
