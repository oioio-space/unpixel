package infoleak

import (
	"image"
	"testing"
)

// solid returns a w×h RGBA filled with the given gray level.
func solid(w, h int, gray uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = gray, gray, gray, 255
	}
	return img
}

func TestSeparability_identicalIsZero(t *testing.T) {
	a := solid(8, 8, 128)
	if got := Separability(a, a); got != 0 {
		t.Errorf("Separability(x,x) = %v; want 0", got)
	}
}

func TestSeparability_differsIsPositive(t *testing.T) {
	a := solid(8, 8, 0)
	b := solid(8, 8, 255)
	got := Separability(a, b)
	if got <= 0.9 { // black vs white ≈ 1.0
		t.Errorf("Separability(black,white) = %v; want ≈1.0", got)
	}
}

func TestJPEGRoundTrip_preservesDimsAltersPixels(t *testing.T) {
	// Vertical 1px stripes: a high-frequency pattern JPEG must distort at low q.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(img.Pix); i += 4 {
		px := (i / 4) % 16
		var v uint8
		if px%2 == 0 {
			v = 255
		}
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
	}

	out, err := JPEGRoundTrip(img, 30)
	if err != nil {
		t.Fatalf("JPEGRoundTrip: %v", err)
	}
	if out.Bounds().Dx() != 16 || out.Bounds().Dy() != 16 {
		t.Errorf("dims = %v; want 16×16", out.Bounds())
	}
	if Separability(img, out) == 0 {
		t.Errorf("JPEG q=30 of a striped image should alter pixels (Separability > 0)")
	}
}

func TestBinarizeHardEdge_twoLevels(t *testing.T) {
	// Gray ramp → after binarize only {0,255} luminance remain.
	img := image.NewRGBA(image.Rect(0, 0, 16, 1))
	for x := 0; x < 16; x++ {
		v := uint8(x * 16)
		img.Pix[x*4], img.Pix[x*4+1], img.Pix[x*4+2], img.Pix[x*4+3] = v, v, v, 255
	}
	out := binarizeHardEdge(img, 128)
	levels := map[int]bool{}
	for x := 0; x < 16; x++ {
		levels[imLum(out, x, 0)] = true
	}
	for l := range levels {
		if l != 0 && l != 255 {
			t.Errorf("binarize produced luminance %d; want only 0 or 255", l)
		}
	}
}

// imLum is a tiny test helper reading a pixel's Lum601.
func imLum(img *image.RGBA, x, y int) int {
	o := img.PixOffset(x, y)
	return (299*int(img.Pix[o]) + 587*int(img.Pix[o+1]) + 114*int(img.Pix[o+2])) / 1000
}
