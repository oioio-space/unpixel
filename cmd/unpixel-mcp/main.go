// Command unpixel-mcp runs the UnPixel MCP server on the stdio transport.
//
// The server exposes the full UnPixel tool suite; see package [mcpserver] for
// the authoritative list. Current tools include:
//
//   - unpixel_analyze: analyzes a pixelated/blurred image (block grid, colorspace, blur, …).
//   - unpixel_decode: recovers hidden text using a selectable decoder method (auto, mosaic,
//     blurred, mono-hmm, window-hmm, trained-hmm, did, varfont, perspective, reference,
//     blind, ensemble, multi-frame). Supports async=true for long decodes.
//   - unpixel_job_result: polls the result of an async decode job.
//   - unpixel_job_cancel: cancels a running async decode job.
//   - unpixel_verify_candidates: scores candidate strings against a mosaic redaction.
//   - unpixel_rank_fonts: ranks bundled fonts by glyph-metric match against a redaction.
//   - unpixel_render: renders text with a named or custom font for visual comparison.
//   - unpixel_calibrate: estimates mosaic parameters (block size, grid offset, font) from an image.
//
// Usage:
//
//	unpixel-mcp
//
// The server reads JSON-RPC 2.0 messages from stdin and writes responses to
// stdout, following the Model Context Protocol stdio transport specification.
package main

import (
	"context"
	"log"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// version is the server version string embedded in MCP implementation metadata.
// It is set at build time via -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	srv := mcpserver.NewServer(version)
	if err := srv.Run(context.Background(), &mcpsdk.StdioTransport{}); err != nil {
		log.Fatalf("unpixel-mcp: %v", err)
	}
}
