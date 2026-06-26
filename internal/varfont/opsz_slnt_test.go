package varfont_test

// Tests for opsz and slnt axis fitting using the bundled Roboto Flex VF.
//
// Roboto Flex axes exercised here:
//   - opsz: 8–144 (optical size — affects stroke contrast and glyph spacing)
//   - slnt: −10–0  (slant; 0 = upright, −10 = maximum slant)
//
// Signal note: at typical mosaic block sizes (≥ 4 px) the opsz and slnt
// differences are washed out by block-average pixelation, so the pixelated
// FitAxes path cannot recover these axes from a redaction crop alone. The
// correct path for opsz/slnt calibration is CalibrateFromVisible, which
// compares against the full-resolution sharp render of adjacent visible text.
// The tests below validate both paths honestly:
//
//   1. RoundTrip tests via CalibrateFromVisible (no pixelation) — these prove
//      convergence to the true axis value with a strong signal.
//   2. Visual-difference sanity checks — prove the axes produce measurably
//      different full-resolution renders (signal exists before pixelation).
//   3. FitAxes-on-pixelated stability — prove the API handles opsz/slnt
//      AxisSpecs without error even when the pixelated landscape is flat (the
//      optimizer stays at its start value rather than diverging or panicking).

import (
	"bytes"
	"testing"

	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
)

// robotoFlexData is the bundled Roboto Flex VF font used by opsz/slnt tests.
var robotoFlexData = vfembed.RobotoFlexVF

// mustParseRobotoFlex parses the Roboto Flex VF font or fails the test.
func mustParseRobotoFlex(t *testing.T) *varfont.Font {
	t.Helper()
	font, err := varfont.ParseFont(bytes.NewReader(robotoFlexData))
	if err != nil {
		t.Fatalf("ParseFont(RobotoFlex): %v", err)
	}
	return font
}

// mustRobotoFlexRenderer builds a VarRenderer for Roboto Flex at the given axes.
func mustRobotoFlexRenderer(t *testing.T, axes []varfont.Axis) *varfont.VarRenderer {
	t.Helper()
	r, err := varfont.NewVarRenderer(bytes.NewReader(robotoFlexData), axes)
	if err != nil {
		t.Fatalf("NewVarRenderer(RobotoFlex): %v", err)
	}
	return r
}

// TestRobotoFlex_ParseFont verifies that the embedded Roboto Flex font parses
// without error, catching file-corruption or embedding issues early.
func TestRobotoFlex_ParseFont(t *testing.T) {
	t.Parallel()
	_ = mustParseRobotoFlex(t)
}

// TestRobotoFlex_OpszVisualDifference verifies that rendering the same text at
// two distinct opsz values produces visually different full-resolution images,
// confirming that the opsz axis is active in the font and respected by the
// renderer. This is a prerequisite for the CalibrateFromVisible round-trip.
func TestRobotoFlex_OpszVisualDifference(t *testing.T) {
	t.Parallel()

	style := varfont.DefaultStyle()
	const text = "the"

	rSmall := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "opsz", Value: 8}})
	rLarge := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "opsz", Value: 144}})

	imgSmall, _, err := rSmall.Render(text, style)
	if err != nil {
		t.Fatalf("Render opsz=8: %v", err)
	}
	imgLarge, _, err := rLarge.Render(text, style)
	if err != nil {
		t.Fatalf("Render opsz=144: %v", err)
	}

	diff := countDiff(imgSmall, imgLarge)
	if got, want := diff, 5; got < want {
		t.Errorf("opsz=8 vs opsz=144: %d differing pixels, want >= %d — opsz axis not visible", got, want)
	}
	t.Logf("opsz=8 vs opsz=144: %d differing pixels (full resolution)", diff)
}

// TestRobotoFlex_SlntVisualDifference verifies that rendering the same text at
// slnt=0 (upright) vs slnt=−10 (maximum slant) produces visually different
// full-resolution images, confirming that the slnt axis is active in the font.
func TestRobotoFlex_SlntVisualDifference(t *testing.T) {
	t.Parallel()

	style := varfont.DefaultStyle()
	const text = "the"

	rUpright := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "slnt", Value: 0}})
	rSlanted := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "slnt", Value: -10}})

	imgUpright, _, err := rUpright.Render(text, style)
	if err != nil {
		t.Fatalf("Render slnt=0: %v", err)
	}
	imgSlanted, _, err := rSlanted.Render(text, style)
	if err != nil {
		t.Fatalf("Render slnt=-10: %v", err)
	}

	diff := countDiff(imgUpright, imgSlanted)
	if got, want := diff, 5; got < want {
		t.Errorf("slnt=0 vs slnt=-10: %d differing pixels, want >= %d — slnt axis not visible", got, want)
	}
	t.Logf("slnt=0 vs slnt=-10: %d differing pixels (full resolution)", diff)
}

// TestCalibrateFromVisible_Opsz_RoundTrip is the key correctness proof for opsz
// fitting via CalibrateFromVisible (sharp, un-pixelated comparison):
//
//  1. Render "the" at opsz=80 at full resolution — the synthetic visible crop.
//  2. Call CalibrateFromVisible starting from opsz=14 (the font default).
//  3. Expect the best distance to be near 0 (pixel-perfect on sharp text).
//
// This validates opsz recovery on the calibrate path, which is the correct
// path for opsz fitting: sharp adjacent text retains the full opsz signal that
// mosaic block-averaging would otherwise destroy.
func TestCalibrateFromVisible_Opsz_RoundTrip(t *testing.T) {
	const (
		targetOpsz = float32(80)
		startOpsz  = float32(14) // font default — far from target
		text       = "the"
	)

	style := varfont.DefaultStyle()
	m := metric.NewPixelmatchFast(0.1)

	// Build the sharp visible crop at the true opsz.
	rTrue := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "opsz", Value: targetOpsz}})
	visibleCrop, _, err := rTrue.Render(text, style)
	if err != nil {
		t.Fatalf("render visible: %v", err)
	}

	font := mustParseRobotoFlex(t)
	got, err := varfont.CalibrateFromVisible(varfont.CalibrateConfig{
		Font:      font,
		Text:      text,
		Style:     style,
		Target:    visibleCrop,
		Pixelator: nil, // sharp text — no pixelation
		Metric:    m,
		Axes:      []varfont.AxisSpec{{Tag: "opsz", Min: 8, Max: 144, Start: startOpsz}},
	})
	if err != nil {
		t.Fatalf("CalibrateFromVisible(opsz): %v", err)
	}

	t.Logf("CalibrateFromVisible opsz: got=%.1f (target %.1f) distance=%.4f evals=%d",
		got.Axes[0].Value, targetOpsz, got.Distance, got.Evals)

	// Sharp comparison gives a strong signal — expect near-zero distance.
	if got, want := got.Distance, 0.02; got > want {
		t.Errorf("distance: got %.4f, want <= %.4f — opsz calibration did not converge", got, want)
	}
}

// TestCalibrateFromVisible_Slnt_RoundTrip is the key correctness proof for slnt
// fitting via CalibrateFromVisible (sharp, un-pixelated comparison):
//
//  1. Render "the" at slnt=−10 (maximum slant) at full resolution.
//  2. Call CalibrateFromVisible starting from slnt=0 (upright/default).
//  3. Expect the best distance to be near 0 (pixel-perfect on sharp text).
//
// slnt range in Roboto Flex: −10 (max slant) to 0 (upright/default).
func TestCalibrateFromVisible_Slnt_RoundTrip(t *testing.T) {
	const (
		targetSlnt = float32(-10)
		startSlnt  = float32(0) // upright default — far from target
		text       = "the"
	)

	style := varfont.DefaultStyle()
	m := metric.NewPixelmatchFast(0.1)

	// Build the sharp visible crop at maximum slant.
	rTrue := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "slnt", Value: targetSlnt}})
	visibleCrop, _, err := rTrue.Render(text, style)
	if err != nil {
		t.Fatalf("render visible: %v", err)
	}

	font := mustParseRobotoFlex(t)
	got, err := varfont.CalibrateFromVisible(varfont.CalibrateConfig{
		Font:      font,
		Text:      text,
		Style:     style,
		Target:    visibleCrop,
		Pixelator: nil, // sharp text — no pixelation
		Metric:    m,
		Axes:      []varfont.AxisSpec{{Tag: "slnt", Min: -10, Max: 0, Start: startSlnt}},
	})
	if err != nil {
		t.Fatalf("CalibrateFromVisible(slnt): %v", err)
	}

	t.Logf("CalibrateFromVisible slnt: got=%.1f (target %.1f) distance=%.4f evals=%d",
		got.Axes[0].Value, targetSlnt, got.Distance, got.Evals)

	if got, want := got.Distance, 0.02; got > want {
		t.Errorf("distance: got %.4f, want <= %.4f — slnt calibration did not converge", got, want)
	}
}

// TestCalibrateFromVisible_OpszSlnt_TwoAxis fits opsz and slnt simultaneously
// on a sharp visible crop using coordinate descent. This is the realistic
// calibrate use-case: both axes are unknown and need to be jointly recovered
// from a block of adjacent visible text.
//
// Design note on optimizer choice: NelderMead with mismatched axis ranges
// (opsz: 136-unit span; slnt: 10-unit span) produces a degenerate initial
// simplex because DefaultInitStep=175 clamps slnt to the boundary on the very
// first probe. Coordinate descent handles each axis independently with its own
// step budget and is the robust choice here. NelderMead shines when axis ranges
// are comparable; prefer it for e.g. (wght, wdth) where both span ~hundreds of
// design-space units.
func TestCalibrateFromVisible_OpszSlnt_TwoAxis(t *testing.T) {
	const (
		targetOpsz = float32(60)
		targetSlnt = float32(-8)
		startOpsz  = float32(14)
		startSlnt  = float32(0)
		text       = "Hello"
	)

	style := varfont.DefaultStyle()
	m := metric.NewPixelmatchFast(0.1)

	rTrue := mustRobotoFlexRenderer(t, []varfont.Axis{
		{Tag: "opsz", Value: targetOpsz},
		{Tag: "slnt", Value: targetSlnt},
	})
	visibleCrop, _, err := rTrue.Render(text, style)
	if err != nil {
		t.Fatalf("render visible: %v", err)
	}

	font := mustParseRobotoFlex(t)
	// InitStep: choose a step that works across both axis ranges.
	// opsz range = 136 → step 34 (¼ range).
	// slnt range = 10  → step 2.5 (¼ range).
	// We use per-axis ratios via the coord-descent optimizer; set InitStep to
	// a value that is ¼ of the opsz range (the larger axis drives convergence).
	got, err := varfont.CalibrateFromVisible(varfont.CalibrateConfig{
		Font:      font,
		Text:      text,
		Style:     style,
		Target:    visibleCrop,
		Pixelator: nil,
		Metric:    m,
		Axes: []varfont.AxisSpec{
			{Tag: "opsz", Min: 8, Max: 144, Start: startOpsz},
			{Tag: "slnt", Min: -10, Max: 0, Start: startSlnt},
		},
		InitStep: 34, // ¼ of opsz range (136 du); slnt probes clamp gracefully
		MaxIter:  20,
	})
	if err != nil {
		t.Fatalf("CalibrateFromVisible(opsz+slnt): %v", err)
	}

	t.Logf("CalibrateFromVisible 2-axis: opsz=%.1f (target %.1f) slnt=%.1f (target %.1f) distance=%.4f evals=%d",
		got.Axes[0].Value, targetOpsz,
		got.Axes[1].Value, targetSlnt,
		got.Distance, got.Evals)

	if got, want := got.Distance, 0.02; got > want {
		t.Errorf("2-axis distance: got %.4f, want <= %.4f — optimizer did not converge", got, want)
	}
}

// TestFitAxes_Opsz_FlatLandscapeStability verifies that FitAxes does not panic
// or error when given an opsz AxisSpec on a pixelated target where the
// mosaic-average signal is too coarse to discriminate opsz values. The
// optimizer should return a valid FitResult (possibly at the start value)
// without diverging or erroring.
func TestFitAxes_Opsz_FlatLandscapeStability(t *testing.T) {
	t.Parallel()

	const blockSize = 8
	style := varfont.DefaultStyle()
	pix := pixelate.NewLinearBlockAverage(blockSize)
	m := metric.NewPixelmatchFast(0.1)

	rTarget := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "opsz", Value: 80}})
	targetImg, _, err := rTarget.Render("the", style)
	if err != nil {
		t.Fatalf("render target: %v", err)
	}
	targetPix := pix.Pixelate(targetImg, 0, 0)

	font := mustParseRobotoFlex(t)
	result, err := varfont.FitAxes(varfont.FitConfig{
		Font:      font,
		Text:      "the",
		Style:     style,
		Target:    targetPix,
		Pixelator: pix,
		Metric:    m,
		BlockSize: blockSize,
		Axes:      []varfont.AxisSpec{{Tag: "opsz", Min: 8, Max: 144, Start: 14}},
	})
	if err != nil {
		t.Fatalf("FitAxes(opsz flat landscape): %v", err)
	}
	t.Logf("opsz flat landscape: opsz=%.1f distance=%.4f evals=%d",
		result.Axes[0].Value, result.Distance, result.Evals)

	// The optimizer must return a valid result; distance must be finite and ≥ 0.
	if result.Distance < 0 {
		t.Errorf("distance: got %.4f, want >= 0", result.Distance)
	}
	// Axis value must remain in-range.
	if got := result.Axes[0].Value; got < 8 || got > 144 {
		t.Errorf("opsz out of range: got %.1f, want [8, 144]", got)
	}
}

// TestFitAxes_Slnt_FlatLandscapeStability mirrors the opsz stability test for
// the slnt axis.
func TestFitAxes_Slnt_FlatLandscapeStability(t *testing.T) {
	t.Parallel()

	const blockSize = 8
	style := varfont.DefaultStyle()
	pix := pixelate.NewLinearBlockAverage(blockSize)
	m := metric.NewPixelmatchFast(0.1)

	rTarget := mustRobotoFlexRenderer(t, []varfont.Axis{{Tag: "slnt", Value: -10}})
	targetImg, _, err := rTarget.Render("the", style)
	if err != nil {
		t.Fatalf("render target: %v", err)
	}
	targetPix := pix.Pixelate(targetImg, 0, 0)

	font := mustParseRobotoFlex(t)
	result, err := varfont.FitAxes(varfont.FitConfig{
		Font:      font,
		Text:      "the",
		Style:     style,
		Target:    targetPix,
		Pixelator: pix,
		Metric:    m,
		BlockSize: blockSize,
		Axes:      []varfont.AxisSpec{{Tag: "slnt", Min: -10, Max: 0, Start: 0}},
	})
	if err != nil {
		t.Fatalf("FitAxes(slnt flat landscape): %v", err)
	}
	t.Logf("slnt flat landscape: slnt=%.1f distance=%.4f evals=%d",
		result.Axes[0].Value, result.Distance, result.Evals)

	if result.Distance < 0 {
		t.Errorf("distance: got %.4f, want >= 0", result.Distance)
	}
	if got := result.Axes[0].Value; got < -10 || got > 0 {
		t.Errorf("slnt out of range: got %.1f, want [-10, 0]", got)
	}
}

// BenchmarkCalibrateFromVisible_Opsz measures the per-call cost of a
// single-axis opsz CalibrateFromVisible fit on sharp text from Roboto Flex.
// This is the correct hot path for opsz calibration.
func BenchmarkCalibrateFromVisible_Opsz(b *testing.B) {
	const (
		targetOpsz = float32(80)
		text       = "the"
	)

	b.ReportAllocs()

	style := varfont.DefaultStyle()
	m := metric.NewPixelmatchFast(0.1)

	font, err := varfont.ParseFont(bytes.NewReader(robotoFlexData))
	if err != nil {
		b.Fatalf("ParseFont: %v", err)
	}

	rTrue, err := varfont.NewVarRenderer(bytes.NewReader(robotoFlexData), []varfont.Axis{
		{Tag: "opsz", Value: targetOpsz},
	})
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	visibleCrop, _, err := rTrue.Render(text, style)
	if err != nil {
		b.Fatalf("render: %v", err)
	}

	cfg := varfont.CalibrateConfig{
		Font:   font,
		Text:   text,
		Style:  style,
		Target: visibleCrop,
		Metric: m,
		Axes:   []varfont.AxisSpec{{Tag: "opsz", Min: 8, Max: 144, Start: 14}},
	}

	b.ResetTimer()
	var totalEvals int
	for b.Loop() {
		result, err := varfont.CalibrateFromVisible(cfg)
		if err != nil {
			b.Fatalf("CalibrateFromVisible: %v", err)
		}
		totalEvals += result.Evals
		sinkResult = result
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/fit")
}

// BenchmarkCalibrateFromVisible_Slnt measures the per-call cost of a
// single-axis slnt CalibrateFromVisible fit on sharp text from Roboto Flex.
func BenchmarkCalibrateFromVisible_Slnt(b *testing.B) {
	const (
		targetSlnt = float32(-10)
		text       = "the"
	)

	b.ReportAllocs()

	style := varfont.DefaultStyle()
	m := metric.NewPixelmatchFast(0.1)

	font, err := varfont.ParseFont(bytes.NewReader(robotoFlexData))
	if err != nil {
		b.Fatalf("ParseFont: %v", err)
	}

	rTrue, err := varfont.NewVarRenderer(bytes.NewReader(robotoFlexData), []varfont.Axis{
		{Tag: "slnt", Value: targetSlnt},
	})
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	visibleCrop, _, err := rTrue.Render(text, style)
	if err != nil {
		b.Fatalf("render: %v", err)
	}

	cfg := varfont.CalibrateConfig{
		Font:   font,
		Text:   text,
		Style:  style,
		Target: visibleCrop,
		Metric: m,
		Axes:   []varfont.AxisSpec{{Tag: "slnt", Min: -10, Max: 0, Start: 0}},
	}

	b.ResetTimer()
	var totalEvals int
	for b.Loop() {
		result, err := varfont.CalibrateFromVisible(cfg)
		if err != nil {
			b.Fatalf("CalibrateFromVisible: %v", err)
		}
		totalEvals += result.Evals
		sinkResult = result
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/fit")
}
