package metric_test

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/metric"
)

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
