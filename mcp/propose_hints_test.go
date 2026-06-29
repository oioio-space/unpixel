package mcpserver_test

import (
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

// TestProposeHints_charCount verifies that ProposeHintsImage returns a
// plausible character-count estimate and the correct block size for the
// block08_go.png fixture ("go" pixelated at block size 8).
func TestProposeHints_charCount(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	rep, err := mcp.ProposeHintsImage(img)
	if err != nil {
		t.Fatalf("ProposeHintsImage: %v", err)
	}
	if rep.CharCountEstimate < 1 || rep.CharCountEstimate > 6 {
		t.Errorf("CharCountEstimate = %d, want a small count (~2 for \"go\")", rep.CharCountEstimate)
	}
	if rep.BlockSize != 8 {
		t.Errorf("BlockSize = %d, want 8", rep.BlockSize)
	}
}
