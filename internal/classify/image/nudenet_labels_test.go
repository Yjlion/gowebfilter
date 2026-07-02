package image

import "testing"

// TestNudeNetV3LabelsMatchesTestdataFixture guards against the shipped
// NudeNetV3Labels constant (used both by loadLabels-compatible sidecar
// generation and by production code) drifting from the checked-in fixture
// other tests/tooling read from disk.
func TestNudeNetV3LabelsMatchesTestdataFixture(t *testing.T) {
	fromFile, err := loadLabels("testdata/nudenet_v3.labels.json")
	if err != nil {
		t.Fatalf("loadLabels(testdata fixture) failed: %v", err)
	}
	if len(fromFile) != len(NudeNetV3Labels) {
		t.Fatalf("testdata fixture has %d labels, NudeNetV3Labels has %d", len(fromFile), len(NudeNetV3Labels))
	}
	for i := range fromFile {
		if fromFile[i] != NudeNetV3Labels[i] {
			t.Fatalf("label %d: testdata fixture has %q, NudeNetV3Labels has %q", i, fromFile[i], NudeNetV3Labels[i])
		}
	}
}
