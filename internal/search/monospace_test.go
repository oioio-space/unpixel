package search_test

import (
	"slices"
	"testing"

	"github.com/oioio-space/unpixel/internal/search"
)

// TestMonospaceStrategy_recovers checks the greedy per-position fast path
// recovers the plaintext from the synthetic "ab" redaction (it is correct on any
// font; it is merely tuned for fixed-advance ones).
func TestMonospaceStrategy_recovers(t *testing.T) {
	inner, cfg, _, _ := buildScorerFixture(t)
	cfg.Charset = "ab cde"
	cfg.MaxLength = 3
	cfg.TopN = 5
	redacted := inner.RedactedImage()

	_, results := drainAndRun(t, search.NewMonospaceStrategy(), redacted, cfg)

	var guesses []string
	for _, r := range results {
		guesses = append(guesses, r.BestGuess)
		for _, e := range r.Candidates {
			guesses = append(guesses, e.Guess)
		}
	}
	if !slices.Contains(guesses, "ab") {
		t.Errorf("MonospaceStrategy did not recover %q; guesses=%v", "ab", guesses)
	}
}

// TestMonospaceStrategy_deterministic: two runs give identical results.
func TestMonospaceStrategy_deterministic(t *testing.T) {
	inner, cfg, _, _ := buildScorerFixture(t)
	cfg.Charset = "ab cde"
	cfg.MaxLength = 3
	redacted := inner.RedactedImage()

	_, a := drainAndRun(t, search.NewMonospaceStrategy(), redacted, cfg)
	_, b := drainAndRun(t, search.NewMonospaceStrategy(), redacted, cfg)
	if resultSignature(a) != resultSignature(b) {
		t.Error("MonospaceStrategy not deterministic across runs")
	}
}
