package mcpserver_test

import (
	"image"
	"image/color"
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

// uprightCleanImage builds a synthetic un-redacted image: a white background
// with a single axis-aligned black rectangle (a stand-in for clean upright
// content). DetectQuad finds this foreground region in any such image, so the
// perspective gate must NOT flag it — the quad is not tilted. Using an in-memory
// image keeps this case self-contained: the real "ground-truth" wild fixtures
// are downloaded by scripts/fetch-wild-fixtures.sh and gitignored, so they are
// absent on a fresh CI checkout and must not be a test dependency.
func uprightCleanImage() image.Image {
	const w, h = 160, 60
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	// Axis-aligned black block, well inside the borders.
	for y := 18; y < 42; y++ {
		for x := 30; x < 130; x++ {
			img.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	return img
}

// TestAnalyze_perspectiveGating verifies that Analyze flags perspective only for
// genuinely tilted mosaics, not for upright redactions or clean content that the
// axis-aligned grid detector simply missed. DetectQuad finds a foreground region
// in almost any image, so the perspective recommendation must be gated on the
// quad actually being skewed.
func TestAnalyze_perspectiveGating(t *testing.T) {
	tests := []struct {
		name        string
		path        string      // committed fixture; empty when img is set
		img         image.Image // in-memory image; takes precedence over path
		wantPersp   bool
		description string
	}{
		{
			name:        "tilted_mosaic_is_perspective",
			path:        "../testdata/perspective/persp_go.png",
			wantPersp:   true,
			description: "photographed mosaic at an angle",
		},
		{
			name:        "upright_label_not_perspective",
			path:        "../testdata/context/ctx_sameline_pin.png",
			wantPersp:   false,
			description: "axis-aligned short redaction the grid detector missed",
		},
		{
			name:        "clean_upright_not_perspective",
			img:         uprightCleanImage(),
			wantPersp:   false,
			description: "clean un-redacted upright content must not be flagged",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := tt.img
			if img == nil {
				loaded, err := loadImageFile(tt.path)
				if err != nil {
					t.Fatalf("load %s: %v", tt.path, err)
				}
				img = loaded
			}
			got, err := mcp.Analyze(img)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if got.PerspectiveDistortion != tt.wantPersp {
				t.Errorf("PerspectiveDistortion = %v, want %v (%s); recommended=%q",
					got.PerspectiveDistortion, tt.wantPersp, tt.description, got.RecommendedDecoder)
			}
		})
	}
}
