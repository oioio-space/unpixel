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
