package search_test

import (
	"context"
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// TestBeamStrategy_recoversTarget verifies that BeamStrategy finds a known target
// when it is the best-scoring candidate at every depth.
func TestBeamStrategy_recoversTarget(t *testing.T) {
	// "ab" is the target: score("a")=0.1, score("ab")=0.05, all others=1.0.
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a":  {Score: 0.1},
		"ab": {Score: 0.05},
	}}
	cfg := unpixel.Config{
		Charset:        "abcdefghijklmnopqrstuvwxyz ",
		MaxLength:      5,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		BeamWidth:      4,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var candidates []unpixel.Eval
	search.BeamDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		candidates = append(candidates, e)
	})

	guesses := make([]string, len(candidates))
	for i, e := range candidates {
		guesses[i] = e.Guess
	}
	if !slices.Contains(guesses, "ab") {
		t.Errorf("BeamDFS did not find 'ab'; found: %v", guesses)
	}
}

// TestBeamStrategy_greedyMiss documents that K=1 (greedy beam) may fail to find
// a target that is not best at depth 1.
//
// With K=1, beam keeps only the top-1 candidate per level. If "a" scores better
// than "b" at depth 1 but "ba" is the true target (best at depth 2), the beam
// prunes "b" and never discovers "ba".
func TestBeamStrategy_greedyMiss(t *testing.T) {
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a":  {Score: 0.05}, // best at depth 1 → kept
		"b":  {Score: 0.10}, // second at depth 1 → pruned by K=1
		"ba": {Score: 0.01}, // true target, never reached
	}}
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      5,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		BeamWidth:      1, // K=1 greedy
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var candidates []unpixel.Eval
	search.BeamDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		candidates = append(candidates, e)
	})

	guesses := make([]string, len(candidates))
	for i, e := range candidates {
		guesses[i] = e.Guess
	}
	// With K=1 the beam prunes "b", so "ba" must NOT be found.
	if slices.Contains(guesses, "ba") {
		t.Errorf("K=1 beam unexpectedly found 'ba'; it should have been pruned")
	}
}

// TestBeamStrategy_determinism verifies that two identical BeamDFS runs produce
// the same ordered candidate list (stable sort by Score then Guess).
func TestBeamStrategy_determinism(t *testing.T) {
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a":  {Score: 0.1},
		"b":  {Score: 0.1}, // tie → resolved by Guess order
		"ab": {Score: 0.05},
		"ba": {Score: 0.05},
	}}
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      3,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		BeamWidth:      4,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	collect := func() []string {
		var evals []unpixel.Eval
		search.BeamDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
			evals = append(evals, e)
		})
		out := make([]string, len(evals))
		for i, e := range evals {
			out[i] = e.Guess
		}
		return out
	}

	first := collect()
	second := collect()
	if !slices.Equal(first, second) {
		t.Errorf("BeamDFS is not deterministic:\nrun1=%v\nrun2=%v", first, second)
	}
}

// TestBeamStrategy_ctxCancel verifies that a cancelled context stops BeamDFS promptly.
func TestBeamStrategy_ctxCancel(t *testing.T) {
	// All guesses pass so the beam would recurse to MaxLength without cancellation.
	scores := make(map[string]search.EvalResult)
	for _, ch := range "abcdefghijklmnopqrstuvwxyz" {
		scores[string(ch)] = search.EvalResult{Score: 0.1}
	}
	scorer := &mockScorer{scores: scores}
	cfg := unpixel.Config{
		Charset:        "abcdefghijklmnopqrstuvwxyz",
		MaxLength:      20,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		BeamWidth:      16,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Must return promptly without hanging.
	search.BeamDFS(ctx, scorer, cfg, offset, func(unpixel.Eval) {})
}

// TestBeamStrategy_emitsDone verifies that BeamStrategy.Search always emits
// EventDone as its terminal progress event.
func TestBeamStrategy_emitsDone(t *testing.T) {
	inner, cfg, _, _ := buildScorerFixture(t)
	cfg.Charset = "ab"
	cfg.MaxLength = 2
	cfg.BeamWidth = 4

	redacted := inner.RedactedImage()

	progress, _ := drainAndRun(t, search.NewBeamStrategy(0), redacted, cfg)

	gotDone := slices.ContainsFunc(progress, func(p unpixel.Progress) bool {
		return p.Kind == unpixel.EventDone
	})
	if !gotDone {
		t.Error("BeamStrategy.Search did not emit EventDone")
	}
}

// TestBeamStrategy_topNPopulated verifies that results produced by BeamStrategy
// have a non-nil TopN slice when candidates were found.
func TestBeamStrategy_topNPopulated(t *testing.T) {
	inner, cfg, _, _ := buildScorerFixture(t)
	cfg.Charset = "abcdefghijklmnopqrstuvwxyz "
	cfg.MaxLength = 3
	cfg.BeamWidth = 8
	cfg.TopN = 5

	redacted := inner.RedactedImage()

	_, results := drainAndRun(t, search.NewBeamStrategy(0), redacted, cfg)

	var found bool
	for _, r := range results {
		if len(r.TopN) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("BeamStrategy produced no results with TopN populated")
	}
}
