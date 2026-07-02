// Package image implements the NSFW image classifier for
// internal/proxy/addons.ImageClassifier: GantMan/nsfw_model (MobileNetV2,
// MIT-licensed - https://github.com/GantMan/nsfw_model), embedded directly
// in the binary (model.bin, fp16-quantized weights + a flattened op list -
// see scripts/nsfw-model/convert.py) and executed by a from-scratch pure-Go
// inference engine (nn.go). No CGO, no ONNX Runtime, no model download -
// this package works immediately after `go build`.
//
// Ported from github.com/Yjlion/privoxy-nsfw-guard (same author,
// MIT-licensed): the model conversion pipeline, the inference engine, and
// the skin-region prefilter (skinprefilter.go) are the same design,
// verified there against real onnxruntime output (max class-probability
// diff <=0.018, same argmax - see model_test.go's ported fixture test).
package image

import (
	"bytes"
	_ "embed"
	"fmt"
	stdimage "image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sync"

	xdraw "golang.org/x/image/draw"

	"github.com/yjlion/gowebfilter/internal/proxy/addons"
)

//go:embed model.bin
var modelBlob []byte

var (
	modelOnce sync.Once
	theModel  *nnModel
	modelErr  error
)

// classifier returns the embedded model, loading it on first use.
func classifier() (*nnModel, error) {
	modelOnce.Do(func() {
		theModel, modelErr = loadNNModel(modelBlob)
	})
	return theModel, modelErr
}

// Scores holds GantMan/nsfw_model's five per-class probabilities.
type Scores struct {
	Drawings float64
	Hentai   float64
	Neutral  float64
	Porn     float64
	Sexy     float64
}

// sexyWeight weights the "sexy" class (revealing but not explicit) in the
// combined NSFW score - matches privoxy-nsfw-guard's own default.
const sexyWeight = 0.5

// nsfw combines the unsafe classes into one score. Drawings and neutral are
// safe; sexy is weighted down since it's revealing rather than explicit.
func (s Scores) nsfw() float64 {
	return s.Porn + s.Hentai + sexyWeight*s.Sexy
}

// predict classifies img. The image is bilinearly resized to 224x224 and
// fed as [0,1] RGB in NCHW; the graph does its own [-1,1] normalization.
func predict(m *nnModel, img stdimage.Image) (Scores, error) {
	const dim = 224
	resized := stdimage.NewRGBA(stdimage.Rect(0, 0, dim, dim))
	xdraw.BiLinear.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Src, nil)

	x := newTensor(1, 3, dim, dim)
	plane := dim * dim
	for y := 0; y < dim; y++ {
		row := resized.Pix[y*resized.Stride:]
		for px := 0; px < dim; px++ {
			i := y*dim + px
			x.Data[i] = float32(row[px*4]) / 255
			x.Data[plane+i] = float32(row[px*4+1]) / 255
			x.Data[2*plane+i] = float32(row[px*4+2]) / 255
		}
	}

	out, err := m.Run(x)
	if err != nil {
		return Scores{}, err
	}
	if len(out.Data) < 5 {
		return Scores{}, fmt.Errorf("model returned %d values, want 5", len(out.Data))
	}
	return Scores{
		Drawings: float64(out.Data[0]),
		Hentai:   float64(out.Data[1]),
		Neutral:  float64(out.Data[2]),
		Porn:     float64(out.Data[3]),
		Sexy:     float64(out.Data[4]),
	}, nil
}

// prefilterSkinRatio is the minimum skin-region ratio (see
// skinprefilter.go's Analyze) below which Score skips the classifier
// entirely - matches privoxy-nsfw-guard's "hybrid" default. Logos,
// screenshots, scenery and most product shots have ~0% skin and never pay
// for inference.
const prefilterSkinRatio = 0.07

// detector implements addons.ImageDetector via the embedded classifier.
type detector struct {
	mu sync.Mutex // classifier() itself is safe for concurrent use, but Run allocates per-call - serializing keeps peak memory predictable under concurrent requests
}

// New returns a ready-to-use addons.ImageDetector backed by the embedded
// GantMan/nsfw_model classifier. It cannot fail in a normal build (the
// model is compiled in); an error here means a corrupt build.
func New() (addons.ImageDetector, error) {
	if _, err := classifier(); err != nil {
		return nil, fmt.Errorf("image classifier: load embedded model: %w", err)
	}
	return &detector{}, nil
}

// Score implements addons.ImageDetector. It first runs a cheap skin-region
// heuristic (skinprefilter.go); only images with a meaningful skin ratio
// reach the MobileNetV2 classifier, which then decides the final score.
func (d *detector) Score(imageBytes []byte) (float64, bool) {
	img, _, err := stdimage.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return 0, false
	}

	if analyzeSkin(img).SkinRatio < prefilterSkinRatio {
		return 0, true
	}

	m, err := classifier()
	if err != nil {
		return 0, false
	}

	d.mu.Lock()
	scores, err := predict(m, img)
	d.mu.Unlock()
	if err != nil {
		return 0, false
	}
	return scores.nsfw(), true
}
