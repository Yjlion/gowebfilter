# NSFW image model: provenance and regeneration

`internal/classify/image/model.bin` is [GantMan/nsfw_model](https://github.com/GantMan/nsfw_model)
(MobileNetV2 1.4@224, MIT-licensed), converted to a compact fp16 blob that
`internal/classify/image/nn.go`'s from-scratch pure-Go engine executes
directly - no ONNX Runtime, no CGO, no model download. It's committed to
git and embedded into the binary via `//go:embed`.

This pipeline (and `nn.go` itself) was ported from
[privoxy-nsfw-guard](https://github.com/Yjlion/privoxy-nsfw-guard), a
sibling MIT-licensed project by the same author - `model.bin` here is a
byte-for-byte copy of that project's own already-converted-and-verified
model, not a fresh conversion.

Regenerating `model.bin` from scratch (only needed if upgrading to a newer
GantMan model release) requires Python with `tflite2onnx`, `onnx`,
`onnxruntime`, and `numpy`:

```bash
# 1. GantMan's released TFLite model -> ONNX
python tflite_to_onnx.py nsfw_model/mobilenet_v2_140_224/saved_model.tflite nsfw224.onnx

# 2. ONNX -> the NSFWG1 fp16 blob nn.go reads (see convert.py's own doc
#    comment for the exact container format)
python convert.py nsfw224.onnx model.bin

# 3. Regenerate the onnxruntime cross-check fixtures
#    (internal/classify/image/testdata/model_fixtures.json) and re-run
#    `go test ./internal/classify/image/...` (TestModelMatchesReference)
#    to confirm the pure-Go engine still matches real onnxruntime output.
python verify.py nsfw224.onnx model_fixtures.json
```
