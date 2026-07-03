package textbayes

import (
	"math"
	"testing"
)

func TestTokenizeNormalizesText(t *testing.T) {
	got := tokenize("Adult-content, XXX, and CAM girls!")
	want := []string{"adult", "content", "xxx", "and", "cam", "girls"}
	if len(got) != len(want) {
		t.Fatalf("tokenize length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokenize()[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestTokenizeNormalizesCommonPluralAdultTerms(t *testing.T) {
	got := normalizePhrase("adult videos and live webcams")
	want := "adult video and live webcam"
	if got != want {
		t.Fatalf("normalizePhrase = %q, want %q", got, want)
	}
}

func TestNewRejectsInvalidData(t *testing.T) {
	if _, err := newFromData(modelData{}); err == nil {
		t.Fatal("newFromData() with empty data should fail")
	}
}

func TestDuplicateSourcesMergeByNormalizedFeature(t *testing.T) {
	m, err := newFromData(modelData{
		AdultPrior: 0.01,
		SafePrior:  0.99,
		AdultTotal: 100,
		SafeTotal:  100,
		Features: []featureData{
			{Text: "Adult Content", Adult: 5, Safe: 1},
			{Text: "adult-content", Adult: 8, Safe: 1},
		},
	})
	if err != nil {
		t.Fatalf("newFromData: %v", err)
	}
	if len(m.features) != 1 {
		t.Fatalf("len(features) = %d, want 1", len(m.features))
	}
}

func TestScoreNeutralTextBelowDefaultThreshold(t *testing.T) {
	m := mustModel(t)
	score, ok := m.Score("Welcome to our gardening club. Today we discuss weather, soil, compost, and spring flowers.")
	if !ok {
		t.Fatal("Score() returned ok=false for non-empty neutral text")
	}
	if score >= 0.8 {
		t.Fatalf("neutral score = %.6f, want below default threshold", score)
	}
}

func TestScoreAdultFixtureAboveDefaultThreshold(t *testing.T) {
	m := mustModel(t)
	score, ok := m.Score("This page advertises adult video galleries, live sex webcam shows, porn video clips, and xxx content.")
	if !ok {
		t.Fatal("Score() returned ok=false")
	}
	if score < 0.8 {
		t.Fatalf("adult score = %.6f, want >= 0.8", score)
	}
}

func TestScoreCommonAdultWordingAboveDefaultThreshold(t *testing.T) {
	m := mustModel(t)
	score, ok := m.Score("Browse free adult videos, live webcams, private cam shows, nude pics, and sexy videos.")
	if !ok {
		t.Fatal("Score() returned ok=false")
	}
	if score < 0.8 {
		t.Fatalf("common adult score = %.6f, want >= 0.8", score)
	}
}

func TestScoreMonotonicWithMoreAdultEvidence(t *testing.T) {
	m := mustModel(t)
	one, ok := m.Score("This page mentions nsfw content.")
	if !ok {
		t.Fatal("Score() returned ok=false")
	}
	many, ok := m.Score("This page mentions nsfw content, adult video, porn video, and live sex webcam shows.")
	if !ok {
		t.Fatal("Score() returned ok=false")
	}
	if many <= one {
		t.Fatalf("score with more evidence = %.6f, want > %.6f", many, one)
	}
}

func TestEmptyInputReturnsNotOK(t *testing.T) {
	m := mustModel(t)
	if score, ok := m.Score("   !!!   "); ok || math.Abs(score) > 0 {
		t.Fatalf("Score(empty) = (%.6f, %v), want (0, false)", score, ok)
	}
}

func TestNilModelReturnsNotOK(t *testing.T) {
	var m *Model
	if score, ok := m.Score("adult video"); ok || score != 0 {
		t.Fatalf("nil Score() = (%.6f, %v), want (0, false)", score, ok)
	}
}

func mustModel(t *testing.T) *Model {
	t.Helper()
	m, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}
