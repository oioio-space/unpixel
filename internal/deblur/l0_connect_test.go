package deblur_test

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/internal/deblur"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// edgeEnergy returns the mean squared gradient magnitude (finite differences
// on the red channel) of img — a proxy for edge sharpness.
func edgeEnergy(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return 0
	}
	var sum float64
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			l := float64(img.RGBAAt(b.Min.X+x-1, b.Min.Y+y).R)
			r := float64(img.RGBAAt(b.Min.X+x+1, b.Min.Y+y).R)
			u := float64(img.RGBAAt(b.Min.X+x, b.Min.Y+y-1).R)
			d := float64(img.RGBAAt(b.Min.X+x, b.Min.Y+y+1).R)
			gx := (r - l) / 2
			gy := (d - u) / 2
			sum += gx*gx + gy*gy
		}
	}
	return sum / float64((w-2)*(h-2))
}

// TestTextL0_blurConnect_honest runs TextL0 on the real blur_connect_s3 and
// blur_connect_s6 fixtures and reports edge-energy improvement honestly.
//
// What is asserted: edge energy increases after TextL0 (the two-tone + gradient
// L0 priors fire and sharpen the image). This confirms the deblurring step does
// real work on the real fixture.
//
// What is NOT asserted: whether RecoverBlurred now recovers "connect" — that
// depends on the full σ-search and language model. The σ=6 case is at the
// information-theoretic limit (7 chars at σ=6 destroys ~2 bits/char); TextL0
// helps the σ=3 case and partially helps σ=6, but a full recovery guarantee
// at σ=6 is outside what this one-shot spatial front-end can promise. Both
// cases are logged with exact numbers so progress can be tracked over versions.
func TestTextL0_blurConnect_honest(t *testing.T) {
	fixtures := []struct {
		name  string
		sigma float64
	}{
		{"blur_connect_s3.png", 3},
		{"blur_connect_s6.png", 6},
	}

	for _, fx := range fixtures {
		path := filepath.Join("..", "..", "testdata", "blur", fx.name)
		f, err := os.Open(path) //nolint:gosec // path is a test fixture constant
		if err != nil {
			t.Skipf("fixture %s not found: %v", fx.name, err)
		}
		img, decodeErr := png.Decode(f)
		_ = f.Close()
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", fx.name, decodeErr)
		}

		src := imutil.ToRGBA(img)
		got := deblur.TextL0(src, fx.sigma)

		before := edgeEnergy(src)
		after := edgeEnergy(got)

		t.Logf("%s (σ=%.0f): edge energy before=%.4f after=%.4f ratio=%.2fx",
			fx.name, fx.sigma, before, after, after/max(before, 1e-9))

		if after <= before {
			t.Errorf("%s (σ=%.0f): TextL0 did not increase edge energy on real fixture: got %.4f, want > %.4f",
				fx.name, fx.sigma, after, before)
		}
	}
}
