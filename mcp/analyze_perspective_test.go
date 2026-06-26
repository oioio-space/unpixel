package mcpserver_test

import (
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

// TestAnalyze_perspectiveGating verifies that Analyze flags perspective only for
// genuinely tilted mosaics, not for upright redactions or clean text that the
// axis-aligned grid detector simply missed. DetectQuad finds a foreground region
// in almost any text image, so the perspective recommendation must be gated on
// the quad actually being skewed.
func TestAnalyze_perspectiveGating(t *testing.T) {
	tests := []struct {
		name        string
		path        string
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
			name:        "clean_text_not_perspective",
			path:        "../testdata/wild/b3_gt_images1.png",
			wantPersp:   false,
			description: "un-redacted ground-truth image must not be flagged",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, err := loadImageFile(tt.path)
			if err != nil {
				t.Fatalf("load %s: %v", tt.path, err)
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
