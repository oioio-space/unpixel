package unpixel_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// stripes fills rows [y0,y1) of img with sharp 4px-wide black/white vertical
// stripes (high-contrast, high-gradient content).
func stripes(img *image.RGBA, y0, y1 int) {
	for y := y0; y < y1; y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			v := uint8(0)
			if (x/4)%2 == 0 {
				v = 255
			}
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
}

// TestLocateRedaction_findsBlurredBand builds a sharp band over a blurred band
// and checks LocateRedaction returns (roughly) the blurred one.
func TestLocateRedaction_findsBlurredBand(t *testing.T) {
	const w, h = 60, 48
	full := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range full.Pix { // white background
		full.Pix[i] = 0xFF
	}
	stripes(full, 6, 18) // sharp text band

	// Blurred content band: stripes blurred with a sizable sigma → low gradient.
	band := image.NewRGBA(image.Rect(0, 0, w, 16))
	stripes(band, 0, 16)
	blurred := pixelate.NewGaussianBlur(4).Pixelate(band, 0, 0)
	for y := range 16 {
		for x := range w {
			full.SetRGBA(x, 28+y, blurred.RGBAAt(x, y))
		}
	}

	rect, ok := unpixel.LocateRedaction(full)
	if !ok {
		t.Fatal("LocateRedaction: ok=false, want a blurred band")
	}
	// The located band should sit in the blurred region (y≈28..44), not the
	// sharp band (y≈6..18). Allow a little slack at the edges.
	if rect.Min.Y < 24 || rect.Max.Y > 46 {
		t.Errorf("located band y=[%d,%d), want within [24,46) (the blurred region)", rect.Min.Y, rect.Max.Y)
	}
	if rect.Dx() < w/2 {
		t.Errorf("located width %d too narrow (want most of %d)", rect.Dx(), w)
	}
}

// TestLocateRedaction_sharpImageNone returns ok=false on an all-sharp image.
func TestLocateRedaction_sharpImageNone(t *testing.T) {
	const w, h = 40, 24
	sharp := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range sharp.Pix {
		sharp.Pix[i] = 0xFF
	}
	stripes(sharp, 4, 20) // all sharp content
	if _, ok := unpixel.LocateRedaction(sharp); ok {
		t.Error("LocateRedaction(sharp) = ok, want false (nothing blurred)")
	}
}
