package mcpserver

// font.go — shared font-loading helper used by all MCP tools that accept a
// custom font upload (unpixel_decode, unpixel_render).
//
// Precedence: explicit custom font data > bundled font name > decoder default.
// At most one of font_path and font_base64 may be set; supplying both is an
// error. The loaded bytes are lightly validated (sfnt/OTF/TTC magic check)
// before being returned.

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/oioio-space/unpixel/fonts"
)

// bundledFontData resolves a bundled font by name (case-insensitive, whitespace
// trimmed) to its raw TTF/OTF bytes. It returns ok=false when no bundled font
// matches, so callers can surface the set of valid names. Names are those listed
// by the unpixel://fonts resource and returned by unpixel_rank_fonts.
func bundledFontData(name string) ([]byte, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return nil, false
	}
	for _, f := range fonts.All() {
		if strings.ToLower(f.Name) == want {
			return f.Data, true
		}
	}
	return nil, false
}

// maxFontBytes is the maximum accepted font file size (16 MiB). Fonts larger
// than this are almost certainly not real font files and are rejected to limit
// memory use on LLM-supplied paths.
const maxFontBytes = 16 << 20 // 16 MiB

// sfntMagics lists the four-byte magic signatures recognised as valid font
// files. Sources: OpenType spec (§1.5), TrueType spec.
//
//   - 0x00010000 — TrueType / OpenType with TrueType outlines
//   - OTTO       — OpenType with CFF (PostScript) outlines
//   - true       — old Mac TrueType (QuickDraw GX / sfnt-v1)
//   - ttcf       — TrueType Collection
var sfntMagics = [][4]byte{
	{0x00, 0x01, 0x00, 0x00}, // TrueType / OpenType-TTF
	{'O', 'T', 'T', 'O'},     // CFF / OpenType-CFF
	{'t', 'r', 'u', 'e'},     // old Mac TrueType
	{'t', 't', 'c', 'f'},     // TrueType Collection
}

// isFontMagic returns true when b starts with a known sfnt magic signature.
func isFontMagic(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	var h [4]byte
	copy(h[:], b[:4])
	for _, m := range sfntMagics {
		if h == m {
			return true
		}
	}
	return false
}

// LoadFontData loads raw font bytes from at most one of the two sources.
//
// Rules:
//   - Both non-empty → error (ambiguous).
//   - fontBase64 non-empty → base64-decode and validate magic.
//   - fontPath non-empty → read file and validate magic.
//   - Both empty → returns (nil, nil); callers fall back to named / default font.
//
// The returned slice is always either nil or a valid (magic-checked) font.
func LoadFontData(fontPath, fontBase64 string) ([]byte, error) {
	if fontPath != "" && fontBase64 != "" {
		return nil, fmt.Errorf("font_path and font_base64 are mutually exclusive; supply at most one")
	}
	if fontBase64 != "" {
		data, err := base64.StdEncoding.DecodeString(fontBase64)
		if err != nil {
			return nil, fmt.Errorf("font_base64: invalid base64: %w", err)
		}
		if !isFontMagic(data) {
			return nil, fmt.Errorf("font_base64: data does not look like a TTF/OTF font (bad magic)")
		}
		return data, nil
	}
	if fontPath != "" {
		info, err := os.Stat(fontPath) // #nosec G304 -- MCP server reads caller-supplied local path by design
		if err != nil {
			return nil, fmt.Errorf("font_path %q: %w", fontPath, err)
		}
		if info.Size() > maxFontBytes {
			return nil, fmt.Errorf("font_path %q: file too large (%d bytes; limit %d)", fontPath, info.Size(), maxFontBytes)
		}
		data, err := os.ReadFile(fontPath) // #nosec G304 -- MCP server reads caller-supplied local path by design
		if err != nil {
			return nil, fmt.Errorf("font_path %q: %w", fontPath, err)
		}
		if !isFontMagic(data) {
			return nil, fmt.Errorf("font_path %q: file does not look like a TTF/OTF font (bad magic)", fontPath)
		}
		return data, nil
	}
	return nil, nil
}
