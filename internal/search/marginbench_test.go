package search

import (
	"image"
	"testing"
)

var sinkMargin int

// BenchmarkMarginColumn exercises the middle-row margin scan over a near-full
// row (the differing column is near the end, so most of the row is scanned).
func BenchmarkMarginColumn(b *testing.B) {
	const w, h = 200, 40
	a := image.NewRGBA(image.Rect(0, 0, w, h))
	c := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range a.Pix {
		a.Pix[i] = 255
		c.Pix[i] = 255
	}
	c.Pix[c.PixOffset(w-8, h/2)] = 0 // differ late on the middle row
	b.ReportAllocs()
	for b.Loop() {
		sinkMargin = marginColumn(a, c)
	}
}
