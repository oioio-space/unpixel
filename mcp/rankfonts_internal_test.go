package mcpserver

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/fontrank"
)

// TestReportFromScores_sortsBestFirst pins the fix for the known-text blend bug:
// the blended slice is built in histogram order, not blended-score order, so
// reportFromScores must re-sort so Best/Ranked[0] is the true minimum.
func TestReportFromScores_sortsBestFirst(t *testing.T) {
	// Deliberately unsorted input (as the known-text blended slice would be).
	in := []fontrank.FontScore{
		{Name: "Carlito", Score: 0.30},
		{Name: "Liberation Sans", Score: 0.10},
		{Name: "Caladea", Score: 0.20},
	}
	rep := reportFromScores(in)

	if got, want := rep.Best, "Liberation Sans"; got != want {
		t.Errorf("Best = %q; want %q (lowest score)", got, want)
	}
	// Ranked must be ascending by Score.
	for i := 1; i < len(rep.Ranked); i++ {
		if rep.Ranked[i].Score < rep.Ranked[i-1].Score {
			t.Errorf("Ranked not ascending at %d: %v", i, rep.Ranked)
		}
	}
}

// TestReportFromScores_tieBreakByName keeps the order deterministic when scores tie.
func TestReportFromScores_tieBreakByName(t *testing.T) {
	in := []fontrank.FontScore{
		{Name: "Zeta", Score: 0.5},
		{Name: "Alpha", Score: 0.5},
	}
	rep := reportFromScores(in)
	if got, want := rep.Best, "Alpha"; got != want {
		t.Errorf("Best = %q; want %q (tie broken by name)", got, want)
	}
}
