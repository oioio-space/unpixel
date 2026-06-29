package mcpserver

// resources.go — read-only MCP resources that expose UnPixel's reference
// catalogue without adding a tool per lookup.
//
//   unpixel://fonts         — JSON list of bundled font names.
//   unpixel://charsets      — the three charset presets with their rune strings.
//   unpixel://methods       — catalog of every decode method + one-line "use when".
//   unpixel://operating-envelope — honest note on real-world recovery rates.

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
)

// ResourcePayload returns the JSON text body of the named unpixel:// resource
// URI. It is the testable core used by the MCP resource handlers. An unknown
// URI returns an error.
func ResourcePayload(uri string) (string, error) {
	ctx := context.Background()
	switch uri {
	case "unpixel://fonts":
		r, err := handleFontsResource(ctx, nil)
		if err != nil {
			return "", err
		}
		return r.Contents[0].Text, nil
	case "unpixel://charsets":
		r, err := handleCharsetsResource(ctx, nil)
		if err != nil {
			return "", err
		}
		return r.Contents[0].Text, nil
	case "unpixel://methods":
		r, err := handleMethodsResource(ctx, nil)
		if err != nil {
			return "", err
		}
		return r.Contents[0].Text, nil
	case "unpixel://operating-envelope":
		r, err := handleEnvelopeResource(ctx, nil)
		if err != nil {
			return "", err
		}
		return r.Contents[0].Text, nil
	default:
		return "", fmt.Errorf("unknown resource URI %q", uri)
	}
}

// registerResources adds the four read-only reference resources to srv.
func registerResources(srv *mcpsdk.Server) {
	srv.AddResource(&mcpsdk.Resource{
		URI:         "unpixel://fonts",
		Name:        "unpixel-fonts",
		Title:       "Bundled font catalogue",
		Description: "JSON list of all bundled font names, styles, and proprietary equivalents available to UnPixel decoders.",
		MIMEType:    "application/json",
	}, handleFontsResource)

	srv.AddResource(&mcpsdk.Resource{
		URI:         "unpixel://charsets",
		Name:        "unpixel-charsets",
		Title:       "Charset presets",
		Description: "The three charset presets (lower, alnum, ascii) with their full rune strings. Pass the preset name as charset_preset in unpixel_decode.",
		MIMEType:    "application/json",
	}, handleCharsetsResource)

	srv.AddResource(&mcpsdk.Resource{
		URI:         "unpixel://methods",
		Name:        "unpixel-methods",
		Title:       "Decode method catalogue",
		Description: "Catalog of every method value accepted by unpixel_decode, with a one-line 'use when' guide.",
		MIMEType:    "application/json",
	}, handleMethodsResource)

	srv.AddResource(&mcpsdk.Resource{
		URI:         "unpixel://operating-envelope",
		Name:        "unpixel-operating-envelope",
		Title:       "Operating envelope and limitations",
		Description: "Honest description of what UnPixel can and cannot recover today, especially for real/wild images.",
		MIMEType:    "application/json",
	}, handleEnvelopeResource)
}

// fontEntry is one entry in the fonts resource payload.
type fontEntry struct {
	Name   string `json:"name"`
	Style  string `json:"style"`
	Approx string `json:"approx,omitzero"`
}

func handleFontsResource(_ context.Context, _ *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	all := fonts.All()
	entries := make([]fontEntry, len(all))
	for i, f := range all {
		entries[i] = fontEntry{Name: f.Name, Style: f.Style, Approx: f.Approx}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal fonts: %w", err)
	}
	return resourceJSON("unpixel://fonts", string(b)), nil
}

// charsetEntry describes one charset preset.
type charsetEntry struct {
	Preset string `json:"preset"`
	Runes  string `json:"runes"`
	Note   string `json:"note"`
}

func handleCharsetsResource(_ context.Context, _ *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	entries := []charsetEntry{
		{
			Preset: "lower",
			Runes:  unpixel.DefaultCharset,
			Note:   "Lowercase ASCII letters plus space. Fewest candidates, fastest search.",
		},
		{
			Preset: "alnum",
			Runes:  unpixel.CharsetAlnum,
			Note:   "Lowercase + uppercase letters, digits, and space. Good default for most redactions.",
		},
		{
			Preset: "ascii",
			Runes:  unpixel.CharsetASCII,
			Note:   "All printable ASCII (0x20–0x7E). Use for passwords or code with symbols.",
		},
		{
			Preset: "digits",
			Runes:  "0123456789",
			Note:   "Digits 0–9 only. Use for PINs, numeric IDs, or known-digit redactions. Fastest search for numeric content.",
		},
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal charsets: %w", err)
	}
	return resourceJSON("unpixel://charsets", string(b)), nil
}

// methodEntry describes one decode method.
type methodEntry struct {
	Method     string `json:"method"`
	UseWhen    string `json:"use_when"`
	TypLatency string `json:"typical_latency"`
}

func handleMethodsResource(_ context.Context, _ *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	entries := []methodEntry{
		{"auto", "Recommended default: routes to engine for axis-aligned mosaics, blurred for blur. Use this when unsure.", "varies"},
		{"engine", "Best-config path: unpixel.Recover with explicit charset, block size, font size, and max-length. Recovers hard fixtures (alnum, symbols, secrets) that zero-config decoders miss. charset_preset is the key parameter — set it to alnum, ascii, or digits as appropriate. Accepts charset_preset (incl. digits), block_size, font_size, max_length, denoise, font_path/font_base64.", "10–60 s"},
		{"mosaic", "Zero-config monospace text; basic first choice for GIMP/GEGL pixel-blur. Accepts font_path/font_base64.", "5–30 s"},
		{"blurred", "Gaussian-blurred redaction (not mosaic pixelation). Accepts denoise for noisy captures.", "10–60 s"},
		{"mono-hmm", "LM-guided beam search; better than mosaic for long monospace strings. Accepts font_path/font_base64.", "10–60 s"},
		{"window-hmm", "Proportional-font beam; use when glyphs have variable width. Accepts font_path/font_base64.", "10–60 s"},
		{"trained-hmm", "Hill-2016 column-anchored HMM; strong for digits and short codes. Accepts font_path/font_base64.", "30–120 s"},
		{"did", "Kopec-Chou trellis (proportional text, no char-boundary assumption). Best for mixed-width fonts. Accepts charset_preset (incl. digits), font_path/font_base64.", "15–60 s"},
		{"varfont", "Variable-font axis fitting; use when font weight/width is unknown.", "30–120 s"},
		{"perspective", "Mosaic photographed at an angle. Supply quad corners (quad field) OR set auto_quad=true for automatic corner detection. Accepts font_path/font_base64, font_size, block_size, beam_width, rect_size_w/h, workers.", "15–60 s"},
		{"reference", "Depix-style per-phase reference matching; strong for short codes. Accepts known_visible_text (recorded for future font calibration), font_path/font_base64.", "5–30 s"},
		{"blind", "Fully zero-config (auto font, auto block, auto gamma); slowest but needs nothing.", "30–120 s"},
		{"ensemble", "Runs mosaic+mono-hmm and keeps the best; safer but 2× slower.", "30–120 s"},
		{"multi-frame", "IBP fusion of multiple mosaics of the same content at different phases.", "30–120 s"},
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal methods: %w", err)
	}
	return resourceJSON("unpixel://methods", string(b)), nil
}

// envelopeEntry is one operating-envelope note.
type envelopeEntry struct {
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

func handleEnvelopeResource(_ context.Context, _ *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	entries := []envelopeEntry{
		{
			Category: "synthetic (fixture)",
			Summary:  "Recovers well on images created with GIMP/GEGL Pixelize at block 4–32, Liberation Sans/Mono, English text. Typical distance < 0.05.",
		},
		{
			Category: "real-world (screenshot redactions)",
			Summary:  "Recovery rate 0/N on typical internet screenshots as of 2026. Font calibration gap, JPEG noise, and sub-pixel rendering are the main barriers.",
		},
		{
			Category: "blurred redactions",
			Summary:  "Gaussian-blur recovery works on synthetic inputs; fails on real images with compression noise.",
		},
		{
			Category: "perspective / photographed",
			Summary:  "DecodePerspective (method=perspective) supports both manual quad annotation (quad field) and automatic corner detection (auto_quad=true). auto_quad works when the redaction is a single convex region on a roughly-uniform background. Recovery on real photos is experimental.",
		},
		{
			Category: "custom fonts",
			Summary:  "unpixel_decode (methods: mono-hmm, window-hmm, trained-hmm, did, reference, perspective) and unpixel_render accept font_path (local TTF/OTF) or font_base64 (base64-encoded TTF/OTF). Precedence: custom font data > bundled font name > decoder default.",
		},
		{
			Category: "variable fonts",
			Summary:  "DecodeVarFont requires a caller-supplied variable font file and known text for calibration (calibration mode) to be effective.",
		},
		{
			Category: "expected use",
			Summary:  "UnPixel is a research tool. It reliably recovers short monospace text redactions from synthetic mosaic images with known fonts. Treat real-world results as best-effort.",
		},
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal operating envelope: %w", err)
	}
	return resourceJSON("unpixel://operating-envelope", string(b)), nil
}

// resourceJSON builds a ReadResourceResult carrying a JSON text payload.
func resourceJSON(uri, text string) *mcpsdk.ReadResourceResult {
	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{
			{URI: uri, MIMEType: "application/json", Text: text},
		},
	}
}
