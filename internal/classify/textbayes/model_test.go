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

// LDNOOBW entries are light-weight: ordinary swearing alone must stay under
// the default threshold, in any language.
func TestScoreSwearingAloneBelowDefaultThreshold(t *testing.T) {
	m := mustModel(t)
	for lang, text := range map[string]string{
		"de": "So ein Arschloch! Der Mistkerl hat mein Fahrrad geklaut, dieser Wichser. Ich bin stinksauer und gehe jetzt nach Hause.",
		"es": "¡Qué cabrón! Ese gilipollas me robó la bicicleta. Estoy muy enfadado y me voy a casa ahora mismo.",
		"en": "This fucking printer is shit. What a bullshit morning, the bastard thing jammed again before my meeting.",
		"ru": "Этот мудак опять сломал принтер, хрен знает что теперь делать. Пойду домой.",
	} {
		score, ok := m.Score(text)
		if !ok {
			t.Fatalf("%s: Score ok=false", lang)
		}
		if score >= 0.8 {
			t.Errorf("%s swearing scored %.3f, want < 0.8", lang, score)
		}
	}
}

// Dense explicit vocabulary in non-English languages now adds evidence
// (the old ASCII tokenizer saw none of it).
func TestScoreNonEnglishExplicitTextRaisesScore(t *testing.T) {
	m := mustModel(t)
	cases := []struct{ lang, neutral, explicit string }{
		{"ja", "今日は公園で散歩をして、美味しいラーメンを食べました。天気がとても良かったです。",
			"無修正のアダルト動画、フェラチオ、中出し、ぶっかけ、手コキ、潮吹きの動画を毎日更新。"},
		{"zh", "今天我们去公园散步，然后吃了很好吃的面条。天气非常好。",
			"免费三级片、色情电影、成人电影、黄色网站、口交、肛交视频每日更新。"},
		{"ko", "오늘은 공원에서 산책을 하고 맛있는 음식을 먹었습니다.",
			"무료 포르노 야동 섹스 몰카 하드코어 영상 매일 업데이트"},
		{"ru", "Сегодня мы гуляли в парке и ели вкусное мороженое.",
			"Бесплатное порно видео, ебля, минет, анальный секс, сиськи и пизда каждый день."},
	}
	for _, c := range cases {
		neutral, ok1 := m.Score(c.neutral)
		explicit, ok2 := m.Score(c.explicit)
		if !ok1 || !ok2 {
			t.Fatalf("%s: Score ok=false (%v, %v)", c.lang, ok1, ok2)
		}
		t.Logf("%s: neutral=%.3f explicit=%.3f", c.lang, neutral, explicit)
		if explicit <= neutral {
			t.Errorf("%s: explicit %.3f not above neutral %.3f", c.lang, explicit, neutral)
		}
		if neutral >= 0.8 {
			t.Errorf("%s: neutral text scored %.3f", c.lang, neutral)
		}
	}
}

func TestTokenizeUnicodeWords(t *testing.T) {
	got := tokenize("Größe ÜBER-Cool пизда")
	want := []string{"größe", "über", "cool", "пизда"}
	if len(got) != len(want) {
		t.Fatalf("tokenize = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokenize = %v, want %v", got, want)
		}
	}
}

func TestScoreUnspacedOnlyTextIsScoreable(t *testing.T) {
	m := mustModel(t)
	if _, ok := m.Score("今日はいい天気です"); !ok {
		t.Fatal("CJK-only text returned ok=false")
	}
}
