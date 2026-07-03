//go:build ignore

// gen_tray_icon.go regenerates the base64 PNG embedded as defaultTrayIcon in
// cmd/webfilter/cmd_tray.go: a 64x64 blue globe (disc plus meridian/parallel
// grid lines), drawn with the standard library only so it doesn't need an
// external image asset or license.
//
// Example:
//
//	go run scripts/gen_tray_icon.go > /tmp/globe.png
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size)/2 - 2

	globeBlue := color.RGBA{0x2b, 0x6c, 0xb0, 0xff}
	lineWhite := color.RGBA{0xff, 0xff, 0xff, 0xff}

	inCircle := func(x, y float64) bool {
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}

	// Fill the globe disc with an anti-aliased edge via supersampling.
	const ss = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			hits := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := float64(px) + (float64(sx)+0.5)/ss
					y := float64(py) + (float64(sy)+0.5)/ss
					if inCircle(x, y) {
						hits++
					}
				}
			}
			if hits > 0 {
				a := uint8(255 * hits / (ss * ss))
				img.Set(px, py, color.RGBA{globeBlue.R, globeBlue.G, globeBlue.B, a})
			}
		}
	}

	drawEllipseArc := func(rx, ry float64) {
		for t := 0.0; t < 2*math.Pi; t += 0.005 {
			x := cx + rx*math.Cos(t)
			y := cy + ry*math.Sin(t)
			px, py := int(math.Round(x)), int(math.Round(y))
			if px < 0 || py < 0 || px >= size || py >= size {
				continue
			}
			if inCircle(float64(px), float64(py)) {
				img.Set(px, py, lineWhite)
				img.Set(px+1, py, lineWhite)
			}
		}
	}

	// Equator and two meridians (at different widths, to fake a 3D globe).
	drawEllipseArc(r, r*0.14)
	drawEllipseArc(r*0.5, r)
	drawEllipseArc(r*0.92, r)

	// Two latitude bands above/below the equator.
	drawLat := func(yOff, rxScale float64) {
		ry := r * 0.1
		rx := r * rxScale
		cy2 := cy + yOff
		for t := 0.0; t < 2*math.Pi; t += 0.005 {
			x := cx + rx*math.Cos(t)
			y := cy2 + ry*math.Sin(t)
			px, py := int(math.Round(x)), int(math.Round(y))
			if px < 0 || py < 0 || px >= size || py >= size {
				continue
			}
			if inCircle(float64(px), float64(py)) {
				img.Set(px, py, lineWhite)
			}
		}
	}
	drawLat(-r*0.45, 0.85)
	drawLat(r*0.45, 0.85)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(buf.Bytes()); err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, base64.StdEncoding.EncodeToString(buf.Bytes()))
}
