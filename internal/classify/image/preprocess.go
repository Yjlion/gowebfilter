package image

import (
	stdimage "image"
	"image/color"
	"image/draw"

	"github.com/disintegration/imaging"
)

// letterboxFill is Ultralytics' own standard YOLOv8 letterbox padding color
// (RGB 114,114,114 - a neutral mid-gray), used here so a NudeNet-v3-style
// export sees the same input distribution it was trained/exported with.
var letterboxFill = &stdimage.Uniform{C: color.RGBA{R: 114, G: 114, B: 114, A: 255}}

// letterbox resizes img to fit within a size x size square, preserving
// aspect ratio, and pads the remainder with letterboxFill - the standard
// YOLOv8 preprocessing step. Returns the square NRGBA image ready for
// toCHWFloat.
func letterbox(img stdimage.Image, size int) *stdimage.NRGBA {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 || size <= 0 {
		return stdimage.NewNRGBA(stdimage.Rect(0, 0, size, size))
	}

	scale := float64(size) / float64(srcW)
	if s := float64(size) / float64(srcH); s < scale {
		scale = s
	}
	newW := maxInt(1, int(float64(srcW)*scale+0.5))
	newH := maxInt(1, int(float64(srcH)*scale+0.5))
	if newW > size {
		newW = size
	}
	if newH > size {
		newH = size
	}
	resized := imaging.Resize(img, newW, newH, imaging.Linear)

	canvas := stdimage.NewNRGBA(stdimage.Rect(0, 0, size, size))
	draw.Draw(canvas, canvas.Bounds(), letterboxFill, stdimage.Point{}, draw.Src)
	offX := (size - newW) / 2
	offY := (size - newH) / 2
	draw.Draw(canvas, stdimage.Rect(offX, offY, offX+newW, offY+newH), resized, stdimage.Point{}, draw.Src)
	return canvas
}

// toCHWFloat converts a size x size NRGBA image into a planar (channel-
// first) float32 tensor normalized to [0,1], RGB order - the standard
// Ultralytics YOLOv8 ONNX input layout (1,3,H,W).
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
