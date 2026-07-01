package addons

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
)

// Detection is one NSFW hit (mirrors NudeNet's detect() output: a class
// label plus confidence score).
type Detection struct {
	Class string
	Score float64
}

// ImageDetector scores image bytes for NSFW content. The ONNX/NudeNet-
// equivalent backend (project plan Phase 7) implements this; a nil
// Detector on ImageClassifier means "never NSFW" - matches the Python
// original's fail-open behavior when the nudenet package isn't available.
type ImageDetector interface {
	Detect(imageBytes []byte) ([]Detection, error)
}

// nsfwLabels are NudeNet 3.x's class labels for explicit exposure.
var nsfwLabels = map[string]bool{
	"FEMALE_GENITALIA_EXPOSED": true,
	"MALE_GENITALIA_EXPOSED":   true,
	"FEMALE_BREAST_EXPOSED":    true,
	"BUTTOCKS_EXPOSED":         true,
	"ANUS_EXPOSED":             true,
}

// minImageBytes is a cheap floor to discard genuine tracking pixels/
// spacers without decoding - real filtering is gated on pixel dimensions
// (imageTooSmall), since heavily compressed thumbnails can be only a few
// KB.
const minImageBytes = 1024

// ImageClassifier detects and blurs/blocks/checkerboards NSFW images.
// Ported from proxy/addons/image_classifier.py.
type ImageClassifier struct {
	// Detector is the optional NSFW scoring backend; nil means every image
	// passes through unmodified.
	Detector ImageDetector
}

func (ImageClassifier) Name() string { return "image_classifier" }

func isNSFW(detector ImageDetector, imageBytes []byte, threshold float64) bool {
	if detector == nil {
		return false
	}
	detections, err := detector.Detect(imageBytes)
	if err != nil {
		return false
	}
	for _, d := range detections {
		if nsfwLabels[d.Class] && d.Score >= threshold {
			return true
		}
	}
	return false
}

// blurImage heavily blurs the entire image, radius scaled to its size.
// Ported from _blur_image.
func blurImage(imageBytes []byte) []byte {
	img, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return imageBytes
	}
	b := img.Bounds()
	radius := float64(b.Dx())
	if b.Dy() < b.Dx() {
		radius = float64(b.Dy())
	}
	radius /= 8
	if radius < 12 {
		radius = 12
	}
	blurred := imaging.Blur(img, radius)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, blurred, &jpeg.Options{Quality: 80}); err != nil {
		return imageBytes
	}
	return out.Bytes()
}

// checkerboardImage replaces the image with a neutral checkerboard
// placeholder of the same size. Ported from _checkerboard.
func checkerboardImage(imageBytes []byte) []byte {
	w, h := 320, 240
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(imageBytes)); err == nil {
		w, h = cfg.Width, cfg.Height
	}
	tile := minInt(w, h) / 10
	if tile < 8 {
		tile = 8
	}
	light := color.RGBA{R: 245, G: 245, B: 240, A: 255}
	dark := color.RGBA{R: 220, G: 220, B: 212, A: 255}

	board := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(board, board.Bounds(), &image.Uniform{C: light}, image.Point{}, draw.Src)
	for y, ty := 0, 0; y < h; y, ty = y+tile, ty+1 {
		for x, tx := 0, 0; x < w; x, tx = x+tile, tx+1 {
			if (tx+ty)%2 != 0 {
				rect := image.Rect(x, y, x+tile, y+tile).Intersect(board.Bounds())
				draw.Draw(board, rect, &image.Uniform{C: dark}, image.Point{}, draw.Src)
			}
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, board); err != nil {
		return transparentGIF
	}
	return out.Bytes()
}

// transparentGIF is a 1x1 transparent GIF, used for the "block" action.
var transparentGIF = []byte{
	'G', 'I', 'F', '8', '9', 'a', 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x21,
	0xf9, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
}

// imageTooSmall reports whether the image's largest side is under
// minDimension pixels, reading only the image header (no full decode via
// image.DecodeConfig - the Go stdlib equivalent of PIL's lazy Image.open
// size read). If dimensions can't be determined, returns false so the
// image still gets classified. Ported from _too_small.
func imageTooSmall(imageBytes []byte, minDimension int) bool {
	if minDimension <= 0 {
		return false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil {
		return false
	}
	largest := cfg.Width
	if cfg.Height > largest {
		largest = cfg.Height
	}
	return largest < minDimension
}

func imageClassifierShouldFilter(host, url string, cfg models.ImageClassifierConfig) bool {
	if len(cfg.IncludeOnly) > 0 {
		return proxy.UrlInList(host, url, cfg.IncludeOnly)
	}
	if len(cfg.Exclude) > 0 {
		return !proxy.UrlInList(host, url, cfg.Exclude)
	}
	return true
}

func (ic ImageClassifier) HandleResponse(fc *proxy.FlowContext) {
	if fc.URLAllowed || fc.MitmPassthrough {
		return
	}
	policy := fc.Policy
	if policy == nil || !policy.ImageClassifier.Enabled {
		return
	}
	if fc.Response == nil {
		return
	}
	ct := fc.Response.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return
	}

	host := fc.Request.URL.Hostname()
	url := fc.Request.URL.String()
	cfg := policy.ImageClassifier
	if !imageClassifierShouldFilter(host, url, cfg) {
		return
	}

	body := fc.ResponseBody
	if len(body) < minImageBytes {
		return
	}
	if imageTooSmall(body, cfg.MinDimension) {
		return
	}
	if !isNSFW(ic.Detector, body, cfg.Threshold) {
		return
	}

	var newBody []byte
	var ctype string
	switch cfg.Action {
	case models.ImageActionCheckerboard:
		newBody, ctype = checkerboardImage(body), "image/png"
	case models.ImageActionBlock:
		newBody, ctype = transparentGIF, "image/gif"
	default: // blur
		newBody, ctype = blurImage(body), "image/jpeg"
	}

	fc.ResponseBody = newBody
	fc.Response.Header.Set("Content-Type", ctype)
	fc.Response.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
	fc.WFAction = "modified"
	fc.WFComponent = "image_classifier"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
