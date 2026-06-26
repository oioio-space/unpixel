package mcpserver_test

import (
	"bytes"
	"image/png"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestRender_returnsNonEmptyPNG verifies that RenderText returns valid PNG
// bytes for a non-empty input string.
func TestRender_returnsNonEmptyPNG(t *testing.T) {
	got, err := mcpserver.RenderText("go", mcpserver.RenderOptions{})
	if err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("RenderText: returned empty bytes")
	}
	// Verify the bytes are valid PNG.
	if _, decErr := png.Decode(bytes.NewReader(got)); decErr != nil {
		t.Errorf("RenderText: output is not valid PNG: %v", decErr)
	}
}

// TestRender_emptyTextError verifies that RenderText returns an error for empty input.
func TestRender_emptyTextError(t *testing.T) {
	_, err := mcpserver.RenderText("", mcpserver.RenderOptions{})
	if err == nil {
		t.Error("RenderText(\"\"): want error, got nil")
	}
}

// TestRender_customFontSize verifies that different font sizes produce non-empty output.
func TestRender_customFontSize(t *testing.T) {
	for _, size := range []float64{12, 24, 48} {
		got, err := mcpserver.RenderText("hello", mcpserver.RenderOptions{FontSize: size})
		if err != nil {
			t.Errorf("RenderText(size=%.0f): %v", size, err)
			continue
		}
		if len(got) == 0 {
			t.Errorf("RenderText(size=%.0f): empty bytes", size)
		}
	}
}
