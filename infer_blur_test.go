package unpixel_test

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// makeStepEdge builds a w×h RGBA image with a vertical black/white step at x=w/2.
// It is the standard synthetic edge used throughout InferBlurSigma tests.
func makeStepEdge(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(0)
			if x >= w/2 {
				v = 255
			}
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

// TestInferBlurSigma_gradientRatioAccuracy is the primary accuracy test for the
// Polyblur gradient-ratio estimator. It renders a sharp step edge, blurs it at
// known σ ∈ {1, 2, 4, 8}, calls InferBlurSigma, and verifies the estimate lands
// within ±35% of the true σ. The tolerance is deliberately generous: the function
// seeds a σ-sweep, not a final answer, so a starting-point accuracy of ±35% is
// more than sufficient to reduce the sweep range.
func TestInferBlurSigma_gradientRatioAccuracy(t *testing.T) {
	const (
		w, h = 201, 60 // wide enough for the kernel at σ=8; tall for averaging
		tol  = 0.35    // ±35% of true σ
	)

	cases := []struct {
		sigma float64
	}{
		{1},
		{2},
		{4},
		{8},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("σ=%.0f", tc.sigma), func(t *testing.T) {
			step := makeStepEdge(w, h)
			blurred := pixelate.NewGaussianBlur(tc.sigma).Pixelate(step, 0, 0)

			got := unpixel.InferBlurSigma(blurred)

			lo, hi := tc.sigma*(1-tol), tc.sigma*(1+tol)
			t.Logf("σ_true=%.1f → σ_est=%.3f (want [%.2f, %.2f])", tc.sigma, got, lo, hi)
			if got < lo || got > hi {
				t.Errorf("InferBlurSigma(σ=%.1f) = %.3f, want in [%.2f, %.2f]",
					tc.sigma, got, lo, hi)
			}
		})
	}
}

// TestInferBlurSigma_recoversKnownSigma blurs a black/white step with known
// sigmas and checks the estimate lands in the right ballpark (retained for
// backward compatibility; the new accuracy test above is the primary gate).
func TestInferBlurSigma_recoversKnownSigma(t *testing.T) {
	for _, sigma := range []float64{2, 4, 8} {
		// A vertical step edge: left half black, right half white, on a tall image.
		const w, h = 81, 40
		step := makeStepEdge(w, h)
		blurred := pixelate.NewGaussianBlur(sigma).Pixelate(step, 0, 0)

		got := unpixel.InferBlurSigma(blurred)
		// Single-step estimator: accept within ~40% (it informs a sweep, not exact).
		if got < sigma*0.6 || got > sigma*1.6 {
			t.Errorf("InferBlurSigma(blur σ=%v) = %.2f, want within 0.6..1.6× of %v", sigma, got, sigma)
		}
	}
}

// TestInferBlurSigma_sharpReturnsNearZero verifies that an unblurred (sharp) step
// edge returns approximately 0 — the reblur ratio r approaches 0 when the image
// is already sharp, so the closed-form estimate also approaches 0. The threshold
// is 1.0 px, well below any practical blur amount that would warrant a sweep.
func TestInferBlurSigma_sharpReturnsNearZero(t *testing.T) {
	const w, h = 201, 60
	sharp := makeStepEdge(w, h)
	got := unpixel.InferBlurSigma(sharp)
	t.Logf("sharp step → σ_est=%.3f (want < 1.0)", got)
	if got >= 1.0 {
		t.Errorf("InferBlurSigma(sharp) = %.3f, want < 1.0", got)
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

// TestInferBlurSigma_zeroGradient_noPanic ensures no division-by-zero or panic
// when the reblurred image has no gradient (e.g. a tiny flat image that barely
// passes the contrast guard). The function must return 0 gracefully.
func TestInferBlurSigma_zeroGradient_noPanic(t *testing.T) {
	// A 5×5 image with a sharp two-pixel black/white boundary: contrast is 255
	// (passes the contrast<8 guard) but is so small the percentile gradient on the
	// reblurred image may be 0. Must not panic.
	tiny := image.NewRGBA(image.Rect(0, 0, 5, 5))
	for y := range 5 {
		for x := range 5 {
			v := uint8(0)
			if x >= 2 {
				v = 255
			}
			tiny.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	got := unpixel.InferBlurSigma(tiny) // must not panic
	t.Logf("tiny sharp image → σ_est=%.3f", got)
}

// BenchmarkInferBlurSigma measures the cost of one InferBlurSigma call on a
// realistic blurred image (σ=4, 201×60 pixels). InferBlurSigma is called O(1)
// per RecoverBlurred invocation (once at the top, not in the per-candidate hot
// loop), so this benchmark is informational rather than a regression gate —
// the per-candidate hot path is GaussianBlur.Pixelate (benchmarked in
// internal/pixelate/bench_test.go). Typical absolute cost: <1ms on developer HW.
func BenchmarkInferBlurSigma(b *testing.B) {
	const w, h = 201, 60
	step := makeStepEdge(w, h)
	blurred := pixelate.NewGaussianBlur(4).Pixelate(step, 0, 0)
	b.ReportAllocs()
	for b.Loop() {
		inferSink = unpixel.InferBlurSigma(blurred)
	}
}

// inferSink defeats dead-code elimination in the benchmark.
var inferSink float64
