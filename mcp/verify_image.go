package mcpserver

import (
	"context"
	"fmt"
	"image"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
)

// toolVerifyImage is the tool descriptor for unpixel_verify_image.
var toolVerifyImage = &mcpsdk.Tool{
	Name: "unpixel_verify_image",
	Description: "Physically verifies a RESTORED image (e.g. from an external diffusion restorer) " +
		"against a redaction by re-applying the forward operator (re-pixelate at the mosaic block) " +
		"and comparing. Returns a distance in [0,1] and a match flag (distance < 0.10). " +
		"Use it as an anti-hallucination gate: a faithful restoration re-pixelates back to the " +
		"observed redaction (match=true); a hallucination does not — except where the mosaic is " +
		"genuinely ambiguous (no physical check can disambiguate that). NOT a recoverer: it scores " +
		"a proposed restoration, it does not produce one.",
}

// verifyImageInput is the JSON input for unpixel_verify_image.
type verifyImageInput struct {
	// RedactionPath is the filesystem path of the pixelated redaction image.
	RedactionPath string `json:"redaction_path" jsonschema:"Filesystem path to the pixelated redaction PNG/JPEG"`
	// RestoredPath is the filesystem path of the proposed restored (clean) image.
	RestoredPath string `json:"restored_path" jsonschema:"Filesystem path to the proposed restored (clean) PNG/JPEG to verify"`
	// BlockSize overrides the auto-detected mosaic block size (0 = auto).
	BlockSize int `json:"block_size,omitzero" jsonschema:"Override mosaic block size in pixels (0 = auto-detect)"`
}

// ImageVerifyReport is the output of unpixel_verify_image.
type ImageVerifyReport struct {
	// Distance is the whole-image distance in [0,1] (lower = more consistent).
	Distance float64 `json:"distance"`
	// Match reports a confident physical match (Distance < 0.10).
	Match bool `json:"match"`
}

// handleVerifyImage is the tool handler for unpixel_verify_image.
func handleVerifyImage(ctx context.Context, _ *mcpsdk.CallToolRequest, in verifyImageInput) (*mcpsdk.CallToolResult, ImageVerifyReport, error) {
	redacted, err := loadImage(in.RedactionPath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_image: load redaction: %w", err)), ImageVerifyReport{}, nil
	}
	restored, err := loadImage(in.RestoredPath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_image: load restored: %w", err)), ImageVerifyReport{}, nil
	}
	report, err := VerifyImageMCP(ctx, redacted, restored, in.BlockSize)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_image: %w", err)), ImageVerifyReport{}, nil
	}
	return toolJSON(report)
}

// VerifyImageMCP verifies restored against redacted with [unpixel.VerifyImage]
// and returns an [ImageVerifyReport]. blockSize pins the mosaic block size
// (0 = auto). It is the testable core of the unpixel_verify_image MCP tool.
func VerifyImageMCP(ctx context.Context, redacted, restored image.Image, blockSize int) (ImageVerifyReport, error) {
	var opts []unpixel.Option
	if blockSize > 0 {
		opts = append(opts, unpixel.WithBlockSize(blockSize))
	}
	v, err := unpixel.VerifyImage(ctx, redacted, restored, opts...)
	if err != nil {
		return ImageVerifyReport{}, err
	}
	return ImageVerifyReport{Distance: v.Distance, Match: v.Match}, nil
}
