package mcpserver_test

// perspective_test.go — tests for method=perspective in unpixel_decode.
//
// Fixtures live in testdata/perspective/ (3 images: persp_go, persp_cat,
// persp_hello). The tests verify:
//   - auto_quad=true runs without error and returns non-empty text on at least
//     the simplest fixture (persp_go.png, 2-char string "go").
//   - malformed quad strings produce a clear error.
//   - explicit quad decodes the cleanest fixture correctly.

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// perspectiveFixturePath returns the absolute path to a perspective fixture.
func perspectiveFixturePath(name string) string {
	return filepath.Join("..", "testdata", "perspective", name)
}

// loadPerspectiveFixture opens a perspective fixture image.
func loadPerspectiveFixture(t *testing.T, name string) image.Image {
	t.Helper()
	p := perspectiveFixturePath(name)
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open perspective fixture %q: %v", p, err)
	}
	img, _, decErr := image.Decode(f)
	if closeErr := f.Close(); closeErr != nil && decErr == nil {
		t.Fatalf("close fixture %q: %v", p, closeErr)
	}
	if decErr != nil {
		t.Fatalf("decode perspective fixture %q: %v", p, decErr)
	}
	return img
}

// TestDecodePerspective_autoQuad verifies that auto_quad=true runs on the
// simplest perspective fixture and returns non-empty text without error.
func TestDecodePerspective_autoQuad(t *testing.T) {
	img := loadPerspectiveFixture(t, "persp_go.png")

	got, err := mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		AutoQuad:      true,
		CharsetPreset: "lower",
		MaxLength:     4,
	})
	if err != nil {
		t.Fatalf("Decode(perspective, auto_quad): %v", err)
	}
	if got.Text == "" {
		t.Error("Decode(perspective, auto_quad): Text is empty")
	}
	if got.MethodUsed != "perspective" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "perspective")
	}
}

// TestDecodePerspective_autoQuadCat verifies auto_quad on a second fixture.
func TestDecodePerspective_autoQuadCat(t *testing.T) {
	img := loadPerspectiveFixture(t, "persp_cat.png")

	got, err := mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		AutoQuad:      true,
		CharsetPreset: "lower",
		MaxLength:     5,
	})
	if err != nil {
		t.Fatalf("Decode(perspective, auto_quad, persp_cat): %v", err)
	}
	if got.Text == "" {
		t.Error("Decode(perspective, auto_quad, persp_cat): Text is empty")
	}
}

// TestDecodePerspective_explicitQuad decodes persp_go.png with the known quad
// from the manifest and checks we recover "go".
func TestDecodePerspective_explicitQuad(t *testing.T) {
	img := loadPerspectiveFixture(t, "persp_go.png")

	// Quad from testdata/perspective/manifest.json for persp_go:
	// TL(40,30) TR(80,48) BR(68,108) BL(46,100)
	got, err := mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		Quad:          "40,30 80,48 68,108 46,100",
		CharsetPreset: "lower",
		MaxLength:     4,
	})
	if err != nil {
		t.Fatalf("Decode(perspective, quad): %v", err)
	}
	if got.Text != "go" {
		t.Errorf("Decode(perspective, quad): Text = %q, want %q", got.Text, "go")
	}
}

// TestDecodePerspective_malformedQuad verifies that a malformed quad string
// returns an error with a clear message.
func TestDecodePerspective_malformedQuad(t *testing.T) {
	img := loadPerspectiveFixture(t, "persp_go.png")

	for _, bad := range []string{
		"not-a-quad",
		"10,20 30,40",            // only 2 pairs
		"10,20 30,40 50,60",      // only 3 pairs
		"10,20 30,x 50,60 70,80", // non-numeric
	} {
		_, err := mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
			Quad: bad,
		})
		if err == nil {
			t.Errorf("Decode(perspective, quad=%q): want error, got nil", bad)
		}
	}
}

// TestDecodePerspective_quadTakesPrecedenceOverAutoQuad verifies that when both
// quad and auto_quad are set, the explicit quad wins (no error, returns a result).
func TestDecodePerspective_quadTakesPrecedenceOverAutoQuad(t *testing.T) {
	img := loadPerspectiveFixture(t, "persp_go.png")

	got, err := mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		Quad:          "40,30 80,48 68,108 46,100",
		AutoQuad:      true, // explicit quad must win
		CharsetPreset: "lower",
		MaxLength:     4,
	})
	if err != nil {
		t.Fatalf("Decode(perspective, quad+auto_quad): %v", err)
	}
	if got.Text != "go" {
		t.Errorf("Decode(perspective, quad+auto_quad): Text = %q, want %q", got.Text, "go")
	}
}

// TestDecodePerspective_extraOptions verifies that font_size, block_size,
// beam_width, rect_size_w/h, and workers are accepted without error.
func TestDecodePerspective_extraOptions(t *testing.T) {
	img := loadPerspectiveFixture(t, "persp_go.png")

	_, err := mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		Quad:          "40,30 80,48 68,108 46,100",
		CharsetPreset: "lower",
		MaxLength:     4,
		FontSize:      32,
		BlockSize:     8,
		BeamWidth:     8,
		RectSizeW:     40,
		RectSizeH:     56,
		Workers:       2,
	})
	if err != nil {
		t.Fatalf("Decode(perspective, extra opts): %v", err)
	}
}

// TestDecodePerspective_customFont verifies that a custom font supplied via
// FontData is accepted without error for method=perspective.
func TestDecodePerspective_customFont(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	fontData, err := mcpserver.LoadFontData(ttfPath, "")
	if err != nil {
		t.Fatalf("LoadFontData: %v", err)
	}

	img := loadPerspectiveFixture(t, "persp_go.png")

	_, err = mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		Quad:          "40,30 80,48 68,108 46,100",
		CharsetPreset: "lower",
		MaxLength:     4,
		FontData:      fontData,
	})
	if err != nil {
		t.Fatalf("Decode(perspective, custom font): %v", err)
	}
}
