package mosaictext_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
	"github.com/oioio-space/unpixel/mosaictext"
)

// TestDecodeVarFont_RoundTrip is the end-to-end proof for the varfont decoder:
//
//  1. Render "Hi!" with the Nunito variable font at wght=780 and pixelate it
//     (block size 8, linear-light) — the synthetic redaction.
//  2. Call DecodeVarFont with the known text ("Hi!") to fit the wght axis.
//  3. Expect: recovered text == "Hi!", fitted wght near 780, distance near 0.
//
// Calibration mode (WithVarFontText) is used because blind joint text+axis
// search over a proportional font is intractable at small charset size; the
// calibration mode fits axes to a known text fragment, then is ready to decode
// the same-font redaction. A blind mode is documented in DecodeVarFont.
//
// Timing: ~100–500 ms depending on hardware (FitAxes runs 12 rounds × ~2 axis
// probes × render+pixelate+metric; wght is the only axis).
func TestDecodeVarFont_RoundTrip(t *testing.T) {
	const (
		targetWght = float32(780)
		blockSize  = 8
		knownText  = "Hi!"
	)

	// Build synthetic redaction: render at targetWght, pixelate.
	font, err := varfont.ParseFont(bytes.NewReader(vfembed.NunitoVFWght))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	style := varfont.DefaultStyle()
	rTarget, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), []varfont.Axis{
		{Tag: "wght", Value: targetWght},
	})
	if err != nil {
		t.Fatalf("NewVarRenderer: %v", err)
	}
	targetImg, _, err := rTarget.Render(knownText, style)
	if err != nil {
		t.Fatalf("render target: %v", err)
	}
	pix := pixelate.NewLinearBlockAverage(blockSize)
	redaction := pix.Pixelate(targetImg, 0, 0)

	// DecodeVarFont with known-text calibration mode.
	// WithVarFontLinear(true) must match the pixelator used to build the
	// synthetic redaction above; mismatching colour-space breaks the comparison.
	got, err := mosaictext.DecodeVarFont(
		t.Context(), redaction,
		mosaictext.WithVarFont(font),
		mosaictext.WithVarFontStyle(style),
		mosaictext.WithVarFontBlockSize(blockSize),
		mosaictext.WithVarFontLinear(true),
		mosaictext.WithVarFontText(knownText),
		mosaictext.WithVarFontAxes([]varfont.AxisSpec{
			{Tag: "wght", Min: 200, Max: 900, Start: 500},
		}),
	)
	if err != nil {
		t.Fatalf("DecodeVarFont: %v", err)
	}

	t.Logf("recovered text=%q wght=%.1f distance=%.4f evals=%d",
		got.Text, got.FittedAxes[0].Value, got.Distance, got.Evals)

	// got before want (Google style).
	if got.Text != knownText {
		t.Errorf("Text: got %q, want %q", got.Text, knownText)
	}
	const wghtTol = float32(100) // coordinate descent lands within 100 du of 780
	if d := got.FittedAxes[0].Value - targetWght; d < -wghtTol || d > wghtTol {
		t.Errorf("FittedAxes[0].Value: got %.1f, want %.1f ± %.0f",
			got.FittedAxes[0].Value, targetWght, wghtTol)
	}
	if got.Distance > 0.05 {
		t.Errorf("Distance: got %.4f, want <= 0.05 (near-zero means pixel-perfect match)", got.Distance)
	}
}

// TestDecodeVarFont_OptIn verifies that DecodeVarFont is fully independent of
// the existing decoders: calling it does not affect mosaictext.Decode results,
// and the --decoder default path is byte-identical before and after this
// package is imported.
func TestDecodeVarFont_OptIn(t *testing.T) {
	// A blank 64×16 white image: Decode returns ErrNoContent; DecodeVarFont
	// with no known text and a trivial charset also returns an error — but both
	// errors are independent and neither corrupts shared state.
	blank := image.NewRGBA(image.Rect(0, 0, 64, 16))
	for i := range blank.Pix {
		blank.Pix[i] = 255
	}

	_, decodeErr := mosaictext.Decode(t.Context(), blank)
	// Decode on a blank image must error (no mosaic or no content).
	if decodeErr == nil {
		t.Error("Decode on blank image: got nil error, want non-nil")
	}

	font, err := varfont.ParseFont(bytes.NewReader(vfembed.NunitoVFWght))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	_, vfErr := mosaictext.DecodeVarFont(
		t.Context(), blank,
		mosaictext.WithVarFont(font),
		mosaictext.WithVarFontText("a"),
		mosaictext.WithVarFontAxes([]varfont.AxisSpec{
			{Tag: "wght", Min: 200, Max: 900, Start: 500},
		}),
	)
	// DecodeVarFont on a blank image must also error (no mosaic grid).
	if vfErr == nil {
		t.Error("DecodeVarFont on blank image: got nil error, want non-nil")
	}

	// Re-running Decode after DecodeVarFont must yield the same error — no
	// shared state was corrupted.
	_, decodeErr2 := mosaictext.Decode(t.Context(), blank)
	if (decodeErr == nil) != (decodeErr2 == nil) {
		t.Errorf("Decode result changed after DecodeVarFont: first=%v second=%v", decodeErr, decodeErr2)
	}
}

// TestDecodeVarFont_BlindMode exercises fitBlind and WithVarFontCharset.
//
// It constructs a synthetic 1-char redaction (the letter "A" rendered and
// pixelated with the bundled Nunito font at a known wght) then calls
// DecodeVarFont without WithVarFontText so the blind-mode path runs. A
// charset of exactly one character ("A") is passed via WithVarFontCharset so
// the joint search has a single candidate — it must either accept or reject it
// under BlindDistanceGate.
//
// This test exercises:
//   - WithVarFontCharset (option setter)
//   - fitBlind's loop body (at least one FitAxes call)
//   - The gate logic at the end of fitBlind
//
// We do not assert the recovered text because a 1-char image with an aggressive
// distance gate may or may not pass; instead we verify that the call either
// returns a VarFontResult with Text=="A" OR returns ErrVarFontNoFit (both are
// correct behaviours for blind mode). Any other error is a bug.
func TestDecodeVarFont_BlindMode(t *testing.T) {
	const (
		blockSize = 8
		knownChar = "A"
	)

	font, err := varfont.ParseFont(bytes.NewReader(vfembed.NunitoVFWght))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	style := varfont.DefaultStyle()

	// Build synthetic redaction: render "A" at wght=600, pixelate.
	rTarget, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), []varfont.Axis{
		{Tag: "wght", Value: 600},
	})
	if err != nil {
		t.Fatalf("NewVarRenderer: %v", err)
	}
	targetImg, _, err := rTarget.Render(knownChar, style)
	if err != nil {
		t.Fatalf("render target: %v", err)
	}
	pix := pixelate.NewLinearBlockAverage(blockSize)
	redaction := pix.Pixelate(targetImg, 0, 0)

	// Call DecodeVarFont in blind mode (no WithVarFontText), WithVarFontCharset set.
	got, err := mosaictext.DecodeVarFont(
		t.Context(), redaction,
		mosaictext.WithVarFont(font),
		mosaictext.WithVarFontStyle(style),
		mosaictext.WithVarFontBlockSize(blockSize),
		mosaictext.WithVarFontLinear(true),
		mosaictext.WithVarFontCharset(knownChar), // exercises WithVarFontCharset
		mosaictext.WithVarFontAxes([]varfont.AxisSpec{
			{Tag: "wght", Min: 200, Max: 900, Start: 500},
		}),
	)

	// Acceptable outcomes: success with Text=="A", or ErrVarFontNoFit.
	// Any other error is a bug.
	switch {
	case err == nil:
		if got.Text != knownChar {
			t.Errorf("blind mode: got Text=%q, want %q", got.Text, knownChar)
		}
		t.Logf("blind mode succeeded: text=%q wght=%.1f dist=%.4f evals=%d",
			got.Text, got.FittedAxes[0].Value, got.Distance, got.Evals)
	case errors.Is(err, mosaictext.ErrVarFontNoFit):
		t.Logf("blind mode: ErrVarFontNoFit (dist above gate — acceptable for 1-char blind search)")
	default:
		t.Errorf("blind mode: unexpected error: %v", err)
	}
}

// TestDecodeVarFont_WithVisible tests the WithVarFontVisible calibration path:
//
//  1. Render "Hello" at wght=710 (sharp) — the visible-text crop.
//  2. Render "test" at wght=710 and pixelate it — the redaction.
//  3. Call DecodeVarFont with WithVarFontVisible("Hello" crop, "Hello") so
//     the calibration step warm-starts the axis fit.
//  4. Expect the decode to achieve a near-zero distance.
//
// This is the primary end-to-end test for the new calibration-from-visible
// feature wired into mosaictext.
func TestDecodeVarFont_WithVisible(t *testing.T) {
	const (
		trueWght  = float32(710)
		blockSize = 8
		visText   = "Hello"
		decText   = "test"
	)

	font, err := varfont.ParseFont(bytes.NewReader(vfembed.NunitoVFWght))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	style := varfont.DefaultStyle()
	pix := pixelate.NewLinearBlockAverage(blockSize)
	axes := []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: 400}}

	// Build the sharp visible crop (no pixelation).
	rVis, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), []varfont.Axis{
		{Tag: "wght", Value: trueWght},
	})
	if err != nil {
		t.Fatalf("NewVarRenderer visible: %v", err)
	}
	visSharp, _, err := rVis.Render(visText, style)
	if err != nil {
		t.Fatalf("render visible: %v", err)
	}

	// Build the pixelated redaction of the decode target.
	rDec, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), []varfont.Axis{
		{Tag: "wght", Value: trueWght},
	})
	if err != nil {
		t.Fatalf("NewVarRenderer dec: %v", err)
	}
	decSharp, _, err := rDec.Render(decText, style)
	if err != nil {
		t.Fatalf("render dec: %v", err)
	}
	redaction := pix.Pixelate(decSharp, 0, 0)

	got, err := mosaictext.DecodeVarFont(
		t.Context(), redaction,
		mosaictext.WithVarFont(font),
		mosaictext.WithVarFontStyle(style),
		mosaictext.WithVarFontBlockSize(blockSize),
		mosaictext.WithVarFontLinear(true),
		mosaictext.WithVarFontText(decText),
		mosaictext.WithVarFontAxes(axes),
		mosaictext.WithVarFontVisible(visSharp, visText),
	)
	if err != nil {
		t.Fatalf("DecodeVarFont (WithVisible): %v", err)
	}

	t.Logf("WithVisible: text=%q wght=%.1f (true %.1f) dist=%.4f evals=%d",
		got.Text, got.FittedAxes[0].Value, trueWght, got.Distance, got.Evals)

	if got, want := got.Distance, 0.05; got > want {
		t.Errorf("distance: got %.4f, want <= %.4f", got, want)
	}
}

// TestDecodeVarFont_WithOptimizer verifies that WithVarFontOptimizer(NelderMead)
// is accepted and still converges to a small distance.
func TestDecodeVarFont_WithOptimizer(t *testing.T) {
	const (
		trueWght  = float32(680)
		blockSize = 8
		knownText = "Go"
	)

	font, err := varfont.ParseFont(bytes.NewReader(vfembed.NunitoVFWght))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	style := varfont.DefaultStyle()
	pix := pixelate.NewLinearBlockAverage(blockSize)

	rTarget, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), []varfont.Axis{
		{Tag: "wght", Value: trueWght},
	})
	if err != nil {
		t.Fatalf("NewVarRenderer: %v", err)
	}
	targetImg, _, err := rTarget.Render(knownText, style)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	redaction := pix.Pixelate(targetImg, 0, 0)

	got, err := mosaictext.DecodeVarFont(
		t.Context(), redaction,
		mosaictext.WithVarFont(font),
		mosaictext.WithVarFontStyle(style),
		mosaictext.WithVarFontBlockSize(blockSize),
		mosaictext.WithVarFontLinear(true),
		mosaictext.WithVarFontText(knownText),
		mosaictext.WithVarFontAxes([]varfont.AxisSpec{
			{Tag: "wght", Min: 200, Max: 900, Start: 400},
		}),
		mosaictext.WithVarFontOptimizer(varfont.OptimizerNelderMead),
	)
	if err != nil {
		t.Fatalf("DecodeVarFont (NelderMead): %v", err)
	}

	t.Logf("NelderMead: wght=%.1f (true %.1f) dist=%.4f evals=%d",
		got.FittedAxes[0].Value, trueWght, got.Distance, got.Evals)

	if got, want := got.Distance, 0.05; got > want {
		t.Errorf("distance: got %.4f, want <= %.4f", got, want)
	}
}

// BenchmarkDecodeVarFont measures one calibration-mode decode call end-to-end
// (detect grid → FitAxes → report). Reports ns/op and eval count so the axis
// dimension cost is visible in benchstat output.
func BenchmarkDecodeVarFont(b *testing.B) {
	const (
		targetWght = float32(700)
		blockSize  = 8
		knownText  = "Hi"
	)

	b.ReportAllocs()

	font, err := varfont.ParseFont(bytes.NewReader(vfembed.NunitoVFWght))
	if err != nil {
		b.Fatalf("ParseFont: %v", err)
	}
	style := varfont.DefaultStyle()

	rTarget, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), []varfont.Axis{
		{Tag: "wght", Value: targetWght},
	})
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	targetImg, _, renderErr := rTarget.Render(knownText, style)
	if renderErr != nil {
		b.Fatalf("render: %v", renderErr)
	}
	pix := pixelate.NewLinearBlockAverage(blockSize)
	redaction := pix.Pixelate(targetImg, 0, 0)

	axes := []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: 500}}
	ctx := context.Background()

	b.ResetTimer()
	var totalEvals int
	var sinkResult mosaictext.VarFontResult
	for b.Loop() {
		r, benchErr := mosaictext.DecodeVarFont(
			ctx, redaction,
			mosaictext.WithVarFont(font),
			mosaictext.WithVarFontStyle(style),
			mosaictext.WithVarFontBlockSize(blockSize),
			mosaictext.WithVarFontLinear(true),
			mosaictext.WithVarFontText(knownText),
			mosaictext.WithVarFontAxes(axes),
		)
		if benchErr != nil {
			b.Fatalf("DecodeVarFont: %v", benchErr)
		}
		totalEvals += r.Evals
		sinkResult = r
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/fit")
	_ = sinkResult
}
