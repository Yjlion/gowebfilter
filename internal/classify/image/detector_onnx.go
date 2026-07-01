//go:build onnx

package image

import (
	"bytes"
	"fmt"
	stdimage "image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/yjlion/gowebfilter/internal/proxy/addons"
)

// This file is only compiled with `-tags onnx` (CGO_ENABLED=1 and a C
// toolchain required - see detector_stub.go's doc comment and HANDOFF.md's
// Phase 7 notes). It has not been build-verified in this project's dev
// sandbox, which has neither a C compiler nor the onnxruntime shared
// library available; the model-agnostic preprocessing/decoding it calls
// into (letterbox, toCHWFloat, decodeYOLOv8, loadLabels) is unit-tested
// directly and does not depend on this build tag.

var (
	onnxInitOnce sync.Once
	onnxInitErr  error
)

// ensureEnvironment initializes onnxruntime's global environment exactly
// once per process. ONNXRUNTIME_SHARED_LIBRARY, if set, points at the
// onnxruntime.dll/.so/.dylib to dynamically load (onnxruntime_go does not
// link against it at compile time); otherwise the library's own
// platform-specific default search path is used.
func ensureEnvironment() error {
	onnxInitOnce.Do(func() {
		if libPath := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY"); libPath != "" {
			ort.SetSharedLibraryPath(libPath)
		}
		onnxInitErr = ort.InitializeEnvironment()
	})
	return onnxInitErr
}

// detector is an onnxruntime-backed addons.ImageDetector for a YOLOv8-style
// ONNX object-detection export (the format NudeNet v3's own "*n.onnx"
// checkpoints use). Its output-tensor interpretation (decodeYOLOv8) is
// model-agnostic; class labels and count come entirely from a sidecar
// label file next to the model (see loadLabels), so this code makes no
// assumption about NudeNet's specific class list or ordering.
type detector struct {
	// onnxruntime does not document AdvancedSession.Run as safe for
	// concurrent use from multiple goroutines sharing the same
	// input/output tensors, so Detect serializes calls. Acceptable here:
	// NSFW scanning only runs on image/* responses, already a small
	// fraction of proxied traffic.
	mu sync.Mutex

	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]

	size       int // square input side length (both width and height)
	numClasses int
	numAnchors int
	labels     []string
}

// New loads modelPath (a YOLOv8-style ONNX export) plus a sibling label
// file - modelPath with its extension replaced by ".labels.json" - and
// returns a ready-to-use addons.ImageDetector. An empty modelPath returns
// (nil, nil): passthrough, matching addons.ImageClassifier's fail-open
// contract for a deployment that hasn't configured Phase 7 at all.
//
// The label file must be a JSON array or {index: label} object (see
// loadLabels) whose length matches the model's output class count exactly
// - this is deliberately strict, since a silently-mismatched label file
// would attach the wrong class name to a real detection.
func New(modelPath string) (addons.ImageDetector, error) {
	if modelPath == "" {
		return nil, nil
	}
	if err := ensureEnvironment(); err != nil {
		return nil, fmt.Errorf("image classifier: initialize onnxruntime: %w", err)
	}

	labelsPath := strings.TrimSuffix(modelPath, filepath.Ext(modelPath)) + ".labels.json"
	labels, err := loadLabels(labelsPath)
	if err != nil {
		return nil, fmt.Errorf("image classifier: %w", err)
	}

	inputInfo, outputInfo, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("image classifier: inspect model %s: %w", modelPath, err)
	}
	if len(inputInfo) != 1 || len(outputInfo) != 1 {
		return nil, fmt.Errorf("image classifier: model %s must have exactly 1 input and 1 output tensor, has %d and %d",
			modelPath, len(inputInfo), len(outputInfo))
	}
	if inputInfo[0].DataType != ort.TensorElementDataTypeFloat {
		return nil, fmt.Errorf("image classifier: input %q has element type %s, want float32",
			inputInfo[0].Name, inputInfo[0].DataType)
	}
	if outputInfo[0].DataType != ort.TensorElementDataTypeFloat {
		return nil, fmt.Errorf("image classifier: output %q has element type %s, want float32",
			outputInfo[0].Name, outputInfo[0].DataType)
	}

	inDims := concreteDims(inputInfo[0].Dimensions)
	if len(inDims) != 4 || inDims[1] != 3 || inDims[2] != inDims[3] {
		return nil, fmt.Errorf("image classifier: input shape %v is not the expected (1,3,size,size)", inDims)
	}
	size := int(inDims[2])

	outDims := concreteDims(outputInfo[0].Dimensions)
	if len(outDims) != 3 || outDims[1] <= 4 {
		return nil, fmt.Errorf("image classifier: output shape %v is not the expected (1,4+numClasses,numAnchors)", outDims)
	}
	numClasses := int(outDims[1]) - 4
	numAnchors := int(outDims[2])
	if numClasses != len(labels) {
		return nil, fmt.Errorf("image classifier: model has %d output classes but label file %s has %d entries",
			numClasses, labelsPath, len(labels))
	}

	inputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, int64(size), int64(size)))
	if err != nil {
		return nil, fmt.Errorf("image classifier: allocate input tensor: %w", err)
	}
	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(4+numClasses), int64(numAnchors)))
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("image classifier: allocate output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{inputInfo[0].Name}, []string{outputInfo[0].Name},
		[]ort.Value{inputTensor}, []ort.Value{outputTensor}, nil)
	if err != nil {
		inputTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("image classifier: create session for %s: %w", modelPath, err)
	}

	return &detector{
		session:    session,
		input:      inputTensor,
		output:     outputTensor,
		size:       size,
		numClasses: numClasses,
		numAnchors: numAnchors,
		labels:     labels,
	}, nil
}

// concreteDims copies dims, replacing any non-positive entry (a dynamic
// axis, almost always the batch dimension) with 1. Spatial/channel
// dimensions are expected to already be concrete for a NudeNet-style
// fixed-input-size export; New rejects the shape afterward if they aren't
// what's expected rather than guessing at them here.
func concreteDims(dims ort.Shape) []int64 {
	out := make([]int64, len(dims))
	for i, d := range dims {
		if d <= 0 {
			d = 1
		}
		out[i] = d
	}
	return out
}

// Detect implements addons.ImageDetector.
func (d *detector) Detect(imageBytes []byte) ([]addons.Detection, error) {
	img, _, err := stdimage.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("image classifier: decode image: %w", err)
	}
	square := letterbox(img, d.size)
	chw := toCHWFloat(square, d.size)

	d.mu.Lock()
	defer d.mu.Unlock()
	copy(d.input.GetData(), chw)
	if err := d.session.Run(); err != nil {
		return nil, fmt.Errorf("image classifier: run inference: %w", err)
	}
	return decodeYOLOv8(d.output.GetData(), d.numClasses, d.numAnchors, d.labels, 0.1), nil
}
