package metric_test

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/metric"
)

// TestMetrics_emptyImage covers the total==0 / w==0 guard in every metric's
// Compare: a zero-sized image pair must score 0 (not NaN/panic).
func TestMetrics_emptyImage(t *testing.T) {
	e := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if got := metric.NewRGB().Compare(e, e); got != 0 {
		t.Errorf("RGB.Compare(empty) = %v, want 0", got)
	}
	if got := metric.NewPixelmatch(0.02).Compare(e, e); got != 0 {
		t.Errorf("Pixelmatch.Compare(empty) = %v, want 0", got)
	}
	if got := metric.NewPixelmatchFast(0.02).Compare(e, e); got != 0 {
		t.Errorf("PixelmatchFast.Compare(empty) = %v, want 0", got)
	}
	if got := metric.NewSSIM(8).Compare(e, e); got != 0 {
		t.Errorf("SSIM.Compare(empty) = %v, want 0", got)
	}
}

// solid builds a w×h RGBA image filled with c.
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// invert returns the complement of a solid color image.
func invert(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			out.SetRGBA(b.Min.X+x, b.Min.Y+y, color.RGBA{
				R: 255 - c.R,
				G: 255 - c.G,
				B: 255 - c.B,
				A: c.A,
			})
		}
	}
	return out
}

// --- CountPixelsNoAA ---

// TestCountPixelsNoAA_supersetOfCountPixels verifies that CountPixelsNoAA always
// returns a value ≥ CountPixels for the same inputs (it counts a superset: no AA
// pixels are excluded).
func TestCountPixelsNoAA_supersetOfCountPixels(t *testing.T) {
	cases := []struct {
		name string
		a, b *image.RGBA
	}{
		{
			name: "identical solid",
			a:    solid(8, 8, color.RGBA{R: 200, G: 200, B: 200, A: 255}),
			b:    solid(8, 8, color.RGBA{R: 200, G: 200, B: 200, A: 255}),
		},
		{
			name: "black vs white",
			a:    solid(8, 8, color.RGBA{R: 0, G: 0, B: 0, A: 255}),
			b:    solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255}),
		},
		{
			name: "one pixel diff",
			a: func() *image.RGBA {
				img := solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255})
				img.SetRGBA(3, 3, color.RGBA{R: 0, G: 0, B: 0, A: 255})
				return img
			}(),
			b: solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255}),
		},
	}
	const threshold = 0.02
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			faithful := metric.CountPixels(tc.a, tc.b, threshold)
			fast := metric.CountPixelsNoAA(tc.a, tc.b, threshold)
			if fast < faithful {
				t.Errorf("CountPixelsNoAA=%d < CountPixels=%d; want ≥", fast, faithful)
			}
		})
	}
}

// TestCountPixelsNoAA_blockConstant verifies that on block-constant (solid)
// image pairs — the mosaic recovery regime — CountPixelsNoAA matches CountPixels
// exactly (no AA pixels to exclude).
func TestCountPixelsNoAA_blockConstant(t *testing.T) {
	// Solid images contain no edges, so the AA neighbourhood check in CountPixels
	// never excludes any pixel. Both functions must agree.
	a := solid(16, 16, color.RGBA{R: 200, G: 200, B: 200, A: 255})
	b := solid(16, 16, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	const threshold = 0.02
	faithful := metric.CountPixels(a, b, threshold)
	fast := metric.CountPixelsNoAA(a, b, threshold)
	if fast != faithful {
		t.Errorf("block-constant: CountPixelsNoAA=%d, CountPixels=%d; want equal", fast, faithful)
	}
}

// TestPixelmatchFast_identical verifies zero distance on equal images.
func TestPixelmatchFast_identical(t *testing.T) {
	a := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	got := metric.NewPixelmatchFast(0.02).Compare(a, b)
	if got != 0 {
		t.Errorf("PixelmatchFast identical = %v, want 0", got)
	}
}

// TestPixelmatchFast_returnsZeroToOne verifies the result is in [0, 1].
func TestPixelmatchFast_returnsZeroToOne(t *testing.T) {
	a := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := invert(a)
	got := metric.NewPixelmatchFast(0.02).Compare(a, b)
	if got < 0 || got > 1 {
		t.Errorf("PixelmatchFast result %v out of [0,1]", got)
	}
}

// TestPixelmatchFast_blockConstantMatchesFaithful verifies that on block-constant
// (mosaic) image pairs — where no anti-aliasing exists — PixelmatchFast and
// Pixelmatch produce identical scores. This is the no-quality-loss guarantee.
func TestPixelmatchFast_blockConstantMatchesFaithful(t *testing.T) {
	cases := []struct {
		name string
		a, b *image.RGBA
	}{
		{
			name: "identical solid",
			a:    solid(16, 16, color.RGBA{R: 200, G: 200, B: 200, A: 255}),
			b:    solid(16, 16, color.RGBA{R: 200, G: 200, B: 200, A: 255}),
		},
		{
			name: "different solids",
			a:    solid(16, 16, color.RGBA{R: 200, G: 200, B: 200, A: 255}),
			b:    solid(16, 16, color.RGBA{R: 100, G: 100, B: 100, A: 255}),
		},
		{
			name: "white vs black",
			a:    solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255}),
			b:    solid(8, 8, color.RGBA{R: 0, G: 0, B: 0, A: 255}),
		},
	}
	const threshold = 0.02
	fast := metric.NewPixelmatchFast(threshold)
	faithful := metric.NewPixelmatch(threshold)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fast.Compare(tc.a, tc.b)
			want := faithful.Compare(tc.a, tc.b)
			if got != want {
				t.Errorf("PixelmatchFast=%v, Pixelmatch=%v; want equal on block-constant input", got, want)
			}
		})
	}
}

// --- RGB metric ---

func TestRGB_identical(t *testing.T) {
	a := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	got := metric.NewRGB().Compare(a, b)
	if got != 0 {
		t.Errorf("RGB identical = %v, want 0", got)
	}
}

func TestRGB_inverted(t *testing.T) {
	a := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := invert(a)
	got := metric.NewRGB().Compare(a, b)
	// All pixels differ → score = 1.0.
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("RGB inverted = %v, want ~1.0", got)
	}
}

func TestRGB_onePixelDiff(t *testing.T) {
	// 4×4 = 16 pixels; one pixel differs → score = 1/16.
	a := solid(4, 4, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	b := solid(4, 4, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	b.SetRGBA(0, 0, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	got := metric.NewRGB().Compare(a, b)
	want := 1.0 / 16.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("RGB one-pixel diff = %v, want %v", got, want)
	}
}

func TestRGB_nPixelDiff(t *testing.T) {
	// 8×8 = 64 pixels; 8 differ → score = 8/64 = 0.125.
	a := solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	b := solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	for i := range 8 {
		b.SetRGBA(i, 0, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	}
	got := metric.NewRGB().Compare(a, b)
	want := 8.0 / 64.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("RGB n-pixel diff = %v, want %v", got, want)
	}
}

// --- Pixelmatch metric ---

func TestPixelmatch_identical(t *testing.T) {
	a := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	got := metric.NewPixelmatch(0.1).Compare(a, b)
	if got != 0 {
		t.Errorf("Pixelmatch identical = %v, want 0", got)
	}
}

func TestPixelmatch_clearlyDifferent(t *testing.T) {
	// Black vs white — all 64 pixels differ well beyond any threshold.
	a := solid(8, 8, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	b := solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	got := metric.NewPixelmatch(0.1).Compare(a, b)
	if got <= 0 {
		t.Errorf("Pixelmatch black vs white = %v, want > 0", got)
	}
}

func TestPixelmatch_returnsZeroToOne(t *testing.T) {
	a := solid(8, 8, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := invert(a)
	got := metric.NewPixelmatch(0.1).Compare(a, b)
	if got < 0 || got > 1 {
		t.Errorf("Pixelmatch result %v out of [0,1]", got)
	}
}

// --- SSIM metric ---

func TestSSIM_identical(t *testing.T) {
	a := solid(16, 16, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := solid(16, 16, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	got := metric.NewSSIM(0).Compare(a, b)
	if math.Abs(got) > 1e-9 {
		t.Errorf("SSIM identical = %v, want 0", got)
	}
}

func TestSSIM_blackVsWhite(t *testing.T) {
	// Maximal luminance contrast → mean SSIM ≈ 0 → distance ≈ 1.
	a := solid(16, 16, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	b := solid(16, 16, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	got := metric.NewSSIM(0).Compare(a, b)
	if got < 0.9 {
		t.Errorf("SSIM black vs white = %v, want > 0.9", got)
	}
}

func TestSSIM_partialDiffBetweenIdenticalAndOpposite(t *testing.T) {
	// A small structural perturbation should score strictly between identical (0)
	// and black-vs-white (≈1).
	a := solid(16, 16, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	b := solid(16, 16, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	for i := range 16 {
		b.SetRGBA(i, 0, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	}
	got := metric.NewSSIM(0).Compare(a, b)
	if got <= 0 || got >= 1 {
		t.Errorf("SSIM partial diff = %v, want in (0,1)", got)
	}
}

func TestSSIM_returnsZeroToOne(t *testing.T) {
	a := solid(16, 16, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	b := invert(a)
	got := metric.NewSSIM(0).Compare(a, b)
	if got < 0 || got > 1 {
		t.Errorf("SSIM result %v out of [0,1]", got)
	}
}

func TestSSIM_emptyImage(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 0, 0))
	b := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if got := metric.NewSSIM(0).Compare(a, b); got != 0 {
		t.Errorf("SSIM empty = %v, want 0", got)
	}
}

func TestSSIM_smallerThanWindow(t *testing.T) {
	// A 4×4 image with the default 8px window must clamp the window and still
	// score identical images as 0.
	a := solid(4, 4, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	b := solid(4, 4, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	if got := metric.NewSSIM(0).Compare(a, b); math.Abs(got) > 1e-9 {
		t.Errorf("SSIM small identical = %v, want 0", got)
	}
}
