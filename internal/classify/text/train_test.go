package text

import "testing"

func TestBuildVocabDropsRareTokensAndCapsSize(t *testing.T) {
	docs := []string{
		"apple banana apple",
		"apple cherry",
		"apple banana",
		"onceonly",
	}
	vocab, idf := BuildVocab(docs, 2, 0)
	if _, ok := vocab["onceonly"]; ok {
		t.Fatalf("BuildVocab() kept a token with df=1 under minDF=2: %v", vocab)
	}
	if _, ok := vocab["apple"]; !ok {
		t.Fatalf("BuildVocab() dropped 'apple' (df=3): %v", vocab)
	}
	if len(vocab) != len(idf) {
		t.Fatalf("BuildVocab() vocab size %d != idf size %d", len(vocab), len(idf))
	}

	capped, _ := BuildVocab(docs, 1, 1)
	if len(capped) != 1 {
		t.Fatalf("BuildVocab() with maxVocab=1 returned %d tokens, want 1", len(capped))
	}
	// "apple" has the highest document frequency (3), so it must be the
	// single token kept.
	if _, ok := capped["apple"]; !ok {
		t.Fatalf("BuildVocab() with maxVocab=1 kept %v, want {apple}", capped)
	}
}

func TestBuildVocabIsOrderIndependent(t *testing.T) {
	docsA := []string{"zebra yak", "yak apple", "apple zebra"}
	docsB := []string{"apple zebra", "zebra yak", "yak apple"}
	vocabA, idfA := BuildVocab(docsA, 1, 0)
	vocabB, idfB := BuildVocab(docsB, 1, 0)
	for tok, idxA := range vocabA {
		idxB, ok := vocabB[tok]
		if !ok || idxA != idxB {
			t.Fatalf("BuildVocab() not order-independent for %q: %d vs %d", tok, idxA, idxB)
		}
	}
	for i := range idfA {
		if idfA[i] != idfB[i] {
			t.Fatalf("BuildVocab() idf not order-independent at index %d: %v vs %v", i, idfA, idfB)
		}
	}
}

func TestTrainSeparatesLinearlySeparableData(t *testing.T) {
	// Two obviously separable 1-D clusters: label 1 clusters near x=+1,
	// label 0 clusters near x=-1.
	vectors := [][]float64{{1.0}, {0.9}, {1.1}, {-1.0}, {-0.9}, {-1.1}}
	labels := []float64{1, 1, 1, 0, 0, 0}
	opts := TrainOptions{Epochs: 500, LR: 0.5, L2: 0}

	coef, intercept := Train(vectors, labels, 1, opts)
	if coef[0] <= 0 {
		t.Fatalf("Train() coef = %v, want a positive weight (higher x -> label 1)", coef)
	}

	for i, vec := range vectors {
		z := intercept + coef[0]*vec[0]
		p := sigmoid(z)
		want := labels[i] == 1
		got := p >= 0.5
		if got != want {
			t.Fatalf("Train() misclassified vector %v: p=%v, want label %v", vec, p, labels[i])
		}
	}
}

// demoCorpus is a small, intentionally tame smoke-test corpus: label 1
// sentences merely *name* adult-content categories the way a content
// warning or site description would (not actual explicit material), label 0
// sentences are ordinary text from unrelated everyday topics. It exists to
// prove the BuildVocab -> Vectorize -> Train -> Score pipeline works
// end-to-end, not as a production-quality classifier - see
// scripts/train_text_classifier.go's doc comment and HANDOFF.md for how an
// operator should point this at a real, properly licensed labeled corpus.
var demoCorpus = []struct {
	text  string
	label float64
}{
	{"This site hosts free porn videos and xxx movies for adults only", 1},
	{"Warning: explicit pornographic content, nudity, and adult material ahead", 1},
	{"Live cam girls and nude photo galleries, hentai and erotic stories", 1},
	{"Adult content: nsfw pornography, masturbation stories and orgasm tips", 1},
	{"This escort service advertises adult companionship and erotic massage", 1},
	{"onlyfans creators post nude and nsfw pornographic content daily", 1},
	{"Gangbang and threesome pornographic videos streamed free in xxx quality", 1},
	{"Hentai anime pornography and nude cartoon galleries updated daily", 1},
	{"Our chocolate chip cookie recipe uses brown sugar and vanilla extract", 0},
	{"The quarterly financial report shows steady revenue growth this year", 0},
	{"Local weather forecast predicts light rain and mild temperatures today", 0},
	{"The soccer team won their match after a late second-half goal", 0},
	{"This gardening guide explains how to prune roses in early spring", 0},
	{"Our new laptop review covers battery life and display quality", 0},
	{"Traveling to Japan: a guide to trains, temples, and local food", 0},
	{"The school district announced a new schedule for the fall semester", 0},
	{"This tutorial explains how to bake sourdough bread at home", 0},
	{"The museum's new exhibit features Renaissance paintings and sculpture", 0},
	{"A beginner's guide to budgeting and saving for retirement", 0},
	{"The hiking trail offers scenic mountain views and a waterfall", 0},
}

func TestTrainModelEndToEndOnDemoCorpus(t *testing.T) {
	texts := make([]string, len(demoCorpus))
	labels := make([]float64, len(demoCorpus))
	for i, row := range demoCorpus {
		texts[i] = row.text
		labels[i] = row.label
	}

	opts := TrainOptions{MaxVocab: 200, MinDF: 1, Epochs: 500, LR: 0.5, L2: 0.001}
	m, err := TrainModel(texts, labels, opts)
	if err != nil {
		t.Fatalf("TrainModel() error = %v", err)
	}

	correct := 0
	for i, row := range demoCorpus {
		score, ok := m.Score(row.text)
		if !ok {
			t.Fatalf("Score() ok = false for row %d", i)
		}
		if (score >= 0.5) == (row.label == 1) {
			correct++
		}
	}
	if correct != len(demoCorpus) {
		t.Fatalf("trained model got %d/%d training rows right, want all correct on this tiny separable demo corpus", correct, len(demoCorpus))
	}

	// A held-out sentence using adult vocabulary not seen verbatim in
	// training should still score high, and an unrelated held-out sentence
	// should score low - checking the model generalizes via shared tokens
	// rather than just memorizing exact training sentences.
	adultScore, _ := m.Score("Free adult pornographic xxx videos and nude photos")
	safeScore, _ := m.Score("A simple guide to planting tomatoes in your garden")
	if !(adultScore > 0.5 && safeScore < 0.5) {
		t.Fatalf("held-out scores: adult=%v safe=%v, want adult>0.5 and safe<0.5", adultScore, safeScore)
	}
}

func TestTrainModelRejectsEmptyOrMismatchedInput(t *testing.T) {
	if _, err := TrainModel(nil, nil, DefaultTrainOptions()); err == nil {
		t.Fatalf("TrainModel() with empty corpus: error = nil, want error")
	}
	if _, err := TrainModel([]string{"a"}, []float64{1, 0}, DefaultTrainOptions()); err == nil {
		t.Fatalf("TrainModel() with mismatched lengths: error = nil, want error")
	}
}
