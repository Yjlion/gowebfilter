#!/usr/bin/env python3
"""Generate reference fixtures: run the ONNX model with onnxruntime on
deterministic inputs reproducible in Go (LCG), save expected outputs.

Usage: verify.py nsfw224.onnx fixtures.json
"""
import json
import sys

import numpy as np
import onnxruntime


def lcg_tensor(seed, n):
    out = np.empty(n, dtype=np.float32)
    s = seed & 0xFFFFFFFF
    for i in range(n):
        s = (1664525 * s + 1013904223) & 0xFFFFFFFF
        out[i] = s / 4294967296.0
    return out


def main(model_path, dst):
    sess = onnxruntime.InferenceSession(model_path)
    inp = sess.get_inputs()[0]
    n = 1 * 3 * 224 * 224
    cases = []
    for name, seed in [("lcg42", 42), ("lcg1337", 1337)]:
        x = lcg_tensor(seed, n).reshape(1, 3, 224, 224)
        y = sess.run(None, {inp.name: x})[0].flatten()
        cases.append({"name": name, "seed": seed, "probs": [float(v) for v in y]})
    x = np.full((1, 3, 224, 224), 0.5, dtype=np.float32)
    y = sess.run(None, {inp.name: x})[0].flatten()
    cases.append({"name": "const0.5", "seed": -1, "probs": [float(v) for v in y]})

    with open(dst, "w") as f:
        json.dump({"input": inp.name, "shape": [1, 3, 224, 224], "cases": cases}, f, indent=1)
    for c in cases:
        print(c["name"], ["%.4f" % p for p in c["probs"]])


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
