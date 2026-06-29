package mcpserver

// leakscan.go — unpixel_leak_scan tool: runs the file-level leak pre-pass
// before any pixel solving. It wraps [leak.Scan] and, for EXIF thumbnail hits,
// returns the recovered image as MCP image content in addition to the JSON
// report.

import (
	"bytes"
	"context"
	"fmt"
	"image/png"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel/internal/leak"
)

// LeakReport is the result of a [LeakScan] call. It is also the JSON output of
// the unpixel_leak_scan MCP tool.
type LeakReport struct {
	// Found is true when a leak was recovered.
	Found bool `json:"found"`
	// Source is the leak channel that produced the result (e.g. "office-text").
	Source string `json:"source,omitzero"`
	// Text is the recovered plaintext (empty for image-only leaks).
	Text string `json:"text,omitzero"`
	// Confidence is in [0, 1]; 1.0 means byte-identical to the original.
	Confidence float64 `json:"confidence,omitzero"`
	// Notes carries human-readable diagnostic remarks from the detector.
	Notes []string `json:"notes,omitzero"`
}

// LeakScan runs the file-level leak pre-pass on the image at path and returns
// a [LeakReport]. It is the testable one-call wrapper for the unpixel_leak_scan
// MCP tool.
//
// visibleText is optional caller-supplied text already visible in the image;
// it enables the partial-redaction detector. Pass an empty string to omit it.
//
// A non-nil error is returned only when path cannot be read. When no leak is
// found, [LeakReport.Found] is false and err is nil.
func LeakScan(path, visibleText string) (LeakReport, error) {
	res, found, err := leak.Scan(path, leak.Options{VisibleText: visibleText})
	if err != nil {
		return LeakReport{}, err
	}
	if !found {
		return LeakReport{Found: false}, nil
	}
	return LeakReport{
		Found:      true,
		Source:     string(res.Source),
		Text:       res.Text,
		Confidence: res.Confidence,
		Notes:      res.Notes,
	}, nil
}

// toolLeakScan is the tool descriptor for unpixel_leak_scan.
var toolLeakScan = &mcpsdk.Tool{
	Name: "unpixel_leak_scan",
	Description: "Runs a file-level leak pre-pass on an image before pixel solving. " +
		"Checks for four leak channels: EXIF embedded thumbnail (JPEG), " +
		"text under a filled rectangle (PDF), body text in OOXML documents (docx/pptx), " +
		"and visible-text-assisted partial redaction (PNG). " +
		"Returns a JSON report with found, source, text, confidence, and notes. " +
		"For EXIF thumbnail hits the recovered image is also returned as MCP image content. " +
		"When found=false the image should be passed to unpixel_decode for pixel solving.",
}

// leakScanInput is the JSON input schema for unpixel_leak_scan.
type leakScanInput struct {
	// ImagePath is the absolute or cwd-relative filesystem path of the image.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the image (PNG, JPEG, PDF, or Office document)"`
	// VisibleText is optional text already visible in the image that enables the
	// partial-redaction detector. Leave empty to omit.
	VisibleText string `json:"visible_text,omitzero" jsonschema:"Optional caller-supplied text visible in the image (enables partial-redaction detector)"`
}

// handleLeakScan is the MCP handler for unpixel_leak_scan.
func handleLeakScan(_ context.Context, _ *mcpsdk.CallToolRequest, in leakScanInput) (*mcpsdk.CallToolResult, LeakReport, error) {
	res, found, err := leak.Scan(in.ImagePath, leak.Options{VisibleText: in.VisibleText})
	if err != nil {
		return errResult(fmt.Errorf("unpixel_leak_scan: %w", err)), LeakReport{}, nil
	}

	report := LeakReport{
		Found:      found,
		Source:     string(res.Source),
		Text:       res.Text,
		Confidence: res.Confidence,
		Notes:      res.Notes,
	}

	// For EXIF thumbnail hits, also return the recovered image as MCP image
	// content. If the (in-memory, already-decoded) thumbnail fails to re-encode,
	// note it rather than silently dropping it — the text report is still useful
	// and the caller learns the image is missing.
	var thumbPNG []byte
	if found && res.Source == leak.SourceEXIFThumbnail && res.Image != nil {
		var buf bytes.Buffer
		if encErr := png.Encode(&buf, res.Image); encErr == nil {
			thumbPNG = buf.Bytes()
		} else {
			report.Notes = append(report.Notes, "thumbnail recovered but PNG re-encode failed: "+encErr.Error())
		}
	}

	result, _, err := toolJSON(report)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_leak_scan: marshal: %w", err)), LeakReport{}, nil
	}

	content := result.Content
	if thumbPNG != nil {
		content = append(content, &mcpsdk.ImageContent{
			Data:     thumbPNG,
			MIMEType: "image/png",
		})
	}

	return &mcpsdk.CallToolResult{Content: content}, report, nil
}
