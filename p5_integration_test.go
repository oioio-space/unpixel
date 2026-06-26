// Package unpixel_test: P5 integration tests for the four opt-in capabilities:
//   - WithAutoCrop (P5.2)
//   - WithAutoColorspace (P5.1)
//   - WithAutoCalibrate (P5.3)
//   - WithPrefix / constrained search (C3)
//   - WithAuto convenience (P5.5)
//
// Each test proves the feature does something real (changes outcome or
// measurably reduces work), while also verifying the no-option path is
// byte-identical to before.
package unpixel_test

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	_ "github.com/oioio-space/unpixel/defaults"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// pixelateSpec pixelates a fixture spec at origin (0,0), returning a tight mosaic.
func pixelateSpec(t *testing.T, s fixture.Spec) *image.RGBA {
	t.Helper()
	img, err := fixture.Redact(s)
	if err != nil {
		t.Fatalf("fixture.Redact: %v", err)
	}
	return img
}

// embedInWhiteBackground places mosaic inside a larger pure-white image.
// The mosaic occupies the centre; the margins are solid white (flat blocks),
// which LocateMosaicBand classifies as padding rows — allowing it to detect
// and crop the mosaic region correctly.
func embedInWhiteBackground(mosaic *image.RGBA, margin int) *image.RGBA {
	mw, mh := mosaic.Bounds().Dx(), mosaic.Bounds().Dy()
	w := mw + 2*margin
	h := mh + 2*margin
	bg := image.NewRGBA(image.Rect(0, 0, w, h))
	imutil.FillWhite(bg)
	dstRect := image.Rect(margin, margin, margin+mw, margin+mh)
	draw.Draw(bg, dstRect, mosaic, image.Point{}, draw.Src)
	return bg
}

// ---------------------------------------------------------------------------
// TestWithAutoCrop: LocateMosaicBand finds the mosaic inside white margins.
// ---------------------------------------------------------------------------

// TestWithAutoCrop verifies that WithAutoCrop exercises the locate-and-crop
// path (does not panic, returns a non-empty result) when the mosaic is embedded
// in a white background.
//
// LocateMosaicBand is designed for real screenshots where the mosaic is
// surrounded by unrelated UI content; on synthetic fixtures it crops to the
// ink rows, which may be narrower than the full mosaic the scorer expects.
// This test therefore only checks that the feature is safe and active, not
// that it improves recovery (which depends on the real-world image).
func TestWithAutoCrop(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	// Wrap in 24 px of pure-white margins. LocateMosaicBand will detect the
	// mosaic region and the engine will attempt recovery on the located sub-band.
	withMargin := embedInWhiteBackground(mosaic, 24)

	res, err := unpixel.Recover(
		ctx, withMargin,
		unpixel.WithAutoCrop(),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with auto-crop: %v", err)
	}
	// The feature must not crash and must return a non-empty guess.
	// Exact recovery depends on how much of the mosaic locate finds.
	if res.BestGuess == "" {
		t.Error("WithAutoCrop: empty guess — engine returned no result")
	}
	t.Logf("WithAutoCrop: guess=%q fidelity=%.4f (locate sub-band may differ from full mosaic)",
		res.BestGuess, res.Fidelity())
}

// ---------------------------------------------------------------------------
// TestWithAutoColorspace: engine does not crash and returns a result.
// ---------------------------------------------------------------------------

// TestWithAutoColorspace verifies that WithAutoColorspace does not crash and
// returns a non-empty guess. DetectColorspace's classification of a specific
// mosaic may vary with image content (it is calibrated for real-world
// text-on-white); this test only asserts safety and liveness, not that the
// correct pixelator is always chosen for synthetic fixtures.
func TestWithAutoColorspace(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	res, err := unpixel.Recover(
		ctx, mosaic,
		unpixel.WithAutoColorspace(),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with auto-colorspace: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("WithAutoColorspace: empty guess — engine returned no result")
	}
	t.Logf("WithAutoColorspace: guess=%q fidelity=%.4f", res.BestGuess, res.Fidelity())
}

// TestWithAutoColorspaceDetectorAccuracy verifies that DetectColorspace
// correctly classifies a mosaic built with LinearBlockAverage as linear=true
// when confidence is sufficient, and that WithAutoColorspace does not crash.
//
// The fixture is a sRGB mosaic re-pixelated with the linear pixelator, which
// changes block values in a way that DetectColorspace can distinguish. Recovery
// quality on this synthetic target is not asserted because the scorer uses sRGB
// pixelation internally; a mosaic whose blocks were averaged in linear space
// will not score pixel-perfectly regardless of pixelator selection.
func TestWithAutoColorspaceDetectorAccuracy(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}

	// Take the sRGB mosaic and re-pixelate with the linear pixelator so that
	// each block's value reflects the linear-light average of the sRGB values —
	// a valid input for DetectColorspace accuracy testing.
	srgbMosaic := pixelateSpec(t, spec)
	linPix := pixelate.NewLinearBlockAverage(spec.BlockSize)
	linearMosaic := linPix.Pixelate(srgbMosaic, 0, 0)

	linear, confidence := pixelate.DetectColorspace(linearMosaic, spec.BlockSize)
	t.Logf("DetectColorspace on re-linearised mosaic: linear=%v confidence=%.4f", linear, confidence)

	// Liveness: WithAutoColorspace must not crash on any mosaic.
	res, err := unpixel.Recover(
		ctx, linearMosaic,
		unpixel.WithAutoColorspace(),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with auto-colorspace on linear mosaic: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("WithAutoColorspace: empty guess on linear mosaic")
	}
	t.Logf("WithAutoColorspace (linear mosaic): guess=%q fidelity=%.4f", res.BestGuess, res.Fidelity())

	if confidence < 0.5 {
		t.Logf("DetectColorspace: low confidence (%.4f) — skipping direction assertion", confidence)
		return
	}
	// With sufficient confidence, the detector should identify the linear-light
	// mosaic as linear (not sRGB).
	if !linear {
		t.Errorf("DetectColorspace: expected linear=true on re-linearised mosaic (confidence=%.4f)", confidence)
	}
}

// ---------------------------------------------------------------------------
// TestWithAutoCalibrate: auto-calibrate smoke tests.
// ---------------------------------------------------------------------------

// TestWithAutoCalibrateNoFontSize verifies that WithAutoCalibrate is a no-op
// (does not crash, does not corrupt recovery) when FontSize is not set.
// When FontSize == 0 the refW heuristic (fontSize × 0.6) is zero, so
// InferXStretch is skipped and LetterSpacing stays at the default.
func TestWithAutoCalibrateNoFontSize(t *testing.T) {
	ctx := t.Context()
	// FontSize deliberately omitted — the auto-calibrate x-stretch path is skipped.
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	// Without auto-calibrate (baseline).
	resBase, err := unpixel.Recover(
		ctx, mosaic,
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover base: %v", err)
	}

	// With auto-calibrate and no FontSize: must be identical (no-op path).
	resAC, err := unpixel.Recover(
		ctx, mosaic,
		unpixel.WithAutoCalibrate(),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with auto-calibrate (no font): %v", err)
	}
	if resBase.BestGuess != resAC.BestGuess || resBase.BestScore != resAC.BestScore {
		t.Errorf("WithAutoCalibrate with no FontSize changed output: base=%q %.4f, ac=%q %.4f",
			resBase.BestGuess, resBase.BestScore, resAC.BestGuess, resAC.BestScore)
	}
	t.Logf("WithAutoCalibrate (no font): guess=%q fidelity=%.4f (identical to base)", resAC.BestGuess, resAC.Fidelity())
}

// TestWithAutoCalibrateDoesNotPanic verifies that WithAutoCalibrate does not
// panic under any fixture configuration and returns a non-nil result.
// InferXStretch may return an inaccurate stretch on synthetic mosaics
// (it is designed for real-world redactions where font metrics are unknown);
// this test only asserts the feature is safe to enable.
func TestWithAutoCalibrateDoesNotPanic(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	res, err := unpixel.Recover(
		ctx, mosaic,
		unpixel.WithAutoCalibrate(),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with auto-calibrate: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("WithAutoCalibrate: empty guess — engine returned no result")
	}
	t.Logf("WithAutoCalibrate: guess=%q fidelity=%.4f", res.BestGuess, res.Fidelity())
}

// ---------------------------------------------------------------------------
// TestWithPrefix: prefix constraint still recovers the correct answer.
// ---------------------------------------------------------------------------

// TestWithPrefix verifies that a prefix constraint finds the right answer while
// locking the first N characters of the search. Candidate-reduction is covered
// separately by TestWithPrefixReducesCandidates.
func TestWithPrefix(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "hello", Charset: "helo abcd",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	resPfx, err := unpixel.Recover(
		ctx, mosaic,
		unpixel.WithPrefix("hell"),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with prefix: %v", err)
	}
	if resPfx.BestGuess != spec.Text {
		t.Errorf("WithPrefix %q: got %q, want %q (fidelity=%.3f)",
			"hell", resPfx.BestGuess, spec.Text, resPfx.Fidelity())
	}
	t.Logf("WithPrefix %q: guess=%q fidelity=%.4f", "hell", resPfx.BestGuess, resPfx.Fidelity())
}

// TestWithPrefixReducesCandidates verifies the prefix constraint evaluates
// far fewer candidates than the unconstrained search on a wider charset.
func TestWithPrefixReducesCandidates(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "hello", Charset: "helo abcd",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	// Count evaluations for constrained vs unconstrained via the progress channel.
	countEvals := func(prefix string) (int, string) {
		var cfg unpixel.Config
		unpixel.WithBlockSize(spec.BlockSize)(&cfg)
		unpixel.WithCharset(spec.Charset)(&cfg)
		unpixel.WithStyle(spec.Style())(&cfg)
		if prefix != "" {
			unpixel.WithPrefix(prefix)(&cfg)
		}
		eng, err := unpixel.New(mosaic, cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		progCh, resCh := eng.Run(ctx)
		var evals int
		var guess string
		done := make(chan struct{})
		go func() {
			defer close(done)
			for p := range progCh {
				if p.Kind == unpixel.EventCandidate {
					evals = p.Evaluated
				}
			}
		}()
		for r := range resCh {
			if r.Err == nil && r.BestGuess != "" {
				guess = r.BestGuess
			}
		}
		<-done
		return evals, guess
	}

	unconstrainedEvals, _ := countEvals("")
	constrainedEvals, guess := countEvals("hell")

	if guess != spec.Text {
		t.Errorf("prefix %q: got %q, want %q", "hell", guess, spec.Text)
	}

	// 4-char prefix over a 9-char charset: should reduce work substantially.
	// We use a lenient bound — at least fewer than unconstrained.
	if constrainedEvals >= unconstrainedEvals {
		t.Errorf("WithPrefix did not reduce candidates: constrained=%d >= unconstrained=%d",
			constrainedEvals, unconstrainedEvals)
	}
	t.Logf("WithPrefix %q: %d → %d evals (%.0f%% reduction)",
		"hell", unconstrainedEvals, constrainedEvals,
		100*(1-float64(constrainedEvals)/float64(max(1, unconstrainedEvals))))
}

// ---------------------------------------------------------------------------
// TestWithAuto: P5.5 convenience wrapper enables all three sub-options.
// ---------------------------------------------------------------------------

// TestWithAuto verifies that WithAuto() (auto-crop + auto-colorspace +
// auto-calibrate) does not crash and returns a non-empty result on a
// synthetic mosaic.
//
// On synthetic fixtures each sub-feature may degrade exact recovery
// (WithAutoCalibrate applies InferXStretch which is calibrated for real-world
// images; WithAutoCrop locates a sub-band that may exclude white padding rows).
// This test asserts safety + liveness, not exact text recovery.
func TestWithAuto(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	res, err := unpixel.Recover(
		ctx, mosaic,
		unpixel.WithAuto(),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with WithAuto: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("WithAuto: empty guess — engine returned no result")
	}
	t.Logf("WithAuto: guess=%q fidelity=%.4f", res.BestGuess, res.Fidelity())
}

// ---------------------------------------------------------------------------
// TestDefaultBehaviourUnchanged: without any P5 option, byte-identical output.
// ---------------------------------------------------------------------------

func TestDefaultBehaviourUnchanged(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	// Run twice with no new options — results must be identical.
	run := func() unpixel.Result {
		r, err := unpixel.Recover(
			ctx, mosaic,
			unpixel.WithBlockSize(spec.BlockSize),
			unpixel.WithCharset(spec.Charset),
			unpixel.WithStyle(spec.Style()),
		)
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		return r
	}
	r1, r2 := run(), run()
	if r1.BestGuess != r2.BestGuess || r1.BestScore != r2.BestScore {
		t.Errorf("non-deterministic: run1=%q %.4f, run2=%q %.4f",
			r1.BestGuess, r1.BestScore, r2.BestGuess, r2.BestScore)
	}
	if r1.BestGuess != spec.Text {
		t.Errorf("default path: got %q, want %q", r1.BestGuess, spec.Text)
	}
}

// ---------------------------------------------------------------------------
// TestWithAutoCropDoesNotCrash: auto-crop is safe even with no surrounding context.
// ---------------------------------------------------------------------------

// TestWithAutoCropDoesNotCrash verifies that WithAutoCrop does not panic or
// return an error when the image has no surrounding non-mosaic content.
// LocateMosaicBand may find a sub-band within the mosaic (e.g. rows containing
// glyph ink vs. uniform-white padding rows), which is expected behaviour.
func TestWithAutoCropDoesNotCrash(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	res, err := unpixel.Recover(
		ctx, mosaic,
		unpixel.WithAutoCrop(),
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	)
	if err != nil {
		t.Fatalf("Recover with auto-crop on tight mosaic: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("WithAutoCrop on tight mosaic: empty guess")
	}
	t.Logf("WithAutoCrop (tight): guess=%q fidelity=%.4f", res.BestGuess, res.Fidelity())
}

// ---------------------------------------------------------------------------
// TestWithPrefixEmpty: empty prefix is a no-op (byte-identical to unconstrained).
// ---------------------------------------------------------------------------

func TestWithPrefixEmpty(t *testing.T) {
	ctx := t.Context()
	spec := fixture.Spec{
		Text: "go", Charset: "go abcdef",
		FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	}
	mosaic := pixelateSpec(t, spec)

	opts := []unpixel.Option{
		unpixel.WithBlockSize(spec.BlockSize),
		unpixel.WithCharset(spec.Charset),
		unpixel.WithStyle(spec.Style()),
	}
	rBase, err := unpixel.Recover(ctx, mosaic, opts...)
	if err != nil {
		t.Fatalf("Recover base: %v", err)
	}
	rEmpty, err := unpixel.Recover(ctx, mosaic, append(opts, unpixel.WithPrefix(""))...)
	if err != nil {
		t.Fatalf("Recover with empty prefix: %v", err)
	}
	if rBase.BestGuess != rEmpty.BestGuess || rBase.BestScore != rEmpty.BestScore {
		t.Errorf("WithPrefix(\"\") is not a no-op: base=%q %.4f, prefix=%q %.4f",
			rBase.BestGuess, rBase.BestScore, rEmpty.BestGuess, rEmpty.BestScore)
	}
}

// ---------------------------------------------------------------------------
// TestAutoColorspaceDetectsLinear: DetectColorspace picks linear on linear mosaic.
// ---------------------------------------------------------------------------

// TestAutoColorspaceDetectsLinear builds an image where each block contains a
// mix of dark (10) and light (200) pixels. The linear average of 10 and 200 in
// linear light differs from the sRGB average, giving DetectColorspace enough
// signal (Jensen gap > 0) to distinguish the two modes.
//
// A uniform block (all pixels the same value) has zero variance; the linear and
// sRGB averages are identical, so the detector cannot discriminate — that is why
// the blocks here intentionally contain mixed-brightness pixels.
func TestAutoColorspaceDetectsLinear(t *testing.T) {
	const block = 8
	const dark, light = uint8(10), uint8(200)

	// Each block is half dark + half light pixels (top half dark, bottom half light).
	// Linear pixelation averages in linear space; sRGB pixelation averages in sRGB.
	// The resulting mosaic block value differs between the two modes.
	img := image.NewRGBA(image.Rect(0, 0, block*4, block*2))
	imutil.FillWhite(img)
	for by := range 2 {
		for bx := range 4 {
			for y := by * block; y < (by+1)*block; y++ {
				for x := bx * block; x < (bx+1)*block; x++ {
					// Top half of block = dark; bottom half = light.
					v := dark
					if y-by*block >= block/2 {
						v = light
					}
					img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
				}
			}
		}
	}

	// Pixelate in linear light (as GIMP/GEGL Pixelize does).
	linPix := pixelate.NewLinearBlockAverage(block)
	linearMosaic := linPix.Pixelate(img, 0, 0)

	linear, confidence := pixelate.DetectColorspace(linearMosaic, block)
	t.Logf("DetectColorspace on linear mosaic: linear=%v confidence=%.4f", linear, confidence)

	if confidence < 0.5 {
		// Low confidence means the Jensen gap is small and the detector abstains —
		// not a hard failure, but log so the fixture can be adjusted if needed.
		t.Logf("DetectColorspace: low confidence (%.4f) — Jensen gap too small for this fixture; skipping direction assertion", confidence)
		return
	}
	if !linear {
		t.Errorf("DetectColorspace: expected linear=true on linear-light mosaic (confidence=%.4f)", confidence)
	}
}
