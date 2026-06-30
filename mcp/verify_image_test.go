package mcpserver_test

import (
	"image"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// cleanMosaicVI renders text and returns (clean image padded to block-width,
// its mosaic). The clean image is pre-padded so its width is a multiple of
// block — matching the width that BlockAverage.Pixelate produces — so
// VerifyImageMCP's re-pixelation of the clean image produces distance ≈ 0
// against the mosaic (pipeline-faithful fixture).
func cleanMosaicVI(t *testing.T, text string, block int) (*image.RGBA, image.Image) {
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

// TestVerifyImageMCP_trueVsHallucination verifies that VerifyImageMCP returns
// Match=true for the faithful restored image and Match=false for a different
// (hallucinated) image.
func TestVerifyImageMCP_trueVsHallucination(t *testing.T) {
	const block = 6
	clean, mosaic := cleanMosaicVI(t, "the", block)

	good, err := mcpserver.VerifyImageMCP(t.Context(), mosaic, clean, block)
	if err != nil {
		t.Fatalf("VerifyImageMCP(true): %v", err)
	}
	if !good.Match {
		t.Errorf("true restoration not Match (distance %.4f)", good.Distance)
	}

	wrong, _ := cleanMosaicVI(t, "xyz", block)
	bad, err := mcpserver.VerifyImageMCP(t.Context(), mosaic, wrong, block)
	if err != nil {
		t.Fatalf("VerifyImageMCP(wrong): %v", err)
	}
	if bad.Match {
		t.Errorf("hallucination unexpectedly Match (distance %.4f)", bad.Distance)
	}
}
