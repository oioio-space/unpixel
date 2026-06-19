package search

import (
	"context"
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
)

// parallelChildrenScorer is a deterministic scorer for the par-vs-seq equivalence
// test: every rune in its charset gets a fixed, unique score derived from the
// rune's position in the charset so that the sorted output is fully predictable.
type parallelChildrenScorer struct{ charset string }

func (s *parallelChildrenScorer) Eval(_ context.Context, guess, _ string, _ unpixel.Offset) EvalResult {
	if guess == "" {
		return EvalResult{Score: 1}
	}
	last := rune(guess[len(guess)-1])
	for i, ch := range s.charset {
		if ch == last {
			// Scores in (0, 1) spaced so no two runes share the same score.
			return EvalResult{Score: float64(i+1) / float64(len(s.charset)+1)}
		}
	}
	return EvalResult{Score: 1}
}

// TestEvalChildrenParCapped_matchesSequential verifies that evalChildrenParCapped
// produces a node slice that is byte-identical (same guess strings, same scores,
// same order) to the sequential evalChildren for several charset widths.
func TestEvalChildrenParCapped_matchesSequential(t *testing.T) {
	cases := []struct {
		name    string
		charset string
		workers int
	}{
		{"narrow_1w", "abcde", 1},
		{"narrow_4w", "abcde", 4},
		{"medium_1w", "abcdefghijklmnopqrstuvwxyz ", 1},
		{"medium_4w", "abcdefghijklmnopqrstuvwxyz ", 4},
		{"wide_8w", unpixel.CharsetASCII, 8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scorer := &parallelChildrenScorer{charset: tc.charset}
			cfg := unpixel.Config{
				Charset:        tc.charset,
				MaxLength:      3,
				Threshold:      0.9, // accept almost everything so the full list is exercised
				SpaceThreshold: 0.99,
			}
			cfg = ensureThresholdFor(cfg)
			offset := unpixel.Offset{X: 0, Y: 0}

			seq := evalChildren(t.Context(), scorer, cfg, offset, "")
			par := evalChildrenParCapped(t.Context(), scorer, cfg, offset, "", tc.workers)

			if len(seq) != len(par) {
				t.Fatalf("len: seq=%d par=%d", len(seq), len(par))
			}
			for i := range seq {
				if seq[i].guess != par[i].guess {
					t.Errorf("[%d] guess: seq=%q par=%q", i, seq[i].guess, par[i].guess)
				}
				if seq[i].result.Score != par[i].result.Score {
					t.Errorf("[%d] score: seq=%f par=%f", i, seq[i].result.Score, par[i].result.Score)
				}
			}
		})
	}
}

// TestGuidedDFS_parallelMatchesSequential verifies that GuidedDFS with
// Workers=GOMAXPROCS (which triggers intra-node parallelism) produces the same
// set of emitted evals as Workers=1 (fully sequential path).
func TestGuidedDFS_parallelMatchesSequential(t *testing.T) {
	charset := "abcdefghijklmnopqrstuvwxyz "
	scorer := &parallelChildrenScorer{charset: charset}
	cfg := unpixel.Config{
		Charset:        charset,
		MaxLength:      3,
		Threshold:      0.8,
		SpaceThreshold: 0.9,
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	var seqEvals, parEvals []unpixel.Eval

	seqCfg := cfg
	seqCfg.Workers = 1
	GuidedDFS(t.Context(), scorer, seqCfg, offset, func(e unpixel.Eval) {
		seqEvals = append(seqEvals, e)
	})

	parCfg := cfg
	parCfg.Workers = 8
	GuidedDFS(t.Context(), scorer, parCfg, offset, func(e unpixel.Eval) {
		parEvals = append(parEvals, e)
	})

	if len(seqEvals) != len(parEvals) {
		t.Fatalf("eval count: seq=%d par=%d", len(seqEvals), len(parEvals))
	}
	// Sort both by (guess, score) before comparing — emit order may differ between
	// sequential and parallel because guessRecursive emits children before recursing,
	// and parallel may interleave. What must be identical is the SET of evals.
	sortEvals := func(es []unpixel.Eval) {
		slices.SortStableFunc(es, func(a, b unpixel.Eval) int {
			if a.Guess < b.Guess {
				return -1
			}
			if a.Guess > b.Guess {
				return 1
			}
			if a.Score < b.Score {
				return -1
			}
			if a.Score > b.Score {
				return 1
			}
			return 0
		})
	}
	sortEvals(seqEvals)
	sortEvals(parEvals)
	for i := range seqEvals {
		if seqEvals[i].Guess != parEvals[i].Guess || seqEvals[i].Score != parEvals[i].Score {
			t.Errorf("[%d] seq=%+v par=%+v", i, seqEvals[i], parEvals[i])
		}
	}
}
