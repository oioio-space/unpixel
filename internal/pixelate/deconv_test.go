package pixelate_test

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// makeEdgeImage builds a w×h RGBA image with a bright white rectangle in the
// centre third on a black background, giving high-contrast edges for testing
// sharpening: the gradient at those edges is the measurable quantity.
func makeEdgeImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	x0, x1 := w/3, 2*w/3
	y0, y1 := h/3, 2*h/3
	for y := range h {
		for x := range w {
			if x >= x0 && x < x1 && y >= y0 && y < y1 {
				img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
			}
		}
	}
	return img
}

// maxGradientMag computes the maximum per-pixel gradient magnitude (Sobel) in
// the red channel, normalised to [0,1]. Higher values mean sharper edges.
func maxGradientMag(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var maxMag float64
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			px := func(dx, dy int) float64 {
				off := img.PixOffset(x+dx, y+dy)
				return float64(img.Pix[off])
			}
			gx := -px(-1, -1) - 2*px(-1, 0) - px(-1, 1) + px(1, -1) + 2*px(1, 0) + px(1, 1)
			gy := -px(-1, -1) - 2*px(0, -1) - px(1, -1) + px(-1, 1) + 2*px(0, 1) + px(1, 1)
			if mag := math.Sqrt(gx*gx+gy*gy) / (8 * 255); mag > maxMag {
				maxMag = mag
			}
		}
	}
	return maxMag
}

// imageMSE computes the mean squared error between two images (R channel only,
// normalised to [0,1]).
func imageMSE(a, b *image.RGBA) float64 {
	n := len(a.Pix) / 4
	if n == 0 {
		return 0
	}
	var sum float64
	for i := range n {
		da := float64(a.Pix[i*4]) / 255
		db := float64(b.Pix[i*4]) / 255
		d := da - db
		sum += d * d
	}
	return sum / float64(n)
}

// copyRGBA makes a pixel-for-pixel copy of src.
func copyRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

// TestRichardsonLucy_edgeCases verifies the contract for degenerate inputs.
func TestRichardsonLucy_edgeCases(t *testing.T) {
	src := makeEdgeImage(40, 40)

	tests := []struct {
		name       string
		sigma      float64
		iterations int
	}{
		{"iterations=0", 2.0, 0},
		{"iterations=-1", 2.0, -1},
		{"sigma=0", 0.0, 10},
		{"sigma=-1", -1.0, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pixelate.RichardsonLucy(src, tc.sigma, tc.iterations)
			if got == nil {
				t.Fatal("RichardsonLucy returned nil")
			}
			if got.Bounds() != src.Bounds() {
				t.Errorf("bounds = %v, want %v", got.Bounds(), src.Bounds())
			}
			// Must be a copy equal to src (not the same pointer).
			for i := range src.Pix {
				if got.Pix[i] != src.Pix[i] {
					t.Fatalf("pixel[%d]: got %d, want %d (should be copy of src)", i, got.Pix[i], src.Pix[i])
				}
			}
		})
	}
}

// TestRichardsonLucy_emptyImage ensures no panic on a zero-size image.
func TestRichardsonLucy_emptyImage(t *testing.T) {
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	got := pixelate.RichardsonLucy(empty, 2.0, 5)
	if got == nil {
		t.Fatal("RichardsonLucy returned nil for empty image")
	}
	if !got.Bounds().Empty() {
		t.Errorf("expected empty bounds, got %v", got.Bounds())
	}
}

// TestRichardsonLucy_tinyImage ensures no panic on a 1×1 image.
func TestRichardsonLucy_tinyImage(t *testing.T) {
	tiny := image.NewRGBA(image.Rect(0, 0, 1, 1))
	tiny.SetRGBA(0, 0, color.RGBA{R: 128, G: 64, B: 32, A: 255})
	got := pixelate.RichardsonLucy(tiny, 1.5, 5)
	if got == nil {
		t.Fatal("RichardsonLucy returned nil")
	}
	if got.Bounds() != tiny.Bounds() {
		t.Errorf("bounds = %v, want %v", got.Bounds(), tiny.Bounds())
	}
}

// TestRichardsonLucy_sharpensBluaredEdges is the core correctness test: blur a
// high-contrast synthetic image with a known sigma, then run RL deconvolution
// with that same sigma and assert:
//
//  1. The deblurred image has higher max-gradient than the blurred input
//     (edges are sharper), and
//  2. The MSE to the original is lower than the blurred image's MSE (deblur
//     brings us closer to the original).
//
// The test uses sigma=3 and 15 iterations — a regime where RL reliably
// converges and the metrics move by more than rounding noise.
func TestRichardsonLucy_sharpensBluaredEdges(t *testing.T) {
	const (
		w, h       = 64, 64
		sigma      = 3.0
		iterations = 15
	)
	original := makeEdgeImage(w, h)
	blurred := pixelate.NewGaussianBlur(sigma).Pixelate(original, 0, 0)
	deblurred := pixelate.RichardsonLucy(blurred, sigma, iterations)

	gradOrig := maxGradientMag(original)
	gradBlurred := maxGradientMag(blurred)
	gradDeblurred := maxGradientMag(deblurred)

	mseBlurred := imageMSE(blurred, original)
	mseDeblurred := imageMSE(deblurred, original)

	t.Logf("gradient: original=%.4f  blurred=%.4f  deblurred=%.4f", gradOrig, gradBlurred, gradDeblurred)
	t.Logf("MSE-to-original: blurred=%.6f  deblurred=%.6f", mseBlurred, mseDeblurred)

	if gradDeblurred <= gradBlurred {
		t.Errorf("edge sharpness did not increase: blurred=%.4f deblurred=%.4f (want deblurred > blurred)",
			gradBlurred, gradDeblurred)
	}
	if mseDeblurred >= mseBlurred {
		t.Errorf("MSE did not decrease: blurred=%.6f deblurred=%.6f (want deblurred < blurred)",
			mseBlurred, mseDeblurred)
	}
}
