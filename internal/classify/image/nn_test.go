package image

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

type fixtureFile struct {
	Input string `json:"input"`
	Shape []int  `json:"shape"`
	Cases []struct {
		Name  string    `json:"name"`
		Seed  int64     `json:"seed"`
		Probs []float64 `json:"probs"`
	} `json:"cases"`
}

// lcgTensor reproduces the fixture generator's deterministic input
// generator (scripts/nsfw-model/verify.py).
func lcgTensor(seed uint32, n int) []float32 {
	out := make([]float32, n)
	s := seed
	for i := range out {
		s = 1664525*s + 1013904223
		out[i] = float32(float64(s) / 4294967296.0)
	}
	return out
}

// TestModelMatchesReference runs the embedded model on the same inputs that
// onnxruntime saw (scripts/nsfw-model/verify.py) and compares the class
// probabilities. Differences come only from the fp16 weight quantization.
// Ported from privoxy-nsfw-guard's nn_test.go.
func TestModelMatchesReference(t *testing.T) {
	raw, err := os.ReadFile("testdata/model_fixtures.json")
	if err != nil {
		t.Skipf("no fixtures: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	m, err := classifier()
	if err != nil {
		t.Fatalf("embedded model: %v", err)
	}

	n := 1
	for _, d := range fx.Shape {
		n *= d
	}
	for _, c := range fx.Cases {
		var data []float32
		if c.Seed >= 0 {
			data = lcgTensor(uint32(c.Seed), n)
		} else {
			data = make([]float32, n)
			for i := range data {
				data[i] = 0.5
			}
		}
		x := &nnTensor{Dims: fx.Shape, Data: data}
		out, err := m.Run(x)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if len(out.Data) != len(c.Probs) {
			t.Fatalf("%s: got %d outputs, want %d", c.Name, len(out.Data), len(c.Probs))
		}
		var maxDiff float64
		argmaxGot, argmaxWant := 0, 0
		for i := range c.Probs {
			d := math.Abs(float64(out.Data[i]) - c.Probs[i])
			if d > maxDiff {
				maxDiff = d
			}
			if out.Data[i] > out.Data[argmaxGot] {
				argmaxGot = i
			}
			if c.Probs[i] > c.Probs[argmaxWant] {
				argmaxWant = i
			}
		}
		t.Logf("%s: got=%v want=%v maxdiff=%.4f", c.Name, out.Data, c.Probs, maxDiff)
		if argmaxGot != argmaxWant {
			t.Errorf("%s: argmax %d != reference %d", c.Name, argmaxGot, argmaxWant)
		}
		if maxDiff > 0.03 {
			t.Errorf("%s: max prob diff %.4f > 0.03", c.Name, maxDiff)
		}
	}
}

func BenchmarkPredict(b *testing.B) {
	m, err := classifier()
	if err != nil {
		b.Fatal(err)
	}
	img := synthNude(640, 480)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := predict(m, img); err != nil {
			b.Fatal(err)
		}
	}
}
