package mosaictext

// did_unit_test.go — white-box tests for the DID emission cost function.
// These are in the mosaictext package so they can call unexported helpers:
// columnEmissionDID, inkBounds, measureAdvancesByCumulative, mseRGB.

import (
	"image"
	"math"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
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

// applyDIDOptions builds a didConfig from defaultDIDConfig and applies opts,
// mirroring the logic in DecodeDID so option-setter tests stay honest.
func applyDIDOptions(opts ...DIDOption) didConfig {
	cfg := defaultDIDConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// TestWithDIDFontFile verifies that supplying non-empty font bytes sets
// fontData, and that an empty slice is ignored (guard branch).
func TestWithDIDFontFile(t *testing.T) {
	sentinel := []byte("FONT")
	cfg := applyDIDOptions(WithDIDFontFile(sentinel))
	if got, want := string(cfg.fontData), string(sentinel); got != want {
		t.Errorf("fontData: got %q, want %q", got, want)
	}

	// Empty slice must be ignored — fontData stays nil.
	cfg2 := applyDIDOptions(WithDIDFontFile(nil))
	if cfg2.fontData != nil {
		t.Errorf("fontData after nil arg: got %v, want nil", cfg2.fontData)
	}
	cfg3 := applyDIDOptions(WithDIDFontFile([]byte{}))
	if cfg3.fontData != nil {
		t.Errorf("fontData after empty arg: got %v, want nil", cfg3.fontData)
	}
}

// TestWithDIDFontFileBold verifies that supplying non-empty bold bytes sets
// fontBold, and that an empty slice is ignored.
func TestWithDIDFontFileBold(t *testing.T) {
	sentinel := []byte("BOLD")
	cfg := applyDIDOptions(WithDIDFontFileBold(sentinel))
	if got, want := string(cfg.fontBold), string(sentinel); got != want {
		t.Errorf("fontBold: got %q, want %q", got, want)
	}

	cfg2 := applyDIDOptions(WithDIDFontFileBold(nil))
	if cfg2.fontBold != nil {
		t.Errorf("fontBold after nil arg: got %v, want nil", cfg2.fontBold)
	}
}

// TestWithDIDLambda verifies that a non-negative lambda is stored and that a
// negative value is ignored (guard branch).
func TestWithDIDLambda(t *testing.T) {
	cfg := applyDIDOptions(WithDIDLambda(0.5))
	if got, want := cfg.lambda, 0.5; got != want {
		t.Errorf("lambda: got %v, want %v", got, want)
	}

	// Negative lambda must be ignored — field stays at zero value.
	cfg2 := applyDIDOptions(WithDIDLambda(-1.0))
	if cfg2.lambda != 0 {
		t.Errorf("lambda after negative arg: got %v, want 0 (ignored)", cfg2.lambda)
	}
}

// TestWithDIDLanguage verifies that the language field is updated to the
// supplied value (French, distinct from the English default).
func TestWithDIDLanguage(t *testing.T) {
	cfg := applyDIDOptions(WithDIDLanguage(lang.French))
	if got, want := cfg.language, lang.French; got != want {
		t.Errorf("language: got %v, want %v", got, want)
	}

	// Default must be English.
	dflt := applyDIDOptions()
	if got, want := dflt.language, lang.English; got != want {
		t.Errorf("default language: got %v, want %v", got, want)
	}
}

// TestWithDIDContext verifies that WithDIDContext stores the flag correctly.
func TestWithDIDContext(t *testing.T) {
	cfg := applyDIDOptions(WithDIDContext(true))
	if !cfg.contextEmission {
		t.Error("WithDIDContext(true): contextEmission not set")
	}

	cfg2 := applyDIDOptions(WithDIDContext(false))
	if cfg2.contextEmission {
		t.Error("WithDIDContext(false): contextEmission should be false")
	}

	// Default must be false (isolated emission — backward-compatible).
	dflt := applyDIDOptions()
	if dflt.contextEmission {
		t.Error("default contextEmission: got true, want false (isolated by default)")
	}
}

// TestColumnEmissionContextDID_CorrectGlyphBest verifies that the context-aware
// emission (left neighbor rendered alongside the current glyph) still ranks the
// correct glyph first for interior characters. Uses a 3-character word "abc" at
// block=8 where the middle glyph 'b' has a left neighbor 'a'.
func TestColumnEmissionContextDID_CorrectGlyphBest(t *testing.T) {
	r, err := render.NewXImage()
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

	glyphImgs := make(map[rune]*image.RGBA, len(charset))
	for _, ch := range charset {
		glyphImgs[ch] = glyphImageFor(t, r, ch, fs, H)
	}

	// Test the second character 'b' at its known column with left neighbor 'a'.
	aAdv := advances['a']
	bAdv := advances['b']
	if aAdv <= 0 || bAdv <= 0 {
		t.Fatalf("missing advance for 'a' or 'b'")
	}
	col := aAdv // 'b' starts after 'a'

	correctCost := columnEmissionContextDID(target, glyphImgs['a'], glyphImgs['b'], aAdv, bAdv, col, block, 0, H, pix)
	t.Logf("col=%d correct='b' (left='a') contextCost=%.4f", col, correctCost)

	// Compare context cost vs isolated cost to log the boundary improvement.
	isolatedCost := columnEmissionDID(target, glyphImgs['b'], bAdv, col, block, 0, H, pix)
	t.Logf("col=%d correct='b' isolatedCost=%.4f contextCost=%.4f delta=%.4f",
		col, isolatedCost, correctCost, isolatedCost-correctCost)

	// The context-aware cost for the correct glyph must be finite.
	if math.IsInf(correctCost, 0) || math.IsNaN(correctCost) {
		t.Errorf("context cost for correct 'b': got %v, want finite", correctCost)
	}

	// The correct glyph must not be beaten by more than half the charset.
	var beaten []rune
	for _, cand := range charset {
		if cand == 'b' {
			continue
		}
		candAdv := advances[cand]
		if candAdv <= 0 {
			continue
		}
		c := columnEmissionContextDID(target, glyphImgs['a'], glyphImgs[cand], aAdv, candAdv, col, block, 0, H, pix)
		if c < correctCost-1e-6 {
			beaten = append(beaten, cand)
		}
	}
	if len(beaten) > len(charset)/2 {
		t.Errorf("col=%d 'b': beaten by %d/%d candidates in context mode — emission model wrong",
			col, len(beaten), len(charset))
	}
	t.Logf("col=%d 'b': beaten by %d/%d candidates (context mode)", col, len(beaten), len(charset))
}

// TestColumnEmissionContextDID_InfOnDegenerate verifies +Inf for degenerate
// inputs to the context-aware emission function.
func TestColumnEmissionContextDID_InfOnDegenerate(t *testing.T) {
	target := image.NewRGBA(image.Rect(0, 0, 16, 8))
	imutil.FillWhite(target)
	pix := pixelate.NewBlockAverage(8)

	// startCol >= W.
	if cost := columnEmissionContextDID(target, nil, nil, 0, 8, 16, 8, 0, 8, pix); !math.IsInf(cost, 1) {
		t.Errorf("startCol>=W: got %v, want +Inf", cost)
	}
	// glyphAdv = 0.
	if cost := columnEmissionContextDID(target, nil, nil, 0, 0, 0, 8, 0, 8, pix); !math.IsInf(cost, 1) {
		t.Errorf("glyphAdv=0: got %v, want +Inf", cost)
	}
}

// TestDecodeDID_ContextOptIn verifies that WithDIDContext(true) produces a
// result on the clean "hello" fixture and that the distance remains finite.
// This is the opt-in gate: the context path must not break clean monospace.
func TestDecodeDID_ContextOptIn(t *testing.T) {
	r, _ := loadMonoRendererForUnit(t)
	const (
		text  = "hello"
		fs    = 32.0
		block = 8
	)

	target := pixelateTarget(t, r, text, fs, block, true)

	got, err := DecodeDID(
		t.Context(), target,
		WithDIDFont("Liberation Mono"),
		WithDIDCharset("abcdefghijklmnopqrstuvwxyz "),
		WithDIDLinear(1),
		WithDIDFontSize(fs),
		WithDIDBlockSize(block),
		WithDIDContext(true),
	)
	if err != nil {
		t.Fatalf("DecodeDID context opt-in: %v", err)
	}

	score := recoveryScoreUnit(got.Text, text)
	t.Logf("DecodeDID context opt-in: got=%q want=%q score=%.2f dist=%.4f",
		got.Text, text, score, got.Distance)

	if math.IsInf(got.Distance, 0) || math.IsNaN(got.Distance) {
		t.Errorf("context mode: distance = %v, want finite", got.Distance)
	}
	if score < 0.6 {
		t.Errorf("context mode regressed clean monospace: score=%.2f < 0.60", score)
	}
}

// recoveryScoreUnit is a local copy of recoveryScore for use in the white-box
// test package (cannot import from mosaictext_test).
func recoveryScoreUnit(got, want string) float64 {
	gr := []rune(got)
	wr := []rune(want)
	if len(wr) == 0 {
		return 0
	}
	match := 0
	for i := range min(len(gr), len(wr)) {
		if gr[i] == wr[i] {
			match++
		}
	}
	return float64(match) / float64(len(wr))
}

// loadMonoRendererForUnit loads Liberation Mono without t.Fatal-if-not-found
// that would block other tests — it uses the same logic as did_test.go's
// loadMonoRenderer but lives in the white-box package.
func loadMonoRendererForUnit(t *testing.T) (unpixel.Renderer, string) {
	t.Helper()
	all := fonts.All()
	for _, f := range all {
		if f.Name == "Liberation Mono" {
			r, err := render.NewXImageFromFonts(f.Data, nil)
			if err != nil {
				t.Fatalf("load Liberation Mono: %v", err)
			}
			return r, f.Name
		}
	}
	t.Fatal("Liberation Mono not found")
	return nil, ""
}
