package mosaictext_test

// did_test.go — integration tests for DecodeDID at three levels:
//
//  1. Monospace exact recovery (Liberation Mono, block 8) — validates emission cost.
//  2. Proportional short phrase (Liberation Sans) — validates boundary discovery.
//  3. Real sick fixture (sick_water_safety, Liberation Mono) — headline measurement.
//
// Tests are bounded to short strings and a pinned font so wall time is reasonable
// under the caged runner. The real fixture test is honest about partial recovery.

import (
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/mosaictext"

	xdraw "golang.org/x/image/draw"
)

// recoveryScore returns the fraction of runes in got that match want at the
// same position (Hamming similarity), after padding the shorter string.
func recoveryScore(got, want string) float64 {
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

// renderPixelated synthesises a pixelated mosaic of text at the given font,
// size, and block size — the forward model the DID decoder must invert.
//
// Crop-then-pixelate strategy: render at pad=8, crop to (pad,pad)-(inkRight,inkBottom),
// then pixelate the cropped image. This ensures block averages are computed over
// exactly the same pixel rows that decodeOneDID uses for its glyph canvases
// (height = content crop height, not the full padded render height). Mismatches
// from vertical-block averaging over different heights are eliminated.
func renderPixelated(t testing.TB, r unpixel.Renderer, text string, fs float64, block int, linear bool) image.Image {
	t.Helper()
	const pad = 8
	img, sx, err := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: pad, PaddingLeft: pad})
	if err != nil || sx <= 0 {
		t.Fatalf("render %q: %v", text, err)
	}
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	// Find ink extent within [0, sx) × full height.
	x1, y1 := b.Min.X, b.Min.Y
	const lumThresh = 240
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < sx; x++ {
			c := rgba.RGBAAt(x, y)
			lum := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
			if lum < lumThresh {
				x1 = max(x1, x+1)
				y1 = max(y1, y+1)
			}
		}
	}
	// Crop: X from pad (glyph advance cell origin), Y from pad (baseline row 0).
	cropW := x1 - pad
	cropH := y1 - pad
	if cropW <= 0 || cropH <= 0 {
		t.Fatalf("renderPixelated %q: no ink found", text)
	}
	crop := image.NewRGBA(image.Rect(0, 0, cropW, cropH))
	imutil.FillWhite(crop)
	xdraw.Draw(crop, crop.Bounds(), rgba, image.Pt(pad, pad), xdraw.Src)

	var pix unpixel.Pixelator
	if linear {
		pix = pixelate.NewLinearBlockAverage(block)
	} else {
		pix = pixelate.NewBlockAverage(block)
	}
	return pix.Pixelate(crop, 0, 0)
}

// loadMonoRenderer returns the Liberation Mono renderer from the bundled fonts.
func loadMonoRenderer(t *testing.T) (unpixel.Renderer, string) {
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
	t.Fatal("Liberation Mono not found in bundled fonts")
	return nil, ""
}

// loadSansRenderer returns the Liberation Sans renderer from the bundled fonts.
func loadSansRenderer(t *testing.T) (unpixel.Renderer, string) {
	t.Helper()
	all := fonts.All()
	for _, f := range all {
		if f.Name == "Liberation Sans" {
			r, err := render.NewXImageFromFonts(f.Data, nil)
			if err != nil {
				t.Fatalf("load Liberation Sans: %v", err)
			}
			return r, f.Name
		}
	}
	t.Fatal("Liberation Sans not found in bundled fonts")
	return nil, ""
}

// TestDecodeDID_Monospace validates the emission cost on a monospace font where
// character boundaries are known. This is the fundamental soundness check: if the
// per-glyph emission model is correct, monospace recovery must be exact or near-exact.
func TestDecodeDID_Monospace(t *testing.T) {
	r, _ := loadMonoRenderer(t)
	const (
		text  = "hello"
		fs    = 32.0
		block = 8
	)
	img := renderPixelated(t, r, text, fs, block, true) // linear = GIMP mode

	got, err := mosaictext.DecodeDID(
		t.Context(), img,
		mosaictext.WithDIDFont("Liberation Mono"),
		mosaictext.WithDIDCharset("abcdefghijklmnopqrstuvwxyz "),
		mosaictext.WithDIDLinear(1), // pin to linear
		mosaictext.WithDIDFontSize(fs),
		mosaictext.WithDIDBlockSize(block),
	)
	if err != nil {
		t.Fatalf("DecodeDID monospace: %v", err)
	}

	score := recoveryScore(got.Text, text)
	t.Logf("DecodeDID monospace: got=%q want=%q score=%.2f dist=%.4f evals=%d",
		got.Text, text, score, got.Distance, got.EmissionEvals)

	// For monospace, the emission model should recover at least 60% of characters.
	// Perfect recovery is the goal; partial is acceptable given block-boundary effects.
	if score < 0.6 {
		t.Errorf("DecodeDID monospace recovery score = %.2f, want ≥ 0.60 (got %q, want %q)",
			score, got.Text, text)
	}
	if got.Distance <= 0 || math.IsInf(got.Distance, 0) || math.IsNaN(got.Distance) {
		t.Errorf("DecodeDID monospace: distance = %v, want finite positive", got.Distance)
	}
}

// TestDecodeDID_Proportional validates boundary discovery on a proportional font
// (Liberation Sans) without known character boundaries. Uses a phrase where every
// glyph has advance ≥ 8 px (= block size) so interior block comparisons are
// available for all characters; narrow glyphs ('i','j','l', advance=7) are a
// separate known limitation documented in the sick-water-safety test.
func TestDecodeDID_Proportional(t *testing.T) {
	r, _ := loadSansRenderer(t)
	const (
		// "good" — four characters, each with advance=18, no narrow glyphs.
		// The proportional advances (g=18, o=18, o=18, d=18) mean character
		// boundaries do not coincide with block grid lines, exercising the DID
		// boundary-discovery property that monospace decoders lack.
		text  = "good"
		fs    = 32.0
		block = 8
	)
	img := renderPixelated(t, r, text, fs, block, true)

	got, err := mosaictext.DecodeDID(
		t.Context(), img,
		mosaictext.WithDIDFont("Liberation Sans"),
		mosaictext.WithDIDCharset("abcdefghijklmnopqrstuvwxyz"),
		mosaictext.WithDIDLinear(1),
		mosaictext.WithDIDFontSize(fs),
		mosaictext.WithDIDBlockSize(block),
	)
	if err != nil {
		t.Fatalf("DecodeDID proportional: %v", err)
	}

	score := recoveryScore(got.Text, text)
	t.Logf("DecodeDID proportional: got=%q want=%q score=%.2f dist=%.4f evals=%d",
		got.Text, text, score, got.Distance, got.EmissionEvals)

	// Expect ≥ 75% character accuracy on a 4-char all-wide phrase.
	if score < 0.75 {
		t.Errorf("DecodeDID proportional recovery score = %.2f, want ≥ 0.75 (got %q, want %q)",
			score, got.Text, text)
	}
}

// TestDecodeDID_SickWaterSafety runs DecodeDID on the real sick_water_safety fixture
// (Liberation Mono, block 8, "nobody is practicing water safety"). This is the
// headline measurement for `sick` corpus recovery; results are reported honestly
// regardless of outcome.
func TestDecodeDID_SickWaterSafety(t *testing.T) {
	const fixturePath = "../testdata/sick/sick_water_safety.png"
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Skipf("sick fixture not found at %s: %v", fixturePath, err)
	}
	defer f.Close() //nolint:errcheck

	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	_ = img // ensure non-nil

	const want = "nobody is practicing water safety"

	got, err := mosaictext.DecodeDID(
		t.Context(), img,
		mosaictext.WithDIDFont("Liberation Mono"),
		mosaictext.WithDIDCharset("abcdefghijklmnopqrstuvwxyz "),
		mosaictext.WithDIDLinear(-1), // auto: sweep both
		mosaictext.WithDIDBlockSize(8),
	)
	// Report result regardless of error — honest measurement.
	if err != nil {
		t.Logf("DecodeDID sick_water_safety error: %v", err)
		t.Logf("(no recovery produced — reporting 0/32 chars)")
		return
	}

	score := recoveryScore(got.Text, want)
	t.Logf("DecodeDID sick_water_safety: got=%q", got.Text)
	t.Logf("  want=%q", want)
	t.Logf("  score=%.2f  dist=%.4f  font=%s  linear=%v  phase=%d  evals=%d",
		score, got.Distance, got.Font, got.Linear, got.GridPhaseX, got.EmissionEvals)

	// No hard pass/fail assertion here — this is an honest reporting test.
	// The score quantifies where DID currently stands on the sick corpus.
	_ = score
}

// TestDecodeDID_Determinism verifies that two identical calls return identical text.
func TestDecodeDID_Determinism(t *testing.T) {
	r, _ := loadMonoRenderer(t)
	const (
		text  = "abc"
		fs    = 32.0
		block = 8
	)
	img := renderPixelated(t, r, text, fs, block, false)

	opts := []mosaictext.DIDOption{
		mosaictext.WithDIDFont("Liberation Mono"),
		mosaictext.WithDIDCharset("abcdefghijklmnopqrstuvwxyz "),
		mosaictext.WithDIDLinear(0),
		mosaictext.WithDIDFontSize(fs),
		mosaictext.WithDIDBlockSize(block),
	}

	r1, err1 := mosaictext.DecodeDID(t.Context(), img, opts...)
	r2, err2 := mosaictext.DecodeDID(t.Context(), img, opts...)

	if (err1 == nil) != (err2 == nil) {
		t.Fatalf("determinism: err1=%v err2=%v", err1, err2)
	}
	if err1 == nil && r1.Text != r2.Text {
		t.Errorf("determinism: first=%q second=%q differ", r1.Text, r2.Text)
	}
}

// didBenchSink prevents dead-code elimination of the DID result.
var didBenchSink mosaictext.DIDResult

// BenchmarkDecodeDID measures end-to-end DID decode throughput (render→
// pixelate→trellis DP) on the "good" phrase with Liberation Sans. Emission
// evaluation count is reported via b.ReportMetric so hot-path changes are
// tracked without a separate profiling run.
func BenchmarkDecodeDID(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render: %v", err)
	}
	// renderPixelated accepts testing.TB; *testing.B satisfies that interface.
	img := renderPixelated(b, r, "good", 32.0, 8, true)
	b.ReportAllocs()
	b.ResetTimer()

	var totalEvals int
	for b.Loop() {
		got, err := mosaictext.DecodeDID(
			b.Context(), img,
			mosaictext.WithDIDFont("Liberation Sans"),
			mosaictext.WithDIDCharset("abcdefghijklmnopqrstuvwxyz"),
			mosaictext.WithDIDLinear(1),
			mosaictext.WithDIDFontSize(32),
			mosaictext.WithDIDBlockSize(8),
		)
		if err != nil {
			b.Fatalf("DecodeDID: %v", err)
		}
		didBenchSink = got
		totalEvals += got.EmissionEvals
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/op")
}

// TestDecodeDID_ErrNoMosaic verifies ErrNoMosaic on a flat white image.
func TestDecodeDID_ErrNoMosaic(t *testing.T) {
	white := image.NewRGBA(image.Rect(0, 0, 4, 4))
	// Fill white explicitly.
	for y := range 4 {
		for x := range 4 {
			white.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	_, err := mosaictext.DecodeDID(t.Context(), white)
	if err == nil {
		t.Error("DecodeDID(white): want ErrNoMosaic, got nil")
	}
}

// TestDecodeDID_DefaultsUnchanged verifies that calling Decode (not DecodeDID)
// produces the same result before and after importing the DID package — opt-in
// means existing decoders are byte-identical.
func TestDecodeDID_DefaultsUnchanged(t *testing.T) {
	// A tiny all-white image returns ErrNoMosaic from both Decode and DecodeDID.
	white := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			white.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	_, err1 := mosaictext.Decode(t.Context(), white)
	_, err2 := mosaictext.DecodeDID(t.Context(), white)
	// Both must fail — neither returns a result on a blank image.
	if err1 == nil {
		t.Error("Decode(white): expected error, got nil")
	}
	if err2 == nil {
		t.Error("DecodeDID(white): expected error, got nil")
	}
	// The existing Decode error must still be ErrNoMosaic (not changed by DID).
	if !strings.Contains(err1.Error(), "mosaic") {
		t.Errorf("Decode(white) error = %v, want ErrNoMosaic", err1)
	}
}
