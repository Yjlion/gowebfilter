package image

// NudeNetV3Labels is NudeNet v3's exact 18-class label list, in the order
// its "*n.onnx" exports produce them - copied verbatim from
// notAI-tech/NudeNet's own inference source (nudenet/nudenet.py, v3
// branch, the `__labels` list). Order matters: it must match
// testdata/nudenet_v3.labels.json exactly (see nudenet_labels_test.go) and
// whatever `webfilter models download` writes as the downloaded model's
// ".labels.json" sidecar - a mismatched order would silently attach the
// wrong class name to a real detection.
var NudeNetV3Labels = []string{
	"FEMALE_GENITALIA_COVERED",
	"FACE_FEMALE",
	"BUTTOCKS_EXPOSED",
	"FEMALE_BREAST_EXPOSED",
	"FEMALE_GENITALIA_EXPOSED",
	"MALE_BREAST_EXPOSED",
	"ANUS_EXPOSED",
	"FEET_EXPOSED",
	"BELLY_COVERED",
	"FEET_COVERED",
	"ARMPITS_COVERED",
	"ARMPITS_EXPOSED",
	"FACE_MALE",
	"BELLY_EXPOSED",
	"MALE_GENITALIA_EXPOSED",
	"ANUS_COVERED",
	"FEMALE_BREAST_COVERED",
	"BUTTOCKS_COVERED",
}
