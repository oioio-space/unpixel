package deblur

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

// sink defeats dead-code elimination for benchmark results.
var sink any

// makeBenchLum creates a w×h random float64 luminance plane seeded
// deterministically for reproducible benchmarks.
func makeBenchLum(w, h int) []float64 {
	rng := rand.New(rand.NewPCG(42, 0))
	lum := make([]float64, w*h)
	for i := range lum {
		lum[i] = rng.Float64() * 255
	}
	return lum
}

// makeBenchRGBA creates a w×h *image.RGBA filled with random grey pixels,
// seeded deterministically.
func makeBenchRGBA(w, h int) *image.RGBA {
	rng := rand.New(rand.NewPCG(7, 0))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		v := uint8(rng.Uint32() & 0xFF)
		img.Pix[i] = v
		img.Pix[i+1] = v
		img.Pix[i+2] = v
		img.Pix[i+3] = 255
	}
	return img
}

// makeVignette returns a w×h RGBA image with a radial vignette and a faint
// text-like pattern — similar to the kind of real image Normalize is designed
// for.
func makeVignette(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cx, cy := float64(w)/2, float64(h)/2
	maxR := cx
	if cy > maxR {
		maxR = cy
	}
	for y := range h {
		for x := range w {
			dx, dy := float64(x)-cx, float64(y)-cy
			d := dx*dx + dy*dy
			fade := 1 - d/(maxR*maxR)
			if fade < 0 {
				fade = 0
			}
			// Simulate text strokes: thin dark horizontal bands.
			base := uint8(fade * 200)
			if y%20 < 3 {
				base = uint8(float64(base) * 0.2)
			}
			img.SetRGBA(x, y, color.RGBA{R: base, G: base, B: base, A: 255})
		}
	}
	return img
}

// BenchmarkErode measures Erode on a 256×256 luminance plane (hot-path rule).
func BenchmarkErode(b *testing.B) {
	lum := makeBenchLum(256, 256)
	b.ReportAllocs()
	b.SetBytes(int64(256 * 256 * 8))
	b.ResetTimer()
	var s []float64
	for b.Loop() {
		s = Erode(lum, 256, 256, 8)
	}
	sink = s
}

// BenchmarkDilate measures Dilate on a 256×256 luminance plane.
func BenchmarkDilate(b *testing.B) {
	lum := makeBenchLum(256, 256)
	b.ReportAllocs()
	b.SetBytes(int64(256 * 256 * 8))
	b.ResetTimer()
	var s []float64
	for b.Loop() {
		s = Dilate(lum, 256, 256, 8)
	}
	sink = s
}

// BenchmarkOpen measures Open (Erode+Dilate) on a 256×256 luminance plane.
func BenchmarkOpen(b *testing.B) {
	lum := makeBenchLum(256, 256)
	b.ReportAllocs()
	b.SetBytes(int64(256 * 256 * 8))
	b.ResetTimer()
	var s []float64
	for b.Loop() {
		s = Open(lum, 256, 256, 8)
	}
	sink = s
}

// BenchmarkNormalize_defaultOpts measures the full Normalize pipeline at
// default options on a 256×256 vignette image — the representative hot-path
// call from RecoverBlurred.
func BenchmarkNormalize_defaultOpts(b *testing.B) {
	src := makeVignette(256, 256)
	opts := DefaultOptions()
	b.ReportAllocs()
	b.SetBytes(int64(256 * 256 * 4))
	b.ResetTimer()
	var img *image.RGBA
	for b.Loop() {
		img = Normalize(src, opts)
	}
	sink = img
}

// BenchmarkNormalize_bgNone measures Normalize with background removal
// disabled so the morphology cost can be isolated by subtraction.
func BenchmarkNormalize_bgNone(b *testing.B) {
	src := makeBenchRGBA(256, 256)
	opts := Options{Bg: BgNone, Invert: InvertOff, Stretch: false}
	b.ReportAllocs()
	b.SetBytes(int64(256 * 256 * 4))
	b.ResetTimer()
	var img *image.RGBA
	for b.Loop() {
		img = Normalize(src, opts)
	}
	sink = img
}
