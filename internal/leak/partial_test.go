package leak

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngWithBlock(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 80, 40))
	for y := range 40 {
		for x := range 80 {
			im.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	for y := 10; y < 30; y++ { // solid black redaction block
		for x := 20; x < 60; x++ {
			im.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPartial_surfacesWithHint(t *testing.T) {
	res, found := partial(pngWithBlock(t), "the-secret")
	if !found {
		t.Fatalf("partial found=false, want true (block present + visible hint)")
	}
	if res.Text != "the-secret" || res.Source != SourcePartial {
		t.Errorf("got {%q,%q}, want {the-secret, partial-redaction}", res.Text, res.Source)
	}
}

func TestPartial_abstainsWithoutHint(t *testing.T) {
	if _, found := partial(pngWithBlock(t), ""); found {
		t.Errorf("found=true without VisibleText, want false (needs OCR — out of scope)")
	}
}

// TestPartial_abstainsWithNoBlock proves the honest guarantee: even with a
// visible-text hint, a plain image with no solid redaction block must abstain
// (the detector only confirms+surfaces; it never fabricates on clean content).
func TestPartial_abstainsWithNoBlock(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 80, 40))
	for y := range 40 {
		for x := range 80 {
			im.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	if _, found := partial(b.Bytes(), "some-hint"); found {
		t.Error("found=true on plain white image, want false (no redaction block)")
	}
}

// TestPartial_abstainsOnBadPNG verifies that partial abstains gracefully when
// the data is not a valid PNG, rather than panicking or returning found=true.
func TestPartial_abstainsOnBadPNG(t *testing.T) {
	if _, found := partial([]byte("not a PNG"), "some-hint"); found {
		t.Error("partial found=true on malformed PNG data, want false (abstain)")
	}
}

// TestAbsDiff_bGreaterThanA exercises the b > a branch of absDiff, which
// returns b - a. The a >= b branch is covered by the solid-block scanner path.
func TestAbsDiff_bGreaterThanA(t *testing.T) {
	if got := absDiff(10, 200); got != 190 {
		t.Errorf("absDiff(10, 200) = %d, want 190", got)
	}
}
