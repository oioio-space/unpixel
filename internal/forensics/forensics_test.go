package forensics

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// srgbMosaic builds a two-tone checkerboard then mosaics it with block-average
// (sRGB colorspace) to produce a deterministic test fixture.
func srgbMosaic(block int) *image.RGBA {
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

func TestFingerprint_srgbMosaic(t *testing.T) {
	op := Fingerprint(srgbMosaic(8), Hint{Block: 8})
	if op.Kind != KindMosaic {
		t.Errorf("Kind = %v, want KindMosaic", op.Kind)
	}
	if op.Block != 8 {
		t.Errorf("Block = %d, want 8", op.Block)
	}
	if op.Gamma != GammaSRGB {
		t.Errorf("Gamma = %v, want GammaSRGB", op.Gamma)
	}
}

func TestOperatorBuild_thresholdGate(t *testing.T) {
	op := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.9, Gamma: 0.9}}
	if px, ok := op.Build(0.5); !ok || px == nil {
		t.Errorf("Build(0.5) ok=%v px=%v, want ok=true non-nil", ok, px)
	}
	low := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.2, Gamma: 0.2}}
	if _, ok := low.Build(0.5); ok {
		t.Errorf("Build(0.5) on low-confidence op = ok, want ok=false (fallback)")
	}
	// Conf.Kind below threshold even though Conf.Gamma is above — must gate.
	lowKind := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.2, Gamma: 0.9}}
	if _, ok := lowKind.Build(0.5); ok {
		t.Errorf("Build(0.5) with low Conf.Kind = ok, want ok=false (§5 per-attribute gate)")
	}
}

var sinkOp Operator

func BenchmarkFingerprint(b *testing.B) {
	img := srgbMosaic(8)
	b.ReportAllocs()
	for b.Loop() {
		sinkOp = Fingerprint(img, Hint{Block: 8})
	}
}
