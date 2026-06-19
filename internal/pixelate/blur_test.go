package pixelate_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// TestGaussianBlur_preservesBounds checks the blurred image keeps src's size.
func TestGaussianBlur_preservesBounds(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 20, 12))
	for i := range src.Pix {
		src.Pix[i] = 0xFF
	}
	out := pixelate.NewGaussianBlur(3).Pixelate(src, 0, 0)
	if out.Bounds() != image.Rect(0, 0, 20, 12) {
		t.Errorf("bounds = %v, want 20x12", out.Bounds())
	}
}

// TestGaussianBlur_spreadsAndConservesEnergy verifies a single bright pixel is
// spread to its neighbours (the defining property of blur) while the total
// luminance is approximately conserved (normalised kernel).
func TestGaussianBlur_spreadsAndConservesEnergy(t *testing.T) {
	const n = 21
	src := image.NewRGBA(image.Rect(0, 0, n, n))
	// Black background, one white pixel in the centre.
	mid := n / 2
	src.SetRGBA(mid, mid, color.RGBA{R: 255, G: 255, B: 255, A: 255})

	out := pixelate.NewGaussianBlur(2).Pixelate(src, 0, 0)

	if c := out.RGBAAt(mid, mid); c.R == 255 {
		t.Errorf("centre not dimmed by spreading: R=%d", c.R)
	}
	if c := out.RGBAAt(mid+1, mid); c.R == 0 {
		t.Error("neighbour received no energy — blur did not spread")
	}
	var before, after int
	for i := 0; i < len(src.Pix); i += 4 {
		before += int(src.Pix[i])
		after += int(out.Pix[i])
	}
	// Energy is approximately conserved (rounding + edge clamping cause drift).
	if diff := before - after; diff < -before/2 || diff > before/2 {
		t.Errorf("energy not conserved: before=%d after=%d", before, after)
	}
}

// TestGaussianBlur_sigmaAccessor checks Sigma reflects construction (clamped).
func TestGaussianBlur_sigmaAccessor(t *testing.T) {
	if got := pixelate.NewGaussianBlur(4).Sigma(); got != 4 {
		t.Errorf("Sigma() = %v, want 4", got)
	}
	if got := pixelate.NewGaussianBlur(0).Sigma(); got <= 0 {
		t.Errorf("Sigma() for 0 input = %v, want clamped positive", got)
	}
}
