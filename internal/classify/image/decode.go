package image

import "github.com/yjlion/gowebfilter/internal/proxy/addons"

// decodeYOLOv8 interprets a raw Ultralytics YOLOv8-style ONNX
// object-detection output tensor - shape (1, 4+numClasses, numAnchors),
// flattened row-major, rows 0-3 holding box xywh (unused here, since
// addons.Detection carries no bounding box) and rows 4..4+numClasses
// holding per-class confidence already in [0,1] (YOLOv8 applies sigmoid
// internally, unlike v5/v7's separate objectness row) - into one
// addons.Detection per anchor for its highest-scoring class, provided that
// score clears minScore.
//
// minScore is a low floor purely to bound how many detections come back;
// the addon layer (addons.ImageClassifier's isNSFW) applies the real,
// policy-configured threshold on top of whatever this returns, so passing a
// low floor here (e.g. 0.1) is intentional - it must never filter out
// anything the policy threshold would have accepted.
//
// This is the well-documented, model-agnostic half of decoding a NudeNet-
// v3-style export: it does not depend on NudeNet's specific class count or
// label order (those come from the labels slice, itself loaded from a
// sidecar file alongside the .onnx model - see loadLabels), only on the
// standard YOLOv8 export tensor layout.
func decodeYOLOv8(data []float32, numClasses, numAnchors int, labels []string, minScore float64) []addons.Detection {
	if numClasses <= 0 || numAnchors <= 0 || len(labels) < numClasses {
		return nil
	}
	stride := 4 + numClasses
	if len(data) < stride*numAnchors {
		return nil
	}

	var out []addons.Detection
	for a := 0; a < numAnchors; a++ {
		bestClass := -1
		bestScore := float32(minScore)
		for c := 0; c < numClasses; c++ {
			score := data[(4+c)*numAnchors+a]
			if score > bestScore {
				bestScore = score
				bestClass = c
			}
		}
		if bestClass >= 0 {
			out = append(out, addons.Detection{Class: labels[bestClass], Score: float64(bestScore)})
		}
	}
	return out
}
