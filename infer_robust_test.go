package unpixel_test

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"github.com/oioio-space/unpixel"
)

// makeNoisyMosaic builds a clean block-pixelated image then applies a
// bilinear-ish resampling simulation: each pixel is replaced by the average
// of itself and its 8-connected neighbours, blending block boundaries. This
// models JPEG-recompressed or screenshot-scaled mosaic images where exact
// colour boundaries are lost.
func makeNoisyMosaic(w, h, block int, rng *rand.Rand) *image.RGBA {
	// Start with a clean mosaic.
	src := pixelatedGrid(w, h, block)

	// Simple box-blur (radius 1) to blend boundaries — simulates resampling.
	blurred := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			var rSum, gSum, bSum, cnt float64
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx < 0 || nx >= w || ny < 0 || ny >= h {
						continue
					}
					c := src.RGBAAt(nx, ny)
					rSum += float64(c.R)
					gSum += float64(c.G)
					bSum += float64(c.B)
					cnt++
				}
			}
			blurred.SetRGBA(x, y, color.RGBA{
				R: uint8(rSum / cnt),
				G: uint8(gSum / cnt),
				B: uint8(bSum / cnt),
				A: 255,
			})
		}
	}

	// Add small uniform noise (±4) to simulate JPEG quantisation.
	for y := range h {
		for x := range w {
			c := blurred.RGBAAt(x, y)
			add := func(v uint8, delta int) uint8 {
				n := int(v) + delta
				if n < 0 {
					return 0
				}
				if n > 255 {
					return 255
				}
				return uint8(n)
			}
			delta := rng.IntN(9) - 4 // [-4, 4]
			blurred.SetRGBA(x, y, color.RGBA{
				R: add(c.R, delta),
				G: add(c.G, delta),
				B: add(c.B, delta),
				A: 255,
			})
		}
	}
	return blurred
}

// makeGaussianBlurred builds an image that is smooth (Gaussian-blurred text-
// like gradient), with no periodic block structure, to confirm the robust
// detector does not mistake it for a mosaic.
func makeGaussianBlurred(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			// A smooth horizontal gradient — no hard block edges at all.
			v := uint8(float64(x) / float64(w) * 200)
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

// TestInferBlockSizeRobust_cleanMosaic verifies that the robust detector
// returns the correct block size on a perfectly clean mosaic (no noise), and
// produces a support score ≥ 0.9.
func TestInferBlockSizeRobust_cleanMosaic(t *testing.T) {
	for _, block := range []int{4, 8, 16} {
		img := pixelatedGrid(8*block, 4*block, block)
		got, support := unpixel.InferBlockSizeRobust(img)
		if got != block {
			t.Errorf("block=%d: InferBlockSizeRobust = %d, want %d", block, got, block)
		}
		if support < 0.9 {
			t.Errorf("block=%d: support=%.3f, want ≥ 0.9 for clean mosaic", block, support)
		}
	}
}

// TestInferBlockSizeRobust_noisyMosaic verifies that the robust detector
// recovers the approximate block size from a noisy (blurred+quantised) mosaic.
// The exact size need not match — within ±2 pixels of the true size is
// acceptable, reflecting real-world variation due to resampling artefacts.
func TestInferBlockSizeRobust_noisyMosaic(t *testing.T) {
	const block = 8
	rng := rand.New(rand.NewPCG(42, 0))
	img := makeNoisyMosaic(8*block, 4*block, block, rng)
	got, support := unpixel.InferBlockSizeRobust(img)
	if got < block-2 || got > block+2 {
		t.Errorf("noisy mosaic (block=%d): InferBlockSizeRobust = %d, want ~%d (±2)", block, got, block)
	}
	if support < 0.3 {
		t.Errorf("noisy mosaic: support=%.3f too low; expected ≥ 0.3 for detectable periodic structure", support)
	}
}

// TestInferBlockSizeRobust_blurredImageNoGrid verifies that a genuinely
// Gaussian-blurred image (no periodic block structure) is NOT detected as a
// mosaic: support should be low (< 0.5) or the returned size should be 0.
func TestInferBlockSizeRobust_blurredImageNoGrid(t *testing.T) {
	img := makeGaussianBlurred(128, 64)
	got, support := unpixel.InferBlockSizeRobust(img)
	// Either the size is 0 (no grid detected) or support is clearly low.
	if got > 0 && support >= 0.5 {
		t.Errorf("Gaussian-blurred image: detected block=%d support=%.3f; expected no strong grid", got, support)
	}
}
