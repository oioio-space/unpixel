package pixelate_test

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// makeVaryingFillSrc builds a w×h RGBA image where each column of blocks has a
// different ink fill fraction (1/(n+1) to n/(n+1) across n block columns). This
// produces a full range of mixed blocks — the ideal case for colorspace detection
// because the block values span [0, 255] under both sRGB and linear averaging.
func makeVaryingFillSrc(w, h, blockSize int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	nBlocks := w / blockSize
	for y := range h {
		for x := range w {
			bx := x / blockSize
			fill := float64(bx+1) / float64(nBlocks+1) // ink fraction per block column
			inInk := float64(x%blockSize)/float64(blockSize) < fill
			if inInk {
				img.SetRGBA(x, y, color.RGBA{A: 255}) // black ink
			} else {
				img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255}) // white
			}
		}
	}
	return img
}

// makeTextOnWhiteSrc renders a rudimentary "text on white" pattern: alternating
// block rows of ink stripes separated by white space, with every third block
// column pure-white (simulating inter-word gaps). This produces blocks with two
// distinct fill fractions: ~0.5 (mixed ink/white) and 1.0 (all white).
func makeTextOnWhiteSrc(w, h, blockSize int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			blockRow := y / blockSize
			blockCol := x / blockSize
			inInkRow := blockRow%2 == 0
			inInkHalf := y%blockSize < blockSize/2
			inInkCol := blockCol%3 != 0
			if inInkRow && inInkHalf && inInkCol {
				img.SetRGBA(x, y, color.RGBA{R: 20, G: 20, B: 20, A: 255}) // near-black ink
			} else {
				img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255}) // white
			}
		}
	}
	return img
}

// makeNearWhiteSrc builds a w×h image that is almost entirely white with tiny
// pixel-level variation — the edge case where no discriminating signal exists.
func makeNearWhiteSrc(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(248 + (x+y)%8)
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

// pixelateSRGB produces an sRGB-averaged mosaic of src.
func pixelateSRGB(src *image.RGBA, blockSize int) *image.RGBA {
	return pixelate.NewBlockAverage(blockSize).Pixelate(cloneRGBA(src), 0, 0)
}

// pixelateLinear produces a linear-light-averaged mosaic of src.
func pixelateLinear(src *image.RGBA, blockSize int) *image.RGBA {
	return pixelate.NewLinearBlockAverage(blockSize).Pixelate(cloneRGBA(src), 0, 0)
}

// TestDetectColorspace_varyFill_sRGB verifies detection on the full-range fill
// fixture (blocks span [0, 255]) produced by sRGB averaging — the ideal case
// for the ratio discriminator (R ≈ 1.04, well below the 1.4 threshold).
func TestDetectColorspace_varyFill_sRGB(t *testing.T) {
	const bs = 8
	src := makeVaryingFillSrc(64, 64, bs)
	mosaic := pixelateSRGB(src, bs)

	linear, conf := pixelate.DetectColorspace(mosaic, bs)
	if linear {
		t.Errorf("DetectColorspace(varyFill sRGB) = linear=true, want false (confidence=%.3f)", conf)
	}
	if conf < 0.9 {
		t.Errorf("DetectColorspace(varyFill sRGB) confidence = %.3f, want ≥ 0.9", conf)
	}
}

// TestDetectColorspace_varyFill_linear verifies detection on the full-range fill
// fixture produced by linear averaging — the ratio R ≈ 1.44 exceeds the 1.4
// threshold with near-maximum confidence.
func TestDetectColorspace_varyFill_linear(t *testing.T) {
	const bs = 8
	src := makeVaryingFillSrc(64, 64, bs)
	mosaic := pixelateLinear(src, bs)

	linear, conf := pixelate.DetectColorspace(mosaic, bs)
	if !linear {
		t.Errorf("DetectColorspace(varyFill linear) = linear=false, want true (confidence=%.3f)", conf)
	}
	if conf < 0.9 {
		t.Errorf("DetectColorspace(varyFill linear) confidence = %.3f, want ≥ 0.9", conf)
	}
}

// TestDetectColorspace_textOnWhite_sRGB verifies detection on the sparse-ink
// text pattern produced by sRGB averaging. The ratio R ≈ 1.28 is below the 1.4
// threshold; confidence ≈ 0.94 from the Jensen gap of ≈ 8.9.
func TestDetectColorspace_textOnWhite_sRGB(t *testing.T) {
	const bs = 8
	src := makeTextOnWhiteSrc(64, 64, bs)
	mosaic := pixelateSRGB(src, bs)

	linear, conf := pixelate.DetectColorspace(mosaic, bs)
	if linear {
		t.Errorf("DetectColorspace(textOnWhite sRGB) = linear=true, want false (confidence=%.3f)", conf)
	}
	if conf < 0.5 {
		t.Errorf("DetectColorspace(textOnWhite sRGB) confidence = %.3f, want ≥ 0.5", conf)
	}
}

// TestDetectColorspace_textOnWhite_linear verifies detection on the sparse-ink
// text pattern produced by linear averaging. The ratio R ≈ 1.71 exceeds the
// 1.4 threshold; confidence ≈ 0.53 from the smaller Jensen gap of ≈ 2.9.
func TestDetectColorspace_textOnWhite_linear(t *testing.T) {
	const bs = 8
	src := makeTextOnWhiteSrc(64, 64, bs)
	mosaic := pixelateLinear(src, bs)

	linear, conf := pixelate.DetectColorspace(mosaic, bs)
	if !linear {
		t.Errorf("DetectColorspace(textOnWhite linear) = linear=false, want true (confidence=%.3f)", conf)
	}
	if conf < 0.3 {
		t.Errorf("DetectColorspace(textOnWhite linear) confidence = %.3f, want ≥ 0.3", conf)
	}
}

// TestDetectColorspace_nearWhite verifies that near-white content returns
// confidence ≈ 0 regardless of mode — the Jensen gap is 0 so there is no
// discriminating signal. Callers must treat confidence < 0.5 as unreliable.
func TestDetectColorspace_nearWhite(t *testing.T) {
	const bs = 8
	src := makeNearWhiteSrc(64, 64)

	for _, lin := range []bool{false, true} {
		var mosaic *image.RGBA
		if lin {
			mosaic = pixelateLinear(src, bs)
		} else {
			mosaic = pixelateSRGB(src, bs)
		}
		_, conf := pixelate.DetectColorspace(mosaic, bs)
		if conf > 0.1 {
			t.Errorf("DetectColorspace(nearWhite, linear=%v) confidence = %.3f, want ≤ 0.1 (no signal)", lin, conf)
		}
	}
}

// TestDetectColorspace_uniform verifies that a fully uniform image returns
// confidence = 0 — zero Jensen gap means the algorithm has nothing to measure.
func TestDetectColorspace_uniform(t *testing.T) {
	const bs = 8
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = 128
	}
	_, conf := pixelate.DetectColorspace(img, bs)
	if conf > 0.01 {
		t.Errorf("DetectColorspace(uniform) confidence = %.3f, want ≈0", conf)
	}
}

// TestDetectColorspace_confidence_range verifies the invariant that confidence
// is always in [0, 1] for a variety of source images and both modes.
func TestDetectColorspace_confidence_range(t *testing.T) {
	const bs = 8
	for _, src := range []*image.RGBA{
		makeVaryingFillSrc(64, 64, bs),
		makeTextOnWhiteSrc(64, 64, bs),
		makeNearWhiteSrc(64, 64),
		makeGradient(64, 64),
	} {
		for _, lin := range []bool{false, true} {
			var mosaic *image.RGBA
			if lin {
				mosaic = pixelateLinear(src, bs)
			} else {
				mosaic = pixelateSRGB(src, bs)
			}
			_, conf := pixelate.DetectColorspace(mosaic, bs)
			if conf < 0 || conf > 1 || math.IsNaN(conf) {
				t.Errorf("confidence = %v out of [0, 1]", conf)
			}
		}
	}
}

// TestDetectColorspace_smallImage verifies DetectColorspace handles tiny mosaics
// (2×2 blocks at 16×16 total) without panicking or returning out-of-range values.
func TestDetectColorspace_smallImage(t *testing.T) {
	const bs = 8
	src := makeTextOnWhiteSrc(16, 16, bs)
	for _, lin := range []bool{false, true} {
		var mosaic *image.RGBA
		if lin {
			mosaic = pixelateLinear(src, bs)
		} else {
			mosaic = pixelateSRGB(src, bs)
		}
		_, conf := pixelate.DetectColorspace(mosaic, bs)
		if conf < 0 || conf > 1 || math.IsNaN(conf) {
			t.Errorf("small image: confidence out of [0, 1]: %v", conf)
		}
	}
}

// TestDetectColorspace_emptyImage verifies that a zero-size image returns
// linear=false, confidence=0 without panicking.
func TestDetectColorspace_emptyImage(t *testing.T) {
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	linear, conf := pixelate.DetectColorspace(empty, 8)
	if linear || conf != 0 {
		t.Errorf("empty image: got linear=%v conf=%v, want false/0", linear, conf)
	}
}

// detectSink defeats dead-code elimination in BenchmarkDetectColorspace.
var detectSink struct {
	linear bool
	conf   float64
}

// BenchmarkDetectColorspace measures the per-call cost of DetectColorspace on
// a 264×40 mosaic at block size 8 — the typical size for a redacted text line.
// Both sRGB and linear input mosaics are benchmarked as sub-cases.
func BenchmarkDetectColorspace(b *testing.B) {
	const bs = 8
	src := makeVaryingFillSrc(264, 40, bs)
	cases := []struct {
		name  string
		input *image.RGBA
	}{
		{"sRGB", pixelateSRGB(src, bs)},
		{"linear", pixelateLinear(src, bs)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				detectSink.linear, detectSink.conf = pixelate.DetectColorspace(c.input, bs)
			}
		})
	}
}
