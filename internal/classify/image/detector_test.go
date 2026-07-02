package image

import (
	"os"
	"testing"
)

// TestScoreSkipsClassifierOnZeroSkin: images with no skin must never reach
// the classifier - the score comes back as the prefilter's implicit "not
// NSFW" (0, true) without running MobileNetV2 at all. Adapted from
// privoxy-nsfw-guard's TestHybridSkinGate.
func TestScoreSkipsClassifierOnZeroSkin(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	score, ok := d.Score(encJPEG(t, synthScene(400, 300)))
	if !ok {
		t.Fatal("Score() on a zero-skin image returned ok=false, want true")
	}
	if score != 0 {
		t.Errorf("Score() on a zero-skin image = %v, want 0 (prefilter should skip the classifier)", score)
	}
}

// TestScoreRunsClassifierOnSkin: skin-heavy images must reach the
// classifier. The synthetic ellipse is not porn, so the score should stay
// low - this is exactly the false positive the model corrects over the
// bare skin heuristic. Adapted from privoxy-nsfw-guard's
// TestHybridRunsModelOnSkin.
func TestScoreRunsClassifierOnSkin(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	score, ok := d.Score(encJPEG(t, synthNude(400, 300)))
	if !ok {
		t.Fatal("Score() on a skin-heavy image returned ok=false, want true")
	}
	t.Logf("synthetic skin ellipse nsfw score: %.3f", score)
	if score >= 0.6 {
		t.Errorf("flat synthetic ellipse scored %.3f, want < 0.6 (unexpected for this model)", score)
	}
}

func TestScoreOnUndecodableReturnsNotOK(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, ok := d.Score([]byte("not an image")); ok {
		t.Error("Score() on undecodable bytes should return ok=false")
	}
}

// TestScoreRealSampleImages sanity-checks the full pipeline against real
// photos (ported from privoxy-nsfw-guard/testdata) rather than synthetic
// ellipses: a real nude photo should score clearly higher than a real
// scenic photo.
func TestScoreRealSampleImages(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	nude, err := os.ReadFile("testdata/nude.jpg")
	if err != nil {
		t.Fatalf("read testdata/nude.jpg: %v", err)
	}
	scene, err := os.ReadFile("testdata/scene.jpg")
	if err != nil {
		t.Fatalf("read testdata/scene.jpg: %v", err)
	}

	nudeScore, ok := d.Score(nude)
	if !ok {
		t.Fatal("Score(nude.jpg) returned ok=false")
	}
	sceneScore, ok := d.Score(scene)
	if !ok {
		t.Fatal("Score(scene.jpg) returned ok=false")
	}

	t.Logf("nude.jpg score=%.3f scene.jpg score=%.3f", nudeScore, sceneScore)
	if nudeScore <= sceneScore {
		t.Errorf("nude.jpg scored %.3f, scene.jpg scored %.3f - expected nude to score clearly higher", nudeScore, sceneScore)
	}
}
