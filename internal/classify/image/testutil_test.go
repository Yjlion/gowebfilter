package image

import (
	"bytes"
	stdimage "image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// Skin tones across ethnicities, used to paint synthetic test images -
// ported from privoxy-nsfw-guard's testutil_test.go.
var skinTones = []color.RGBA{
	{255, 224, 189, 255},
	{241, 194, 125, 255},
	{229, 184, 143, 255},
	{224, 172, 105, 255},
	{198, 134, 66, 255},
	{172, 112, 61, 255},
	{147, 95, 55, 255},
	{120, 79, 49, 255},
}

// fillEllipse paints an ellipse of skin tone with deterministic per-pixel
// jitter so the region isn't one flat color.
func fillEllipse(img *stdimage.RGBA, cx, cy, rx, ry int, tone color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx := float64(x-cx) / float64(rx)
			dy := float64(y-cy) / float64(ry)
			if dx*dx+dy*dy > 1 {
				continue
			}
			j := int8((x*31 + y*17) % 13)
			img.SetRGBA(x, y, color.RGBA{
				jitter(tone.R, j), jitter(tone.G, j), jitter(tone.B, j), 255,
			})
		}
	}
}

func jitter(v uint8, j int8) uint8 {
	n := int(v) + int(j) - 6
	if n < 0 {
		n = 0
	}
	if n > 255 {
		n = 255
	}
	return uint8(n)
}

func flat(w, h int, c color.RGBA) *stdimage.RGBA {
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

var bgSlate = color.RGBA{44, 50, 66, 255}

// synthNude: one large connected skin ellipse covering ~59% of the frame.
func synthNude(w, h int) *stdimage.RGBA {
	img := flat(w, h, bgSlate)
	fillEllipse(img, w/2, h/2, int(0.45*float64(w)), int(0.42*float64(h)), skinTones[3])
	return img
}

// synthPortrait: a face-sized skin ellipse, ~12% of the frame.
func synthPortrait(w, h int) *stdimage.RGBA {
	img := flat(w, h, bgSlate)
	fillEllipse(img, w/2, h/3, w/5, h/5, skinTones[1])
	return img
}

// synthScattered: many small separated skin patches, ~14% total.
func synthScattered(w, h int) *stdimage.RGBA {
	img := flat(w, h, bgSlate)
	r := w / 28
	for i := 0; i < 20; i++ {
		cx := (i%5)*(w/5) + w/10
		cy := (i/5)*(h/4) + h/8
		fillEllipse(img, cx, cy, r, r, skinTones[i%len(skinTones)])
	}
	return img
}

// synthScene: sky over grass, no skin at all.
func synthScene(w, h int) *stdimage.RGBA {
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, w, h))
	sky := color.RGBA{110, 165, 220, 255}
	grass := color.RGBA{60, 130, 70, 255}
	for y := 0; y < h; y++ {
		c := sky
		if y > h/2 {
			c = grass
		}
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func encJPEG(t *testing.T, img stdimage.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encPNG(t *testing.T, img stdimage.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
