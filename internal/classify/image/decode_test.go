package image

import "testing"

// buildTensor lays out a synthetic (4+numClasses, numAnchors) YOLOv8-style
// output tensor channel-major (data[channel*numAnchors+anchor]), matching
// decodeYOLOv8's expected layout. classScores[c][a] sets the score for
// class c at anchor a; box channels (0-3) are left zero since decodeYOLOv8
// ignores them.
func buildTensor(numClasses, numAnchors int, classScores map[[2]int]float32) []float32 {
	stride := 4 + numClasses
	data := make([]float32, stride*numAnchors)
	for k, v := range classScores {
		c, a := k[0], k[1]
		data[(4+c)*numAnchors+a] = v
	}
	return data
}

func TestDecodeYOLOv8PicksHighestScoringClassPerAnchor(t *testing.T) {
	labels := []string{"SAFE_A", "NSFW_B", "SAFE_C"}
	data := buildTensor(3, 2, map[[2]int]float32{
		{0, 0}: 0.2, {1, 0}: 0.9, {2, 0}: 0.1, // anchor 0 -> class 1 wins
		{0, 1}: 0.05, {1, 1}: 0.05, {2, 1}: 0.05, // anchor 1 -> all below floor
	})

	dets := decodeYOLOv8(data, 3, 2, labels, 0.1)
	if len(dets) != 1 {
		t.Fatalf("decodeYOLOv8() returned %d detections, want 1: %v", len(dets), dets)
	}
	if dets[0].Class != "NSFW_B" || dets[0].Score != float64(float32(0.9)) {
		t.Fatalf("decodeYOLOv8() = %+v, want {NSFW_B 0.9}", dets[0])
	}
}

func TestDecodeYOLOv8RespectsMinScoreFloor(t *testing.T) {
	labels := []string{"A", "B"}
	data := buildTensor(2, 1, map[[2]int]float32{{0, 0}: 0.05, {1, 0}: 0.03})
	if dets := decodeYOLOv8(data, 2, 1, labels, 0.1); len(dets) != 0 {
		t.Fatalf("decodeYOLOv8() = %v, want no detections below the floor", dets)
	}
}

func TestDecodeYOLOv8HandlesMultipleAnchors(t *testing.T) {
	labels := []string{"A", "B"}
	data := buildTensor(2, 3, map[[2]int]float32{
		{0, 0}: 0.9, {1, 0}: 0.1,
		{0, 1}: 0.1, {1, 1}: 0.8,
		{0, 2}: 0.01, {1, 2}: 0.02,
	})
	dets := decodeYOLOv8(data, 2, 3, labels, 0.1)
	if len(dets) != 2 {
		t.Fatalf("decodeYOLOv8() returned %d detections, want 2: %v", len(dets), dets)
	}
}

func TestDecodeYOLOv8GuardsAgainstBadShapes(t *testing.T) {
	if dets := decodeYOLOv8(nil, 0, 0, nil, 0.1); dets != nil {
		t.Fatalf("decodeYOLOv8() with zero shape = %v, want nil", dets)
	}
	shortData := make([]float32, 3)
	if dets := decodeYOLOv8(shortData, 2, 5, []string{"a", "b"}, 0.1); dets != nil {
		t.Fatalf("decodeYOLOv8() with too-short data = %v, want nil", dets)
	}
	data := buildTensor(2, 1, nil)
	if dets := decodeYOLOv8(data, 2, 1, []string{"only-one"}, 0.1); dets != nil {
		t.Fatalf("decodeYOLOv8() with too-few labels = %v, want nil", dets)
	}
}
