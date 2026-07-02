package image

import (
	stdimage "image"
	"image/color"
	"image/draw"

	"github.com/disintegration/imaging"
)

// blackFill is NudeNet v3's own preprocessing pad color (cv2.copyMakeBorder
// with BORDER_CONSTANT, default fill value 0 - i.e. black), confirmed
// against its actual Python inference code (_read_image in
// notAI-tech/NudeNet's nudenet/nudenet.py, v3 branch). This is NOT the
// centered 114-gray Ultralytics YOLOv8 export convention - NudeNet's own
// preprocessing deliberately differs from that, so this package matches
// NudeNet specifically rather than "standard YOLOv8 letterboxing".
var blackFill = &stdimage.Uniform{C: color.RGBA{A: 255}}

// padToSquareTopLeft pads img to a square canvas the size of its longer
// side, anchoring the original content at (0,0) and filling the new
// bottom/right region with black - matching NudeNet v3's _read_image
// (cv2.copyMakeBorder(mat, 0, y_pad, 0, x_pad, BORDER_CONSTANT)), which
// pads only the bottom and right edges rather than centering the content.
func padToSquareTopLeft(img stdimage.Image) *stdimage.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	size := w
	if h > size {
		size = h
	}
	if size <= 0 {
		size = 1
	}

	canvas := stdimage.NewNRGBA(stdimage.Rect(0, 0, size, size))
	draw.Draw(canvas, canvas.Bounds(), blackFill, stdimage.Point{}, draw.Src)
	if w > 0 && h > 0 {
		draw.Draw(canvas, stdimage.Rect(0, 0, w, h), img, b.Min, draw.Src)
	}
	return canvas
}

// resizeSquare resizes a square image to size x size in one step - matching
// NudeNet v3's cv2.dnn.blobFromImage resize of the already-padded square
// (see padToSquareTopLeft), not a resize-then-pad.
func resizeSquare(img *stdimage.NRGBA, size int) *stdimage.NRGBA {
	if size <= 0 {
		size = 1
	}
	resized := imaging.Resize(img, size, size, imaging.Linear)
	// imaging.Resize returns *image.NRGBA already, but its exported return
	// type is the concrete image.NRGBA - convert defensively in case that
	// ever changes upstream.
	if nrgba, ok := stdimage.Image(resized).(*stdimage.NRGBA); ok {
		return nrgba
	}
	canvas := stdimage.NewNRGBA(stdimage.Rect(0, 0, size, size))
	draw.Draw(canvas, canvas.Bounds(), resized, stdimage.Point{}, draw.Src)
	return canvas
}

// toCHWFloat converts a size x size NRGBA image into a planar (channel-
// first) float32 tensor normalized to [0,1], RGB order. Confirmed against
// NudeNet v3's own preprocessing (cv2.dnn.blobFromImage with swapRB=true,
// scale 1/255, no per-channel mean subtraction beyond that) - RGB channel
// order and [0,1] normalization were already correct here; only the
// padding/resize above needed fixing.
func toCHWFloat(img *stdimage.NRGBA, size int) []float32 {
	out := make([]float32, 3*size*size)
	plane := size * size
	for y := 0; y < size; y++ {
		row := img.PixOffset(0, y)
		for x := 0; x < size; x++ {
			i := row + x*4
			idx := y*size + x
			out[idx] = float32(img.Pix[i]) / 255.0
			out[plane+idx] = float32(img.Pix[i+1]) / 255.0
			out[2*plane+idx] = float32(img.Pix[i+2]) / 255.0
		}
	}
	return out
}
