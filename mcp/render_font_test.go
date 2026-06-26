package mcpserver_test

// render_font_test.go — tests for custom font upload in unpixel_render.

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"os"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestRenderText_customFontPath verifies that RenderText produces valid PNG
// when a custom font is loaded from a local path via LoadFontData.
func TestRenderText_customFontPath(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	fontData, err := mcpserver.LoadFontData(ttfPath, "")
	if err != nil {
		t.Fatalf("LoadFontData(path): %v", err)
	}

	got, err := mcpserver.RenderText("hello", mcpserver.RenderOptions{
		FontData: fontData,
		FontSize: 32,
	})
	if err != nil {
		t.Fatalf("RenderText(custom font): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("RenderText(custom font): returned empty bytes")
	}
	if _, decErr := png.Decode(bytes.NewReader(got)); decErr != nil {
		t.Errorf("RenderText(custom font): output is not valid PNG: %v", decErr)
	}
}

// TestRenderText_customFontBase64 verifies that RenderText produces valid PNG
// when a custom font is supplied as base64-encoded bytes via LoadFontData.
func TestRenderText_customFontBase64(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	raw, err := os.ReadFile(ttfPath)
	if err != nil {
		t.Fatalf("read TTF: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)

	fontData, err := mcpserver.LoadFontData("", b64)
	if err != nil {
		t.Fatalf("LoadFontData(base64): %v", err)
	}

	got, err := mcpserver.RenderText("world", mcpserver.RenderOptions{
		FontData: fontData,
	})
	if err != nil {
		t.Fatalf("RenderText(custom font via base64): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("RenderText(custom font via base64): returned empty bytes")
	}
	if _, decErr := png.Decode(bytes.NewReader(got)); decErr != nil {
		t.Errorf("RenderText(custom font via base64): output is not valid PNG: %v", decErr)
	}
}

// TestRenderText_customFontVsDefault verifies that rendering with a custom
// font and the default font both produce non-empty PNG output.
func TestRenderText_customFontVsDefault(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	fontData, err := mcpserver.LoadFontData(ttfPath, "")
	if err != nil {
		t.Fatalf("LoadFontData: %v", err)
	}

	withCustom, err := mcpserver.RenderText("test", mcpserver.RenderOptions{FontData: fontData})
	if err != nil {
		t.Fatalf("RenderText(custom): %v", err)
	}
	withDefault, err := mcpserver.RenderText("test", mcpserver.RenderOptions{})
	if err != nil {
		t.Fatalf("RenderText(default): %v", err)
	}
	if len(withCustom) == 0 {
		t.Error("RenderText(custom): empty output")
	}
	if len(withDefault) == 0 {
		t.Error("RenderText(default): empty output")
	}
}

// TestRenderText_badBase64Error verifies that invalid base64 in font_base64
// returns an error from LoadFontData before reaching RenderText.
func TestRenderText_badBase64Error(t *testing.T) {
	_, err := mcpserver.LoadFontData("", "!!!not-base64!!!")
	if err == nil {
		t.Error("LoadFontData(bad base64): want error, got nil")
	}
}

// TestRenderText_bothFontFieldsError verifies that supplying both font_path
// and font_base64 returns an error.
func TestRenderText_bothFontFieldsError(t *testing.T) {
	_, err := mcpserver.LoadFontData("/some/font.ttf", "dGVzdA==")
	if err == nil {
		t.Error("LoadFontData(path+base64): want error for mutually exclusive inputs, got nil")
	}
}
