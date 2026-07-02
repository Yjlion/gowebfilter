package image

import (
	"image/color"
	"testing"
)

func TestSkinPaletteClassified(t *testing.T) {
	hits := 0
	for _, tone := range skinTones {
		if isSkin(tone.R, tone.G, tone.B) {
			hits++
		} else {
			t.Logf("tone %v not classified as skin", tone)
		}
	}
	if hits < 6 {
		t.Errorf("only %d/%d skin tones classified as skin", hits, len(skinTones))
	}
}

func TestNonSkinRejected(t *testing.T) {
	nonSkin := []color.RGBA{
		{110, 165, 220, 255}, // sky blue
		{60, 130, 70, 255},   // grass
		{128, 128, 128, 255}, // gray
		{250, 250, 250, 255}, // white
		{10, 10, 10, 255},    // black
		{200, 30, 30, 255},   // pure red
		{44, 50, 66, 255},    // slate background
	}
	for _, c := range nonSkin {
		if isSkin(c.R, c.G, c.B) {
			t.Errorf("non-skin color %v classified as skin", c)
		}
	}
}

func TestAnalyzeSkinNudeScoresHigh(t *testing.T) {
	an := analyzeSkin(synthNude(400, 300))
	t.Logf("nude: %+v", an)
	if an.Score < 0.5 {
		t.Errorf("nude synthetic scored %.3f, want >= 0.5", an.Score)
	}
	if an.SkinRatio < 0.4 {
		t.Errorf("skin ratio %.3f, want >= 0.4", an.SkinRatio)
	}
}

func TestAnalyzeSkinPortraitScoresLow(t *testing.T) {
	an := analyzeSkin(synthPortrait(400, 300))
	t.Logf("portrait: %+v", an)
	if an.Score >= 0.45 {
		t.Errorf("portrait scored %.3f, want < 0.45", an.Score)
	}
}

func TestAnalyzeSkinScatteredScoresLow(t *testing.T) {
	an := analyzeSkin(synthScattered(400, 300))
	t.Logf("scattered: %+v", an)
	if an.Score >= 0.40 {
		t.Errorf("scattered patches scored %.3f, want < 0.40", an.Score)
	}
}

func TestAnalyzeSkinSceneScoresZero(t *testing.T) {
	an := analyzeSkin(synthScene(400, 300))
	t.Logf("scene: %+v", an)
	if an.SkinRatio > 0.01 {
		t.Errorf("scene skin ratio %.3f, want ~0", an.SkinRatio)
	}
	if an.Score > 0.05 {
		t.Errorf("scene scored %.3f, want ~0", an.Score)
	}
}

func TestSkinComponents(t *testing.T) {
	// Two regions: 2x2 block and a single pixel, on a 4x4 grid.
	mask := []bool{
		true, true, false, false,
		true, true, false, false,
		false, false, false, true,
		false, false, false, false,
	}
	sizes := skinComponents(mask, 4, 4)
	if len(sizes) != 2 || sizes[0] != 4 || sizes[1] != 1 {
		t.Errorf("skinComponents = %v, want [4 1]", sizes)
	}
}

func TestDownscaleDims(t *testing.T) {
	img := flat(1000, 500, bgSlate)
	small := downscale(img, 192)
	if small.Rect.Dx() != 192 || small.Rect.Dy() != 96 {
		t.Errorf("downscale dims = %dx%d, want 192x96", small.Rect.Dx(), small.Rect.Dy())
	}
}
