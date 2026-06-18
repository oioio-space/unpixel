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
