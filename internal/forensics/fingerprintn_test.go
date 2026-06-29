package forensics

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

func srgbMosaicN(block int) *image.RGBA {
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := byte(255)
			if (x/3+y/3)%2 == 0 {
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return pixelate.NewBlockAverage(block).Pixelate(src, 0, 0)
}

func TestFingerprintN_ranksAndDelegates(t *testing.T) {
	img := srgbMosaicN(8)
	ranked := FingerprintN(img, Hint{Block: 8})
	if len(ranked) == 0 {
		t.Fatalf("FingerprintN returned no operators")
	}
	// Top operator must equal the singular Fingerprint (delegation contract):
	// same structural fields AND same Conf (no inflation of observed confidence).
	got := ranked[0]
	want := Fingerprint(img, Hint{Block: 8})
	if got.Kind != want.Kind || got.Gamma != want.Gamma || got.Block != want.Block {
		t.Errorf("FingerprintN[0] = {%v,%v,%d}, Fingerprint = {%v,%v,%d}; want equal",
			got.Kind, got.Gamma, got.Block, want.Kind, want.Gamma, want.Block)
	}
	if got.Conf != want.Conf {
		t.Errorf("FingerprintN[0].Conf = %+v, Fingerprint.Conf = %+v; want identical (no inflation)",
			got.Conf, want.Conf)
	}
	// Confidence is monotonic non-increasing.
	for i := 1; i < len(ranked); i++ {
		if ranked[i].Conf.Kind > ranked[i-1].Conf.Kind {
			t.Errorf("ranking not sorted by Conf.Kind at %d: %.3f > %.3f", i, ranked[i].Conf.Kind, ranked[i-1].Conf.Kind)
		}
	}
}

var sinkRanked []Operator

func BenchmarkFingerprintN(b *testing.B) {
	img := srgbMosaicN(8)
	b.ReportAllocs()
	for b.Loop() {
		sinkRanked = FingerprintN(img, Hint{Block: 8})
	}
}
