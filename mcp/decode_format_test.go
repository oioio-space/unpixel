package mcpserver_test

import (
	"image"
	"strings"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/secrets"
)

// renderPixelatedMCP renders text with the default components and pixelates it
// in memory, returning a mosaic image suitable for passing to [mcpserver.Decode].
// No file or network I/O is performed.
func renderPixelatedMCP(t *testing.T, text string, block int, fontSize float64) image.Image {
	t.Helper()
	cfg := unpixel.Config{BlockSize: block, Style: unpixel.Style{FontSize: fontSize}}
	if err := defaults.Wire(&cfg); err != nil {
		t.Fatalf("wire defaults: %v", err)
	}
	rendered, _, err := cfg.Renderer.Render(text, cfg.Style)
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	return cfg.Pixelator.Pixelate(rendered, 0, 0)
}

// TestParseFormat_engineFieldNames verifies that every name documented in the
// MCP expected_format field schema is recognised by secrets.ParseFormat.
func TestParseFormat_engineFieldNames(t *testing.T) {
	for _, name := range []string{"digits", "credit_card", "iban", "date", "phone_fr", "phone_us", "phone_e164"} {
		if _, ok := secrets.ParseFormat(name); !ok {
			t.Errorf("ParseFormat(%q) not recognised", name)
		}
	}
}

// TestDecodeEngine_expectedFormatRecoversDigits verifies that the engine decode
// path recovers a digit string when expected_format="digits" is set, and that
// the result includes an "expected_format=digits" note.
func TestDecodeEngine_expectedFormatRecoversDigits(t *testing.T) {
	const secret = "8675309"
	img := renderPixelatedMCP(t, secret, 6, 24)
	res, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{
		CharsetPreset:  "digits",
		BlockSize:      6,
		FontSize:       24,
		MaxLength:      len(secret),
		ExpectedFormat: "digits",
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Text != secret {
		t.Errorf("text = %q; want %q", res.Text, secret)
	}
	joined := strings.Join(res.Notes, " ")
	if !strings.Contains(joined, "expected_format=digits") {
		t.Errorf("notes = %v; want an expected_format note", res.Notes)
	}
}
