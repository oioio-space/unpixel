package unpixel_test

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// makeBlurredPNG renders text, blurs it, and encodes it as PNG bytes. It is
// the blur analogue of the PNG helpers in recover_test.go.
func makeBlurredPNG(t *testing.T, text string, sigma float64) []byte {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	blur := pixelate.NewGaussianBlur(sigma)
	c := components{
		renderer:  r,
		pixelator: blur,
		metric:    nil, // not needed for image generation
	}
	// Use makeSyntheticRedacted which is already wired for blur (blockSize=1).
	img := makeSyntheticRedacted(t, c, text, style, 1)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// TestRecoverBlurredReader_roundTrip verifies that RecoverBlurredReader decodes
// a PNG from an io.Reader and returns a non-empty best guess.
func TestRecoverBlurredReader_roundTrip(t *testing.T) {
	const (
		text  = "go"
		sigma = 3.0
	)
	pngBytes := makeBlurredPNG(t, text, sigma)
	res, err := unpixel.RecoverBlurredReader(
		t.Context(), bytes.NewReader(pngBytes),
		unpixel.WithCharset("go abc"),
		unpixel.WithMaxLength(len(text)+1),
	)
	if err != nil {
		t.Fatalf("RecoverBlurredReader: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("RecoverBlurredReader returned empty best guess")
	}
}

// TestRecoverBlurredFile_roundTrip verifies that RecoverBlurredFile opens a PNG
// file on disk and returns a non-empty best guess.
func TestRecoverBlurredFile_roundTrip(t *testing.T) {
	const (
		text  = "go"
		sigma = 3.0
	)
	pngBytes := makeBlurredPNG(t, text, sigma)

	path := filepath.Join(t.TempDir(), "blurred.png")
	if err := os.WriteFile(path, pngBytes, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res, err := unpixel.RecoverBlurredFile(
		t.Context(), path,
		unpixel.WithCharset("go abc"),
		unpixel.WithMaxLength(len(text)+1),
	)
	if err != nil {
		t.Fatalf("RecoverBlurredFile: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("RecoverBlurredFile returned empty best guess")
	}
}

// TestRecoverBlurredReader_badImage verifies that RecoverBlurredReader returns
// a decode error on garbage input.
func TestRecoverBlurredReader_badImage(t *testing.T) {
	_, err := unpixel.RecoverBlurredReader(t.Context(), bytes.NewReader([]byte("not a png")))
	if err == nil {
		t.Error("RecoverBlurredReader(garbage) = nil error, want decode error")
	}
}

// TestRecoverBlurredFile_missing verifies that RecoverBlurredFile returns an
// error when the path does not exist.
func TestRecoverBlurredFile_missing(t *testing.T) {
	_, err := unpixel.RecoverBlurredFile(t.Context(), filepath.Join(t.TempDir(), "nope.png"))
	if err == nil {
		t.Error("RecoverBlurredFile(missing) = nil error, want open error")
	}
}
