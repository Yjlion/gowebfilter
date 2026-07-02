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

func TestPadToSquareTopLeftPadsWideImageBottomOnly(t *testing.T) {
	// 400x200: the longer side (400) sets the square size, so the 200px
	// tall image gets 200 rows of black padding added at the bottom only -
	// content stays anchored at (0,0), unlike centered YOLOv8 letterboxing.
	img := solidImage(400, 200, color.RGBA{R: 255, A: 255})
	out := padToSquareTopLeft(img)
	if b := out.Bounds(); b.Dx() != 400 || b.Dy() != 400 {
		t.Fatalf("padToSquareTopLeft() size = %dx%d, want 400x400", b.Dx(), b.Dy())
	}

	topLeft := out.NRGBAAt(0, 0)
	if topLeft.R != 255 {
		t.Fatalf("padToSquareTopLeft() top-left pixel = %+v, want the source image's red (content anchored top-left)", topLeft)
	}
	bottomRight := out.NRGBAAt(399, 399)
	if bottomRight.R != 0 || bottomRight.G != 0 || bottomRight.B != 0 {
		t.Fatalf("padToSquareTopLeft() bottom-right pixel = %+v, want black padding", bottomRight)
	}
}

func TestPadToSquareTopLeftHandlesDegenerateInput(t *testing.T) {
	empty := stdimage.NewNRGBA(stdimage.Rect(0, 0, 0, 0))
	out := padToSquareTopLeft(empty)
	if b := out.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Fatalf("padToSquareTopLeft() on empty input = %dx%d, want 1x1 (still produces a canvas)", b.Dx(), b.Dy())
	}
}

func TestResizeSquareOutputIsRequestedSize(t *testing.T) {
	img := solidImage(400, 400, color.RGBA{R: 255, A: 255})
	out := resizeSquare(img, 320)
	if b := out.Bounds(); b.Dx() != 320 || b.Dy() != 320 {
		t.Fatalf("resizeSquare() size = %dx%d, want 320x320", b.Dx(), b.Dy())
	}
}

func TestResizeSquareOnAlreadyCorrectSizeIsIdentity(t *testing.T) {
	img := solidImage(64, 64, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	out := resizeSquare(img, 64)
	center := out.NRGBAAt(32, 32)
	if center.R != 200 || center.G != 100 || center.B != 50 {
		t.Fatalf("resizeSquare() at identity size changed pixel content: %+v", center)
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
