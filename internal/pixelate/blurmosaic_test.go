package pixelate_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// TestBlurMosaic_accessors verifies constructor accessors reflect their inputs.
func TestBlurMosaic_accessors(t *testing.T) {
	bm := pixelate.NewBlurMosaic(4, 8, false)
	if got := bm.Sigma(); got != 4 {
		t.Errorf("Sigma() = %v, want 4", got)
	}
	if got := bm.BlockSize(); got != 8 {
		t.Errorf("BlockSize() = %v, want 8", got)
	}
}

// TestBlurMosaic_sigmaClamped verifies very small or zero sigma is clamped.
func TestBlurMosaic_sigmaClamped(t *testing.T) {
	bm := pixelate.NewBlurMosaic(0, 4, false)
	if bm.Sigma() <= 0 {
		t.Errorf("Sigma() = %v for zero input, want clamped positive", bm.Sigma())
	}
}

// TestBlurMosaic_blockClamped verifies block ≤ 0 is clamped to 1.
func TestBlurMosaic_blockClamped(t *testing.T) {
	bm := pixelate.NewBlurMosaic(2, 0, false)
	if got := bm.BlockSize(); got != 1 {
		t.Errorf("BlockSize() = %v for block=0 input, want 1", got)
	}
}

// TestBlurMosaic_outputIsBlockSized verifies the output dimensions are a
// multiple of the block size (same contract as BlockAverage).
func TestBlurMosaic_outputIsBlockSized(t *testing.T) {
	// 20 px wide, block=8 → padded to 24 (next multiple of 8).
	src := image.NewRGBA(image.Rect(0, 0, 20, 12))
	for i := range src.Pix {
		src.Pix[i] = 0x80
	}
	bm := pixelate.NewBlurMosaic(2, 8, false)
	out := bm.Pixelate(src, 0, 0)
	if out.Bounds().Dx()%8 != 0 {
		t.Errorf("output width %d not a multiple of 8", out.Bounds().Dx())
	}
	if out.Bounds().Dy() != 12 {
		t.Errorf("output height %d, want 12", out.Bounds().Dy())
	}
}

// TestBlurMosaic_uniformity verifies that the output of BlurMosaic is
// block-uniform: within each b×b tile every pixel must have the same colour
// (the defining property of block-average after blur). We check a 3×3 grid
// of blocks in the interior (avoiding edge-padding differences).
func TestBlurMosaic_uniformity(t *testing.T) {
	const (
		w, h  = 64, 32
		block = 8
		sigma = 3.0
	)
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	// Gradient pattern: non-trivial but deterministic.
	for y := range h {
		for x := range w {
			v := uint8((x*3 + y*7) % 200)
			src.SetRGBA(x, y, color.RGBA{R: v, G: 255 - v, B: uint8(x % 256), A: 255})
		}
	}

	bm := pixelate.NewBlurMosaic(sigma, block, false)
	out := bm.Pixelate(src, 0, 0)

	// Sample 3 complete interior blocks (skip first block, which may be affected
	// by edge-clamped blur, and last block, which may be padded).
	for bx := 1; bx <= 3; bx++ {
		x0 := bx * block
		y0 := 0
		want := out.RGBAAt(x0, y0)
		for dy := range block {
			for dx := range block {
				got := out.RGBAAt(x0+dx, y0+dy)
				if got != want {
					t.Errorf("block (%d,%d) not uniform: pixel (%d,%d)=%v, want %v",
						bx, 0, x0+dx, y0+dy, got, want)
				}
			}
		}
	}
}

// TestBlurMosaic_linear verifies the linear-mode constructor stores a different
// block-size and produces output (does not panic).
func TestBlurMosaic_linear(t *testing.T) {
	src := makePixelateSrc()
	bm := pixelate.NewBlurMosaic(3, 8, true)
	if got := bm.BlockSize(); got != 8 {
		t.Errorf("BlockSize() = %v, want 8", got)
	}
	out := bm.Pixelate(src, 0, 0)
	if out == nil {
		t.Fatal("Pixelate returned nil")
	}
}

// TestBlurMosaic_tinyImage verifies BlurMosaic does not panic on 1×1 and 2×2.
func TestBlurMosaic_tinyImage(t *testing.T) {
	for _, sz := range []int{1, 2} {
		src := image.NewRGBA(image.Rect(0, 0, sz, sz))
		src.SetRGBA(0, 0, color.RGBA{R: 128, G: 64, B: 32, A: 255})
		bm := pixelate.NewBlurMosaic(1, 4, false)
		out := bm.Pixelate(src, 0, 0)
		if out == nil {
			t.Errorf("sz=%d: Pixelate returned nil", sz)
		}
	}
}

// TestBlurMosaic_sigmaZeroApprox verifies σ≈0 (clamped to 0.1) produces an
// output that is close to a plain BlockAverage — the blur step is nearly a
// no-op, so both outputs should have the same dominant colour in the first block.
func TestBlurMosaic_sigmaZeroApprox(t *testing.T) {
	src := makePixelateSrc()
	bmSmall := pixelate.NewBlurMosaic(0.0001, 8, false)
	plain := pixelate.NewBlockAverage(8)

	outBM := bmSmall.Pixelate(src, 0, 0)
	outPlain := plain.Pixelate(src, 0, 0)

	// The first block's colours must be very similar (within 5 per channel)
	// because σ=0.1 is nearly identity.
	cx, cy := 4, 4
	bm := outBM.RGBAAt(cx, cy)
	pl := outPlain.RGBAAt(cx, cy)
	diff := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	const tol = 5
	if diff(bm.R, pl.R) > tol || diff(bm.G, pl.G) > tol || diff(bm.B, pl.B) > tol {
		t.Errorf("σ≈0 BlurMosaic %v diverges from BlockAverage %v by >%d", bm, pl, tol)
	}
}

// BenchmarkBlurMosaic_Pixelate benchmarks the composite blur+mosaic operator on
// a 264×40 image at sigma=6 and block=6. This is the per-candidate cost of the
// remosaic recovery path and must be tracked alongside the plain operators.
func BenchmarkBlurMosaic_Pixelate(b *testing.B) {
	src := makePixelateSrc()
	bm := pixelate.NewBlurMosaic(6, 6, false)
	b.ReportAllocs()
	for b.Loop() {
		sink = bm.Pixelate(src, 0, 0)
	}
}

// BenchmarkBlurMosaic_LinearPixelate benchmarks the linear-light variant.
func BenchmarkBlurMosaic_LinearPixelate(b *testing.B) {
	src := makePixelateSrc()
	bm := pixelate.NewBlurMosaic(6, 6, true)
	b.ReportAllocs()
	for b.Loop() {
		sink = bm.Pixelate(src, 0, 0)
	}
}
