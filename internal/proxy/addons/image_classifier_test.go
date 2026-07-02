package addons_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
)

// fakeDetector always reports the given score for every image.
type fakeDetector struct {
	score float64
	ok    bool
}

func (d fakeDetector) Score(imageBytes []byte) (float64, bool) {
	return d.score, d.ok
}

// testJPEG builds a solid-color JPEG of the given size, padded past the
// 1KB floor with irrelevant EXIF-like comment bytes if needed.
func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode test jpeg: %v", err)
	}
	return buf.Bytes()
}

func newImageFlow(t *testing.T, body []byte) (*models.Policy, *http.Response) {
	t.Helper()
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"image/jpeg"}}}
	policy := models.NewPolicy()
	policy.ImageClassifier = models.NewImageClassifierConfig()
	policy.ImageClassifier.Enabled = true
	return &policy, resp
}

func TestImageClassifierBlursNSFWImage(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 200, 200)
	policy, resp := newImageFlow(t, body)
	fc := newFlow(t, rt, "http://example.com/pic.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.9, ok: true}}
	ic.HandleResponse(fc)

	if fc.WFAction != "modified" || fc.WFComponent != "image_classifier" {
		t.Fatalf("WFAction/WFComponent = %q/%q", fc.WFAction, fc.WFComponent)
	}
	if fc.Response.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg (blur re-encodes as jpeg)", fc.Response.Header.Get("Content-Type"))
	}
	if bytes.Equal(fc.ResponseBody, body) {
		t.Error("expected the image body to change after blurring")
	}
	// The blurred output must still decode as a valid image of the same size.
	img, _, err := image.Decode(bytes.NewReader(fc.ResponseBody))
	if err != nil {
		t.Fatalf("decode blurred image: %v", err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 200 {
		t.Errorf("blurred image size = %v, want 200x200", img.Bounds())
	}
}

func TestImageClassifierCheckerboardAction(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 150, 100)
	policy, resp := newImageFlow(t, body)
	policy.ImageClassifier.Action = models.ImageActionCheckerboard
	fc := newFlow(t, rt, "http://example.com/pic.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.9, ok: true}}
	ic.HandleResponse(fc)

	if fc.Response.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", fc.Response.Header.Get("Content-Type"))
	}
	img, _, err := image.Decode(bytes.NewReader(fc.ResponseBody))
	if err != nil {
		t.Fatalf("decode checkerboard image: %v", err)
	}
	if img.Bounds().Dx() != 150 || img.Bounds().Dy() != 100 {
		t.Errorf("checkerboard size = %v, want 150x100", img.Bounds())
	}
}

func TestImageClassifierBlockAction(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 200, 200)
	policy, resp := newImageFlow(t, body)
	policy.ImageClassifier.Action = models.ImageActionBlock
	fc := newFlow(t, rt, "http://example.com/pic.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.9, ok: true}}
	ic.HandleResponse(fc)

	if fc.Response.Header.Get("Content-Type") != "image/gif" {
		t.Errorf("Content-Type = %q, want image/gif", fc.Response.Header.Get("Content-Type"))
	}
	if len(fc.ResponseBody) == 0 {
		t.Error("expected a non-empty transparent GIF body")
	}
}

func TestImageClassifierSkipsBelowThreshold(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 200, 200)
	policy, resp := newImageFlow(t, body)
	policy.ImageClassifier.Threshold = 0.8
	fc := newFlow(t, rt, "http://example.com/pic.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.5, ok: true}} // below threshold
	ic.HandleResponse(fc)

	if !bytes.Equal(fc.ResponseBody, body) {
		t.Error("did not expect modification for a score below threshold")
	}
}

func TestImageClassifierSkipsWhenNotOK(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 200, 200)
	policy, resp := newImageFlow(t, body)
	fc := newFlow(t, rt, "http://example.com/pic.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.99, ok: false}} // scoring failed/unavailable
	ic.HandleResponse(fc)

	if !bytes.Equal(fc.ResponseBody, body) {
		t.Error("did not expect modification when the detector reports ok=false")
	}
}

func TestImageClassifierSkipsSmallImages(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 50, 50) // under default min_dimension of 100
	policy, resp := newImageFlow(t, body)
	fc := newFlow(t, rt, "http://example.com/icon.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.99, ok: true}}
	ic.HandleResponse(fc)

	if !bytes.Equal(fc.ResponseBody, body) {
		t.Error("expected small (icon-sized) images to be skipped regardless of detection")
	}
}

func TestImageClassifierSkipsTinyByteFloor(t *testing.T) {
	rt := newTestRuntime(t)
	body := []byte{0xFF, 0xD8, 0xFF} // way under the 1KB floor, not even valid
	policy, resp := newImageFlow(t, body)
	fc := newFlow(t, rt, "http://example.com/pixel.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.99, ok: true}}
	ic.HandleResponse(fc)

	if !bytes.Equal(fc.ResponseBody, body) {
		t.Error("expected sub-1KB bodies to be skipped before any decode is attempted")
	}
}

func TestImageClassifierNilDetectorNeverBlocks(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 200, 200)
	policy, resp := newImageFlow(t, body)
	fc := newFlow(t, rt, "http://example.com/pic.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	addons.ImageClassifier{}.HandleResponse(fc) // no Detector wired

	if !bytes.Equal(fc.ResponseBody, body) {
		t.Error("expected a nil Detector to never modify the image")
	}
}

func TestImageClassifierDisabledIsNoop(t *testing.T) {
	rt := newTestRuntime(t)
	body := testJPEG(t, 200, 200)
	policy, resp := newImageFlow(t, body)
	policy.ImageClassifier.Enabled = false
	fc := newFlow(t, rt, "http://example.com/pic.jpg")
	fc.Response = resp
	fc.ResponseBody = body
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.99, ok: true}}
	ic.HandleResponse(fc)

	if !bytes.Equal(fc.ResponseBody, body) {
		t.Error("did not expect any effect when image_classifier is disabled")
	}
}
