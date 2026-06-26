package mcpserver

// render.go — unpixel_render tool: rasterises a candidate string and returns
// the PNG bytes as MCP ImageContent so a multimodal LLM can inspect it visually.

import (
	"bytes"
	"context"
	"fmt"
	"image/png"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/render"
)

// toolRender is the tool descriptor for unpixel_render.
var toolRender = &mcpsdk.Tool{
	Name: "unpixel_render",
	Description: "Renders a candidate string as a PNG image using the UnPixel font pipeline " +
		"and returns it as MCP image content so a multimodal LLM can visually compare it against " +
		"a pixelated redaction. " +
		"Use it to sanity-check a decode result or to explore what a particular string looks like " +
		"in a given font before committing to a full decode. " +
		"Custom font: supply font_path (local TTF/OTF path) or font_base64 (base64-encoded TTF/OTF bytes) " +
		"to render with a non-bundled font; at most one may be set. " +
		"Font precedence: custom font data > bundled default (Liberation Sans). " +
		"NOT suitable for recovering hidden text — use unpixel_decode for that. " +
		"Latency: very fast (< 50 ms).",
}

// renderInput is the JSON input schema for unpixel_render.
type renderInput struct {
	// Text is the string to render.
	Text string `json:"text" jsonschema:"The string to render as a PNG image"`
	// FontSize is the point size (default 32).
	FontSize float64 `json:"font_size,omitzero" jsonschema:"Font size in points (default 32)"`
	// Bold renders in bold weight when true.
	Bold bool `json:"bold,omitzero" jsonschema:"Render in bold weight"`
	// LetterSpacing adds extra pixels between each glyph.
	LetterSpacing float64 `json:"letter_spacing,omitzero" jsonschema:"Extra pixels of spacing between glyphs (default 0)"`
	// FontPath is a local filesystem path to a TTF/OTF font file. Mutually
	// exclusive with font_base64. Precedence: custom font > bundled default.
	FontPath string `json:"font_path,omitzero" jsonschema:"Local TTF/OTF path for a custom font (mutually exclusive with font_base64)"`
	// FontBase64 carries raw TTF/OTF bytes encoded as standard base64. Mutually
	// exclusive with font_path.
	FontBase64 string `json:"font_base64,omitzero" jsonschema:"Raw TTF/OTF bytes as base64 (mutually exclusive with font_path)"`
}

// RenderOptions carries optional parameters for [RenderText].
type RenderOptions struct {
	// FontSize is the point size (default 32).
	FontSize float64
	// Bold renders in bold weight when true.
	Bold bool
	// LetterSpacing adds extra pixels between each glyph.
	LetterSpacing float64
	// FontData carries raw TTF/OTF bytes for a custom font. When non-nil it
	// replaces the bundled default. Use [LoadFontData] to populate this field.
	FontData []byte
}

// RenderText rasterises text and returns the PNG-encoded bytes. When
// opts.FontData is non-nil the custom font is used; otherwise the embedded
// Liberation Sans is used. It is the testable core of the unpixel_render MCP
// tool.
func RenderText(text string, opts RenderOptions) ([]byte, error) {
	if text == "" {
		return nil, fmt.Errorf("unpixel_render: text must not be empty")
	}
	fontSize := opts.FontSize
	if fontSize <= 0 {
		fontSize = 32
	}

	var (
		r   *render.XImage
		err error
	)
	if len(opts.FontData) > 0 {
		r, err = render.NewXImageFromFonts(opts.FontData, nil)
	} else {
		r, err = render.NewXImage()
	}
	if err != nil {
		return nil, fmt.Errorf("unpixel_render: build renderer: %w", err)
	}

	style := unpixel.Style{
		FontSize:      fontSize,
		Bold:          opts.Bold,
		LetterSpacing: opts.LetterSpacing,
	}
	img, _, err := r.Render(text, style)
	if err != nil {
		return nil, fmt.Errorf("unpixel_render: render %q: %w", text, err)
	}

	var buf bytes.Buffer
	if encErr := png.Encode(&buf, img); encErr != nil {
		return nil, fmt.Errorf("unpixel_render: encode PNG: %w", encErr)
	}
	return buf.Bytes(), nil
}

// handleRender is the tool handler for unpixel_render.
func handleRender(_ context.Context, _ *mcpsdk.CallToolRequest, in renderInput) (*mcpsdk.CallToolResult, struct{}, error) {
	fontData, err := LoadFontData(in.FontPath, in.FontBase64)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_render: %w", err)), struct{}{}, nil
	}

	pngBytes, err := RenderText(in.Text, RenderOptions{
		FontSize:      in.FontSize,
		Bold:          in.Bold,
		LetterSpacing: in.LetterSpacing,
		FontData:      fontData,
	})
	if err != nil {
		return errResult(err), struct{}{}, nil
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.ImageContent{
				Data:     pngBytes,
				MIMEType: "image/png",
			},
		},
	}, struct{}{}, nil
}
