package mcpserver_test

import (
	"image"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
)

// mosaicFixtureMCP renders text with the named bundled font at the given block
// size and pixelates it in memory. No file or network I/O is performed.
func mosaicFixtureMCP(t *testing.T, fontName, text string, block int) image.Image {
	t.Helper()
	var fontData []byte
	for _, f := range fonts.All() {
		if f.Name == fontName {
			fontData = f.Data
			break
		}
	}
	if fontData == nil {
		t.Fatalf("mosaicFixtureMCP: font %q not found in bundled fonts", fontName)
	}
	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("mosaicFixtureMCP: build renderer: %v", err)
	}
	cfg := unpixel.Config{BlockSize: block}
	if err := defaults.Wire(&cfg); err != nil {
		t.Fatalf("mosaicFixtureMCP: wire defaults: %v", err)
	}
	rendered, _, err := r.Render(text, cfg.Style)
	if err != nil {
		t.Fatalf("mosaicFixtureMCP: render %q: %v", text, err)
	}
	return cfg.Pixelator.Pixelate(rendered, 0, 0)
}

// TestRankFonts_returnsRankedList verifies that RankFonts returns at least one
// entry for a known fixture and known text.
func TestRankFonts_returnsRankedList(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.RankFonts(ctx, img, "go")
	if err != nil {
		t.Fatalf("RankFonts: %v", err)
	}
	if len(report.Ranked) == 0 {
		t.Fatal("RankFonts: empty ranked list")
	}
	if report.Best == "" {
		t.Error("RankFonts: Best is empty")
	}
	// Best must be the first entry.
	if report.Best != report.Ranked[0].Font {
		t.Errorf("RankFonts: Best = %q, want Ranked[0].Font = %q", report.Best, report.Ranked[0].Font)
	}
}

// TestRankFonts_blindNoKnownText verifies that RankFonts with an empty known_text
// returns a valid histogram-only ranking (blind mode).
func TestRankFonts_blindNoKnownText(t *testing.T) {
	img := mosaicFixtureMCP(t, "Liberation Mono", "ABC123", 6)
	rep, err := mcpserver.RankFonts(t.Context(), img, "") // blind: empty known_text
	if err != nil {
		t.Fatalf("blind RankFonts: %v", err)
	}
	if len(rep.Ranked) == 0 || rep.Best == "" {
		t.Errorf("blind RankFonts returned empty ranking")
	}
}

// TestRankFonts_scoresInRange verifies that all returned scores are in [0, 1].
func TestRankFonts_scoresInRange(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.RankFonts(ctx, img, "go")
	if err != nil {
		t.Fatalf("RankFonts: %v", err)
	}
	for _, e := range report.Ranked {
		if e.Score < 0 || e.Score > 1 {
			t.Errorf("RankFonts: %q score = %.4f, want in [0, 1]", e.Font, e.Score)
		}
	}
}
