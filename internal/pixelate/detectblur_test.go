package pixelate

import (
	"image"
	"testing"
)

// mosaicFixture renders a crude 2-tone "glyph" then mosaics it, to exercise detection.
func mosaicFixture(tb testing.TB, block int) *image.RGBA {
	tb.Helper()
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := byte(255)
			if (x/3+y/3)%2 == 0 { // some structure
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return NewBlockAverage(block).Pixelate(src, 0, 0)
}

func TestDetectBlur_mosaicClassifiedAsMosaic(t *testing.T) {
	img := mosaicFixture(t, 8)
	got := DetectBlur(img, 8)
	if got.Kind != BlurKindMosaic {
		t.Errorf("DetectBlur(mosaic).Kind = %v, want BlurKindMosaic", got.Kind)
	}
	if got.Conf < 0.5 {
		t.Errorf("DetectBlur(mosaic).Conf = %.2f, want >= 0.5", got.Conf)
	}
}

func TestDetectBlur_gaussianClassifiedAndSigmaEstimated(t *testing.T) {
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := byte(255)
			if x >= w/2 { // single hard edge
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	const sigma = 3.0
	img := NewGaussianBlur(sigma).Pixelate(src, 0, 0)

	got := DetectBlur(img, 0)
	if got.Kind != BlurKindGaussian {
		t.Fatalf("DetectBlur(blur).Kind = %v, want BlurKindGaussian", got.Kind)
	}
	if got.Sigma < sigma*0.6 || got.Sigma > sigma*1.4 {
		t.Errorf("DetectBlur(blur).Sigma = %.2f, want within +-40%% of %.1f", got.Sigma, sigma)
	}
}

var sinkBlur BlurInfo

func BenchmarkDetectBlur(b *testing.B) {
	img := mosaicFixture(b, 8)
	b.ReportAllocs()
	for b.Loop() {
		sinkBlur = DetectBlur(img, 8)
	}
}
