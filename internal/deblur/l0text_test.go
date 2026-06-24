package deblur

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// makeSharpText returns a synthetic sharp text image: white background with a
// dark stripe simulating a text stroke.
func makeSharpText(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(240) // white background
			if y >= h/3 && y < 2*h/3 {
				v = 30 // dark text stroke
			}
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

// applyGaussianBlur blurs an *image.RGBA with a simple separable Gaussian.
// Used in tests to produce known-σ blurred images without importing pixelate
// (to keep the deblur package dependency-light).
func applyGaussianBlur(src *image.RGBA, sigma float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return image.NewRGBA(b)
	}

	radius := int(math.Ceil(3 * sigma))
	kernel := make([]float64, 2*radius+1)
	var sum float64
	for i := -radius; i <= radius; i++ {
		v := math.Exp(-float64(i*i) / (2 * sigma * sigma))
		kernel[i+radius] = v
		sum += v
	}
	for i := range kernel {
		kernel[i] /= sum
	}

	n := w * h
	tmp := make([]float64, n)
	out := make([]float64, n)

	// Extract luminance.
	lum := make([]float64, n)
	for y := range h {
		for x := range w {
			c := src.RGBAAt(b.Min.X+x, b.Min.Y+y)
			lum[y*w+x] = float64(c.R)
		}
	}

	// Horizontal pass.
	for y := range h {
		for x := range w {
			var acc float64
			for k := -radius; k <= radius; k++ {
				sx := x + k
				if sx < 0 {
					sx = 0
				} else if sx >= w {
					sx = w - 1
				}
				acc += lum[y*w+sx] * kernel[k+radius]
			}
			tmp[y*w+x] = acc
		}
	}
	// Vertical pass.
	for y := range h {
		for x := range w {
			var acc float64
			for k := -radius; k <= radius; k++ {
				sy := y + k
				if sy < 0 {
					sy = 0
				} else if sy >= h {
					sy = h - 1
				}
				acc += tmp[sy*w+x] * kernel[k+radius]
			}
			out[y*w+x] = acc
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range n {
		v := uint8(math.Round(math.Max(0, math.Min(255, out[i]))))
		dst.Pix[i*4] = v
		dst.Pix[i*4+1] = v
		dst.Pix[i*4+2] = v
		dst.Pix[i*4+3] = 255
	}
	return dst
}

// mse computes the mean squared error between two *image.RGBA images.
func mse(a, b *image.RGBA) float64 {
	ab := a.Bounds()
	bb := b.Bounds()
	w := min(ab.Dx(), bb.Dx())
	h := min(ab.Dy(), bb.Dy())
	if w == 0 || h == 0 {
		return 0
	}
	var sum float64
	for y := range h {
		for x := range w {
			ca := a.RGBAAt(ab.Min.X+x, ab.Min.Y+y)
			cb := b.RGBAAt(bb.Min.X+x, bb.Min.Y+y)
			d := float64(ca.R) - float64(cb.R)
			sum += d * d
		}
	}
	return sum / float64(w*h)
}

// gradEnergy computes the mean squared gradient magnitude (Sobel-like) as a
// proxy for edge sharpness.
func gradEnergy(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return 0
	}
	var sum float64
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			// Horizontal gradient.
			l := float64(img.RGBAAt(b.Min.X+x-1, b.Min.Y+y).R)
			r := float64(img.RGBAAt(b.Min.X+x+1, b.Min.Y+y).R)
			// Vertical gradient.
			u := float64(img.RGBAAt(b.Min.X+x, b.Min.Y+y-1).R)
			d := float64(img.RGBAAt(b.Min.X+x, b.Min.Y+y+1).R)
			gx := (r - l) / 2
			gy := (d - u) / 2
			sum += gx*gx + gy*gy
		}
	}
	return sum / float64((w-2)*(h-2))
}

// TestTextL0_syntheticSharpening verifies that TextL0 reduces the
// MSE between the deblurred result and the original sharp image compared to the
// blurred input. This tests σ=2 and σ=3.
func TestTextL0_syntheticSharpening(t *testing.T) {
	sharp := makeSharpText(64, 64)
	for _, sigma := range []float64{2, 3} {
		blurred := applyGaussianBlur(sharp, sigma)
		deblurred := TextL0(blurred, sigma)

		mseBefore := mse(blurred, sharp)
		mseAfter := mse(deblurred, sharp)

		t.Logf("σ=%.0f: MSE blurred=%.2f  deblurred=%.2f", sigma, mseBefore, mseAfter)
		if mseAfter >= mseBefore {
			t.Errorf("σ=%.0f: TextL0 did not improve MSE: got %.2f, want < %.2f",
				sigma, mseAfter, mseBefore)
		}
	}
}

// TestTextL0_edgeSharpening asserts that the gradient energy (edge
// sharpness) increases after deblurring.
func TestTextL0_edgeSharpening(t *testing.T) {
	sharp := makeSharpText(64, 64)
	for _, sigma := range []float64{2, 3} {
		blurred := applyGaussianBlur(sharp, sigma)
		deblurred := TextL0(blurred, sigma)

		egBefore := gradEnergy(blurred)
		egAfter := gradEnergy(deblurred)

		t.Logf("σ=%.0f: grad energy blurred=%.4f  deblurred=%.4f", sigma, egBefore, egAfter)
		if egAfter <= egBefore {
			t.Errorf("σ=%.0f: TextL0 did not increase edge energy: got %.4f, want > %.4f",
				sigma, egAfter, egBefore)
		}
	}
}

// TestTextL0_deterministic verifies that two calls with the same inputs
// produce byte-identical results.
func TestTextL0_deterministic(t *testing.T) {
	sharp := makeSharpText(32, 32)
	blurred := applyGaussianBlur(sharp, 2)

	r1 := TextL0(blurred, 2)
	r2 := TextL0(blurred, 2)

	b := r1.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c1 := r1.RGBAAt(x, y)
			c2 := r2.RGBAAt(x, y)
			if c1 != c2 {
				t.Fatalf("non-deterministic at (%d,%d): got %v vs %v", x, y, c1, c2)
			}
		}
	}
}

// TestTextL0_doesNotMutateSrc verifies the input is not modified.
func TestTextL0_doesNotMutateSrc(t *testing.T) {
	sharp := makeSharpText(32, 32)
	blurred := applyGaussianBlur(sharp, 2)
	orig := make([]byte, len(blurred.Pix))
	copy(orig, blurred.Pix)

	_ = TextL0(blurred, 2)

	for i := range orig {
		if blurred.Pix[i] != orig[i] {
			t.Fatalf("TextL0 mutated src.Pix[%d]: got %d, want %d", i, blurred.Pix[i], orig[i])
		}
	}
}

// TestTextL0_outputAlwaysOpaque verifies A=255 for every output pixel.
func TestTextL0_outputAlwaysOpaque(t *testing.T) {
	sharp := makeSharpText(32, 32)
	blurred := applyGaussianBlur(sharp, 2)
	got := TextL0(blurred, 2)
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if a := got.RGBAAt(x, y).A; a != 255 {
				t.Fatalf("pixel (%d,%d) has alpha %d, want 255", x, y, a)
			}
		}
	}
}

// TestTextL0_emptyImage verifies no panic on a zero-size image.
func TestTextL0_emptyImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 0, 0))
	got := TextL0(src, 2)
	if got == nil {
		t.Fatal("TextL0(empty) returned nil")
	}
}

// TestTextL0_WithOptions verifies that option setters work without panic
// and produce valid output.
func TestTextL0_WithOptions(t *testing.T) {
	sharp := makeSharpText(32, 32)
	blurred := applyGaussianBlur(sharp, 2)
	got := TextL0(
		blurred, 2,
		WithL0Lambda(2e-3),
		WithL0Mu(5e-4),
		WithL0Iterations(5),
	)
	if got == nil {
		t.Fatal("TextL0 with options returned nil")
	}
	if got.Bounds().Dx() != 32 || got.Bounds().Dy() != 32 {
		t.Errorf("unexpected output size %v", got.Bounds())
	}
}

// TestWithL0Deblur_defaultOff verifies that RecoverBlurredPreprocess without
// the L0 option returns a byte-identical copy (default-off / non-regression).
func TestWithL0Deblur_defaultOff(t *testing.T) {
	sharp := makeSharpText(32, 32)
	blurred := applyGaussianBlur(sharp, 2)

	// RecoverBlurredPreprocess with nil opts = identity.
	out := RecoverBlurredPreprocess(blurred, nil)

	for i := range blurred.Pix {
		if out.Pix[i] != blurred.Pix[i] {
			t.Fatalf("default-off: RecoverBlurredPreprocess mutated pixel %d: got %d, want %d",
				i, out.Pix[i], blurred.Pix[i])
		}
	}
}

// TestWithL0Deblur_appliesWhenEnabled verifies that RecoverBlurredPreprocess
// with a non-nil L0Options does apply deblurring (output differs from input).
func TestWithL0Deblur_appliesWhenEnabled(t *testing.T) {
	sharp := makeSharpText(48, 48)
	blurred := applyGaussianBlur(sharp, 3)

	opts := &L0Options{Sigma: 3}
	out := RecoverBlurredPreprocess(blurred, opts)

	// At least one pixel should differ (the L0 deblur modifies the image).
	differs := false
	for i := range blurred.Pix {
		if out.Pix[i] != blurred.Pix[i] {
			differs = true
			break
		}
	}
	if !differs {
		t.Error("RecoverBlurredPreprocess with L0Options did not modify the image")
	}
}
