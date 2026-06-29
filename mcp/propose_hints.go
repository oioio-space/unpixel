package mcpserver

import (
	"context"
	"fmt"
	"image"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/leak"
)

// HintsReport is the output of unpixel_propose_hints. It aggregates all
// information an LLM needs to propose candidate strings for the
// propose→physics-verify loop: a character-count estimate, detected grid
// geometry, optional redaction bounding box, a coarse charset suggestion,
// and any plaintext leaked from the file's metadata (PDF, Office).
type HintsReport struct {
	// CharCountEstimate is the estimated number of characters hidden in the
	// redaction = round(bbox width / average rendered glyph width) at the inferred
	// font size. It is a coarse cue (accurate to ±1 for typical proportional text;
	// monospace/wide fonts shift it). When no redaction bounding box is detected,
	// the full image width is used as a conservative upper bound, so the estimate
	// can be large — cross-check against block_size and the visible layout.
	CharCountEstimate int `json:"char_count_estimate"`
	// BlockSize is the detected mosaic block side length in pixels.
	BlockSize int `json:"block_size"`
	// FontSizePt is the estimated source text font size in points.
	FontSizePt float64 `json:"font_size_pt"`
	// RedactionBbox is [x0, y0, x1, y1] of the detected redaction rectangle.
	// Omitted when no bounding box was detected.
	RedactionBbox []int `json:"redaction_bbox,omitzero"`
	// CharsetHint is a coarse alphabet suggestion derived from Analyze's
	// recommended charset. Omitted when no recommendation was produced.
	CharsetHint string `json:"charset_hint,omitzero"`
	// LeakedContext is plaintext recovered from the file's metadata (e.g. PDF
	// text stream or Office body text). Omitted when no text leak was found or
	// when the file path is not available (use ProposeHints instead of
	// ProposeHintsImage to enable this field).
	LeakedContext string `json:"leaked_context,omitzero"`
}

// toolProposeHints is the tool descriptor for unpixel_propose_hints.
var toolProposeHints = &mcpsdk.Tool{
	Name: "unpixel_propose_hints",
	Description: "Aggregates hints to help an LLM propose candidate strings for " +
		"the propose→physics-verify loop: estimated character count, detected " +
		"block size and font size, optional redaction bounding box, a coarse " +
		"charset suggestion, and any plaintext leaked from file metadata " +
		"(PDF text, Office body text). Pass the returned char_count_estimate, " +
		"block_size, and charset_hint to unpixel_verify_candidates alongside " +
		"your proposed candidates.",
}

// proposeHintsInput is the JSON-decoded input for unpixel_propose_hints.
type proposeHintsInput struct {
	// ImagePath is the absolute (or cwd-relative) filesystem path of the PNG or JPEG image.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the pixelated PNG or JPEG image"`
}

// handleProposeHints is the tool handler for unpixel_propose_hints.
func handleProposeHints(_ context.Context, _ *mcpsdk.CallToolRequest, in proposeHintsInput) (*mcpsdk.CallToolResult, HintsReport, error) {
	report, err := ProposeHints(in.ImagePath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_propose_hints: %w", err)), HintsReport{}, nil
	}
	return toolJSON(report)
}

// ProposeHints returns a [HintsReport] for the image at path. It is the
// full-featured variant of [ProposeHintsImage]: it loads the image, runs the
// same analysis, and additionally calls [leak.Scan] on the raw file so that
// text leaked from PDF text streams or Office body text is surfaced in
// [HintsReport.LeakedContext].
func ProposeHints(path string) (HintsReport, error) {
	img, err := loadImage(path)
	if err != nil {
		return HintsReport{}, fmt.Errorf("load image: %w", err)
	}

	rep, err := ProposeHintsImage(img)
	if err != nil {
		return HintsReport{}, err
	}

	// Scan the raw file for text leaks (PDF / Office only).
	result, found, err := leak.Scan(path, leak.Options{})
	if err != nil {
		// Unreadable file: non-fatal — the image was already decoded above.
		return rep, nil
	}
	if found && isTextLeak(result.Source) && result.Text != "" {
		rep.LeakedContext = result.Text
	}
	return rep, nil
}

// ProposeHintsImage returns a [HintsReport] derived solely from img. It runs
// block/font/bbox analysis and estimates the character count from the redaction
// bounding box width and the average rendered glyph width. No file I/O is
// performed; use [ProposeHints] when the source file path is available so that
// metadata leaks (PDF, Office) can be surfaced in [HintsReport.LeakedContext].
func ProposeHintsImage(img image.Image) (HintsReport, error) {
	analysis, err := Analyze(img)
	if err != nil {
		return HintsReport{}, fmt.Errorf("analyze: %w", err)
	}

	rep := HintsReport{
		BlockSize:     analysis.BlockSize,
		FontSizePt:    analysis.FontSizePt,
		RedactionBbox: analysis.RedactionBbox,
		CharsetHint:   analysis.RecommendedCharset,
	}

	// Obtain a default renderer so capacity.Analyze can render glyphs.
	cfg := unpixel.Config{}
	if unpixel.DefaultComponents != nil {
		if err := unpixel.DefaultComponents(&cfg); err != nil {
			return HintsReport{}, fmt.Errorf("wire default components: %w", err)
		}
	}
	if cfg.Renderer == nil {
		return HintsReport{}, fmt.Errorf("no renderer available: import _ \"github.com/oioio-space/unpixel/defaults\"")
	}

	block := max(1, analysis.BlockSize)
	fontSize := analysis.FontSizePt
	if fontSize <= 0 {
		fontSize = 11 // safe fallback matching panel fixtures
	}

	// Estimate character count from redaction width and average glyph advance.
	// Render a mid-width sample character ("m") to get the per-char pixel width;
	// this render also validates the renderer + font size.
	style := unpixel.Style{FontSize: fontSize}
	_, sentinelX, err := cfg.Renderer.Render("m", style)
	if err != nil {
		return HintsReport{}, fmt.Errorf("render sample glyph: %w", err)
	}
	charWidthPx := sentinelX // sentinelX = text advance (no padding in zero Style)
	if charWidthPx <= 0 {
		charWidthPx = block // guard against degenerate renderer output
	}

	// Round to nearest (not truncate) so a 1.5-glyph-wide bbox estimates 2, not 1
	// — integer truncation would systematically under-count.
	bboxW := bboxWidth(analysis.RedactionBbox, img.Bounds())
	rep.CharCountEstimate = max(1, (bboxW+charWidthPx/2)/charWidthPx)

	return rep, nil
}

// bboxWidth returns the width of the redaction bounding box in pixels.
// When bbox is empty, it falls back to the full image width.
func bboxWidth(bbox []int, bounds image.Rectangle) int {
	if len(bbox) == 4 {
		w := bbox[2] - bbox[0]
		if w > 0 {
			return w
		}
	}
	return bounds.Dx()
}

// isTextLeak reports whether source is a text-bearing metadata channel
// (PDF text stream or Office body text). EXIF thumbnails and partial
// redactions are not surfaced as LeakedContext.
//
// Maintenance: extend this when a new text-bearing leak.Source is added to
// internal/leak, or that channel will be silently excluded from LeakedContext.
func isTextLeak(source leak.Source) bool {
	return source == leak.SourcePDFText || source == leak.SourceOfficeText
}
