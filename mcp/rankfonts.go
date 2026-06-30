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
		"Optionally takes a known_text string; without it, ranks blind by pixelated-signature histogram. " +
		"NOT a full decode — it does not recover the hidden text. " +
		"Latency: fast (< 500 ms for all bundled fonts).",
}

// rankFontsInput is the JSON input schema for unpixel_rank_fonts.
type rankFontsInput struct {
	// ImagePath is the filesystem path of the pixelated image.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the pixelated PNG or JPEG image"`
	// KnownText is cleartext whose glyphs are visible in the image (or a close approximation).
	// When empty, ranking falls back to blind histogram-only mode (no glyph fingerprint).
	KnownText string `json:"known_text,omitzero" jsonschema:"Known cleartext whose glyphs appear in the image; omit for blind histogram ranking"`
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
// When knownText is empty, ranking uses histogram-only blind mode (no glyph
// fingerprint). It is the testable core of the unpixel_rank_fonts MCP tool.
func RankFonts(ctx context.Context, img image.Image, knownText string) (RankFontsReport, error) {
	all := fonts.All()
	named := make([]fontrank.NamedFont, len(all))
	for i, f := range all {
		named[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}

	// Blind mode: no known text → histogram-only ranking (no glyph fingerprint).
	if knownText == "" {
		histScores, err := fontrank.RankFonts(ctx, img, named)
		if err != nil {
			return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: rank fonts: %w", err)
		}
		return reportFromScores(histScores), nil
	}

	// Known-text mode: blend histogram + glyph-metric fingerprint.
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

	histScores, err := fontrank.RankFonts(ctx, img, named)
	if err != nil {
		return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: rank fonts: %w", err)
	}

	fpRanked := fontrank.RankByMetrics(fp, named)
	fpDist := make(map[string]float64, len(fpRanked))
	for _, e := range fpRanked {
		fpDist[e.Name] = e.Dist
	}

	blended := make([]fontrank.FontScore, len(histScores))
	for i, hs := range histScores {
		score := hs.Score
		if fpScore, ok := fpDist[hs.Name]; ok {
			score = (hs.Score + fpScore) / 2
		}
		blended[i] = fontrank.FontScore{Name: hs.Name, Score: score}
	}
	return reportFromScores(blended), nil
}

// reportFromScores converts a slice of FontScore into a RankFontsReport, setting
// Best to the first entry's font name (which is already ranked best-first).
func reportFromScores(scores []fontrank.FontScore) RankFontsReport {
	ranked := make([]FontRankEntry, len(scores))
	for i, s := range scores {
		ranked[i] = FontRankEntry{Font: s.Name, Score: s.Score}
	}
	best := ""
	if len(ranked) > 0 {
		best = ranked[0].Font
	}
	return RankFontsReport{Ranked: ranked, Best: best}
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
