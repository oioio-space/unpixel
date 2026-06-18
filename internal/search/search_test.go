package search_test

import (
	"context"
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// mockScorer returns scripted scores for specific guesses; unknown guesses get 1.0.
type mockScorer struct {
	scores map[string]search.EvalResult
}

func (m *mockScorer) Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) search.EvalResult {
	if r, ok := m.scores[guess]; ok {
		return r
	}
	return search.EvalResult{Score: 1.0, TooBig: false}
}

// --- GuidedDFS pruning tests ---

// TestGuidedDFS_findsCorrectGuess verifies that the DFS finds a known correct
// guess when the scorer returns a low score for it and high scores for others.
func TestGuidedDFS_findsCorrectGuess(t *testing.T) {
	// "ab" is the target: score("a")=0.1, score("ab")=0.05, all others=1.0.
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a":  {Score: 0.1, TooBig: false},
		"ab": {Score: 0.05, TooBig: false},
	}}

	cfg := unpixel.Config{
		Charset:        "abcdefghijklmnopqrstuvwxyz ",
		MaxLength:      5,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var found []string
	search.GuidedDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		found = append(found, e.Guess)
	})

	if !slices.Contains(found, "ab") {
		t.Errorf("GuidedDFS did not find 'ab'; found: %v", found)
	}
}

// TestGuidedDFS_prunesWhenTooBig verifies that branches marked TooBig are not
// extended.
func TestGuidedDFS_prunesWhenTooBig(t *testing.T) {
	// "a" is tooBig — no children should be explored.
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a": {Score: 0.1, TooBig: true},
	}}
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      5,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var found []string
	search.GuidedDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		found = append(found, e.Guess)
	})

	// "aa", "ab" etc. must not appear since "a" was tooBig.
	for _, g := range found {
		if len(g) > 1 {
			t.Errorf("expected no extension of tooBig guess, got %q", g)
		}
	}
}

// TestGuidedDFS_spaceUsesHigherThreshold verifies the space character uses
// SpaceThreshold (0.5) not Threshold (0.25).
func TestGuidedDFS_spaceUsesHigherThreshold(t *testing.T) {
	// "a " scores 0.3 — above normal threshold but below space threshold.
	const spaceChar = " "
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a":             {Score: 0.1, TooBig: false},
		"a" + spaceChar: {Score: 0.3, TooBig: false},
	}}
	cfg := unpixel.Config{
		Charset:        "a" + spaceChar,
		MaxLength:      5,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var found []string
	search.GuidedDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		found = append(found, e.Guess)
	})

	if !slices.Contains(found, "a ") {
		t.Errorf("GuidedDFS did not find 'a ' using space threshold; found: %v", found)
	}
}

// TestGuidedDFS_thresholdGate verifies candidates above threshold are pruned.
func TestGuidedDFS_thresholdGate(t *testing.T) {
	// "b" scores 0.3 — above threshold, must be pruned (not extended).
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"b":  {Score: 0.3, TooBig: false},
		"ba": {Score: 0.1, TooBig: false}, // would be found if "b" weren't pruned
	}}
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      5,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var found []string
	search.GuidedDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		found = append(found, e.Guess)
	})

	if slices.Contains(found, "ba") {
		t.Error("GuidedDFS should not find 'ba' when 'b' is above threshold")
	}
}

// TestGuidedDFS_maxLength verifies the DFS stops at MaxLength.
func TestGuidedDFS_maxLength(t *testing.T) {
	// All single-char guesses score 0.1, so the DFS would recurse forever
	// if not for MaxLength=3.
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a":   {Score: 0.1},
		"aa":  {Score: 0.1},
		"aaa": {Score: 0.1},
		"ab":  {Score: 0.1},
	}}
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      3,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var found []string
	search.GuidedDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		found = append(found, e.Guess)
	})

	for _, g := range found {
		if len(g) > cfg.MaxLength {
			t.Errorf("found guess %q with length %d > MaxLength %d", g, len(g), cfg.MaxLength)
		}
	}
}

// TestGuidedDFS_ctxCancel verifies that cancelling the context stops the DFS promptly.
func TestGuidedDFS_ctxCancel(t *testing.T) {
	// A scorer that always passes, so without cancellation the DFS would run to MaxLength.
	scorer := &mockScorer{scores: func() map[string]search.EvalResult {
		m := make(map[string]search.EvalResult)
		charset := "abcdefghijklmnopqrstuvwxyz"
		for _, c := range charset {
			m[string(c)] = search.EvalResult{Score: 0.1}
		}
		return m
	}()}

	cfg := unpixel.Config{
		Charset:        "abcdefghijklmnopqrstuvwxyz",
		MaxLength:      20,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	var found []string
	search.GuidedDFS(ctx, scorer, cfg, offset, func(e unpixel.Eval) {
		found = append(found, e.Guess)
	})
	// After immediate cancellation we expect very few (or zero) results.
	// The key assertion is that the function returns at all (no deadlock/hang).
}

// TestOffsetDiscovery_keepsGoodOffsets verifies that offsets whose best single-char
// score is below threshold are kept and sorted.
func TestOffsetDiscovery_keepsGoodOffsets(t *testing.T) {
	// Offset (0,0): best score 0.1 (passes). Offset (1,0): best score 0.8 (fails).
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a": {Score: 0.1}, // only at offset (0,0) — mockScorer ignores offset
	}}
	cfg := unpixel.Config{
		Charset:   "ab",
		BlockSize: 2, // small for test speed
		Threshold: 0.25,
	}

	offsets := search.DiscoverOffsets(t.Context(), scorer, cfg, nil)
	// With blockSize=2 we try 4 origins; "a" scores 0.1 at all of them,
	// "b" scores 1.0. So all 4 offsets should survive.
	if len(offsets) == 0 {
		t.Error("DiscoverOffsets returned no offsets; expected at least one")
	}
	// Offsets must be sorted ascending by score.
	for i := 1; i < len(offsets); i++ {
		if offsets[i].Score < offsets[i-1].Score {
			t.Errorf("offsets not sorted: [%d].Score=%v > [%d].Score=%v",
				i-1, offsets[i-1].Score, i, offsets[i].Score)
		}
	}
}
