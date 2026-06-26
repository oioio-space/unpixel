package mcpserver

// rankfonts.go — unpixel_rank_fonts tool: scores every bundled font against a
// mosaic redaction by glyph-metric fingerprinting and returns them ranked best-first.

import (
	"context"
	"fmt"
	"image"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fontrank"
	"github.com/oioio-space/unpixel/internal/render"
)

// toolRankFonts is the tool descriptor for unpixel_rank_fonts.
var toolRankFonts = &mcpsdk.Tool{
	Name: "unpixel_rank_fonts",
	Description: "Ranks all bundled fonts by how well their glyph metrics match a mosaic-pixelated image. " +
		"Use it as a fast pre-filter before a full decode to find the most likely font (top-1 or top-3), " +
		"then pass that font name to unpixel_decode via a subsequent call with method=mosaic or method=mono-hmm. " +
		"Requires a known_text string whose glyphs appear in the image. " +
		"NOT a full decode — it does not recover the hidden text. " +
		"Latency: fast (< 500 ms for all bundled fonts).",
}

// rankFontsInput is the JSON input schema for unpixel_rank_fonts.
type rankFontsInput struct {
	// ImagePath is the filesystem path of the pixelated image.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the pixelated PNG or JPEG image"`
	// KnownText is cleartext whose glyphs are visible in the image (or a close approximation).
	KnownText string `json:"known_text" jsonschema:"Known cleartext string whose glyphs appear in the image"`
}

// FontRankEntry is one ranked result in RankFontsReport.
type FontRankEntry struct {
	// Font is the bundled font name.
	Font string `json:"font"`
	// Score is the L1 histogram distance between the font's glyph profile and the image (lower = better match).
	Score float64 `json:"score"`
}

// RankFontsReport is the output of unpixel_rank_fonts.
type RankFontsReport struct {
	// Ranked lists all bundled fonts sorted best-first (lowest score first).
	Ranked []FontRankEntry `json:"ranked"`
	// Best is the name of the top-ranked font.
	Best string `json:"best"`
}

// RankFonts scores every bundled font against img using glyph-metric
// fingerprinting and histogram comparison, returning them ranked best-first.
// knownText must contain glyphs that appear in the image. It is the testable
// core of the unpixel_rank_fonts MCP tool.
func RankFonts(ctx context.Context, img image.Image, knownText string) (RankFontsReport, error) {
	if knownText == "" {
		return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: known_text must not be empty")
	}

	r, err := render.NewXImage()
	if err != nil {
		return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: build renderer: %w", err)
	}
	rendered, sentinelX, err := r.Render(knownText, defaultRenderStyle())
	if err != nil {
		return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: render known text: %w", err)
	}
	fp, err := fontrank.FingerprintFromGlyphs(rendered, knownText, sentinelX)
	if err != nil {
		return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: fingerprint: %w", err)
	}

	all := fonts.All()
	named := make([]fontrank.NamedFont, len(all))
	for i, f := range all {
		named[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}
	histScores, err := fontrank.RankFonts(ctx, img, named)
	if err != nil {
		return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: rank fonts: %w", err)
	}

	fpRanked := fontrank.RankByMetrics(fp, named)
	fpDist := make(map[string]float64, len(fpRanked))
	for _, e := range fpRanked {
		fpDist[e.Name] = e.Dist
	}

	ranked := make([]FontRankEntry, len(histScores))
	for i, hs := range histScores {
		score := hs.Score
		if fpScore, ok := fpDist[hs.Name]; ok {
			score = (hs.Score + fpScore) / 2
		}
		ranked[i] = FontRankEntry{Font: hs.Name, Score: score}
	}
	best := ""
	if len(ranked) > 0 {
		best = ranked[0].Font
	}
	return RankFontsReport{Ranked: ranked, Best: best}, nil
}

// handleRankFonts is the tool handler for unpixel_rank_fonts.
func handleRankFonts(ctx context.Context, _ *mcpsdk.CallToolRequest, in rankFontsInput) (*mcpsdk.CallToolResult, RankFontsReport, error) {
	img, err := loadImage(in.ImagePath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_rank_fonts: load image: %w", err)), RankFontsReport{}, nil
	}
	report, err := RankFonts(ctx, img, in.KnownText)
	if err != nil {
		return errResult(err), RankFontsReport{}, nil
	}
	return toolJSON(report)
}
