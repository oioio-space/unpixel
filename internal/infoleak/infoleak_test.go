package infoleak

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/render"
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

// TestSeparability_nonZeroMin pins the PixOffset arithmetic for sub-images whose
// Bounds().Min is non-zero — a regression guard for the ab.Min.X+x / ab.Min.Y+y
// access pattern.
func TestSeparability_nonZeroMin(t *testing.T) {
	// Build a 13×13 image and take a sub-image with a non-zero origin.
	full := solid(13, 13, 200)
	sub := full.SubImage(image.Rect(5, 5, 13, 13)).(*image.RGBA)

	// A sub-image compared to itself is identical — Separability must be 0.
	if got := Separability(sub, sub); got != 0 {
		t.Errorf("Separability(sub,sub) = %v; want 0", got)
	}

	// A different-valued same-size sub-image must yield positive separability.
	other := solid(8, 8, 50) // same dims as sub (8×8), zero origin
	if got := Separability(sub, other); got <= 0 {
		t.Errorf("Separability(gray200-sub, gray50) = %v; want > 0", got)
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

func testRenderer(t *testing.T) unpixel.Renderer {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil) // Liberation Sans
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	return r
}

func TestMeasureJPEGImpact_driftGrowsAsQualityDrops(t *testing.T) {
	r := testRenderer(t)
	rep, err := MeasureJPEGImpact(r, "the", "tho", 6, 28, []int{90, 50, 20})
	if err != nil {
		t.Fatalf("MeasureJPEGImpact: %v", err)
	}
	if len(rep.Points) != 3 {
		t.Fatalf("points = %d; want 3", len(rep.Points))
	}
	// Drift is monotonically non-decreasing as quality drops (90 → 50 → 20).
	if !(rep.Points[0].Drift <= rep.Points[1].Drift && rep.Points[1].Drift <= rep.Points[2].Drift) {
		t.Errorf("drift not non-decreasing as quality drops: %+v", rep.Points)
	}
	// At high quality the true candidate should still win.
	if !rep.Points[0].TrueStillWins {
		t.Errorf("q=90: true candidate should still win")
	}
}

func TestMeasureAALeak_runsAndAggregates(t *testing.T) {
	r := testRenderer(t)
	rep, err := MeasureAALeak(r, "Liberation Sans", [][2]string{{"rn", "m"}, {"0", "O"}}, 6, 28)
	if err != nil {
		t.Fatalf("MeasureAALeak: %v", err)
	}
	if len(rep.Pairs) != 2 {
		t.Fatalf("pairs = %d; want 2", len(rep.Pairs))
	}
	// Aggregates are the means of the per-pair values.
	if rep.MeanGain != (rep.Pairs[0].Gain+rep.Pairs[1].Gain)/2 {
		t.Errorf("MeanGain %v != mean of pair gains", rep.MeanGain)
	}
	// Separabilities are in [0,1].
	for _, p := range rep.Pairs {
		if p.AASep < 0 || p.AASep > 1 || p.HardSep < 0 || p.HardSep > 1 {
			t.Errorf("pair %q/%q separabilities out of [0,1]: %+v", p.A, p.B, p)
		}
	}
}
