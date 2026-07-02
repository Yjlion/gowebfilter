#!/usr/bin/env python3
"""Convert the NSFW MobileNetV2 ONNX model to nsfw-guard's embedded format.

Output layout:
    magic   "NSFWG1"
    uint32  little-endian JSON header length
    json    {input, output, input_shape, classes, ops[], weights[]}
    blob    all weights as float16, little-endian, concatenated

The Go engine executes ops[] in order; weights[] gives each initializer's
dims and element offset into the blob. Ops that are inference no-ops
(Identity, Dropout, Cast) are dropped here, not at runtime.

Usage: convert.py model.onnx model.bin
"""
import json
import struct
import sys

import numpy as np
import onnx
from onnx import numpy_helper

NOOPS = {"Identity", "Dropout", "Cast"}
SUPPORTED = {
    "Conv", "BatchNormalization", "Clip", "Relu", "Add", "Mul", "Sub", "Div", "Pad",
    "GlobalAveragePool", "ReduceMean", "Reshape", "Flatten", "Squeeze",
    "Unsqueeze", "Transpose", "Gemm", "MatMul", "Softmax", "Sigmoid",
    "AveragePool", "Concat",
}

CLASSES = ["drawings", "hentai", "neutral", "porn", "sexy"]


def attr_value(a):
    if a.type == onnx.AttributeProto.INT:
        return int(a.i)
    if a.type == onnx.AttributeProto.FLOAT:
        return float(a.f)
    if a.type == onnx.AttributeProto.INTS:
        return [int(v) for v in a.ints]
    if a.type == onnx.AttributeProto.FLOATS:
        return [float(v) for v in a.floats]
    if a.type == onnx.AttributeProto.STRING:
        return a.s.decode()
    if a.type == onnx.AttributeProto.TENSOR:
        return numpy_helper.to_array(a.t).tolist()
    raise SystemExit(f"unsupported attribute type {a.type} ({a.name})")


def main(src, dst):
    model = onnx.load(src)
    graph = model.graph

    inits = {}  # name -> np array
    for t in graph.initializer:
        inits[t.name] = numpy_helper.to_array(t)

    # Constant nodes feed things like Reshape shapes and Clip bounds.
    consts = dict(inits)
    rename = {}  # output of dropped no-op -> its input

    ops = []
    for node in graph.node:
        if node.op_type == "Constant":
            consts[node.output[0]] = numpy_helper.to_array(node.attribute[0].t)
            continue
        inputs = [rename.get(i, i) for i in node.input]
        if node.op_type in NOOPS:
            rename[node.output[0]] = inputs[0]
            continue
        if node.op_type not in SUPPORTED:
            raise SystemExit(f"unsupported op {node.op_type} ({node.name})")
        attrs = {a.name: attr_value(a) for a in node.attribute}

        # Inline small non-weight constants (Reshape shape, Clip min/max,
        # Pad pads...) into attrs so the engine never sees them as tensors.
        kept_inputs = []
        for idx, name in enumerate(inputs):
            if name == "":
                continue
            # Never inline the data input (index 0) — ops like Reshape/Pad can
            # legitimately operate on a small constant tensor.
            if idx >= 1 and name in consts and consts[name].size <= 16 and consts[name].ndim <= 1:
                attrs[f"const_{idx}"] = np.asarray(consts[name]).astype(float).flatten().tolist()
            else:
                kept_inputs.append(name)
        ops.append({
            "type": node.op_type,
            "inputs": kept_inputs,
            "outputs": list(node.output),
            "attrs": attrs,
        })

    # Weights actually referenced by surviving ops.
    used = set()
    for op in ops:
        for i in op["inputs"]:
            if i in inits:
                used.add(i)

    blob = bytearray()
    weights = []
    offset = 0
    for name in sorted(used):
        arr = inits[name].astype(np.float32)
        weights.append({
            "name": name,
            "dims": list(arr.shape) if arr.shape else [1],
            "offset": offset,
            "len": int(arr.size),
        })
        blob += arr.astype("<f2").tobytes()
        offset += int(arr.size)

    g_in = graph.input[0]
    shape = [d.dim_value if d.dim_value else 1
             for d in g_in.type.tensor_type.shape.dim]
    header = {
        "input": g_in.name,
        "output": graph.output[0].name,
        "input_shape": shape,
        "classes": CLASSES,
        "ops": ops,
        "weights": weights,
    }
    hjson = json.dumps(header, separators=(",", ":")).encode()

    with open(dst, "wb") as f:
        f.write(b"NSFWG1")
        f.write(struct.pack("<I", len(hjson)))
        f.write(hjson)
        f.write(bytes(blob))

    optypes = {}
    for op in ops:
        optypes[op["type"]] = optypes.get(op["type"], 0) + 1
    print(f"input={g_in.name} shape={shape} output={graph.output[0].name}")
    print(f"ops: {json.dumps(optypes, sort_keys=True)}")
    print(f"weights: {len(weights)} tensors, {offset} elements, "
          f"blob {len(blob)//1024} KiB, file {(10+len(hjson)+len(blob))//1024} KiB")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    main(sys.argv[1], sys.argv[2])
