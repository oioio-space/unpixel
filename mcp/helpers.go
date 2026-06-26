package mcpserver

// helpers.go — shared utilities for the MCP server tools.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/rectify"
)

// parseQuad parses the perspective quad string format "x0,y0 x1,y1 x2,y2 x3,y3"
// (top-left, top-right, bottom-right, bottom-left) and returns the four
// rectify.Point corners. It returns an error for malformed input.
func parseQuad(s string) ([4]rectify.Point, error) {
	parts := strings.Fields(s)
	if len(parts) != 4 {
		return [4]rectify.Point{}, fmt.Errorf("quad must have 4 'x,y' pairs separated by spaces, got %d", len(parts))
	}
	var corners [4]rectify.Point
	for i, p := range parts {
		xy := strings.Split(p, ",")
		if len(xy) != 2 {
			return [4]rectify.Point{}, fmt.Errorf("corner %d: expected 'x,y', got %q", i, p)
		}
		x, err := strconv.ParseFloat(strings.TrimSpace(xy[0]), 64)
		if err != nil {
			return [4]rectify.Point{}, fmt.Errorf("corner %d x: %w", i, err)
		}
		y, err := strconv.ParseFloat(strings.TrimSpace(xy[1]), 64)
		if err != nil {
			return [4]rectify.Point{}, fmt.Errorf("corner %d y: %w", i, err)
		}
		corners[i] = rectify.Point{X: x, Y: y}
	}
	return corners, nil
}

// defaultRenderStyle returns the Style used by rank-fonts glyph rendering:
// 28 pt, no bold, no letter spacing. It matches exemplarFontSize in fontrank.
func defaultRenderStyle() unpixel.Style {
	return unpixel.Style{FontSize: 28}
}
