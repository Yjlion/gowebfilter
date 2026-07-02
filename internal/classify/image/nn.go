package image

// Minimal float32 inference engine for the embedded NSFW classifier
// (GantMan/nsfw_model MobileNetV2, MIT-licensed - see model.go). Ported
// verbatim from privoxy-nsfw-guard (github.com/Yjlion/privoxy-nsfw-guard,
// same author, MIT-licensed) - it executes the op plan produced by
// tools/convert.py (an ONNX graph flattened to a list); only the ops
// MobileNetV2 needs are implemented.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sync"
)

// nnTensor is a dense float32 tensor. Layout follows ONNX conventions
// (NCHW for images).
type nnTensor struct {
	Dims []int
	Data []float32
}

func newTensor(dims ...int) *nnTensor {
	n := 1
	for _, d := range dims {
		n *= d
	}
	return &nnTensor{Dims: dims, Data: make([]float32, n)}
}

func (t *nnTensor) numel() int {
	n := 1
	for _, d := range t.Dims {
		n *= d
	}
	return n
}

type modelOp struct {
	Type    string         `json:"type"`
	Inputs  []string       `json:"inputs"`
	Outputs []string       `json:"outputs"`
	Attrs   map[string]any `json:"attrs"`
}

type weightInfo struct {
	Name   string `json:"name"`
	Dims   []int  `json:"dims"`
	Offset int    `json:"offset"`
	Len    int    `json:"len"`
}

type modelHeader struct {
	Input      string       `json:"input"`
	Output     string       `json:"output"`
	InputShape []int        `json:"input_shape"`
	Classes    []string     `json:"classes"`
	Ops        []modelOp    `json:"ops"`
	Weights    []weightInfo `json:"weights"`
}

// nnModel is a loaded op plan plus its weights.
type nnModel struct {
	hdr     modelHeader
	weights map[string]*nnTensor
}

// loadNNModel parses the NSFWG1 container (see scripts/nsfw-model/convert.py).
func loadNNModel(blob []byte) (*nnModel, error) {
	if len(blob) < 10 || string(blob[:6]) != "NSFWG1" {
		return nil, fmt.Errorf("not a NSFWG1 model (%d bytes)", len(blob))
	}
	hlen := int(binary.LittleEndian.Uint32(blob[6:10]))
	if 10+hlen > len(blob) {
		return nil, fmt.Errorf("truncated header")
	}
	m := &nnModel{weights: map[string]*nnTensor{}}
	if err := json.Unmarshal(blob[10:10+hlen], &m.hdr); err != nil {
		return nil, fmt.Errorf("model header: %w", err)
	}
	wblob := blob[10+hlen:]
	for _, w := range m.hdr.Weights {
		if (w.Offset+w.Len)*2 > len(wblob) {
			return nil, fmt.Errorf("weight %s out of range", w.Name)
		}
		t := &nnTensor{Dims: w.Dims, Data: make([]float32, w.Len)}
		for i := 0; i < w.Len; i++ {
			t.Data[i] = f16to32(binary.LittleEndian.Uint16(wblob[(w.Offset+i)*2:]))
		}
		m.weights[w.Name] = t
	}
	return m, nil
}

func f16to32(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	man := uint32(h) & 0x3ff
	switch exp {
	case 0:
		if man == 0 {
			return math.Float32frombits(sign)
		}
		e := uint32(127 - 15 + 1)
		for man&0x400 == 0 {
			man <<= 1
			e--
		}
		return math.Float32frombits(sign | e<<23 | (man&0x3ff)<<13)
	case 0x1f:
		return math.Float32frombits(sign | 0xff<<23 | man<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | man<<13)
}

// Run executes the plan on input and returns the output tensor.
func (m *nnModel) Run(input *nnTensor) (out *nnTensor, err error) {
	var cur string
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("inference panic at %s: %v", cur, r)
		}
	}()
	env := map[string]*nnTensor{m.hdr.Input: input}
	get := func(name string) *nnTensor {
		if t, ok := env[name]; ok {
			return t
		}
		if t, ok := m.weights[name]; ok {
			return t
		}
		panic(fmt.Sprintf("tensor %q not found", name))
	}

	for opIdx, op := range m.hdr.Ops {
		cur = fmt.Sprintf("op %d %s -> %s", opIdx, op.Type, op.Outputs[0])
		var r *nnTensor
		switch op.Type {
		case "Conv":
			var bias *nnTensor
			if len(op.Inputs) > 2 {
				bias = get(op.Inputs[2])
			}
			r = opConv(get(op.Inputs[0]), get(op.Inputs[1]), bias, op)
		case "BatchNormalization":
			r = opBatchNorm(get(op.Inputs[0]), get(op.Inputs[1]), get(op.Inputs[2]),
				get(op.Inputs[3]), get(op.Inputs[4]), attrFloat(op, "epsilon", 1e-5))
		case "Clip":
			lo := attrScalar(op, "min", "const_1", float32(math.Inf(-1)))
			hi := attrScalar(op, "max", "const_2", float32(math.Inf(1)))
			r = opClip(get(op.Inputs[0]), lo, hi)
		case "Relu":
			r = opClip(get(op.Inputs[0]), 0, float32(math.Inf(1)))
		case "Sigmoid":
			r = opSigmoid(get(op.Inputs[0]))
		case "Add":
			r = opBinary(op, get, func(a, b float32) float32 { return a + b })
		case "Mul":
			r = opBinary(op, get, func(a, b float32) float32 { return a * b })
		case "Sub":
			r = opBinary(op, get, func(a, b float32) float32 { return a - b })
		case "Div":
			r = opBinary(op, get, func(a, b float32) float32 { return a / b })
		case "Pad":
			r = opPad(get(op.Inputs[0]), op)
		case "GlobalAveragePool":
			r = opGlobalAvgPool(get(op.Inputs[0]))
		case "AveragePool":
			r = opAveragePool(get(op.Inputs[0]), op)
		case "ReduceMean":
			r = opReduceMean(get(op.Inputs[0]), op)
		case "Reshape", "Flatten", "Squeeze", "Unsqueeze":
			r = opReshapeLike(get(op.Inputs[0]), op)
		case "Transpose":
			r = opTranspose(get(op.Inputs[0]), attrInts(op, "perm", nil))
		case "Gemm":
			var c *nnTensor
			if len(op.Inputs) > 2 {
				c = get(op.Inputs[2])
			}
			r = opGemm(get(op.Inputs[0]), get(op.Inputs[1]), c, op)
		case "MatMul":
			r = opGemm(get(op.Inputs[0]), get(op.Inputs[1]), nil, op)
		case "Softmax":
			r = opSoftmax(get(op.Inputs[0]))
		default:
			return nil, fmt.Errorf("unsupported op %s", op.Type)
		}
		env[op.Outputs[0]] = r
	}
	o, ok := env[m.hdr.Output]
	if !ok {
		return nil, fmt.Errorf("output %q not produced", m.hdr.Output)
	}
	return o, nil
}

// ---- attribute helpers (JSON numbers arrive as float64) ----

func attrInt(op modelOp, name string, def int) int {
	if v, ok := op.Attrs[name]; ok {
		return int(v.(float64))
	}
	return def
}

func attrFloat(op modelOp, name string, def float32) float32 {
	if v, ok := op.Attrs[name]; ok {
		return float32(v.(float64))
	}
	return def
}

func attrInts(op modelOp, name string, def []int) []int {
	v, ok := op.Attrs[name]
	if !ok {
		return def
	}
	raw := v.([]any)
	out := make([]int, len(raw))
	for i, x := range raw {
		out[i] = int(x.(float64))
	}
	return out
}

// attrScalar reads a scalar that may live in an attribute (ONNX <11) or in
// an inlined constant input (const_N, see converter).
func attrScalar(op modelOp, attr, constKey string, def float32) float32 {
	if v, ok := op.Attrs[attr]; ok {
		return float32(v.(float64))
	}
	if v, ok := op.Attrs[constKey]; ok {
		raw := v.([]any)
		if len(raw) > 0 {
			return float32(raw[0].(float64))
		}
	}
	return def
}

// ---- ops ----

func opConv(x, w *nnTensor, bias *nnTensor, op modelOp) *nnTensor {
	// x [1,C,H,W], w [M,C/g,kh,kw]
	C, H, W := x.Dims[1], x.Dims[2], x.Dims[3]
	M, Cg, kh, kw := w.Dims[0], w.Dims[1], w.Dims[2], w.Dims[3]
	group := attrInt(op, "group", 1)
	strides := attrInts(op, "strides", []int{1, 1})
	dil := attrInts(op, "dilations", []int{1, 1})
	pads := attrInts(op, "pads", []int{0, 0, 0, 0}) // top,left,bottom,right
	if ap, ok := op.Attrs["auto_pad"].(string); ok && (ap == "SAME_UPPER" || ap == "SAME_LOWER") {
		pads = samePads(H, W, kh, kw, strides, dil, ap == "SAME_LOWER")
	}
	sh, sw := strides[0], strides[1]
	dh, dw := dil[0], dil[1]
	outH := (H+pads[0]+pads[2]-((kh-1)*dh+1))/sh + 1
	outW := (W+pads[1]+pads[3]-((kw-1)*dw+1))/sw + 1
	y := newTensor(1, M, outH, outW)

	parallelFor(M, func(m0, m1 int) {
		for m := m0; m < m1; m++ {
			g := m / (M / group)
			var b float32
			if bias != nil {
				b = bias.Data[m]
			}
			dst := y.Data[m*outH*outW : (m+1)*outH*outW]
			if kh == 1 && kw == 1 && sh == 1 && sw == 1 && pads[0] == 0 && pads[1] == 0 && pads[2] == 0 && pads[3] == 0 {
				// Pointwise conv: pure channel mix, the hot path in MobileNet.
				for i := range dst {
					dst[i] = b
				}
				for c := 0; c < Cg; c++ {
					src := x.Data[(g*Cg+c)*H*W : (g*Cg+c+1)*H*W]
					axpy(w.Data[(m*Cg+c)*1], src, dst)
				}
				continue
			}
			for oy := 0; oy < outH; oy++ {
				for ox := 0; ox < outW; ox++ {
					sum := b
					iy0 := oy*sh - pads[0]
					ix0 := ox*sw - pads[1]
					for c := 0; c < Cg; c++ {
						src := x.Data[(g*Cg+c)*H*W:]
						wrow := w.Data[(m*Cg+c)*kh*kw:]
						for ky := 0; ky < kh; ky++ {
							iy := iy0 + ky*dh
							if iy < 0 || iy >= H {
								continue
							}
							for kx := 0; kx < kw; kx++ {
								ix := ix0 + kx*dw
								if ix < 0 || ix >= W {
									continue
								}
								sum += wrow[ky*kw+kx] * src[iy*W+ix]
							}
						}
					}
					dst[oy*outW+ox] = sum
				}
			}
		}
	})
	_ = C
	return y
}

func samePads(h, w, kh, kw int, strides, dil []int, lower bool) []int {
	effKH := (kh-1)*dil[0] + 1
	effKW := (kw-1)*dil[1] + 1
	outH := (h + strides[0] - 1) / strides[0]
	outW := (w + strides[1] - 1) / strides[1]
	padH := max(0, (outH-1)*strides[0]+effKH-h)
	padW := max(0, (outW-1)*strides[1]+effKW-w)
	if lower {
		return []int{padH - padH/2, padW - padW/2, padH / 2, padW / 2}
	}
	return []int{padH / 2, padW / 2, padH - padH/2, padW - padW/2}
}

func axpy(a float32, x, y []float32) {
	_ = y[len(x)-1]
	for i, v := range x {
		y[i] += a * v
	}
}

func opBatchNorm(x, scale, bshift, mean, variance *nnTensor, eps float32) *nnTensor {
	C := x.Dims[1]
	plane := x.numel() / C
	y := &nnTensor{Dims: append([]int{}, x.Dims...), Data: make([]float32, len(x.Data))}
	for c := 0; c < C; c++ {
		s := scale.Data[c] / float32(math.Sqrt(float64(variance.Data[c]+eps)))
		off := bshift.Data[c] - mean.Data[c]*s
		src := x.Data[c*plane : (c+1)*plane]
		dst := y.Data[c*plane : (c+1)*plane]
		for i, v := range src {
			dst[i] = v*s + off
		}
	}
	return y
}

func opClip(x *nnTensor, lo, hi float32) *nnTensor {
	y := &nnTensor{Dims: append([]int{}, x.Dims...), Data: make([]float32, len(x.Data))}
	for i, v := range x.Data {
		if v < lo {
			v = lo
		}
		if v > hi {
			v = hi
		}
		y.Data[i] = v
	}
	return y
}

func opSigmoid(x *nnTensor) *nnTensor {
	y := &nnTensor{Dims: append([]int{}, x.Dims...), Data: make([]float32, len(x.Data))}
	for i, v := range x.Data {
		y.Data[i] = float32(1 / (1 + math.Exp(-float64(v))))
	}
	return y
}

// opBinary resolves the two operands of an elementwise op in their original
// positions: tensors come from Inputs in order, small constants the converter
// inlined come from attrs const_0/const_1. Order matters for Sub and Div.
func opBinary(op modelOp, get func(string) *nnTensor, f func(x, y float32) float32) *nnTensor {
	var ab [2]*nnTensor
	next := 0
	for pos := 0; pos < 2; pos++ {
		if v, ok := op.Attrs[fmt.Sprintf("const_%d", pos)]; ok {
			raw := v.([]any)
			t := &nnTensor{Dims: []int{len(raw)}, Data: make([]float32, len(raw))}
			for i, x := range raw {
				t.Data[i] = float32(x.(float64))
			}
			ab[pos] = t
			continue
		}
		ab[pos] = get(op.Inputs[next])
		next++
	}
	a, b := ab[0], ab[1]

	// Same-shape fast path.
	if len(a.Data) == len(b.Data) {
		y := &nnTensor{Dims: append([]int{}, a.Dims...), Data: make([]float32, len(a.Data))}
		for i := range a.Data {
			y.Data[i] = f(a.Data[i], b.Data[i])
		}
		return y
	}

	// Broadcast the smaller operand. Supported shapes: scalar, or per-channel
	// vector against NCHW (axis 1) / NHWC (last axis) — enough for this model.
	big, small := a, b
	smallIsB := true
	if len(b.Data) > len(a.Data) {
		big, small = b, a
		smallIsB = false
	}
	y := &nnTensor{Dims: append([]int{}, big.Dims...), Data: make([]float32, len(big.Data))}
	pick := func(bigV, smallV float32) float32 {
		if smallIsB {
			return f(bigV, smallV)
		}
		return f(smallV, bigV)
	}
	switch {
	case len(small.Data) == 1:
		sv := small.Data[0]
		for i, v := range big.Data {
			y.Data[i] = pick(v, sv)
		}
	case len(big.Dims) == 4 && len(small.Data) == big.Dims[1]:
		// NCHW per-channel
		C := big.Dims[1]
		plane := len(big.Data) / C
		for c := 0; c < C; c++ {
			sv := small.Data[c]
			for i := 0; i < plane; i++ {
				y.Data[c*plane+i] = pick(big.Data[c*plane+i], sv)
			}
		}
	case len(small.Data) == big.Dims[len(big.Dims)-1]:
		// last-axis vector (NHWC channels or dense bias)
		C := len(small.Data)
		for i, v := range big.Data {
			y.Data[i] = pick(v, small.Data[i%C])
		}
	default:
		panic(fmt.Sprintf("unsupported broadcast %v vs %v", big.Dims, small.Dims))
	}
	return y
}

func opPad(x *nnTensor, op modelOp) *nnTensor {
	pads := attrInts(op, "pads", nil)
	if pads == nil {
		if v, ok := op.Attrs["const_1"]; ok {
			raw := v.([]any)
			pads = make([]int, len(raw))
			for i, p := range raw {
				pads[i] = int(p.(float64))
			}
		}
	}
	if pads == nil {
		return x
	}
	n := len(x.Dims)
	newDims := make([]int, n)
	for i := range newDims {
		newDims[i] = x.Dims[i] + pads[i] + pads[n+i]
	}
	y := newTensor(newDims...)
	// Generic N-d copy is overkill: the model only pads H and W of NCHW.
	C, H, W := x.Dims[1], x.Dims[2], x.Dims[3]
	for c := 0; c < C+pads[1]+pads[n+1]; c++ {
		if c < pads[1] || c >= pads[1]+C {
			continue
		}
		for h := 0; h < H; h++ {
			srcOff := ((c-pads[1])*H + h) * W
			dstOff := ((c*newDims[2])+h+pads[2])*newDims[3] + pads[3]
			copy(y.Data[dstOff:dstOff+W], x.Data[srcOff:srcOff+W])
		}
	}
	return y
}

func opGlobalAvgPool(x *nnTensor) *nnTensor {
	C := x.Dims[1]
	plane := x.Dims[2] * x.Dims[3]
	y := newTensor(1, C, 1, 1)
	for c := 0; c < C; c++ {
		var s float64
		for _, v := range x.Data[c*plane : (c+1)*plane] {
			s += float64(v)
		}
		y.Data[c] = float32(s / float64(plane))
	}
	return y
}

func opAveragePool(x *nnTensor, op modelOp) *nnTensor {
	k := attrInts(op, "kernel_shape", nil)
	if k != nil && k[0] == x.Dims[2] && k[1] == x.Dims[3] {
		return opGlobalAvgPool(x)
	}
	panic("AveragePool: only global kernel supported")
}

func opReduceMean(x *nnTensor, op modelOp) *nnTensor {
	axes := attrInts(op, "axes", nil)
	keep := attrInt(op, "keepdims", 1)
	// Only spatial mean over NCHW (axes 2,3) or NHWC (axes 1,2) is needed.
	if len(axes) == 2 && ((axes[0] == 2 && axes[1] == 3) || (axes[0] == 1 && axes[1] == 2)) {
		var y *nnTensor
		if axes[0] == 2 { // NCHW
			y = opGlobalAvgPool(x)
			if keep == 0 {
				y.Dims = []int{1, x.Dims[1]}
			}
			return y
		}
		// NHWC: mean over H,W keeping channel last
		H, W, C := x.Dims[1], x.Dims[2], x.Dims[3]
		y = newTensor(1, C)
		if keep == 1 {
			y.Dims = []int{1, 1, 1, C}
		}
		for c := 0; c < C; c++ {
			var s float64
			for p := 0; p < H*W; p++ {
				s += float64(x.Data[p*C+c])
			}
			y.Data[c] = float32(s / float64(H*W))
		}
		return y
	}
	panic(fmt.Sprintf("ReduceMean axes %v unsupported", axes))
}

func opReshapeLike(x *nnTensor, op modelOp) *nnTensor {
	// Shape bookkeeping only; data is contiguous row-major already.
	n := x.numel()
	var dims []int
	if shape := attrInts(op, "const_1", nil); shape != nil && op.Type == "Reshape" {
		dims = make([]int, len(shape))
		rem := n
		negIdx := -1
		for i, d := range shape {
			switch {
			case d == -1:
				negIdx = i
				dims[i] = 1
			case d == 0:
				dims[i] = x.Dims[i]
			default:
				dims[i] = d
			}
			rem /= max(dims[i], 1)
		}
		if negIdx >= 0 {
			prod := 1
			for i, d := range dims {
				if i != negIdx {
					prod *= d
				}
			}
			dims[negIdx] = n / prod
		}
	} else {
		dims = []int{1, n}
	}
	return &nnTensor{Dims: dims, Data: x.Data}
}

func opTranspose(x *nnTensor, perm []int) *nnTensor {
	n := len(x.Dims)
	if perm == nil {
		perm = make([]int, n)
		for i := range perm {
			perm[i] = n - 1 - i
		}
	}
	newDims := make([]int, n)
	for i, p := range perm {
		newDims[i] = x.Dims[p]
	}
	y := newTensor(newDims...)
	oldStr := strides(x.Dims)
	newStr := strides(newDims)
	idx := make([]int, n)
	for off := 0; off < len(y.Data); off++ {
		rem := off
		srcOff := 0
		for d := 0; d < n; d++ {
			idx[d] = rem / newStr[d]
			rem %= newStr[d]
			srcOff += idx[d] * oldStr[perm[d]]
		}
		y.Data[off] = x.Data[srcOff]
	}
	return y
}

func strides(dims []int) []int {
	s := make([]int, len(dims))
	acc := 1
	for i := len(dims) - 1; i >= 0; i-- {
		s[i] = acc
		acc *= dims[i]
	}
	return s
}

func opGemm(a, b, c *nnTensor, op modelOp) *nnTensor {
	transA := attrInt(op, "transA", 0)
	transB := attrInt(op, "transB", 0)
	alpha := attrFloat(op, "alpha", 1)
	beta := attrFloat(op, "beta", 1)

	M, K := a.Dims[0], a.Dims[1]
	if transA == 1 {
		M, K = K, M
	}
	Kb, N := b.Dims[0], b.Dims[1]
	if transB == 1 {
		Kb, N = N, Kb
	}
	if K != Kb {
		panic(fmt.Sprintf("gemm shape mismatch K=%d Kb=%d", K, Kb))
	}
	y := newTensor(M, N)
	at := func(i, k int) float32 {
		if transA == 1 {
			return a.Data[k*M+i]
		}
		return a.Data[i*K+k]
	}
	bt := func(k, j int) float32 {
		if transB == 1 {
			return b.Data[j*K+k]
		}
		return b.Data[k*N+j]
	}
	for i := 0; i < M; i++ {
		for j := 0; j < N; j++ {
			var s float32
			for k := 0; k < K; k++ {
				s += at(i, k) * bt(k, j)
			}
			s *= alpha
			if c != nil {
				s += beta * c.Data[j%len(c.Data)]
			}
			y.Data[i*N+j] = s
		}
	}
	return y
}

func opSoftmax(x *nnTensor) *nnTensor {
	// Applied on the last axis; the model only does [1,classes].
	y := &nnTensor{Dims: append([]int{}, x.Dims...), Data: make([]float32, len(x.Data))}
	n := x.Dims[len(x.Dims)-1]
	for row := 0; row < len(x.Data)/n; row++ {
		seg := x.Data[row*n : (row+1)*n]
		maxv := seg[0]
		for _, v := range seg {
			if v > maxv {
				maxv = v
			}
		}
		var sum float64
		out := y.Data[row*n : (row+1)*n]
		for i, v := range seg {
			e := math.Exp(float64(v - maxv))
			out[i] = float32(e)
			sum += e
		}
		for i := range out {
			out[i] = float32(float64(out[i]) / sum)
		}
	}
	return y
}

// parallelFor splits [0,n) across NumCPU goroutines.
func parallelFor(n int, f func(lo, hi int)) {
	workers := runtime.NumCPU()
	if workers > n {
		workers = n
	}
	if workers <= 1 {
		f(0, n)
		return
	}
	var wg sync.WaitGroup
	chunk := (n + workers - 1) / workers
	for lo := 0; lo < n; lo += chunk {
		hi := min(lo+chunk, n)
		wg.Add(1)
		go func(l, h int) {
			defer wg.Done()
			f(l, h)
		}(lo, hi)
	}
	wg.Wait()
}
