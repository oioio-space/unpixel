package metric_test

// Differential equivalence test: the in-repo pixelmatch counting path must
// produce bit-identical results to github.com/orisano/pixelmatch for all
// image shapes and content types exercised below.

import (
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"github.com/orisano/pixelmatch"

	"github.com/oioio-space/unpixel/internal/metric"
)

// refCount calls the upstream library and panics on error (sizes always match
// in this test).
func refCount(a, b *image.RGBA, threshold float64) int {
	n, err := pixelmatch.MatchPixel(a, b, pixelmatch.Threshold(threshold))
	if err != nil {
		panic(err)
	}
	return n
}

// makeRand builds a random w×h *image.RGBA with the provided RNG.
func makeRand(rng *rand.Rand, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.IntN(256))
	}
	return img
}

// makeGradient builds a horizontal luminance ramp across [0, 255].
func makeGradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(x * 255 / max(w-1, 1))
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

// makeAlpha builds a random image where alpha values are < 255 (semi-transparent).
func makeAlpha(rng *rand.Rand, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(rng.IntN(256)),
				G: uint8(rng.IntN(256)),
				B: uint8(rng.IntN(256)),
				A: uint8(rng.IntN(200)), // always < 255
			})
		}
	}
	return img
}

// sparseDiff returns a copy of src with ~5% of pixels changed to a random color.
func sparseDiff(rng *rand.Rand, src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	copy(dst.Pix, src.Pix)
	total := b.Dx() * b.Dy()
	for range total / 20 {
		x := b.Min.X + rng.IntN(b.Dx())
		y := b.Min.Y + rng.IntN(b.Dy())
		dst.SetRGBA(x, y, color.RGBA{
			R: uint8(rng.IntN(256)),
			G: uint8(rng.IntN(256)),
			B: uint8(rng.IntN(256)),
			A: 255,
		})
	}
	return dst
}

// subImage returns a sub-image of src with non-zero Rect.Min, preserving pixel
// data via a fresh allocation (so Stride padding is present and Min != {0,0}).
func subImage(src *image.RGBA, offsetX, offsetY int) *image.RGBA {
	b := src.Bounds()
	newW := b.Dx() - offsetX
	newH := b.Dy() - offsetY
	if newW <= 0 || newH <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	// Allocate a larger canvas and use the sub-image region so Rect.Min != {0,0}.
	canvas := image.NewRGBA(image.Rect(0, 0, newW+offsetX, newH+offsetY))
	for y := range newH {
		for x := range newW {
			canvas.SetRGBA(offsetX+x, offsetY+y, src.RGBAAt(b.Min.X+offsetX+x, b.Min.Y+offsetY+y))
		}
	}
	return canvas.SubImage(image.Rect(offsetX, offsetY, offsetX+newW, offsetY+newH)).(*image.RGBA)
}

// TestPixelmatchNative_Differential is the bit-exactness fidelity test.
// It exercises thousands of randomised image pairs and verifies that the
// in-repo implementation returns the same diff count as the upstream library
// for thresholds 0.02, 0.1, and 0.25.
func TestPixelmatchNative_Differential(t *testing.T) {
	thresholds := []float64{0.02, 0.1, 0.25}

	// Seeded for determinism.
	rng := rand.New(rand.NewPCG(0xdeadbeef, 0xcafebabe))

	type tc struct {
		name string
		a, b *image.RGBA
	}

	var cases []tc

	// 1×1 edge cases.
	cases = append(cases, tc{"1x1_identical", solid(1, 1, color.RGBA{R: 100, G: 150, B: 200, A: 255}), solid(1, 1, color.RGBA{R: 100, G: 150, B: 200, A: 255})})
	cases = append(cases, tc{"1x1_different", solid(1, 1, color.RGBA{R: 0, G: 0, B: 0, A: 255}), solid(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})})
	cases = append(cases, tc{"1x1_alpha0", solid(1, 1, color.RGBA{R: 128, G: 64, B: 32, A: 0}), solid(1, 1, color.RGBA{R: 200, G: 100, B: 50, A: 0})})

	// Fully identical solid images.
	for _, sz := range [][2]int{{8, 8}, {16, 16}, {33, 17}, {200, 40}} {
		w, h := sz[0], sz[1]
		c := color.RGBA{R: uint8(rng.IntN(256)), G: uint8(rng.IntN(256)), B: uint8(rng.IntN(256)), A: 255}
		img := solid(w, h, c)
		cases = append(cases, tc{
			name: "solid_identical",
			a:    img,
			b:    solid(w, h, c),
		})
	}

	// Fully different (black vs white).
	cases = append(cases, tc{"bvw_8x8", solid(8, 8, color.RGBA{A: 255}), solid(8, 8, color.RGBA{R: 255, G: 255, B: 255, A: 255})})
	cases = append(cases, tc{"bvw_33x17", solid(33, 17, color.RGBA{A: 255}), solid(33, 17, color.RGBA{R: 255, G: 255, B: 255, A: 255})})

	// Random image pairs — varied sizes including odd widths.
	sizes := [][2]int{{8, 8}, {16, 16}, {33, 17}, {1, 100}, {100, 1}, {200, 40}, {47, 53}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		a := makeRand(rng, w, h)
		b := makeRand(rng, w, h)
		cases = append(cases, tc{fmt.Sprintf("rand_%dx%d", w, h), a, b})
		// Same-content pair (identical fast-path).
		aCopy := image.NewRGBA(a.Bounds())
		copy(aCopy.Pix, a.Pix)
		cases = append(cases, tc{fmt.Sprintf("rand_identical_fastpath_%dx%d", w, h), a, aCopy})
	}

	// Sparse diffs.
	for range 20 {
		w := 8 + rng.IntN(100)
		h := 8 + rng.IntN(100)
		a := makeRand(rng, w, h)
		b := sparseDiff(rng, a)
		cases = append(cases, tc{"sparse_diff", a, b})
	}

	// Gradient / anti-aliasing-like images.
	for _, sz := range [][2]int{{64, 64}, {33, 17}, {200, 40}} {
		w, h := sz[0], sz[1]
		g := makeGradient(w, h)
		gShift := makeGradient(w, h)
		// Shift gradient slightly.
		for i := range gShift.Pix {
			if gShift.Pix[i] > 10 {
				gShift.Pix[i] -= 10
			}
		}
		cases = append(cases, tc{"gradient_shifted", g, gShift})
	}

	// Alpha < 255 cases.
	for range 10 {
		w := 16 + rng.IntN(50)
		h := 16 + rng.IntN(50)
		a := makeAlpha(rng, w, h)
		b := makeAlpha(rng, w, h)
		cases = append(cases, tc{"alpha_rand", a, b})
	}

	// Sub-images with non-zero Rect.Min and Stride padding.
	// The upstream library panics when Rect.Min.X > 0 (its At() uses raw x as
	// array index), so we test the native implementation separately below and
	// do not include sub-images in the differential loop.
	_ = subImage // used in TestPixelmatchNative_SubImage below

	// Large random batch — 500 additional random pairs for thorough coverage.
	for range 500 {
		w := 1 + rng.IntN(60)
		h := 1 + rng.IntN(60)
		a := makeRand(rng, w, h)
		b := makeRand(rng, w, h)
		cases = append(cases, tc{"large_batch", a, b})
	}

	for _, threshold := range thresholds {
		for _, tc := range cases {
			got := metric.CountPixels(tc.a, tc.b, threshold)
			want := refCount(tc.a, tc.b, threshold)
			if got != want {
				t.Errorf("threshold=%.2f case=%s bounds=%v: native=%d ref=%d",
					threshold, tc.name, tc.a.Bounds(), got, want)
			}
		}
	}
}

// TestPixelmatchNative_IdenticalFastPath verifies the bytes.Equal fast path
// returns 0 without any per-pixel work.
func TestPixelmatchNative_IdenticalFastPath(t *testing.T) {
	a := solid(64, 64, color.RGBA{R: 123, G: 45, B: 67, A: 255})
	b := solid(64, 64, color.RGBA{R: 123, G: 45, B: 67, A: 255})
	m := metric.NewPixelmatch(0.02)
	if got := m.Compare(a, b); got != 0 {
		t.Errorf("identical fast path = %v, want 0", got)
	}
}

// TestPixelmatchNative_EmptyImage verifies the zero-pixel guard.
func TestPixelmatchNative_EmptyImage(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 0, 0))
	b := image.NewRGBA(image.Rect(0, 0, 0, 0))
	m := metric.NewPixelmatch(0.1)
	if got := m.Compare(a, b); got != 0 {
		t.Errorf("empty image = %v, want 0", got)
	}
}

// TestPixelmatchNative_SubImage verifies that CountPixels handles *image.RGBA
// sub-images (non-zero Rect.Min, Stride padding) correctly. The upstream
// library panics on these, so we verify correctness by comparing against a
// freshly-allocated crop that the upstream library CAN handle.
func TestPixelmatchNative_SubImage(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x1234, 0x5678))

	for _, off := range [][2]int{{2, 3}, {5, 5}, {1, 0}, {0, 4}} {
		ox, oy := off[0], off[1]
		base := makeRand(rng, 40, 40)
		baseDiff := sparseDiff(rng, base)

		// Sub-images: non-zero Rect.Min, Stride > w*4.
		siA := subImage(base, ox, oy)
		siB := subImage(baseDiff, ox, oy)
		if !siA.Bounds().Eq(siB.Bounds()) {
			continue
		}

		// Crop equivalents: fresh allocations with Rect starting at (0,0).
		cropA := image.NewRGBA(image.Rect(0, 0, siA.Bounds().Dx(), siA.Bounds().Dy()))
		cropB := image.NewRGBA(image.Rect(0, 0, siB.Bounds().Dx(), siB.Bounds().Dy()))
		for y := range cropA.Bounds().Dy() {
			for x := range cropA.Bounds().Dx() {
				cropA.SetRGBA(x, y, siA.RGBAAt(siA.Bounds().Min.X+x, siA.Bounds().Min.Y+y))
				cropB.SetRGBA(x, y, siB.RGBAAt(siB.Bounds().Min.X+x, siB.Bounds().Min.Y+y))
			}
		}

		for _, threshold := range []float64{0.02, 0.1, 0.25} {
			got := metric.CountPixels(siA, siB, threshold)
			want := refCount(cropA, cropB, threshold)
			if got != want {
				t.Errorf("offset=(%d,%d) threshold=%.2f: CountPixels(subimage)=%d want=%d",
					ox, oy, threshold, got, want)
			}
		}
	}
}
