// Package image implements the optional ONNX-backed NSFW detector for
// internal/proxy/addons.ImageClassifier (project plan Phase 7). New is the
// entry point both build variants share:
//
//   - Built with -tags onnx (requires CGO_ENABLED=1, a C toolchain, and the
//     onnxruntime shared library available at runtime): loads a YOLOv8-style
//     ONNX object-detection model (the export format NudeNet v3's own
//     "*n.onnx" checkpoints use) via github.com/yalue/onnxruntime_go and
//     returns a real addons.ImageDetector.
//   - Built without it (the default): New returns ErrNotBuilt whenever a
//     model path is actually configured, so a misconfigured deployment gets
//     a clear, actionable error instead of silent passthrough.
//
// See HANDOFF.md's Phase 7 notes for why the onnx-tagged path is not
// build-verified in this project's dev sandbox (no C toolchain available)
// even though it's implemented and its non-CGO helpers (this file,
// preprocess.go, decode.go) are unit-tested directly.
package image

import (
	"encoding/json"
	"fmt"
	"os"
)

// loadLabels reads a class-index-ordered label list from path, accepting
// either a JSON array (["FEMALE_GENITALIA_EXPOSED", ...], index = position)
// or a JSON object keyed by decimal string index ({"0": "...", "1": "..."})
// - the two shapes real-world YOLOv8 export tooling (e.g. Ultralytics'
// data.yaml-derived metadata) commonly produces. The result is dense and
// ordered by index, suitable for direct use as decodeYOLOv8's labels
// argument.
func loadLabels(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read label file: %w", err)
	}

	var asArray []string
	if err := json.Unmarshal(data, &asArray); err == nil {
		return asArray, nil
	}

	var asMap map[string]string
	if err := json.Unmarshal(data, &asMap); err != nil {
		return nil, fmt.Errorf("parse label file %s: not a JSON array or {index: label} object", path)
	}
	out := make([]string, len(asMap))
	seen := make([]bool, len(asMap))
	for key, label := range asMap {
		var idx int
		if _, err := fmt.Sscanf(key, "%d", &idx); err != nil || idx < 0 || idx >= len(asMap) {
			return nil, fmt.Errorf("parse label file %s: key %q is not a valid index in [0,%d)", path, key, len(asMap))
		}
		out[idx] = label
		seen[idx] = true
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("parse label file %s: missing index %d", path, i)
		}
	}
	return out, nil
}
