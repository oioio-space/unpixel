package metric_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/metric"
)

// sinkFloat defeats dead-code elimination for float64 benchmark results.
var sinkFloat float64

// makeSolidRGBA builds a w×h RGBA image filled with c.
func makeSolidRGBA(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// makePartialDiffRGBA returns a copy of src where roughly 10% of pixels differ.
func makePartialDiffRGBA(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	copy(dst.Pix, src.Pix)
	// Alter every 10th pixel in the first row to create ~10% difference.
	total := b.Dx() * b.Dy()
	stride := 10
	for i := range total / stride {
		x := (i * stride) % b.Dx()
		y := (i * stride) / b.Dx()
		dst.SetRGBA(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	}
	return dst
}

// BenchmarkPixelmatch_Distance benchmarks Pixelmatch.Compare on two 200×40
// images. Sub-benchmarks cover identical vs ~10%-different inputs.
func BenchmarkPixelmatch_Distance(b *testing.B) {
	const w, h = 200, 40
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	a := makeSolidRGBA(w, h, white)
	bSame := makeSolidRGBA(w, h, white)
	bDiff := makePartialDiffRGBA(a)

	m := metric.NewPixelmatch(0.1)

	// gradient vs slightly-shifted gradient: every pixel differs, so the
	// per-pixel colorDelta + anti-alias path runs on the whole image. This is
	// the representative redaction-band regime (unlike the mostly-identical
	// solid cases above, where the equal-pixel fast path dominates).
	g := makeGradient(w, h)
	gShift := makeGradient(w, h)
	for i := range gShift.Pix {
		if gShift.Pix[i] > 10 {
			gShift.Pix[i] -= 10
		}
	}

	b.Run("identical", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bSame)
		}
	})

	b.Run("10pct_different", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bDiff)
		}
	})

	b.Run("gradient", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(g, gShift)
		}
	})
}

// BenchmarkPixelmatchFast_Distance benchmarks PixelmatchFast.Compare on two
// 200×40 images — the same shapes as BenchmarkPixelmatch_Distance so results
// are directly comparable with benchstat.
func BenchmarkPixelmatchFast_Distance(b *testing.B) {
	const w, h = 200, 40
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	a := makeSolidRGBA(w, h, white)
	bSame := makeSolidRGBA(w, h, white)
	bDiff := makePartialDiffRGBA(a)

	m := metric.NewPixelmatchFast(0.1)

	g := makeGradient(w, h)
	gShift := makeGradient(w, h)
	for i := range gShift.Pix {
		if gShift.Pix[i] > 10 {
			gShift.Pix[i] -= 10
		}
	}

	b.Run("identical", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bSame)
		}
	})

	b.Run("10pct_different", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bDiff)
		}
	})

	b.Run("gradient", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(g, gShift)
		}
	})
}

// BenchmarkPixelmatchFast_CompareBounded benchmarks the early-exit ceiling path
// on a fully-different image pair (black vs white, 200×40). Three sub-benchmarks
// cover representative ceiling fractions:
//
//   - ceiling_1pct: very tight ceiling — aborts after ~1% of pixels (first row).
//   - ceiling_25pct: typical threshold — aborts after ~25% of pixels.
//   - ceiling_100pct: ceiling > total diff — no early exit, full scan (regression guard).
//
// Compare against BenchmarkPixelmatchFast_Distance/gradient to see the speedup
// on the dense-diff path.
func BenchmarkPixelmatchFast_CompareBounded(b *testing.B) {
	const w, h = 200, 40
	black := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	a := makeSolidRGBA(w, h, black)
	bImg := makeSolidRGBA(w, h, white)

	m := metric.NewPixelmatchFast(0.02)
	bc, ok := any(m).(metric.BoundedComparer)
	if !ok {
		b.Fatal("PixelmatchFast does not implement BoundedComparer")
	}

	for _, tc := range []struct {
		name    string
		ceiling float64
	}{
		{"ceiling_1pct", 0.01},
		{"ceiling_25pct", 0.25},
		{"ceiling_100pct", 1.01}, // above 1.0 → no early exit
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkFloat = bc.CompareBounded(a, bImg, tc.ceiling)
			}
		})
	}
}

// BenchmarkSSIM_Distance benchmarks SSIM.Compare on two 200×40 images.
// Sub-benchmarks cover identical vs ~10%-different inputs.
func BenchmarkSSIM_Distance(b *testing.B) {
	const w, h = 200, 40
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	a := makeSolidRGBA(w, h, white)
	bSame := makeSolidRGBA(w, h, white)
	bDiff := makePartialDiffRGBA(a)

	m := metric.NewSSIM(0)

	b.Run("identical", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bSame)
		}
	})

	b.Run("10pct_different", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bDiff)
		}
	})
}

// BenchmarkRGB_Distance benchmarks RGB.Compare on two 200×40 images.
// Sub-benchmarks cover identical vs ~10%-different inputs.
func BenchmarkRGB_Distance(b *testing.B) {
	const w, h = 200, 40
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	a := makeSolidRGBA(w, h, white)
	bSame := makeSolidRGBA(w, h, white)
	bDiff := makePartialDiffRGBA(a)

	m := metric.NewRGB()

	b.Run("identical", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bSame)
		}
	})

	b.Run("10pct_different", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkFloat = m.Compare(a, bDiff)
		}
	})
}
