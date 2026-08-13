package image

import (
	"image/color"
	"os"
	"testing"

	"github.com/yjlion/gowebfilter/internal/webptest"
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

// TestScoreDecodesWebP guards the golang.org/x/image/webp registration. Before
// it, a WebP body failed stdimage.Decode and Score returned ok=false, which
// ImageClassifier reads as "not NSFW" - so every NSFW WebP passed through
// unfiltered. WebP is the format Google Images and most CDNs serve today, so
// that was the majority of real thumbnail traffic.
//
// The fixture is a flat skin tone, which puts the skin ratio at 1.0 and so
// carries the decoded pixels all the way through the prefilter into
// MobileNetV2 - if the WebP decoder were missing, or wired up but producing
// garbage, this would not reach the classifier at all.
func TestScoreDecodesWebP(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	skin := color.RGBA{R: 241, G: 194, B: 125, A: 255}
	score, ok := d.Score(webptest.Flat(240, 180, skin))
	if !ok {
		t.Fatal("Score() on a WebP returned ok=false - is golang.org/x/image/webp still imported by detector.go?")
	}
	if score == 0 {
		t.Error("Score() = 0 on an all-skin image, want the classifier to have run")
	}
	t.Logf("flat skin-tone webp nsfw score: %.3f", score)
}
