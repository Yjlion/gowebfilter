#!/usr/bin/env python3
"""One-shot: GantMan saved_model.tflite -> nsfw224.onnx (via tflite2onnx)."""
import sys

import tflite2onnx

src = sys.argv[1] if len(sys.argv) > 1 else "nsfw_model/mobilenet_v2_140_224/saved_model.tflite"
dst = sys.argv[2] if len(sys.argv) > 2 else "nsfw224.onnx"
tflite2onnx.convert(src, dst)
print("converted", src, "->", dst)
