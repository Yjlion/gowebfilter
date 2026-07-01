//go:build !onnx

package image

import (
	"errors"

	"github.com/yjlion/gowebfilter/internal/proxy/addons"
)

// ErrNotBuilt is returned by New when a model path is configured but this
// binary was built without ONNX support (the default). Rebuild with
// `-tags onnx` (requires CGO_ENABLED=1, a C toolchain, and the onnxruntime
// shared library available at runtime - see HANDOFF.md's Phase 7 notes) to
// get a real detector.
var ErrNotBuilt = errors.New("image classifier: built without onnx support (rebuild with -tags onnx)")

// New always fails when a model is actually requested, in this build
// variant. An empty modelPath returns (nil, nil), matching
// addons.ImageClassifier's documented "nil Detector never flags anything"
// fail-open behavior for a deployment that hasn't configured Phase 7 at
// all.
func New(modelPath string) (addons.ImageDetector, error) {
	if modelPath == "" {
		return nil, nil
	}
	return nil, ErrNotBuilt
}
