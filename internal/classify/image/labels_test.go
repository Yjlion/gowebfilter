package image

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLabelsArrayForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(path, []byte(`["FEMALE_GENITALIA_EXPOSED", "FACE_FEMALE"]`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	labels, err := loadLabels(path)
	if err != nil {
		t.Fatalf("loadLabels() error = %v", err)
	}
	if len(labels) != 2 || labels[0] != "FEMALE_GENITALIA_EXPOSED" || labels[1] != "FACE_FEMALE" {
		t.Fatalf("loadLabels() = %v, want [FEMALE_GENITALIA_EXPOSED FACE_FEMALE]", labels)
	}
}

func TestLoadLabelsMapForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(path, []byte(`{"1": "FACE_FEMALE", "0": "FEMALE_GENITALIA_EXPOSED"}`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	labels, err := loadLabels(path)
	if err != nil {
		t.Fatalf("loadLabels() error = %v", err)
	}
	if len(labels) != 2 || labels[0] != "FEMALE_GENITALIA_EXPOSED" || labels[1] != "FACE_FEMALE" {
		t.Fatalf("loadLabels() = %v, want [FEMALE_GENITALIA_EXPOSED FACE_FEMALE]", labels)
	}
}

func TestLoadLabelsMapFormRejectsGaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(path, []byte(`{"0": "A", "2": "C"}`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if _, err := loadLabels(path); err == nil {
		t.Fatalf("loadLabels() error = nil, want a missing-index error")
	}
}

func TestLoadLabelsRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if _, err := loadLabels(path); err == nil {
		t.Fatalf("loadLabels() error = nil, want a parse error")
	}
}

func TestLoadLabelsMissingFile(t *testing.T) {
	if _, err := loadLabels(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatalf("loadLabels() error = nil, want a not-found error")
	}
}
