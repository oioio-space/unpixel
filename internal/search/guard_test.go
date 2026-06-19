package search

import (
	"context"
	"testing"

	"github.com/oioio-space/unpixel"
)

// TestSubstantive checks the whitespace predicate that guards result selection.
func TestSubstantive(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		" ":     false,
		"   ":   false,
		"\t ":   false,
		"a":     true,
		" a":    true,
		"a ":    true,
		"hi yo": true,
	}
	for in, want := range cases {
		if got := Substantive(in); got != want {
			t.Errorf("Substantive(%q): got %v, want %v", in, got, want)
		}
	}
}

// TestSubstantiveOnly verifies all-whitespace candidates are dropped while order
// and substantive entries are preserved.
func TestSubstantiveOnly(t *testing.T) {
	in := []unpixel.Eval{
		{Guess: " ", Score: 0},
		{Guess: "a", Score: 0.1},
		{Guess: "  ", Score: 0},
		{Guess: " b", Score: 0.2},
	}
	got := substantiveOnly(in)
	want := []string{"a", " b"}
	if len(got) != len(want) {
		t.Fatalf("substantiveOnly: got %d entries, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Guess != w {
			t.Errorf("substantiveOnly[%d].Guess: got %q, want %q", i, got[i].Guess, w)
		}
	}
}

// fakeTotal is a TotalScorer returning preset whole-image scores by guess.
type fakeTotal struct{ totals map[string]float64 }

func (f fakeTotal) TotalScore(_ context.Context, guess string, _ unpixel.Offset) float64 {
	if v, ok := f.totals[guess]; ok {
		return v
	}
	return 1
}

func TestNonSpaceCount(t *testing.T) {
	cases := map[string]int{"": 0, "  ": 0, "go run": 5, "abc": 3, " a b ": 2}
	for in, want := range cases {
		if got := nonSpaceCount(in); got != want {
			t.Errorf("nonSpaceCount(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestRankFinal_picksCompleteOverPrefix verifies that among candidates tying at
// the same (zero) marginal score, RankFinal selects the one with the lowest
// whole-image TotalScore — the complete "go run", not a correct prefix ("go r")
// or a coincidental same-length match ("dcnrun").
func TestRankFinal_picksCompleteOverPrefix(t *testing.T) {
	cands := []unpixel.Eval{
		{Guess: "go", Score: 0},
		{Guess: "go r", Score: 0},
		{Guess: "dcnrun", Score: 0},
		{Guess: "go run", Score: 0},
		{Guess: "   ", Score: 0}, // all-whitespace must be dropped
	}
	ts := fakeTotal{totals: map[string]float64{
		"go": 0.12, "go r": 0.09, "dcnrun": 0.09, "go run": 0.0,
	}}
	top, bestTotal := RankFinal(t.Context(), ts, cands, unpixel.Offset{}, 5)
	if len(top) == 0 || top[0].Guess != "go run" {
		t.Fatalf("RankFinal top[0] = %+v, want \"go run\"", top)
	}
	if bestTotal != 0 {
		t.Errorf("bestTotal = %v, want 0", bestTotal)
	}
	for _, e := range top {
		if !Substantive(e.Guess) {
			t.Errorf("RankFinal returned all-whitespace candidate %q", e.Guess)
		}
	}
}

// spaceScorer scores any all-whitespace guess at a perfect 0 (the degenerate
// blank-matches-blank case) and any substantive guess at 0.1. It models the
// failure mode guard #2 fixes: a blank candidate looks like a perfect match.
type spaceScorer struct{}

func (spaceScorer) Eval(_ context.Context, guess, _ string, _ unpixel.Offset) EvalResult {
	if Substantive(guess) {
		return EvalResult{Score: 0.1}
	}
	return EvalResult{Score: 0}
}

// TestSearchOffsets_skipsAllWhitespace runs the full per-offset search with a
// scorer where blank guesses score a perfect 0, and asserts the authoritative
// result never selects an all-whitespace guess and never reports confidence 1
// from one — the bug guard #2 closes.
func TestSearchOffsets_skipsAllWhitespace(t *testing.T) {
	cfg := unpixel.Config{
		Charset:        "a ",
		MaxLength:      3,
		BlockSize:      2,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		Workers:        1,
	}

	out := make(chan unpixel.Progress, 256)
	results := make(chan unpixel.Result, 16)
	var got []unpixel.Result
	resDone := make(chan struct{})
	go func() {
		for r := range results {
			got = append(got, r)
		}
		close(resDone)
	}()
	go func() {
		for range out { //nolint:revive // drain progress
		}
	}()

	searchOffsets(t.Context(), spaceScorer{}, cfg, out, results, GuidedDFS)
	close(out)
	close(results)
	<-resDone

	if len(got) == 0 {
		t.Fatal("no results produced")
	}
	for _, r := range got {
		if r.BestGuess != "" && !Substantive(r.BestGuess) {
			t.Errorf("offset %v: BestGuess %q is all-whitespace", r.Offset, r.BestGuess)
		}
		if r.Confidence == 1.0 {
			t.Errorf("offset %v: confidence 1.0 from a blank candidate", r.Offset)
		}
		for _, e := range r.TopN {
			if !Substantive(e.Guess) {
				t.Errorf("offset %v: TopN holds all-whitespace candidate %q", r.Offset, e.Guess)
			}
		}
	}
	if got[0].BestGuess == "" {
		t.Error("BestGuess empty: expected a substantive 'a' candidate to win")
	}
}
