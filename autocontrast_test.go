package unpixel_test

import (
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
)

// invertRGBA returns a copy of img with every RGB channel inverted.
func invertRGBA(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			out.SetRGBA(x, y, color.RGBA{R: 255 - c.R, G: 255 - c.G, B: 255 - c.B, A: c.A})
		}
	}
	return out
}

func TestInferDarkBackground(t *testing.T) {
	light := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range light.Pix {
		light.Pix[i] = 0xFF
	}
	if unpixel.InferDarkBackground(light) {
		t.Error("light image detected as dark background")
	}
	if !unpixel.InferDarkBackground(invertRGBA(light)) {
		t.Error("dark image not detected as dark background")
	}
}

// TestNew_autoContrastRecoversDarkMode verifies that an inverted (dark-mode)
// redaction is auto-normalised so it recovers exactly like the light original.
func TestNew_autoContrastRecoversDarkMode(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)
	light := makeSyntheticRedacted(t, c, "hello", style, blockSize)
	dark := invertRGBA(light) // simulate a dark-mode capture

	res, err := unpixel.Recover(t.Context(), dark,
		unpixel.WithMaxLength(7), unpixel.WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("Recover(dark): %v", err)
	}
	guesses := []string{res.BestGuess}
	for _, e := range res.Candidates {
		guesses = append(guesses, e.Guess)
	}
	if !slices.Contains(guesses, "hello") {
		t.Errorf("dark-mode recovery missed %q; best=%q", "hello", res.BestGuess)
	}
}

// TestNew_lightImageUntouched confirms the faithful light-background path is not
// altered by auto-contrast (the stored image still recovers the plaintext).
func TestNew_lightImageUntouched(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)
	light := makeSyntheticRedacted(t, c, "hello", style, blockSize)

	if unpixel.InferDarkBackground(light) {
		t.Fatal("light fixture misdetected as dark — would corrupt the faithful path")
	}
	res, err := unpixel.Recover(t.Context(), light,
		unpixel.WithMaxLength(7), unpixel.WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("Recover(light): %v", err)
	}
	guesses := []string{res.BestGuess}
	for _, e := range res.Candidates {
		guesses = append(guesses, e.Guess)
	}
	if !slices.Contains(guesses, "hello") {
		t.Errorf("light recovery missed %q; best=%q", "hello", res.BestGuess)
	}
}
