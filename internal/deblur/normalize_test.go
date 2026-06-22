package deblur

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// makeGrey returns an *image.RGBA filled with a uniform grey level v in [0,255].
func makeGrey(w, h int, v uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = v
		img.Pix[i+1] = v
		img.Pix[i+2] = v
		img.Pix[i+3] = 255
	}
	return img
}

// meanLum returns the mean red-channel value of img (all channels equal for
// greyscale output).
func meanLum(img *image.RGBA) float64 {
	b := img.Bounds()
	var sum float64
	n := b.Dx() * b.Dy()
	if n == 0 {
		return 0
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			sum += float64(img.RGBAAt(x, y).R)
		}
	}
	return sum / float64(n)
}

// TestNormalize_pureIdentity verifies that Normalize with BgNone, no stretch,
// InvertOff, no deblock, and no binarise is (within rounding) the identity on
// a uniform mid-grey image.
func TestNormalize_pureIdentity(t *testing.T) {
	src := makeGrey(32, 32, 128)
	got := Normalize(src, Options{
		Bg:      BgNone,
		Invert:  InvertOff,
		Stretch: false,
	})
	m := meanLum(got)
	if math.Abs(m-128) > 2.0 {
		t.Errorf("identity: mean = %.1f, want ≈128", m)
	}
}

// TestNormalize_doesNotMutateSrc confirms the input is never modified.
func TestNormalize_doesNotMutateSrc(t *testing.T) {
	src := makeGrey(16, 16, 200)
	// Save a copy of pixel bytes.
	orig := make([]byte, len(src.Pix))
	copy(orig, src.Pix)

	_ = Normalize(src, DefaultOptions())

	for i := range orig {
		if src.Pix[i] != orig[i] {
			t.Fatalf("Normalize mutated src.Pix[%d]: got %d, want %d", i, src.Pix[i], orig[i])
		}
	}
}

// TestNormalize_outputAlwaysOpaque checks that every output pixel has A=255.
func TestNormalize_outputAlwaysOpaque(t *testing.T) {
	src := makeGrey(20, 20, 100)
	got := Normalize(src, DefaultOptions())
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if a := got.RGBAAt(x, y).A; a != 255 {
				t.Fatalf("pixel (%d,%d) has alpha %d, want 255", x, y, a)
			}
		}
	}
}

// TestNormalize_outputGreyscale checks that R==G==B for every output pixel.
func TestNormalize_outputGreyscale(t *testing.T) {
	// Use a coloured source to verify the output is still grey.
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 50, A: 255})
		}
	}
	got := Normalize(src, DefaultOptions())
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := got.RGBAAt(x, y)
			if c.R != c.G || c.R != c.B {
				t.Fatalf("pixel (%d,%d) not grey: %v", x, y, c)
			}
		}
	}
}

// TestNormalize_invertAuto_darkMeanInverts verifies that when the normalised
// luminance plane has mean < 127 (i.e., the image is dark), InvertAuto flips
// it so the output is predominantly light.
func TestNormalize_invertAuto_darkMeanInverts(t *testing.T) {
	// A very dark image: should end up bright after inversion.
	src := makeGrey(32, 32, 20)
	got := Normalize(src, Options{
		Bg:      BgNone,
		Invert:  InvertAuto,
		Stretch: false,
	})
	m := meanLum(got)
	if m < 127 {
		t.Errorf("InvertAuto on dark image: output mean = %.1f, want ≥ 127", m)
	}
}

// TestNormalize_invertForce always inverts regardless of input brightness.
func TestNormalize_invertForce_brightensDark(t *testing.T) {
	src := makeGrey(16, 16, 30)
	before := Normalize(src, Options{Bg: BgNone, Invert: InvertOff, Stretch: false})
	after := Normalize(src, Options{Bg: BgNone, Invert: InvertForce, Stretch: false})
	mb := meanLum(before)
	ma := meanLum(after)
	if ma <= mb {
		t.Errorf("InvertForce on dark: before mean=%.1f, after mean=%.1f, want after > before", mb, ma)
	}
}

// TestNormalize_stretchIncreasesContrast verifies that Stretch=true produces a
// wider dynamic range than Stretch=false on a low-contrast input.
func TestNormalize_stretchIncreasesContrast(t *testing.T) {
	// Low-contrast: values in [110,140].
	w, h := 32, 32
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(110 + (x+y)%31)
			src.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}

	noStretch := Normalize(src, Options{Bg: BgNone, Invert: InvertOff, Stretch: false})
	withStretch := Normalize(src, Options{Bg: BgNone, Invert: InvertOff, Stretch: true})

	rangeOf := func(img *image.RGBA) (lo, hi uint8) {
		lo, hi = 255, 0
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				v := img.RGBAAt(x, y).R
				lo = min(lo, v)
				hi = max(hi, v)
			}
		}
		return lo, hi
	}

	lo0, hi0 := rangeOf(noStretch)
	lo1, hi1 := rangeOf(withStretch)
	rng0 := int(hi0) - int(lo0)
	rng1 := int(hi1) - int(lo1)
	if rng1 <= rng0 {
		t.Errorf("Stretch: range without=(%d-%d=%d) range with=(%d-%d=%d); want with > without",
			lo0, hi0, rng0, lo1, hi1, rng1)
	}
}

// TestNormalize_binarize checks that the Binarize option produces only {0,255}
// pixel values.
func TestNormalize_binarize(t *testing.T) {
	src := makeGrey(16, 16, 128)
	// Give the image some variation.
	for x := range 8 {
		src.SetRGBA(x, 8, color.RGBA{R: 50, G: 50, B: 50, A: 255})
	}
	got := Normalize(src, Options{Bg: BgNone, Invert: InvertOff, Stretch: false, Binarize: true})
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			v := got.RGBAAt(x, y).R
			if v != 0 && v != 255 {
				t.Fatalf("Binarize: pixel (%d,%d) = %d, want 0 or 255", x, y, v)
			}
		}
	}
}

// TestNormalize_bgDivide_flattensVignette verifies that BgDivide reduces the
// luminance variation across a simple radial vignette (brighter centre, darker
// edges) when compared with BgNone.
func TestNormalize_bgDivide_flattensVignette(t *testing.T) {
	w, h := 64, 64
	cx, cy := float64(w)/2, float64(h)/2
	maxDist := math.Sqrt(cx*cx + cy*cy)

	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			dx, dy := float64(x)-cx, float64(y)-cy
			d := math.Sqrt(dx*dx + dy*dy)
			// Centre ≈ 220, edge ≈ 80 (strong vignette).
			v := uint8(80 + 140*(1-d/maxDist))
			src.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}

	stdDev := func(img *image.RGBA) float64 {
		m := meanLum(img)
		b := img.Bounds()
		var acc float64
		n := float64(b.Dx() * b.Dy())
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				diff := float64(img.RGBAAt(x, y).R) - m
				acc += diff * diff
			}
		}
		return math.Sqrt(acc / n)
	}

	none := Normalize(src, Options{Bg: BgNone, Invert: InvertOff, Stretch: false})
	divided := Normalize(src, Options{Bg: BgDivide, Invert: InvertOff, Stretch: false})

	sdNone := stdDev(none)
	sdDivided := stdDev(divided)
	if sdDivided >= sdNone {
		t.Errorf("BgDivide: stddev without=%.2f with=%.2f; want with < without (vignette flattened)", sdNone, sdDivided)
	}
}

// TestNormalize_emptyImage does not panic on a zero-size image.
func TestNormalize_emptyImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 0, 0))
	got := Normalize(src, DefaultOptions())
	if got == nil {
		t.Fatal("Normalize(empty) returned nil")
	}
	b := got.Bounds()
	if b.Dx() != 0 || b.Dy() != 0 {
		t.Errorf("Normalize(empty): bounds = %v, want empty", b)
	}
}
