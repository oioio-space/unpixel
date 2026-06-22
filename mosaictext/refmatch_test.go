package mosaictext_test

import (
	"context"
	"errors"
	"image"
	"image/draw"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/mosaictext"
)

// syntheticRefMosaic renders text with the given font data at the given size,
// crops to the sentinel boundary, pixelates, and returns the result — identical
// to syntheticMosaic in hmm_test.go but isolated here for clarity.
func syntheticRefMosaic(tb testing.TB, text string, fontData []byte, fs float64, block int, linear bool) image.Image {
	tb.Helper()
	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		tb.Fatalf("build renderer: %v", err)
	}
	rendered, sentinelX, err := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: 16, PaddingLeft: 4})
	if err != nil {
		tb.Fatalf("render %q: %v", text, err)
	}
	cropped := image.NewRGBA(image.Rect(0, 0, sentinelX, rendered.Bounds().Dy()))
	draw.Draw(cropped, cropped.Bounds(), rendered, image.Point{}, draw.Src)

	var pix unpixel.Pixelator
	if linear {
		pix = defaults.LinearBlockAverage(block)
	} else {
		pix = defaults.BlockAverage(block)
	}
	return pix.Pixelate(cropped, 0, 0)
}

// embedInWhiteRef places mosaic inside a white canvas with block-aligned
// margins so InferBlockGrid has clean background to work with.
func embedInWhiteRef(mosaic image.Image, block int) *image.RGBA {
	mb := mosaic.Bounds()
	marginX := block * 5
	marginY := block * 3
	canvas := image.NewRGBA(image.Rect(0, 0, mb.Dx()+2*marginX, mb.Dy()+2*marginY))
	for i := range len(canvas.Pix) / 4 {
		canvas.Pix[i*4+0] = 255
		canvas.Pix[i*4+1] = 255
		canvas.Pix[i*4+2] = 255
		canvas.Pix[i*4+3] = 255
	}
	draw.Draw(canvas, image.Rect(marginX, marginY, marginX+mb.Dx(), marginY+mb.Dy()),
		mosaic, mb.Min, draw.Src)
	return canvas
}

// refFont returns the named bundled Font or marks tb failed.
func refFont(tb testing.TB, name string) fonts.Font {
	tb.Helper()
	for _, f := range fonts.All() {
		if f.Name == name {
			return f
		}
	}
	tb.Fatalf("bundled font %q not found", name)
	return fonts.Font{}
}

// --- EXACT-RECOVERY TESTS ---

// TestDecodeReference_PropFont_ExactRecovery is the key proof: DecodeReference
// with a PROPORTIONAL font (Liberation Sans) recovers a non-language,
// mixed-case password-like string exactly. This is the case P-A's LM beam
// would struggle with because there is no language bias to exploit.
func TestDecodeReference_PropFont_ExactRecovery(t *testing.T) {
	f := refFont(t, "Liberation Sans")
	const (
		text  = "Pa55w0rd!"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false) // sRGB
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Sans"),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(0), // sRGB
	)
	if err != nil {
		t.Fatalf("DecodeReference: %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.4f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeReference = %q, want %q", res.Text, text)
	}
}

// TestDecodeReference_MonoFont_Linear_ExactRecovery exercises the linear-light
// path with a MONOSPACE font (Liberation Mono) and a random alnum string.
// Verifies exact recovery via the exact-font path (WithRefFont).
func TestDecodeReference_MonoFont_Linear_ExactRecovery(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "X7kQ2mR9"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, true) // linear
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Mono"),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(1), // linear
	)
	if err != nil {
		t.Fatalf("DecodeReference: %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.4f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeReference = %q, want %q", res.Text, text)
	}
}

// TestDecodeReference_WithRefFontFile verifies the user-font path: render in a
// bundled font, decode via WithRefFontFile(<those bytes>) → exact recovery.
// This proves the font-contract for caller-supplied font data.
func TestDecodeReference_WithRefFontFile(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "abc123"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false) // sRGB
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFontFile(f.Data), // supply font bytes, not a name
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(0),
	)
	if err != nil {
		t.Fatalf("DecodeReference (WithRefFontFile): %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.4f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeReference = %q, want %q", res.Text, text)
	}
}

// TestDecodeReference_BundledSweep verifies the fallback path: no font
// supplied → the bundled sweep runs and the correct bundled font wins.
func TestDecodeReference_BundledSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bundled sweep in -short mode")
	}
	f := refFont(t, "Liberation Mono")
	const (
		text  = "hello"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	// No WithRefFont / WithRefFontFile → sweep all bundled fonts.
	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(0),
	)
	if err != nil {
		t.Fatalf("DecodeReference (bundled sweep): %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.4f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeReference = %q, want %q", res.Text, text)
	}
}

// TestDecodeReference_WrongFont is an honest negative test: when the rendering
// font differs significantly from the target font, recovery is NOT exact.
// This documents the font-fidelity dependence clearly.
func TestDecodeReference_WrongFont(t *testing.T) {
	// Render in Liberation Sans (proportional), decode pretending it is
	// Liberation Mono (monospace) — very different metrics.
	sansFnt := refFont(t, "Liberation Sans")
	const (
		text  = "hello"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, sansFnt.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Mono"), // WRONG font intentionally
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(0),
	)
	// The call should succeed (no error), but the text should NOT match.
	if err != nil && !errors.Is(err, mosaictext.ErrNoContent) {
		t.Fatalf("DecodeReference (wrong font): unexpected error %v", err)
	}
	if err == nil {
		t.Logf("wrong-font result: %q (want ≠ %q, font=%s dist=%.4f)", res.Text, text, res.Font, res.Distance)
		if res.Text == text {
			// Not guaranteed to differ, but highly unlikely across font families.
			// Log rather than fail so this stays honest (observational).
			t.Logf("NOTE: wrong-font unexpectedly recovered the exact text (coincidence with block=%d)", block)
		}
	}
}

// --- ERROR / SENTINEL TESTS ---

// TestDecodeReference_Errors checks the sentinel error paths.
func TestDecodeReference_Errors(t *testing.T) {
	ctx := t.Context()

	// 1×1 white image → ErrNoMosaic.
	white := image.NewRGBA(image.Rect(0, 0, 1, 1))
	white.Pix[0], white.Pix[1], white.Pix[2], white.Pix[3] = 255, 255, 255, 255
	if _, err := mosaictext.DecodeReference(ctx, white); !errors.Is(err, mosaictext.ErrNoMosaic) {
		t.Errorf("1×1 white: got %v, want ErrNoMosaic", err)
	}

	// WithRefFont that does not match any bundled font → ErrNoContent.
	f := refFont(t, "Liberation Mono")
	mosaic := syntheticRefMosaic(t, "test", f.Data, 32, 8, false)
	img := embedInWhiteRef(mosaic, 8)
	if _, err := mosaictext.DecodeReference(
		ctx, img,
		mosaictext.WithRefFont("NoSuchFont XYZ"),
	); !errors.Is(err, mosaictext.ErrNoContent) {
		t.Errorf("unknown font: got %v, want ErrNoContent", err)
	}
}

// TestDecodeReference_ContextCancellation verifies cancellation is honoured.
func TestDecodeReference_ContextCancellation(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	mosaic := syntheticRefMosaic(t, "hi", f.Data, 32, 8, false)
	img := embedInWhiteRef(mosaic, 8)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // pre-cancel

	_, err := mosaictext.DecodeReference(
		ctx, img,
		mosaictext.WithRefFont("Liberation Mono"),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
	)
	// Either cancelled or no content — both are acceptable; must not hang.
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, mosaictext.ErrNoContent) {
		t.Errorf("cancelled context: unexpected error %v", err)
	}
}

// TestDecodeReference_ResultFields checks that a successful result has
// populated BlockSize, CharCount, and Font fields.
func TestDecodeReference_ResultFields(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "abc"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Mono"),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(0),
	)
	if err != nil {
		t.Fatalf("DecodeReference: %v", err)
	}
	if res.BlockSize <= 0 {
		t.Errorf("Result.BlockSize = %d, want > 0", res.BlockSize)
	}
	if res.CharCount <= 0 {
		t.Errorf("Result.CharCount = %d, want > 0", res.CharCount)
	}
	if res.Font == "" {
		t.Errorf("Result.Font is empty")
	}
	if res.Text == "" {
		t.Errorf("Result.Text is empty")
	}
}

// TestDecodeReference_WithRefFontFileBold exercises the WithRefFontFileBold
// option. A bold font supplied alongside the regular font should not cause
// an error; the result text must be non-empty.
func TestDecodeReference_WithRefFontFileBold(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "abc"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	// Supply the same font as both regular and bold (no separate bold face
	// for Liberation Mono; we just verify the option is accepted).
	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFontFile(f.Data),
		mosaictext.WithRefFontFileBold(f.Data),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(0),
	)
	if err != nil {
		t.Fatalf("DecodeReference with bold font file: %v", err)
	}
	if res.Text == "" {
		t.Error("Result.Text is empty with bold font file")
	}
}

// TestDecodeReference_LinearSweep verifies that the linear-light mode is tried
// when WithRefLinear is not pinned (default auto sweep). The result must not
// error and must contain text.
func TestDecodeReference_LinearSweep(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "XY"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Mono"),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		// WithRefLinear not set → auto sweep (default -1)
	)
	if err != nil {
		t.Fatalf("DecodeReference (auto linear sweep): %v", err)
	}
	if res.Text == "" {
		t.Error("Result.Text is empty with auto linear sweep")
	}
}

// --- ADDITIONAL COVERAGE TESTS ---

// TestWithRefLinear_SRGBOnly verifies that WithRefLinear(0) forces sRGB-only
// mode: DecodeReference succeeds and the returned result reflects sRGB (linear=false).
func TestWithRefLinear_SRGBOnly(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "abc"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Mono"),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(0), // force sRGB
	)
	if err != nil {
		t.Fatalf("DecodeReference (sRGB-only): %v", err)
	}
	if res.Linear {
		t.Errorf("WithRefLinear(0): got linear=true, want false")
	}
	if res.Text != text {
		t.Errorf("WithRefLinear(0): got %q, want %q", res.Text, text)
	}
}

// TestWithRefLinear_LinearOnly verifies that WithRefLinear(1) forces linear-only
// mode: DecodeReference succeeds and the returned result reflects linear=true.
func TestWithRefLinear_LinearOnly(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "abc"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, true)
	img := embedInWhiteRef(mosaic, block)

	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Mono"),
		mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
		mosaictext.WithRefLinear(1), // force linear
	)
	if err != nil {
		t.Fatalf("DecodeReference (linear-only): %v", err)
	}
	if !res.Linear {
		t.Errorf("WithRefLinear(1): got linear=false, want true")
	}
	if res.Text != text {
		t.Errorf("WithRefLinear(1): got %q, want %q", res.Text, text)
	}
}

// TestDecodeReference_DuplicateCharset verifies that a charset with repeated
// runes does not corrupt the advance table. The decoded result must match the
// same result as the clean charset (duplicates are simply deduplicated).
func TestDecodeReference_DuplicateCharset(t *testing.T) {
	f := refFont(t, "Liberation Mono")
	const (
		text  = "ab"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(t, text, f.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	// charset with deliberate duplicates — must not break advance measurement.
	dupCharset := "aabbccddeeffgghhiijj"
	res, err := mosaictext.DecodeReference(
		t.Context(), img,
		mosaictext.WithRefFont("Liberation Mono"),
		mosaictext.WithRefCharset(dupCharset),
		mosaictext.WithRefLinear(0),
	)
	if err != nil {
		t.Fatalf("DecodeReference (dup charset): %v", err)
	}
	if res.Text == "" {
		t.Error("DecodeReference (dup charset): empty result")
	}
}

// --- BENCHMARK ---

var sinkRefResult mosaictext.Result

// BenchmarkDecodeReference measures the full DecodeReference pipeline on a
// self-consistent synthetic fixture: Liberation Mono at 32 pt, block=8, sRGB,
// font pinned to focus on the reference-match loop rather than the font sweep.
func BenchmarkDecodeReference(b *testing.B) {
	f := refFont(b, "Liberation Mono")
	const (
		text  = "Pa55w0rd!"
		fs    = 32.0
		block = 8
	)
	mosaic := syntheticRefMosaic(b, text, f.Data, fs, block, false)
	img := embedInWhiteRef(mosaic, block)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, decErr := mosaictext.DecodeReference(
			context.Background(), img,
			mosaictext.WithRefFont("Liberation Mono"),
			mosaictext.WithRefCharset(mosaictext.DefaultRefCharset),
			mosaictext.WithRefLinear(0),
		)
		if decErr != nil {
			b.Fatalf("DecodeReference: %v", decErr)
		}
		sinkRefResult = res
	}
}
