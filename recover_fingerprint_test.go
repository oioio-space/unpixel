package unpixel

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// TestRecover_autoBlurSafeFallback verifies that a near-uniform image (zero
// variance) yields DetectBlur/DetectColorspace confidence well below 0.5, so
// applyAutoFingerprint must NOT install any pixelator — leaving cfg.Pixelator
// nil and preserving the byte-identical default path.
func TestRecover_autoBlurSafeFallback(t *testing.T) {
	// A blank image mosaiced with block=8 is uniform — no structure for the
	// detectors to work on, so confidence will be near zero.
	blank := image.NewRGBA(image.Rect(0, 0, 32, 16))
	img := pixelate.NewBlockAverage(8).Pixelate(blank, 0, 0)

	cfg := Config{BlockSize: 8}
	WithAutoBlur()(&cfg)
	WithAutoColorspace()(&cfg)
	applyAutoFingerprint(&cfg, imutil.ToRGBA(img))

	if cfg.Pixelator != nil {
		t.Errorf("Pixelator = %T, want nil (safe fallback on low-confidence input)", cfg.Pixelator)
	}
}

// TestRecover_autoFingerprintInstallsLinear verifies that a high-variance
// linear-light mosaic causes applyAutoFingerprint to install the LINEAR
// *pixelate.BlockAverage variant (not the sRGB one) when DetectColorspace is
// confident enough.
//
// The fixture uses varying ink-fill fractions (black pixels on a white
// background) within each 8×8 block — the same approach as the detect_test
// fixtures — so that linear vs sRGB block averaging produce detectably different
// block means. DetectColorspace identifies the mode from the ratio of per-block
// delta_from_linear vs delta_to_linear, which is ≈1.44 for linear averaging
// and ≈1.04 for sRGB averaging (threshold 1.4).
//
// The assertion is BEHAVIORAL: after applyAutoFingerprint installs the
// pixelator, run BOTH installed and reference pixelators on a fresh half-0 /
// half-255 probe block. Linear and sRGB averaging of {0, 255} produce
// measurably different grey levels (~188 vs 127), so the output pin-points
// which variant was installed.
func TestRecover_autoFingerprintInstallsLinear(t *testing.T) {
	const (
		w, h  = 64, 64
		block = 8
	)
	// Build a "varying fill" source: each block column has a different ink fill
	// fraction (1/(n+1) … n/(n+1)) with black ink on white, identical to the
	// makeVaryingFillSrc fixture used by the internal detect_test suite. This
	// guarantees blocks span the full luminance range so DetectColorspace is
	// confident and the ratio discriminator (≥1.4 → linear) fires.
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	nBlockCols := w / block
	for y := range h {
		for x := range w {
			bx := x / block
			fill := float64(bx+1) / float64(nBlockCols+1) // ink fraction: 1/9 … 8/9
			inInk := float64(x%block)/float64(block) < fill
			i := src.PixOffset(x, y)
			if inInk {
				src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 0, 0, 0, 255
			} else {
				src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 255, 255, 255, 255
			}
		}
	}
	fixture := pixelate.NewLinearBlockAverage(block).Pixelate(src, 0, 0)

	cfg := Config{BlockSize: block}
	WithAuto()(&cfg)
	applyAutoFingerprint(&cfg, fixture)

	// Type-check: must be *pixelate.BlockAverage.
	if _, ok := cfg.Pixelator.(*pixelate.BlockAverage); !ok {
		t.Errorf("Pixelator = %T, want *pixelate.BlockAverage", cfg.Pixelator)
		return
	}

	// Behavioral check: construct a single 8×8 probe block whose left half is
	// pure black (0) and right half is pure white (255). Linear and sRGB
	// averaging of {0, 255} give different grey levels:
	//   linear: linearToSrgb8(mean(0, 1))  ≈ 188
	//   sRGB:   avg8(0+255, 2)             = 127
	// So the installed pixelator's output reveals which variant was selected.
	probe := image.NewRGBA(image.Rect(0, 0, block, block))
	for y := range block {
		for x := range block {
			i := probe.PixOffset(x, y)
			c := byte(0)
			if x >= block/2 {
				c = 255
			}
			probe.Pix[i], probe.Pix[i+1], probe.Pix[i+2], probe.Pix[i+3] = c, c, c, 255
		}
	}

	gotPix := cfg.Pixelator.(*pixelate.BlockAverage).Pixelate(probe, 0, 0)
	linearPix := pixelate.NewLinearBlockAverage(block).Pixelate(probe, 0, 0)
	srgbPix := pixelate.NewBlockAverage(block).Pixelate(probe, 0, 0)

	// Sample the centre of the single block to read the averaged colour.
	cx, cy := block/2-1, block/2-1
	got := gotPix.RGBAAt(cx, cy).R
	wantLinear := linearPix.RGBAAt(cx, cy).R
	wantSRGB := srgbPix.RGBAAt(cx, cy).R

	if wantLinear == wantSRGB {
		// Probe cannot distinguish the two variants — update probe values.
		t.Errorf("probe indistinguishable: linear R=%d == sRGB R=%d (update probe)", wantLinear, wantSRGB)
	}
	if got != wantLinear {
		t.Errorf("installed pixelator output R=%d, want linear R=%d (sRGB would be R=%d): linear variant not installed", got, wantLinear, wantSRGB)
	}
}
