package addons_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"regexp"
	"strconv"
	"strings"
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

// newInlineFlow builds a text-type response carrying body for the inline
// data-URI scanning tests.
func newInlineFlow(t *testing.T, contentType string, body []byte) (*models.Policy, *http.Response) {
	t.Helper()
	resp := &http.Response{Header: http.Header{"Content-Type": []string{contentType}}}
	policy := models.NewPolicy()
	policy.ImageClassifier = models.NewImageClassifierConfig()
	policy.ImageClassifier.Enabled = true
	return &policy, resp
}

func TestImageClassifierReplacesInlineNSFWDataURI(t *testing.T) {
	rt := newTestRuntime(t)
	big := base64.StdEncoding.EncodeToString(testJPEG(t, 200, 200))
	icon := base64.StdEncoding.EncodeToString(testJPEG(t, 50, 50)) // under min_dimension
	html := `<html><body>` +
		`<img src="data:image/jpeg;base64,` + big + `">` +
		`<img src="data:image/jpeg;base64,` + icon + `">` +
		`</body></html>`
	policy, resp := newInlineFlow(t, "text/html; charset=UTF-8", []byte(html))
	policy.ImageClassifier.Action = models.ImageActionCheckerboard
	fc := newFlow(t, rt, "http://example.com/search?q=x")
	fc.Response = resp
	fc.ResponseBody = []byte(html)
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.9, ok: true}}
	ic.HandleResponse(fc)

	if fc.WFAction != "modified" || fc.WFComponent != "image_classifier" {
		t.Fatalf("WFAction/WFComponent = %q/%q", fc.WFAction, fc.WFComponent)
	}
	got := string(fc.ResponseBody)
	if strings.Contains(got, big) {
		t.Error("expected the large NSFW inline image to be replaced")
	}
	if !strings.Contains(got, icon) {
		t.Error("expected the icon-sized inline image to be left intact")
	}
	if fc.Response.Header.Get("Content-Type") != "text/html; charset=UTF-8" {
		t.Errorf("Content-Type = %q, want the page's own type preserved", fc.Response.Header.Get("Content-Type"))
	}
	if cl := fc.Response.Header.Get("Content-Length"); cl != strconv.Itoa(len(fc.ResponseBody)) {
		t.Errorf("Content-Length = %q, want %d", cl, len(fc.ResponseBody))
	}

	// The replacement must be a decodable PNG data URI of the original size.
	m := regexp.MustCompile(`data:image/png;base64,([A-Za-z0-9+/=]+)`).FindStringSubmatch(got)
	if m == nil {
		t.Fatal("expected a data:image/png replacement URI in the body")
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("decode replacement base64: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode replacement image: %v", err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 200 {
		t.Errorf("replacement size = %v, want 200x200", img.Bounds())
	}
}

func TestImageClassifierReplacesEscapedInlineDataURI(t *testing.T) {
	rt := newTestRuntime(t)
	b64 := base64.StdEncoding.EncodeToString(testJPEG(t, 200, 200))
	// Google's inline scripts escape `=` padding as \x3d; JSON escapes `/`
	// as \/ - the scanner must see through both.
	escaped := strings.NewReplacer("/", "\\/", "=", "\\x3d").Replace(b64)
	js := `var s='data:image\/jpeg;base64,` + escaped + `';`
	policy, resp := newInlineFlow(t, "text/javascript; charset=UTF-8", []byte(js))
	policy.ImageClassifier.Action = models.ImageActionBlock
	fc := newFlow(t, rt, "http://example.com/async.js")
	fc.Response = resp
	fc.ResponseBody = []byte(js)
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.9, ok: true}}
	ic.HandleResponse(fc)

	if fc.WFAction != "modified" {
		t.Fatalf("WFAction = %q, want modified", fc.WFAction)
	}
	got := string(fc.ResponseBody)
	if strings.Contains(got, escaped) {
		t.Error("expected the escaped NSFW inline image to be replaced")
	}
	if !strings.Contains(got, "data:image/gif;base64,") {
		t.Error("expected a transparent-GIF replacement URI for the block action")
	}
	// The surrounding JS string syntax must survive the splice.
	if !strings.HasPrefix(got, "var s='data:image") || !strings.HasSuffix(got, "';") {
		t.Errorf("surrounding JS was damaged: %q", got[:min(40, len(got))])
	}
}

func TestImageClassifierInlineBelowThresholdUntouched(t *testing.T) {
	rt := newTestRuntime(t)
	html := `<img src="data:image/jpeg;base64,` +
		base64.StdEncoding.EncodeToString(testJPEG(t, 200, 200)) + `">`
	policy, resp := newInlineFlow(t, "text/html", []byte(html))
	policy.ImageClassifier.Threshold = 0.8
	fc := newFlow(t, rt, "http://example.com/page")
	fc.Response = resp
	fc.ResponseBody = []byte(html)
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.5, ok: true}}
	ic.HandleResponse(fc)

	if string(fc.ResponseBody) != html {
		t.Error("did not expect modification for an inline image below threshold")
	}
	if fc.WFAction != "" {
		t.Errorf("WFAction = %q, want unset", fc.WFAction)
	}
}

func TestImageClassifierInlineIgnoresNonScannableContentType(t *testing.T) {
	rt := newTestRuntime(t)
	body := `data:image/jpeg;base64,` +
		base64.StdEncoding.EncodeToString(testJPEG(t, 200, 200))
	policy, resp := newInlineFlow(t, "application/octet-stream", []byte(body))
	fc := newFlow(t, rt, "http://example.com/blob")
	fc.Response = resp
	fc.ResponseBody = []byte(body)
	fc.Policy = policy

	ic := addons.ImageClassifier{Detector: fakeDetector{score: 0.9, ok: true}}
	ic.HandleResponse(fc)

	if string(fc.ResponseBody) != body {
		t.Error("did not expect scanning inside non-text content types")
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
