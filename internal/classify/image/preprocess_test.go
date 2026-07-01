package image

import (
	stdimage "image"
	"image/color"
	"testing"
)

func solidImage(w, h int, c color.Color) *stdimage.NRGBA {
	img := stdimage.NewNRGBA(stdimage.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestLetterboxOutputIsSquareOfRequestedSize(t *testing.T) {
	img := solidImage(400, 200, color.RGBA{R: 255, A: 255})
	out := letterbox(img, 320)
	if b := out.Bounds(); b.Dx() != 320 || b.Dy() != 320 {
		t.Fatalf("letterbox() size = %dx%d, want 320x320", b.Dx(), b.Dy())
	}
}

func TestLetterboxPadsWideImageTopAndBottom(t *testing.T) {
	// 400x200 into 320x320: scale = 320/400 = 0.8 -> 320x160, padded 80px
	// top and bottom with the neutral fill color.
	img := solidImage(400, 200, color.RGBA{R: 255, A: 255})
	out := letterbox(img, 320)

	corner := out.NRGBAAt(0, 0)
	if corner.R != 114 || corner.G != 114 || corner.B != 114 {
		t.Fatalf("letterbox() corner pixel = %+v, want the 114-gray fill color", corner)
	}
	center := out.NRGBAAt(160, 160)
	if center.R != 255 {
		t.Fatalf("letterbox() center pixel = %+v, want the source image's red", center)
	}
}

func TestLetterboxHandlesDegenerateInput(t *testing.T) {
	empty := stdimage.NewNRGBA(stdimage.Rect(0, 0, 0, 0))
	out := letterbox(empty, 64)
	if b := out.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("letterbox() on empty input = %dx%d, want 64x64 (still produces a canvas)", b.Dx(), b.Dy())
	}
}

func TestToCHWFloatNormalizesAndPlanarizes(t *testing.T) {
	img := stdimage.NewNRGBA(stdimage.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	img.Set(1, 0, color.RGBA{R: 0, G: 255, B: 0, A: 255})
	img.Set(0, 1, color.RGBA{R: 0, G: 0, B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 128, G: 128, B: 128, A: 255})

	out := toCHWFloat(img, 2)
	if len(out) != 3*2*2 {
		t.Fatalf("toCHWFloat() length = %d, want %d", len(out), 3*2*2)
	}

	plane := 4
	// Pixel (0,0) is pure red: R-plane index 0 should be 1.0, G/B near 0.
	if got := out[0]; got != 1.0 {
		t.Fatalf("toCHWFloat() R-plane[0] = %v, want 1.0", got)
	}
	if got := out[plane+0]; got != 0.0 {
		t.Fatalf("toCHWFloat() G-plane[0] = %v, want 0.0", got)
	}
	// Pixel (1,1) is mid-gray 128: all three planes should read ~128/255.
	want := float32(128) / 255.0
	idx := 1*2 + 1
	for p := 0; p < 3; p++ {
		if got := out[p*plane+idx]; got != want {
			t.Fatalf("toCHWFloat() plane %d at gray pixel = %v, want %v", p, got, want)
		}
	}
}
