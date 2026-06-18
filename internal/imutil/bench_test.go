package imutil_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// sinkRGBA defeats dead-code elimination for *image.RGBA benchmark results.
var sinkRGBA *image.RGBA

// makeRGBA builds a w×h RGBA image filled with a mid-grey solid colour.
func makeRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := color.RGBA{R: 128, G: 128, B: 128, A: 255}
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// BenchmarkFillWhite benchmarks the memset-style FillWhite on a 200×40 image.
func BenchmarkFillWhite(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 40))
	b.ReportAllocs()
	b.SetBytes(int64(len(img.Pix)))
	for b.Loop() {
		imutil.FillWhite(img)
	}
	sinkRGBA = img
}

// BenchmarkCrop benchmarks Crop on a 200×40 source image requesting a
// 160×32 sub-rectangle, which exercises the row-copy hot path.
func BenchmarkCrop(b *testing.B) {
	src := makeRGBA(200, 40)
	b.ReportAllocs()
	for b.Loop() {
		sinkRGBA = imutil.Crop(src, 10, 4, 160, 32)
	}
}

// BenchmarkCompose benchmarks blitting a 160×32 src onto a 200×40 dst at
// offset (10, 4), which delegates to the stdlib draw fast-path for RGBA images.
func BenchmarkCompose(b *testing.B) {
	dst := makeRGBA(200, 40)
	src := makeRGBA(160, 32)
	b.ReportAllocs()
	for b.Loop() {
		imutil.Compose(dst, src, 10, 4)
	}
	sinkRGBA = dst
}
