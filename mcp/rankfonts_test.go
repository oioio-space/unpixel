package mcpserver_test

import (
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

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

// TestRankFonts_emptyTextError verifies that an empty known_text returns an error.
func TestRankFonts_emptyTextError(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	_, err = mcpserver.RankFonts(ctx, img, "")
	if err == nil {
		t.Error("RankFonts(emptyText): want error, got nil")
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
