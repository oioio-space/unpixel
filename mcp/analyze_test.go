package mcpserver_test

import (
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestAnalyze_reportsForwardOperator verifies that Analyze populates the
// ForwardOperator field with a non-empty Kind for a known mosaic fixture.
func TestAnalyze_reportsForwardOperator(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	got, err := mcpserver.Analyze(img)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got.ForwardOperator.Kind == "" {
		t.Errorf("ForwardOperator.Kind = empty, want a detected kind (e.g. \"mosaic\")")
	}
}
