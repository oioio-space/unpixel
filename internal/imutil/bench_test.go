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

// sinkInt defeats dead-code elimination for integer benchmark results.
var sinkInt int

// makeWhiteWithInkAt returns a w×h white RGBA image with a single non-white
// pixel at (inkX, inkY). This gives LeftEdge and Margins a realistic early-exit
// scenario: the ink column is well to the right of x=0 so the savings are visible.
func makeWhiteWithInkAt(w, h, inkX, inkY int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Fill white.
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	// Single ink pixel.
	img.SetRGBA(inkX, inkY, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	return img
}

// BenchmarkLeftEdge benchmarks LeftEdge on a 400×40 image whose only non-white
// pixel is at column 200, row 20. This exercises the early-break optimisation:
// once column 200 is found, scanning can stop scanning further right.
func BenchmarkLeftEdge(b *testing.B) {
	img := makeWhiteWithInkAt(400, 40, 200, 20)
	b.ReportAllocs()
	for b.Loop() {
		sinkInt = imutil.LeftEdge(img)
	}
}

// BenchmarkMargins benchmarks Margins on a 400×40 image whose only red pixel
// is at column 200, mid-row. This exercises the early-break path.
func BenchmarkMargins(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 40))
	// Fill white.
	for y := range 40 {
		for x := range 400 {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	// Red pixel at column 200, mid-row (20).
	img.SetRGBA(200, 20, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	b.ReportAllocs()
	for b.Loop() {
		sinkInt = imutil.Margins(img)
	}
}
