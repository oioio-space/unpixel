package defaults_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// cleanAndMosaic renders text and returns (clean image padded to block-width,
// its mosaic). The clean image is pre-padded so its width is already a multiple
// of block — exactly matching the width that BlockAverage.Pixelate produces —
// so verifyImageCore's re-pixelation of the clean image produces distance ≈ 0
// against the mosaic (pipeline-faithful fixture).
func cleanAndMosaic(t *testing.T, text string, block int) (*image.RGBA, image.Image) {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	clean, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}

	// Pre-pad to block-multiple width so clean.Bounds() == mosaic.Bounds().
	// BlockAverage.Pixelate pads the source to the same multiple before averaging,
	// so this makes the clean image and the mosaic share the same rectangle and the
	// re-pixelation distance is exactly 0 (no CatmullRom resize noise).
	w := clean.Bounds().Dx()
	if rem := block - (w % block); rem < block {
		clean = imutil.PadWhite(clean, w+rem, clean.Bounds().Dy())
	}

	mosaic := pixelate.NewBlockAverage(block).Pixelate(clean, 0, 0)
	return clean, mosaic
}

func TestVerifyImage_acceptsTrueRejectsHallucination(t *testing.T) {
	const block = 6
	clean, mosaic := cleanAndMosaic(t, "the", block)

	// The true clean image re-pixelates back to the observed mosaic → Match.
	good, err := unpixel.VerifyImage(t.Context(), mosaic, clean, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(true): %v", err)
	}
	if !good.Match {
		t.Errorf("true restoration not a Match (distance %.4f)", good.Distance)
	}

	// A different clean image (wrong text) does not re-pixelate to the mosaic.
	wrongClean, _ := cleanAndMosaic(t, "xyz", block)
	bad, err := unpixel.VerifyImage(t.Context(), mosaic, wrongClean, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(wrong): %v", err)
	}
	if bad.Match {
		t.Errorf("hallucinated restoration unexpectedly Match (distance %.4f)", bad.Distance)
	}
	if !(bad.Distance > good.Distance) {
		t.Errorf("wrong distance %.4f should exceed true distance %.4f", bad.Distance, good.Distance)
	}
}

func TestVerifyImage_resizesRestored(t *testing.T) {
	const block = 6
	clean, mosaic := cleanAndMosaic(t, "the", block)
	// Upscale the clean image 2× so VerifyImage must resize it back.
	big := image.NewRGBA(image.Rect(0, 0, clean.Bounds().Dx()*2, clean.Bounds().Dy()*2))
	// nearest-neighbour blow-up is fine for the test; just exercise the resize path.
	for y := big.Bounds().Min.Y; y < big.Bounds().Max.Y; y++ {
		for x := big.Bounds().Min.X; x < big.Bounds().Max.X; x++ {
			big.Set(x, y, clean.At(x/2, y/2))
		}
	}
	v, err := unpixel.VerifyImage(t.Context(), mosaic, big, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(resized): %v", err)
	}
	// Observed distance after CatmullRom downscale of a 2× nearest-neighbour
	// blow-up is 0.0000; threshold of 0.05 catches a broken resize path while
	// leaving comfortable headroom for interpolation noise.
	if v.Distance > 0.05 {
		t.Errorf("resized true restoration distance %.4f too high (resize path broken?)", v.Distance)
	}
}
