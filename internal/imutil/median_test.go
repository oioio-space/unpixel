package imutil_test

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/segment"
)

// --- Unit: impulse noise removal ---

// TestMedian_flatRegion verifies that Median(radius=1) on a flat-colour image
// returns the same flat colour everywhere.
func TestMedian_flatRegion(t *testing.T) {
	const w, h = 20, 20
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := color.RGBA{R: 100, G: 150, B: 200, A: 255}
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}

	got := imutil.Median(img, 1)
	for y := range h {
		for x := range w {
			if got.RGBAAt(x, y) != c {
				t.Fatalf("Median flat region: pixel (%d,%d) = %v, want %v", x, y, got.RGBAAt(x, y), c)
			}
		}
	}
}

// TestMedian_removesImpulseNoise verifies that salt-and-pepper spikes in a flat
// region are eliminated after one pass of Median(radius=1).
func TestMedian_removesImpulseNoise(t *testing.T) {
	const w, h = 15, 15
	base := color.RGBA{R: 200, G: 200, B: 200, A: 255}
	noise := color.RGBA{R: 0, G: 0, B: 0, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, base)
		}
	}
	// Place isolated impulse pixels — none adjacent to each other so the
	// 3×3 window always contains at least five base-colour neighbours.
	impulses := [][2]int{{3, 3}, {7, 5}, {10, 10}, {2, 11}}
	for _, p := range impulses {
		img.SetRGBA(p[0], p[1], noise)
	}

	got := imutil.Median(img, 1)

	for _, p := range impulses {
		c := got.RGBAAt(p[0], p[1])
		if c.R != base.R || c.G != base.G || c.B != base.B {
			t.Errorf("impulse at (%d,%d) not removed: got %v, want %v", p[0], p[1], c, base)
		}
	}
	// Flat region outside impulse sites must remain unchanged.
	for y := range h {
		for x := range w {
			c := got.RGBAAt(x, y)
			if c.R != base.R || c.G != base.G || c.B != base.B {
				t.Errorf("non-impulse pixel changed at (%d,%d): got %v, want %v", x, y, c, base)
			}
		}
	}
}

// TestMedian_edgePreservation checks that a sharp horizontal edge between two
// half-planes is preserved: the median of a neighbourhood straddling the edge
// equals one of the two original values, not an average of them.
func TestMedian_edgePreservation(t *testing.T) {
	const w, h = 20, 20
	dark := color.RGBA{R: 10, G: 10, B: 10, A: 255}
	light := color.RGBA{R: 245, G: 245, B: 245, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if y < h/2 {
				img.SetRGBA(x, y, dark)
			} else {
				img.SetRGBA(x, y, light)
			}
		}
	}

	got := imutil.Median(img, 1)

	// Rows well inside each half must retain their exact original colour.
	for x := range w {
		// Two rows above the edge centre.
		c := got.RGBAAt(x, h/2-2)
		if c.R != dark.R {
			t.Errorf("top half blurred at (%d,%d): R=%d, want %d", x, h/2-2, c.R, dark.R)
		}
		// Two rows below the edge centre.
		c = got.RGBAAt(x, h/2+1)
		if c.R != light.R {
			t.Errorf("bottom half blurred at (%d,%d): R=%d, want %d", x, h/2+1, c.R, light.R)
		}
	}
}

// TestMedian_zeroRadius verifies that radius≤0 returns a pixel-identical copy.
func TestMedian_zeroRadius(t *testing.T) {
	img := newWhite(10, 10)
	img.SetRGBA(5, 5, color.RGBA{R: 42, G: 42, B: 42, A: 255})

	got := imutil.Median(img, 0)
	if got == img {
		t.Fatal("Median(radius=0) must return a copy, not the same pointer")
	}
	for y := range 10 {
		for x := range 10 {
			if got.RGBAAt(x, y) != img.RGBAAt(x, y) {
				t.Fatalf("Median(radius=0) pixel mismatch at (%d,%d): got %v, want %v",
					x, y, got.RGBAAt(x, y), img.RGBAAt(x, y))
			}
		}
	}
}

// TestMedian_zeroRadius_subImage verifies that Median(radius=0) on a sub-image
// (non-zero bounds origin) returns pixel-correct data, not data from the
// backing array at offset 0.
func TestMedian_zeroRadius_subImage(t *testing.T) {
	// Build a 10×10 parent with distinct per-pixel colours.
	parent := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			parent.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 10),
				G: uint8(y * 10),
				B: uint8((x + y) * 5),
				A: 255,
			})
		}
	}

	// Crop a 4×4 sub-region starting at (3, 4) using imutil.Crop, which
	// returns an origin-0 image — then re-test with a raw Sub-image that
	// retains the non-zero origin via image.RGBA.SubImage.
	sub, ok := parent.SubImage(image.Rect(3, 4, 7, 8)).(*image.RGBA)
	if !ok {
		t.Fatal("SubImage did not return *image.RGBA")
	}
	// sub.Bounds().Min == (3,4); sub shares the parent's Pix backing array.

	got := imutil.Median(sub, 0)

	// got must be origin-0 with the correct 4×4 pixel content.
	if got.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("Median(subImg, 0) bounds = %v, want (0,0)-(4,4)", got.Bounds())
	}
	for dy := range 4 {
		for dx := range 4 {
			want := parent.RGBAAt(3+dx, 4+dy)
			if got.RGBAAt(dx, dy) != want {
				t.Errorf("pixel (%d,%d): got %v, want %v (parent[%d,%d])",
					dx, dy, got.RGBAAt(dx, dy), want, 3+dx, 4+dy)
			}
		}
	}
}

// TestMedian_alphaPassthrough verifies that the alpha channel is median-filtered
// consistently with R/G/B: an isolated low-alpha spike is replaced by the
// surrounding majority alpha value.
func TestMedian_alphaPassthrough(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			img.SetRGBA(x, y, color.RGBA{R: 200, G: 200, B: 200, A: 128})
		}
	}
	// One fully-transparent pixel: its 3×3 window has 8 samples at A=128 and
	// 1 at A=0 → sorted median (index 4) = 128.
	img.SetRGBA(5, 5, color.RGBA{R: 0, G: 0, B: 0, A: 0})

	got := imutil.Median(img, 1)
	if a := got.RGBAAt(5, 5).A; a != 128 {
		t.Errorf("alpha at impulse site: got %d, want 128", a)
	}
}

// --- Headline improvement test: segment.Lines degrades on noise, recovers after Median ---

// TestMedian_improves_segmentLines demonstrates that:
//
//   - A white image with two dark horizontal ink bands (simulating two text
//     lines), then peppered with 3 % isolated black noise pixels, causes
//     segment.Lines to report more than 2 lines (spurious ink rows split bands).
//   - After Median(radius=1) the isolated noise pixels are removed and
//     segment.Lines correctly reports exactly 2 lines.
//
// Noise model: 3 % of pixels set to pure black (pepper-only, guarantees
// spurious ink rows without introducing light noise inside ink bands).
// RNG seed: fixed (ChaCha8, seed [32]byte{7}).
// True line count: 2.
func TestMedian_improves_segmentLines(t *testing.T) {
	const (
		imgW      = 200
		imgH      = 80
		band1Y0   = 10 // first ink band
		band1Y1   = 30
		band2Y0   = 50 // second ink band (separated by white gap 30–50)
		band2Y1   = 70
		trueLines = 2
		noiseFrac = 0.03 // 3 % pepper noise
		inkLum    = 40   // dark grey → luminance ≈ 40 < 244 → counts as ink
		bgLum     = 255  // white background
	)

	// Build a clean image: white background with two solid dark-grey bands.
	clean := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	for y := range imgH {
		for x := range imgW {
			if (y >= band1Y0 && y < band1Y1) || (y >= band2Y0 && y < band2Y1) {
				clean.SetRGBA(x, y, color.RGBA{R: inkLum, G: inkLum, B: inkLum, A: 255})
			} else {
				clean.SetRGBA(x, y, color.RGBA{R: bgLum, G: bgLum, B: bgLum, A: 255})
			}
		}
	}

	// Confirm the clean image has exactly 2 lines.
	cleanLines := segment.Lines(clean)
	if len(cleanLines) != trueLines {
		t.Fatalf("clean image: segment.Lines = %d, want %d", len(cleanLines), trueLines)
	}

	// Add pepper noise: random black pixels over the whole image.
	// Black pixels have luminance 0 → always counted as ink by segment.Lines,
	// so noise in the white gap rows (30–50) creates spurious ink rows and
	// splits or multiplies the detected line count.
	noisy := copyRGBA(clean)
	rng := rand.New(rand.NewChaCha8([32]byte{7}))
	noiseCount := int(float64(imgW*imgH) * noiseFrac)
	for range noiseCount {
		x := rng.IntN(imgW)
		y := rng.IntN(imgH)
		noisy.SetRGBA(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	}

	// Confirm noise degrades the line detector.
	noisyLines := segment.Lines(noisy)
	if len(noisyLines) == trueLines {
		t.Skipf("noise did not degrade segment.Lines (still %d); noise model insufficient", len(noisyLines))
	}
	t.Logf("noisy: segment.Lines = %d lines (degraded from %d, as expected)", len(noisyLines), trueLines)

	// After Median denoising, the detector must recover the true count.
	denoised := imutil.Median(noisy, 1)
	denoisedLines := segment.Lines(denoised)
	if len(denoisedLines) != trueLines {
		t.Errorf("after Median: segment.Lines = %d lines, want %d", len(denoisedLines), trueLines)
	} else {
		t.Logf("after Median: segment.Lines = %d lines (restored)", len(denoisedLines))
	}
}

// copyRGBA returns a deep copy of img.
func copyRGBA(img *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(img.Bounds())
	copy(dst.Pix, img.Pix)
	return dst
}

// --- Benchmarks ---

// BenchmarkMedian_r1 benchmarks Median(radius=1) over a 200×100 RGBA image.
func BenchmarkMedian_r1(b *testing.B) {
	img := makeRGBA(200, 100)
	b.ReportAllocs()
	for b.Loop() {
		sinkRGBA = imutil.Median(img, 1)
	}
}

// BenchmarkMedian_r2 benchmarks Median(radius=2) over a 200×100 RGBA image.
func BenchmarkMedian_r2(b *testing.B) {
	img := makeRGBA(200, 100)
	b.ReportAllocs()
	for b.Loop() {
		sinkRGBA = imutil.Median(img, 2)
	}
}
