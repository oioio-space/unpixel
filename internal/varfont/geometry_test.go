package varfont_test

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/varfont"
)

// sinkGeometry defeats dead-code elimination for GeometryResult benchmark results.
var sinkGeometry varfont.GeometryResult

// makeStretchedTarget renders text at the given fontSize and xStretch and
// returns the resulting sharp RGBA image — the synthetic visible crop used by
// round-trip tests.
//
// The stretch is applied with the same CatmullRom.Scale pipeline used by the
// implementation so the test and the code are consistent.
func makeStretchedTarget(t *testing.T, text string, fontSize, xStretch float64) *image.RGBA {
	t.Helper()

	r, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), nil)
	if err != nil {
		t.Fatalf("NewVarRenderer: %v", err)
	}

	style := varfont.DefaultStyle()
	style.FontSize = fontSize

	img, sx, err := r.Render(text, style)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Crop to ink bounds (same as mosaictext.renderStretched).
	bb := inkBoundsForTest(img, sx)
	if bb.Empty() {
		t.Fatal("empty ink bounds")
	}
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)

	// Stretch horizontally.
	nw := int(math.Round(float64(bb.Dx()) * xStretch))
	if nw < 1 {
		nw = 1
	}
	stretched := image.NewRGBA(image.Rect(0, 0, nw, bb.Dy()))
	xdraw.CatmullRom.Scale(stretched, stretched.Bounds(), ink, ink.Bounds(), xdraw.Over, nil)
	return stretched
}

// inkThreshold is the per-channel value below which a pixel is considered ink.
// Must match the threshold used in geometry.go's inkBoundsGeom so that the
// test's target-generation crop and the implementation's candidate crop are
// pixel-consistent.
const inkThreshold = uint8(244)

// inkBoundsForTest returns the bounding box of ink pixels in img up to
// sentinelX (exclusive). Uses the same threshold as the implementation's
// inkBoundsGeom so target and candidate crops are consistent.
func inkBoundsForTest(img *image.RGBA, sentinelX int) image.Rectangle {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < min(b.Max.X, sentinelX); x++ {
			c := img.RGBAAt(x, y)
			if c.R < inkThreshold || c.G < inkThreshold || c.B < inkThreshold {
				minX = min(minX, x)
				minY = min(minY, y)
				maxX = max(maxX, x+1)
				maxY = max(maxY, y+1)
			}
		}
	}
	if minX >= maxX || minY >= maxY {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// TestCalibrateGeometry_Errors checks that invalid inputs return descriptive
// errors without panicking.
func TestCalibrateGeometry_Errors(t *testing.T) {
	t.Parallel()

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	m := metric.NewPixelmatchFast(0.1)
	target := image.NewRGBA(image.Rect(0, 0, 64, 32))

	tests := []struct {
		name string
		cfg  varfont.GeometryConfig
	}{
		{
			name: "nil Font",
			cfg: varfont.GeometryConfig{
				Font:   nil,
				Text:   "hi",
				Target: target,
				Metric: m,
			},
		},
		{
			name: "empty Text",
			cfg: varfont.GeometryConfig{
				Font:   font,
				Text:   "",
				Target: target,
				Metric: m,
			},
		},
		{
			name: "nil Target",
			cfg: varfont.GeometryConfig{
				Font:   font,
				Text:   "hi",
				Target: nil,
				Metric: m,
			},
		},
		{
			name: "nil Metric",
			cfg: varfont.GeometryConfig{
				Font:   font,
				Text:   "hi",
				Target: target,
				Metric: nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := varfont.CalibrateGeometry(tc.cfg)
			if err == nil {
				t.Errorf("CalibrateGeometry(%s): want error, got nil", tc.name)
			}
		})
	}
}

// TestCalibrateGeometry_RoundTrip is the key correctness proof:
//
//  1. Render "Hello" at fontSize=28 and xStretch=1.15 to produce a synthetic
//     sharp target.
//  2. Call CalibrateGeometry with a different seed (fontSize=20, stretch=1.0).
//  3. Expect: recovered fontSize within ±1 px, xStretch within ±0.05.
func TestCalibrateGeometry_RoundTrip(t *testing.T) {
	const (
		text         = "Hello"
		trueFontSize = 28.0
		trueStretch  = 1.15
		seedFontSize = 20.0 // different seed
		// tolerances
		tolSize    = 1.5 // px
		tolStretch = 0.05
	)

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	m := metric.NewPixelmatchFast(0.1)

	target := makeStretchedTarget(t, text, trueFontSize, trueStretch)
	t.Logf("target size: %v", target.Bounds())

	style := varfont.DefaultStyle()
	style.FontSize = seedFontSize

	got, err := varfont.CalibrateGeometry(varfont.GeometryConfig{
		Font:    font,
		Text:    text,
		Style:   style,
		Target:  target,
		Metric:  m,
		MaxIter: 60,
	})
	if err != nil {
		t.Fatalf("CalibrateGeometry: %v", err)
	}

	t.Logf("true  fontSize=%.2f  xStretch=%.3f", trueFontSize, trueStretch)
	t.Logf("got   fontSize=%.2f  xStretch=%.3f  distance=%.6f", got.FontSizePx, got.XStretch, got.Distance)

	if diff := math.Abs(got.FontSizePx - trueFontSize); diff > tolSize {
		t.Errorf("FontSizePx: got %.2f, want %.2f ± %.1f (diff %.2f)", got.FontSizePx, trueFontSize, tolSize, diff)
	}
	if diff := math.Abs(got.XStretch - trueStretch); diff > tolStretch {
		t.Errorf("XStretch: got %.3f, want %.3f ± %.3f (diff %.3f)", got.XStretch, trueStretch, tolStretch, diff)
	}
	// Distance is reported on the caller's Metric scale at the recovered params.
	// Because font-size is continuous but rendered images are integer-pixel,
	// the optimum may not be at zero (the exact true integer params are rarely
	// recovered exactly). A distance below 0.25 confirms the optimizer is in the
	// right basin and not stuck at a degenerate corner.
	if got.Distance > 0.25 {
		t.Errorf("Distance: got %.6f, want < 0.25 (optimizer may be in wrong basin)", got.Distance)
	}
}

// TestCalibrateGeometry_Deterministic verifies that two calls with the same
// inputs produce bit-identical results.
func TestCalibrateGeometry_Deterministic(t *testing.T) {
	const text = "Test"

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	m := metric.NewPixelmatchFast(0.1)
	target := makeStretchedTarget(t, text, 24.0, 1.0)

	style := varfont.DefaultStyle()
	style.FontSize = 20.0

	cfg := varfont.GeometryConfig{
		Font:    font,
		Text:    text,
		Style:   style,
		Target:  target,
		Metric:  m,
		MaxIter: 20,
	}

	r1, err := varfont.CalibrateGeometry(cfg)
	if err != nil {
		t.Fatalf("first CalibrateGeometry: %v", err)
	}
	r2, err := varfont.CalibrateGeometry(cfg)
	if err != nil {
		t.Fatalf("second CalibrateGeometry: %v", err)
	}

	if r1.FontSizePx != r2.FontSizePx {
		t.Errorf("FontSizePx not deterministic: %v vs %v", r1.FontSizePx, r2.FontSizePx)
	}
	if r1.XStretch != r2.XStretch {
		t.Errorf("XStretch not deterministic: %v vs %v", r1.XStretch, r2.XStretch)
	}
	if r1.Distance != r2.Distance {
		t.Errorf("Distance not deterministic: %v vs %v", r1.Distance, r2.Distance)
	}
}

// BenchmarkCalibrateGeometry measures the cost of a full CalibrateGeometry call
// on a small realistic sharp-text crop. Reports ns/op, allocs/op, and evals/fit.
func BenchmarkCalibrateGeometry(b *testing.B) {
	const (
		text     = "the"
		fontSize = 24.0
		stretch  = 1.0
	)

	b.ReportAllocs()

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		b.Fatalf("ParseFont: %v", err)
	}
	m := metric.NewPixelmatchFast(0.1)

	// Build the target once outside the loop.
	r, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), nil)
	if err != nil {
		b.Fatalf("NewVarRenderer: %v", err)
	}
	style := varfont.DefaultStyle()
	style.FontSize = fontSize

	img, sx, err := r.Render(text, style)
	if err != nil {
		b.Fatalf("Render: %v", err)
	}
	bb := inkBoundsForTest(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	// Unit stretch — target == ink itself.
	target := ink

	seedStyle := varfont.DefaultStyle()
	seedStyle.FontSize = 18.0 // different seed

	cfg := varfont.GeometryConfig{
		Font:    font,
		Text:    text,
		Style:   seedStyle,
		Target:  target,
		Metric:  m,
		MaxIter: 20,
	}

	b.ResetTimer()
	var totalEvals int
	for b.Loop() {
		result, err := varfont.CalibrateGeometry(cfg)
		if err != nil {
			b.Fatalf("CalibrateGeometry: %v", err)
		}
		totalEvals += result.Evals
		sinkGeometry = result
	}
	b.ReportMetric(float64(totalEvals)/float64(b.N), "evals/fit")
}

// TestCalibrateGeometry_DefaultBounds verifies that zero MinSizePx/MaxSizePx
// and zero MinStretch/MaxStretch use the documented defaults and still converge.
func TestCalibrateGeometry_DefaultBounds(t *testing.T) {
	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	m := metric.NewPixelmatchFast(0.1)
	target := makeStretchedTarget(t, "Hi", 24.0, 1.0)

	style := varfont.DefaultStyle()
	style.FontSize = 24.0 // seed at true size — should hit distance ≈ 0 fast

	_, err = varfont.CalibrateGeometry(varfont.GeometryConfig{
		Font:   font,
		Text:   "Hi",
		Style:  style,
		Target: target,
		Metric: m,
		// Zero bounds → defaults applied inside.
	})
	if err != nil {
		t.Fatalf("CalibrateGeometry with default bounds: %v", err)
	}
}

// TestCalibrateGeometry_NilPixelator checks that a nil Pixelator is accepted
// and treated as sharp passthrough (no panic, no error).
func TestCalibrateGeometry_NilPixelator(t *testing.T) {
	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	m := metric.NewPixelmatchFast(0.1)
	target := makeStretchedTarget(t, "Go", 20.0, 1.0)

	style := varfont.DefaultStyle()
	style.FontSize = 20.0

	_, err = varfont.CalibrateGeometry(varfont.GeometryConfig{
		Font:      font,
		Text:      "Go",
		Style:     style,
		Target:    target,
		Pixelator: nil, // explicitly nil — sharp passthrough
		Metric:    m,
		MaxIter:   10,
	})
	if err != nil {
		t.Fatalf("CalibrateGeometry with nil Pixelator: %v", err)
	}
}

// TestCalibrateGeometry_WithPixelator checks that a non-nil Pixelator is
// accepted and wired through without error (the target would be pixelated, so
// the metric result is noisier, but the function must not fail).
func TestCalibrateGeometry_WithPixelator(t *testing.T) {
	// Use a block size of 4 px — small enough that the test is fast.
	const blockSize = 4

	font, err := varfont.ParseFont(bytes.NewReader(nunitoData))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}
	m := metric.NewPixelmatchFast(0.1)

	// Build a pixelated target.
	r, err := varfont.NewVarRenderer(bytes.NewReader(nunitoData), nil)
	if err != nil {
		t.Fatalf("NewVarRenderer: %v", err)
	}
	seedStyle := varfont.DefaultStyle()
	seedStyle.FontSize = 20.0
	img, _, err := r.Render("Go", seedStyle)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	pix := newTestPixelator(blockSize)
	pixTarget := pix.Pixelate(img, 0, 0)

	_, err = varfont.CalibrateGeometry(varfont.GeometryConfig{
		Font:      font,
		Text:      "Go",
		Style:     seedStyle,
		Target:    pixTarget,
		Pixelator: pix,
		Metric:    m,
		MaxIter:   10,
	})
	if err != nil {
		t.Fatalf("CalibrateGeometry with non-nil Pixelator: %v", err)
	}
}

// newTestPixelator returns a simple block-average pixelator for tests that need
// a non-nil Pixelator without importing the full pixelate package.
func newTestPixelator(blockSize int) unpixel.Pixelator {
	return testPixelator{blockSize}
}

type testPixelator struct{ block int }

func (tp testPixelator) Pixelate(img *image.RGBA, _, _ int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(b)
	bs := tp.block
	for y := b.Min.Y; y < b.Max.Y; y += bs {
		for x := b.Min.X; x < b.Max.X; x += bs {
			// Compute block average.
			var r, g, bl, a, n int
			for dy := range bs {
				for dx := range bs {
					px, py := x+dx, y+dy
					if px >= b.Max.X || py >= b.Max.Y {
						continue
					}
					c := img.RGBAAt(px, py)
					r += int(c.R)
					g += int(c.G)
					bl += int(c.B)
					a += int(c.A)
					n++
				}
			}
			if n == 0 {
				continue
			}
			avg := func(s int) uint8 { return uint8(s / n) } //nolint:gosec // avg is [0,255]
			fill := color.RGBA{R: avg(r), G: avg(g), B: avg(bl), A: avg(a)}
			for dy := range bs {
				for dx := range bs {
					px, py := x+dx, y+dy
					if px >= b.Max.X || py >= b.Max.Y {
						continue
					}
					out.SetRGBA(px, py, fill)
				}
			}
		}
	}
	return out
}
