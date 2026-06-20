package pixelate_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// sink defeats dead-code elimination for benchmark results.
var sink *image.RGBA

// makePixelateSrc builds a 264×40 RGBA image filled with a mid-grey gradient,
// approximating a realistic secret.png redaction region.
func makePixelateSrc() *image.RGBA {
	const w, h = 264, 40
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8((x + y) % 256)
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

// BenchmarkBlockAverage_Pixelate benchmarks the full pixelation pass on a
// 264×40 RGBA image at a non-zero origin (3, 3), which exercises per-block
// mean computation, the row-copy path, and FillWhite padding.
func BenchmarkBlockAverage_Pixelate(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewBlockAverage(8)
	b.ReportAllocs()
	for b.Loop() {
		sink = p.Pixelate(src, 3, 3)
	}
}

// BenchmarkLinearBlockAverage_Pixelate benchmarks the linear-light variant (the
// opt-in GEGL-matching mosaic) so its per-block sRGB↔linear conversion cost is
// tracked alongside the default sRGB path.
func BenchmarkLinearBlockAverage_Pixelate(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewLinearBlockAverage(8)
	b.ReportAllocs()
	for b.Loop() {
		sink = p.Pixelate(src, 3, 3)
	}
}

// BenchmarkGaussianBlur_Pixelate benchmarks the separable Gaussian blur (the
// blur-redaction operator) on the same 264×40 image at sigma=6 — a realistic
// redaction blur radius.
func BenchmarkGaussianBlur_Pixelate(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewGaussianBlur(6)
	b.ReportAllocs()
	for b.Loop() {
		sink = p.Pixelate(src, 0, 0)
	}
}

// BenchmarkFastBlur_Pixelate benchmarks the 3-box-pass Gaussian approximation at
// sigma=6 — O(1) per pixel, so much cheaper than the exact GaussianBlur.
func BenchmarkFastBlur_Pixelate(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewFastBlur(6)
	b.ReportAllocs()
	for b.Loop() {
		sink = p.Pixelate(src, 0, 0)
	}
}

// BenchmarkRichardsonLucy_5iter benchmarks RL deconvolution at sigma=3 with 5
// iterations on a 264×40 image — the light-iteration regime (fast preview).
func BenchmarkRichardsonLucy_5iter(b *testing.B) {
	src := makePixelateSrc()
	b.ReportAllocs()
	for b.Loop() {
		sink = pixelate.RichardsonLucy(src, 3, 5)
	}
}

// BenchmarkRichardsonLucy_20iter benchmarks RL deconvolution at sigma=3 with
// 20 iterations on a 264×40 image — the higher-quality convergence regime.
func BenchmarkRichardsonLucy_20iter(b *testing.B) {
	src := makePixelateSrc()
	b.ReportAllocs()
	for b.Loop() {
		sink = pixelate.RichardsonLucy(src, 3, 20)
	}
}
