//go:build !ml

package mcpserver_test

import (
	"image"
	"strings"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
)

// mosaicFixtureExt renders text with the named bundled font at the given block
// size and pixelates it in memory. No file or network I/O is performed.
func mosaicFixtureExt(t *testing.T, fontName, text string, block int) image.Image {
	t.Helper()
	var fontData []byte
	for _, f := range fonts.All() {
		if f.Name == fontName {
			fontData = f.Data
			break
		}
	}
	if fontData == nil {
		t.Fatalf("mosaicFixtureExt: font %q not found in bundled fonts", fontName)
	}
	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("mosaicFixtureExt: build renderer: %v", err)
	}
	cfg := unpixel.Config{BlockSize: block}
	if err := defaults.Wire(&cfg); err != nil {
		t.Fatalf("mosaicFixtureExt: wire defaults: %v", err)
	}
	rendered, _, err := r.Render(text, cfg.Style)
	if err != nil {
		t.Fatalf("mosaicFixtureExt: render %q: %v", text, err)
	}
	return cfg.Pixelator.Pixelate(rendered, 0, 0)
}

// TestDecodeEngine_fontPriorTopK verifies that the engine path with
// FontPriorTopK>0 runs a prior-ordered multi-font sweep, populates Font in the
// result, and records a "font prior top-N" note.
func TestDecodeEngine_fontPriorTopK(t *testing.T) {
	img := mosaicFixtureExt(t, "Liberation Mono", "GO2024", 6)
	res, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{
		CharsetPreset: "alnum",
		BlockSize:     6,
		FontSize:      28,
		FontPriorTopK: 3,
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Font == "" {
		t.Errorf("expected a chosen font in the result")
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "font prior top-3") {
		t.Errorf("notes = %v; want a font-prior note", res.Notes)
	}
}
