package varfont_test

import (
	"bytes"
	"testing"

	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/varfont"
)

// sink defeats dead-code elimination for benchmark results.
var sink varfont.FitResult

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
		sink = result
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/fit")
}
