package mosaictext

// did_unit_test.go — white-box tests for the DID emission cost function.
// These are in the mosaictext package so they can call unexported helpers:
// columnEmissionDID, inkBounds, measureAdvancesByCumulative, mseRGB.

import (
	"image"
	"math"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"

	xdraw "golang.org/x/image/draw"
)

// pixelateTarget renders text with PaddingTop/PaddingLeft=pad, then crops to
// [pad, inkRight) × [pad, inkBottom) — using pad as the X and Y origin so the
// glyph's advance and baseline both align with (0,0) in the output. This
// matches how decodeOneDID processes a real mosaic: the image starts at the
// glyph's upper-left corner, so isolated PaddingTop=0 PaddingLeft=0 renders
// placed at (col, 0) align both horizontally and vertically.
func pixelateTarget(t *testing.T, r unpixel.Renderer, text string, fs float64, block int, linear bool) *image.RGBA {
	t.Helper()
	const pad = 8
	img, sx, err := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: pad, PaddingLeft: pad})
	if err != nil || sx <= 0 {
		t.Fatalf("render %q: %v (sx=%d)", text, err, sx)
	}
	rgba := imutil.ToRGBA(img)
	bb := inkBounds(rgba, sx)
	if bb.Empty() {
		t.Fatalf("no ink in render of %q", text)
	}
	// Crop: X starts at pad (glyph bearing aligns with x=0), Y starts at pad
	// (glyph cap-height aligns with y=0 for a PaddingTop=0 render).
	cropX, cropY := pad, pad
	cropW := bb.Max.X - cropX
	cropH := bb.Max.Y - cropY
	if cropW <= 0 || cropH <= 0 {
		t.Fatalf("render %q: content fully inside padding", text)
	}
	crop := image.NewRGBA(image.Rect(0, 0, cropW, cropH))
	imutil.FillWhite(crop)
	xdraw.Draw(crop, crop.Bounds(), rgba, image.Pt(cropX, cropY), xdraw.Src)

	var pix unpixel.Pixelator
	if linear {
		pix = pixelate.NewLinearBlockAverage(block)
	} else {
		pix = pixelate.NewBlockAverage(block)
	}
	return pix.Pixelate(crop, 0, 0)
}

// glyphImageFor renders a single glyph tile matching exactly what decodeOneDID
// stores in glyphImgs: PaddingTop=0, PaddingLeft=0, full render height clipped
// to bandH — no Y-ink-crop, so the glyph's baseline position is preserved.
func glyphImageFor(t *testing.T, r unpixel.Renderer, ch rune, fs float64, bandH int) *image.RGBA {
	t.Helper()
	img, sx, err := r.Render(string(ch), unpixel.Style{FontSize: fs, PaddingTop: 0, PaddingLeft: 0})
	if err != nil || sx <= 0 {
		blank := image.NewRGBA(image.Rect(0, 0, 1, bandH))
		imutil.FillWhite(blank)
		return blank
	}
	clipW := min(sx, img.Bounds().Dx())
	clipH := min(bandH, img.Bounds().Dy())
	tile := image.NewRGBA(image.Rect(0, 0, clipW, clipH))
	imutil.FillWhite(tile)
	xdraw.Draw(tile, tile.Bounds(), img, img.Bounds().Min, xdraw.Src)
	return tile
}

// TestColumnEmissionDID_CorrectGlyphBest verifies that for each character in a
// short word, the correct glyph's emission cost is ≤ that of every wrong glyph
// at the same column position. This is the fundamental soundness check: if the
// emission model is wrong, characters will be misidentified.
func TestColumnEmissionDID_CorrectGlyphBest(t *testing.T) {
	r, err := render.NewXImage() // Liberation Sans (default embedded font)
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}

	const (
		text  = "abc"
		fs    = 32.0
		block = 8
	)

	target := pixelateTarget(t, r, text, fs, block, false)
	W := target.Bounds().Dx()
	H := target.Bounds().Dy()
	t.Logf("target W=%d H=%d", W, H)

	pix := pixelate.NewBlockAverage(block)
	charset := []rune("abcdefghijklmnopqrstuvwxyz ")

	advances := measureAdvancesByCumulative(r, charset, fs)
	t.Logf("'a' advance=%d 'b' advance=%d 'c' advance=%d", advances['a'], advances['b'], advances['c'])

	glyphImgs := make(map[rune]*image.RGBA, len(charset))
	for _, ch := range charset {
		glyphImgs[ch] = glyphImageFor(t, r, ch, fs, H)
	}

	col := 0
	for _, correct := range text {
		adv := advances[correct]
		if adv <= 0 {
			t.Fatalf("no advance for %q", correct)
		}
		correctCost := columnEmissionDID(target, glyphImgs[correct], adv, col, block, 0, H, pix)
		t.Logf("col=%d correct='%c' cost=%.4f", col, correct, correctCost)

		var beaten []rune
		for _, cand := range charset {
			if cand == correct {
				continue
			}
			candAdv := advances[cand]
			if candAdv <= 0 {
				continue
			}
			c := columnEmissionDID(target, glyphImgs[cand], candAdv, col, block, 0, H, pix)
			if c < correctCost-1e-6 {
				beaten = append(beaten, cand)
				t.Logf("  '%c' beats correct with cost=%.4f", cand, c)
			}
		}
		if len(beaten) > 0 {
			t.Errorf("col=%d '%c': beaten by %d candidates — emission model wrong", col, correct, len(beaten))
		}
		col += adv
	}
}

// TestColumnEmissionDID_InfOnDegenerate verifies +Inf for degenerate inputs.
func TestColumnEmissionDID_InfOnDegenerate(t *testing.T) {
	target := image.NewRGBA(image.Rect(0, 0, 16, 8))
	imutil.FillWhite(target)
	pix := pixelate.NewBlockAverage(8)

	if cost := columnEmissionDID(target, nil, 8, 16, 8, 0, 8, pix); !math.IsInf(cost, 1) {
		t.Errorf("startCol>=W: cost=%v, want +Inf", cost)
	}
	if cost := columnEmissionDID(target, nil, 0, 0, 8, 0, 8, pix); !math.IsInf(cost, 1) {
		t.Errorf("glyphAdv=0: cost=%v, want +Inf", cost)
	}
}
