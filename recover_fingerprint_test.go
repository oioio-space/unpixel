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
// linear-light mosaic causes applyAutoFingerprint to install a
// *pixelate.BlockAverage (the linear variant) when DetectColorspace is
// confident enough.
//
// The fixture tiles alternating block-sized (8×8) columns of 64 and 192.
// Each block therefore contains a mix of mid-tone pixels so the Jensen gap
// between linear and sRGB averaging is large. Pure 0/255 or sub-block-period
// patterns yield zero gap and cannot trigger the detector.
func TestRecover_autoFingerprintInstallsLinear(t *testing.T) {
	// Fill with alternating 8-wide columns of grey 64 and grey 192.
	// Each 8×8 block then contains 4 columns of 64 and 4 of 192 (or the reverse),
	// giving a mixed-luminance block that maximises the Jensen gap.
	const (
		w, h  = 64, 32
		block = 8
	)
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := byte(192)
			if (x/block)%2 == 0 {
				c = 64
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	img := pixelate.NewLinearBlockAverage(block).Pixelate(src, 0, 0)

	cfg := Config{BlockSize: block}
	WithAuto()(&cfg)
	applyAutoFingerprint(&cfg, img)

	if _, ok := cfg.Pixelator.(*pixelate.BlockAverage); !ok {
		t.Errorf("Pixelator = %T, want *pixelate.BlockAverage (linear variant)", cfg.Pixelator)
	}
}
