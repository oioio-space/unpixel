package unpixel_test

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults"
)

// TestRecoverWithCrop_embedded proves Recover honours WithCrop: a decodable mosaic
// fixture is composited into a larger white canvas at a known offset, and Recover
// with WithCrop pinned to the band recovers it — the analyze→decode flow for a
// redaction embedded in a bigger screenshot.
func TestRecoverWithCrop_embedded(t *testing.T) {
	f, err := os.Open("testdata/largeblock/lb_block20_go.png")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	fixture, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	fb := fixture.Bounds()

	// Composite the fixture into a larger white canvas at offset (60, 40).
	const ox, oy = 60, 40
	canvas := image.NewRGBA(image.Rect(0, 0, fb.Dx()+140, fb.Dy()+90))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	band := image.Rect(ox, oy, ox+fb.Dx(), oy+fb.Dy())
	draw.Draw(canvas, band, fixture, fb.Min, draw.Src)

	opts := []unpixel.Option{
		unpixel.WithCharset("go abcdef"),
		unpixel.WithBlockSize(20),
		unpixel.WithStyle(unpixel.Style{FontSize: 80, PaddingTop: 8, PaddingLeft: 8}),
	}
	res, err := unpixel.Recover(t.Context(), canvas, append(opts, unpixel.WithCrop(band))...)
	if err != nil {
		t.Fatalf("Recover with crop: %v", err)
	}
	if res.BestGuess != "go" {
		t.Errorf("Recover(WithCrop band) BestGuess = %q, want %q — WithCrop should crop before search", res.BestGuess, "go")
	}
}

// TestRecoverBytes_matchesFile checks the in-memory RecoverBytes helper decodes an
// image identically to RecoverFile on the same bytes.
func TestRecoverBytes_matchesFile(t *testing.T) {
	const path = "testdata/largeblock/lb_block20_go.png"
	opts := []unpixel.Option{
		unpixel.WithCharset("go abcdef"),
		unpixel.WithBlockSize(20),
		unpixel.WithStyle(unpixel.Style{FontSize: 80, PaddingTop: 8, PaddingLeft: 8}),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	fromBytes, err := unpixel.RecoverBytes(t.Context(), data, opts...)
	if err != nil {
		t.Fatalf("RecoverBytes: %v", err)
	}
	fromFile, err := unpixel.RecoverFile(t.Context(), path, opts...)
	if err != nil {
		t.Fatalf("RecoverFile: %v", err)
	}
	if fromBytes.BestGuess != fromFile.BestGuess {
		t.Errorf("RecoverBytes BestGuess = %q, RecoverFile = %q", fromBytes.BestGuess, fromFile.BestGuess)
	}
	if fromBytes.BestGuess != "go" {
		t.Errorf("BestGuess = %q, want %q", fromBytes.BestGuess, "go")
	}
}

// TestRecoverBytes_unrecognisedFormat checks the actionable error for non-image data.
func TestRecoverBytes_unrecognisedFormat(t *testing.T) {
	_, err := unpixel.RecoverBytes(t.Context(), []byte("this is not an image"))
	if err == nil {
		t.Fatal("RecoverBytes on non-image data: got nil error, want a decode error")
	}
	if !strings.Contains(err.Error(), "unrecognised format") {
		t.Errorf("error = %q, want it to mention 'unrecognised format'", err)
	}
}

// TestVerifyThresholdOption checks WithVerifyThreshold overrides the default and a
// non-positive value falls back to VerifyMatchThreshold.
func TestVerifyThresholdOption(t *testing.T) {
	var c unpixel.Config
	if got := c.VerifyThreshold(); got != unpixel.VerifyMatchThreshold {
		t.Errorf("default VerifyThreshold = %v, want %v", got, unpixel.VerifyMatchThreshold)
	}
	unpixel.WithVerifyThreshold(0.03)(&c)
	if got := c.VerifyThreshold(); got != 0.03 {
		t.Errorf("VerifyThreshold after WithVerifyThreshold(0.03) = %v, want 0.03", got)
	}
	unpixel.WithVerifyThreshold(-1)(&c)
	if got := c.VerifyThreshold(); got != unpixel.VerifyMatchThreshold {
		t.Errorf("VerifyThreshold after WithVerifyThreshold(-1) = %v, want default %v", got, unpixel.VerifyMatchThreshold)
	}
}
