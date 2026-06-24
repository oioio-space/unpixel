package varfont_test

import (
	"bytes"
	"testing"

	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/varfont"
)

// sinkResult defeats dead-code elimination for FitResult benchmark results.
var sinkResult varfont.FitResult

// sinkImg defeats dead-code elimination for Render benchmark results.
var sinkImg any

// BenchmarkVarRenderer_Render measures the per-call cost of VarRenderer.Render.
// This is the innermost hot-loop operation in the fitter: every FitAxes eval
// calls Render once. Baseline allocs/op are dominated by gtfont.NewFace +
// extentsCache (one alloc per glyph). The H1(conc) optimisation replaces that
// with a sync.Pool get/put, driving allocs/op to near zero for the fitter path.
//
// Workflow: mise run bench:baseline → apply H1 → mise run bench:compare
// (-count ≥ 10, -benchmem); keep only on statistically significant ns/op AND
// allocs/op reduction.
func BenchmarkVarRenderer_Render(b *testing.B) {
	b.ReportAllocs()

	r, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), []varfont.Axis{
		{Tag: "wght", Value: 700},
	})
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	style := varfont.DefaultStyle()

	b.ResetTimer()
	for b.Loop() {
		img, sx, err := r.Render("the", style)
		if err != nil {
			b.Fatalf("Render: %v", err)
		}
		sinkImg = img
		_ = sx
	}
}

// BenchmarkFitAxes measures the cost of one complete FitAxes call over wght.
// It reports ns/op, allocs/op, and evals/fit (render+pixelate+metric evaluations
// per fit) so the marginal cost of adding more axes is understood before the
// optimizer is wired into recovery.
//
// Workflow: mise run bench:baseline → change → mise run bench:compare (benchstat,
// -count >= 10) — keep only statistically significant gains with no alloc regression.
func BenchmarkFitAxes(b *testing.B) {
	const (
		targetWght = float32(700)
		blockSize  = 8
	)

	b.ReportAllocs()

	style := varfont.DefaultStyle()
	pix := pixelate.NewLinearBlockAverage(blockSize)
	m := metric.NewPixelmatchFast(0.1)

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		b.Fatalf("ParseFont: %v", err)
	}

	// Build the synthetic target once outside the loop; it is read-only during
	// the bench loop so we measure only FitAxes, not target construction.
	rTarget, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), []varfont.Axis{
		{Tag: "wght", Value: targetWght},
	})
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	targetImg, _, err := rTarget.Render("the", style)
	if err != nil {
		b.Fatalf("render target: %v", err)
	}
	targetPix := pix.Pixelate(targetImg, 0, 0)

	cfg := varfont.FitConfig{
		Font:      font,
		Text:      "the",
		Style:     style,
		Target:    targetPix,
		Pixelator: pix,
		Metric:    m,
		BlockSize: blockSize,
		Axes:      []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: 400}},
	}

	b.ResetTimer()
	var totalEvals int
	for b.Loop() {
		result, err := varfont.FitAxes(cfg)
		if err != nil {
			b.Fatalf("FitAxes: %v", err)
		}
		totalEvals += result.Evals
		sinkResult = result
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/fit")
}

// BenchmarkFitAxes_NelderMead measures the Nelder-Mead optimizer on the same
// single-axis wght landscape as BenchmarkFitAxes so benchstat can compare them
// directly. Reports ns/op, allocs/op, and evals/fit.
func BenchmarkFitAxes_NelderMead(b *testing.B) {
	const (
		targetWght = float32(700)
		blockSize  = 8
	)

	b.ReportAllocs()

	style := varfont.DefaultStyle()
	pix := pixelate.NewLinearBlockAverage(blockSize)
	m := metric.NewPixelmatchFast(0.1)

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		b.Fatalf("ParseFont: %v", err)
	}

	rTarget, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), []varfont.Axis{
		{Tag: "wght", Value: targetWght},
	})
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	targetImg, _, err := rTarget.Render("the", style)
	if err != nil {
		b.Fatalf("render target: %v", err)
	}
	targetPix := pix.Pixelate(targetImg, 0, 0)

	cfg := varfont.FitConfig{
		Font:      font,
		Text:      "the",
		Style:     style,
		Target:    targetPix,
		Pixelator: pix,
		Metric:    m,
		BlockSize: blockSize,
		Axes:      []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: 400}},
		Optimizer: varfont.OptimizerNelderMead,
	}

	b.ResetTimer()
	var totalEvals int
	for b.Loop() {
		result, err := varfont.FitAxes(cfg)
		if err != nil {
			b.Fatalf("FitAxes NelderMead: %v", err)
		}
		totalEvals += result.Evals
		sinkResult = result
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/fit")
}

// BenchmarkCalibrateFromVisible measures the CalibrateFromVisible call on a
// sharp (un-pixelated) wght fit. This is the hot path for the calibration
// phase when the user supplies a visible-text crop adjacent to the redaction.
func BenchmarkCalibrateFromVisible(b *testing.B) {
	const (
		trueWght = float32(700)
		text     = "the"
	)

	b.ReportAllocs()

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		b.Fatalf("ParseFont: %v", err)
	}
	style := varfont.DefaultStyle()
	m := metric.NewPixelmatchFast(0.1)

	rTrue, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), []varfont.Axis{
		{Tag: "wght", Value: trueWght},
	})
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	visibleCrop, _, err := rTrue.Render(text, style)
	if err != nil {
		b.Fatalf("render visible: %v", err)
	}

	cfg := varfont.CalibrateConfig{
		Font:   font,
		Text:   text,
		Style:  style,
		Target: visibleCrop,
		Metric: m,
		Axes:   []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: 400}},
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
