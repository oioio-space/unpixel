package mcpserver_test

import (
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestVerifyCandidates_rerankReordersByLM verifies that a positive rerank_weight
// produces a well-formed fused ranked list and that Pick always remains a
// physical match regardless of language re-ordering.
func TestVerifyCandidates_rerankReordersByLM(t *testing.T) {
	// Build an in-memory mosaic of the real word so both candidates verify, then
	// confirm a positive rerank weight can promote the linguistically-plausible
	// candidate. Use a fixture where the true text is a dictionary word.
	img := mosaicFixtureMCP(t, "Liberation Sans", "the", 6)

	// rerank_weight 0 → physics order (baseline, unchanged behaviour).
	base, err := mcpserver.VerifyCandidates(t.Context(), img, []string{"the", "tho"}, 6, "lower", 0)
	if err != nil {
		t.Fatalf("VerifyCandidates(0): %v", err)
	}
	if base.Best == "" {
		t.Fatal("empty Best")
	}

	// rerank_weight > 0 → fused order is well-formed and Pick stays physical.
	fused, err := mcpserver.VerifyCandidates(t.Context(), img, []string{"the", "tho"}, 6, "lower", 0.1)
	if err != nil {
		t.Fatalf("VerifyCandidates(0.1): %v", err)
	}
	if len(fused.Ranked) != 2 {
		t.Fatalf("fused ranked len = %d; want 2", len(fused.Ranked))
	}
	// Pick is a physical decision: it must be identical whether or not the LM
	// re-ranks the candidates (rerank_weight must not change which candidate is
	// the lowest-distance physical match).
	if fused.Pick != base.Pick {
		t.Errorf("Pick changed under rerank: weight 0 = %q, weight 0.1 = %q (Pick must stay physical)", base.Pick, fused.Pick)
	}
	// And when a Pick exists it must be a physical Match present in Ranked.
	if fused.Pick != "" {
		var matched bool
		for _, rc := range fused.Ranked {
			if rc.Text == fused.Pick && rc.Match {
				matched = true
			}
		}
		if !matched {
			t.Errorf("Pick %q is not a physical Match in Ranked", fused.Pick)
		}
	}
}
