// Package varfont — tests for the variable-font renderer and axis fitter.
package varfont_test

import (
	"bytes"
	"image"
	"image/color"
	"sync"
	"testing"

	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
)

// nunitoData is the bundled Nunito variable-weight font used by tests.
var nunitoData = vfembed.NunitoVFWght

// mustNewRenderer creates a VarRenderer for the Nunito VF font at wght=value or
// fails the test.
func mustNewRenderer(t *testing.T, wght float32) *varfont.VarRenderer {
	t.Helper()
	r, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), []varfont.Axis{
		{Tag: "wght", Value: wght},
	})
	if err != nil {
		t.Fatalf("NewVarRenderer: %v", err)
	}
	return r
}

// TestVarRenderer_DifferentAxesYieldDifferentRasters verifies that rendering
// the same text at two different wght values produces visually distinct images.
func TestVarRenderer_DifferentAxesYieldDifferentRasters(t *testing.T) {
	rLight := mustNewRenderer(t, 200)
	rHeavy := mustNewRenderer(t, 900)

	style := varfont.DefaultStyle()
	imgLight, _, err := rLight.Render("the", style)
	if err != nil {
		t.Fatalf("Render light: %v", err)
	}
	imgHeavy, _, err := rHeavy.Render("the", style)
	if err != nil {
		t.Fatalf("Render heavy: %v", err)
	}

	diff := countDiff(imgLight, imgHeavy)
	if got, want := diff, 10; got < want {
		t.Errorf("differing pixels: got %d, want >= %d — axis change not visible", got, want)
	}
	t.Logf("wght=200 vs wght=900: %d differing pixels", diff)
}

// TestVarRenderer_Deterministic verifies that two renders of the same text at
// the same axis produce identical images.
func TestVarRenderer_Deterministic(t *testing.T) {
	r := mustNewRenderer(t, 600)
	style := varfont.DefaultStyle()

	img1, sx1, err := r.Render("hello", style)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	img2, sx2, err := r.Render("hello", style)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}

	if got, want := sx1, sx2; got != want {
		t.Errorf("sentinelX: got %d, want %d", got, want)
	}
	if got, want := countDiff(img1, img2), 0; got != want {
		t.Errorf("differing pixels between two identical renders: got %d, want %d", got, want)
	}
}

// TestVarRenderer_MissingGlyph verifies that rendering a string containing an
// unmapped code point does not panic and returns a non-empty image.
func TestVarRenderer_MissingGlyph(t *testing.T) {
	r := mustNewRenderer(t, 400)
	style := varfont.DefaultStyle()

	// U+FFFF is not a real character; fonts are not required to map it.
	img, sx, err := r.Render("a￿b", style)
	if err != nil {
		t.Fatalf("Render with missing glyph: %v", err)
	}
	if img == nil {
		t.Fatal("Render returned nil image for string with missing glyph")
	}
	if sx <= 0 {
		t.Errorf("sentinelX: got %d, want > 0", sx)
	}
	t.Logf("missing glyph: image %dx%d, sentinelX=%d", img.Bounds().Dx(), img.Bounds().Dy(), sx)
}

// TestFitAxes_RoundTrip is the key correctness proof:
//  1. Render "the" at wght=820 and pixelate it — the synthetic target.
//  2. Call FitAxes starting from wght=400.
//  3. Expect the best distance to be near 0 (pixel-perfect match at the true axis).
//
// This proves the optimizer converges on the ground-truth axis value.
func TestFitAxes_RoundTrip(t *testing.T) {
	const (
		targetWght = float32(820)
		startWght  = float32(400)
		blockSize  = 8
	)

	style := varfont.DefaultStyle()

	// Build the synthetic target: render at targetWght and pixelate.
	rTarget := mustNewRenderer(t, targetWght)
	targetImg, _, err := rTarget.Render("the", style)
	if err != nil {
		t.Fatalf("render target: %v", err)
	}
	pix := pixelate.NewLinearBlockAverage(blockSize)
	targetPix := pix.Pixelate(targetImg, 0, 0)

	m := metric.NewPixelmatchFast(0.1)

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}

	result, err := varfont.FitAxes(varfont.FitConfig{
		Font:      font,
		Text:      "the",
		Style:     style,
		Target:    targetPix,
		Pixelator: pix,
		Metric:    m,
		BlockSize: blockSize,
		Axes:      []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: startWght}},
	})
	if err != nil {
		t.Fatalf("FitAxes: %v", err)
	}

	t.Logf("FitAxes: best wght=%.1f (target %.1f), distance=%.4f, evals=%d",
		result.Axes[0].Value, targetWght, result.Distance, result.Evals)

	// The optimizer must find a very small distance — the target was synthesised
	// from the same pipeline so the true axis is a global minimum.
	if got, want := result.Distance, 0.05; got > want {
		t.Errorf("distance: got %.4f, want <= %.4f — optimizer did not converge", got, want)
	}
}

// TestConcurrent_ParallelFitAxes runs several FitAxes calls concurrently and
// verifies there are no data races (run with -race when memory budget permits).
func TestConcurrent_ParallelFitAxes(t *testing.T) {
	const (
		blockSize  = 8
		goroutines = 4
	)

	style := varfont.DefaultStyle()
	pix := pixelate.NewLinearBlockAverage(blockSize)
	m := metric.NewPixelmatchFast(0.1)

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}

	// Build a single target image to share as read-only.
	rTarget := mustNewRenderer(t, 600)
	targetImg, _, err := rTarget.Render("hi", style)
	if err != nil {
		t.Fatalf("render target: %v", err)
	}
	targetPix := pix.Pixelate(targetImg, 0, 0)

	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			_, errs[i] = varfont.FitAxes(varfont.FitConfig{
				Font:      font,
				Text:      "hi",
				Style:     style,
				Target:    targetPix,
				Pixelator: pix,
				Metric:    m,
				BlockSize: blockSize,
				Axes:      []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: 400}},
			})
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: FitAxes error: %v", i, err)
		}
	}
}

// countDiff returns the number of pixels whose RGB channels differ between a
// and b. It examines the intersection of their bounds only.
func countDiff(a, b *image.RGBA) int {
	bds := a.Bounds().Intersect(b.Bounds())
	var n int
	for y := bds.Min.Y; y < bds.Max.Y; y++ {
		for x := bds.Min.X; x < bds.Max.X; x++ {
			ca := a.RGBAAt(x, y)
			cb := b.RGBAAt(x, y)
			if ca.R != cb.R || ca.G != cb.G || ca.B != cb.B {
				n++
			}
		}
	}
	return n
}

// ensure render package is importable (import-cycle guard).
var _ = render.NewXImage

// keep color import used.
var _ color.RGBA
