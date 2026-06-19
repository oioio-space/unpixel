package unpixel_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// TestInferBlurSigma_recoversKnownSigma blurs a black/white step with known
// sigmas and checks the estimate lands in the right ballpark.
func TestInferBlurSigma_recoversKnownSigma(t *testing.T) {
	for _, sigma := range []float64{2, 4, 8} {
		// A vertical step edge: left half black, right half white, on a tall image.
		const w, h = 81, 40
		step := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				v := uint8(0)
				if x >= w/2 {
					v = 255
				}
				step.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
			}
		}
		blurred := pixelate.NewGaussianBlur(sigma).Pixelate(step, 0, 0)

		got := unpixel.InferBlurSigma(blurred)
		// Single-step estimator: accept within ~40% (it informs a sweep, not exact).
		if got < sigma*0.6 || got > sigma*1.6 {
			t.Errorf("InferBlurSigma(blur σ=%v) = %.2f, want within 0.6..1.6× of %v", sigma, got, sigma)
		}
	}
}

// TestInferBlurSigma_sharpIsSmall checks a sharp step yields a small sigma
// (so auto-detection can tell "sharp / not blurred" from "blurred").
func TestInferBlurSigma_sharpIsSmall(t *testing.T) {
	const w, h = 40, 20
	sharp := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(0)
			if x >= w/2 {
				v = 255
			}
			sharp.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	if got := unpixel.InferBlurSigma(sharp); got > 1.0 {
		t.Errorf("InferBlurSigma(sharp) = %.2f, want <= 1.0", got)
	}
}

// TestInferBlurSigma_flat returns 0 on a uniform image.
func TestInferBlurSigma_flat(t *testing.T) {
	flat := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := range flat.Pix {
		flat.Pix[i] = 0xFF
	}
	if got := unpixel.InferBlurSigma(flat); got != 0 {
		t.Errorf("InferBlurSigma(flat) = %v, want 0", got)
	}
}
