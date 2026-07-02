package image

// Cheap skin-region heuristic used to gate the MobileNetV2 classifier (see
// detector.go's prefilterSkinRatio) - ported verbatim from
// privoxy-nsfw-guard's detect.go (same author, MIT-licensed).

import (
	"image"
	"sort"
)

// skinAnalysis is the result of the skin-region heuristic for one image.
//
// The detector classifies each pixel as skin/non-skin in RGB+YCbCr color
// space, groups skin pixels into connected regions, and scores the image on
// how much of it is skin and how concentrated that skin is. Nude photos are
// dominated by a few large connected skin regions; ordinary photos either
// have little skin (scenes, products, screenshots) or scattered small
// patches (crowds).
type skinAnalysis struct {
	SkinRatio    float64 // skin pixels / all pixels
	LargestShare float64 // largest skin region / all pixels
	Top3OfSkin   float64 // 3 largest regions / all skin pixels
	Regions      int
	Score        float64 // 0..1
}

// analysisDim is the raster size detection runs at. Detection quality does
// not improve past ~200px and the cost is quadratic.
const analysisDim = 192

// analyzeSkin runs the skin-region heuristic on img.
func analyzeSkin(img image.Image) skinAnalysis {
	small := downscale(img, analysisDim)
	w, h := small.Rect.Dx(), small.Rect.Dy()
	total := w * h
	an := skinAnalysis{}
	if total == 0 {
		return an
	}

	mask := make([]bool, total)
	skin := 0
	for y := 0; y < h; y++ {
		row := small.Pix[y*small.Stride:]
		for x := 0; x < w; x++ {
			p := row[x*4 : x*4+3]
			if isSkin(p[0], p[1], p[2]) {
				mask[y*w+x] = true
				skin++
			}
		}
	}
	an.SkinRatio = float64(skin) / float64(total)
	if skin == 0 {
		return an
	}

	sizes := skinComponents(mask, w, h)
	an.Regions = len(sizes)
	an.LargestShare = float64(sizes[0]) / float64(total)
	top3 := 0
	for i := 0; i < len(sizes) && i < 3; i++ {
		top3 += sizes[i]
	}
	an.Top3OfSkin = float64(top3) / float64(skin)

	// Score: how far the skin ratio is into "mostly skin" territory, damped
	// when the skin is scattered across many small regions.
	base := clampFloat((an.SkinRatio-0.12)/0.38, 0, 1) // 0 at 12% skin, 1 at 50%
	concentration := 0.45 + 0.55*an.Top3OfSkin
	an.Score = clampFloat(base*concentration, 0, 1)
	return an
}

// isSkin classifies one pixel. Two RGB rules (daylight and darker skin)
// gated by a YCbCr chroma window — the classic Ap-Apid style classifier.
func isSkin(r8, g8, b8 uint8) bool {
	r, g, b := int(r8), int(g8), int(b8)

	maxc := r
	if g > maxc {
		maxc = g
	}
	if b > maxc {
		maxc = b
	}
	minc := r
	if g < minc {
		minc = g
	}
	if b < minc {
		minc = b
	}

	// Daylight rule.
	a := r > 95 && g > 40 && b > 20 && maxc-minc > 15 && absInt(r-g) > 15 && r > g && r > b
	// Relaxed rule for darker skin / dimmer lighting.
	d := r > 60 && g > 30 && b > 15 && r > g && r > b && absInt(r-g) >= 10
	if !a && !d {
		return false
	}

	// YCbCr chroma gate (JPEG coefficients).
	fr, fg, fb := float64(r), float64(g), float64(b)
	y := 0.299*fr + 0.587*fg + 0.114*fb
	cb := 128 - 0.168736*fr - 0.331264*fg + 0.5*fb
	cr := 128 + 0.5*fr - 0.418688*fg - 0.081312*fb
	return y > 30 && cb >= 75 && cb <= 130 && cr >= 131 && cr <= 178
}

// skinComponents labels 8-connected regions in mask and returns their
// sizes, largest first.
func skinComponents(mask []bool, w, h int) []int {
	seen := make([]bool, len(mask))
	var sizes []int
	stack := make([]int, 0, 256)
	for start := range mask {
		if !mask[start] || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		size := 0
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			px, py := p%w, p/w
			for dy := -1; dy <= 1; dy++ {
				ny := py + dy
				if ny < 0 || ny >= h {
					continue
				}
				for dx := -1; dx <= 1; dx++ {
					nx := px + dx
					if nx < 0 || nx >= w {
						continue
					}
					q := ny*w + nx
					if mask[q] && !seen[q] {
						seen[q] = true
						stack = append(stack, q)
					}
				}
			}
		}
		sizes = append(sizes, size)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sizes)))
	return sizes
}

// downscale box-samples src so its longest side is maxDim, returning RGBA.
// Each destination pixel averages up to 4x4 samples from its source cell —
// plenty for region statistics and much cheaper than a full-resolution pass.
func downscale(src image.Image, maxDim int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	dw, dh := sw, sh
	if sw >= sh && sw > maxDim {
		dw = maxDim
		dh = sh * maxDim / sw
	} else if sh > sw && sh > maxDim {
		dh = maxDim
		dw = sw * maxDim / sh
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		sy0 := b.Min.Y + dy*sh/dh
		sy1 := b.Min.Y + (dy+1)*sh/dh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			sx0 := b.Min.X + dx*sw/dw
			sx1 := b.Min.X + (dx+1)*sw/dw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			r, g, bl := avgRect(src, sx0, sy0, sx1, sy1)
			o := dst.PixOffset(dx, dy)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = r, g, bl, 255
		}
	}
	return dst
}

// avgRect averages up to 4x4 sample points inside the source rect.
func avgRect(src image.Image, x0, y0, x1, y1 int) (uint8, uint8, uint8) {
	stepX := (x1 - x0 + 3) / 4
	if stepX < 1 {
		stepX = 1
	}
	stepY := (y1 - y0 + 3) / 4
	if stepY < 1 {
		stepY = 1
	}
	var r, g, b, n uint64
	for y := y0; y < y1; y += stepY {
		for x := x0; x < x1; x += stepX {
			pr, pg, pb, _ := src.At(x, y).RGBA()
			r += uint64(pr >> 8)
			g += uint64(pg >> 8)
			b += uint64(pb >> 8)
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return uint8(r / n), uint8(g / n), uint8(b / n)
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
