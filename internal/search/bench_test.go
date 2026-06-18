package search_test

import (
	"context"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// sinkEvals defeats dead-code elimination for GuidedDFS benchmark results.
var sinkEvals []unpixel.Eval

// scriptedScorer returns fixed scores for any single-character guess that
// appears in its map; all others return 1.0 (pruned). Multi-character guesses
// inherit the last character's score, so the DFS can recurse predictably
// without real rendering.
type scriptedScorer struct {
	charScore map[rune]float64
}

func (s *scriptedScorer) Eval(_ context.Context, guess, _ string, _ unpixel.Offset) search.EvalResult {
	if guess == "" {
		return search.EvalResult{Score: 1.0}
	}
	lastRune := rune(guess[len(guess)-1])
	if score, ok := s.charScore[lastRune]; ok {
		return search.EvalResult{Score: score}
	}
	return search.EvalResult{Score: 1.0}
}

// BenchmarkGuidedDFS benchmarks the search bookkeeping cost (evalChildren,
// sorting, recursive dispatch) using a mock scorer that returns scripted scores
// — no real rendering is involved. Sub-benchmarks vary the charset width and
// hence the branching factor.
func BenchmarkGuidedDFS(b *testing.B) {
	cases := []struct {
		name    string
		charset string
		// fraction of charset chars that pass the threshold
		passFrac float64
	}{
		// Narrow charset: 10-char alphabet, all pass — high branching factor but
		// pruned quickly by MaxLength=3.
		{"charset10_allpass", "abcdefghij", 0.1},
		// Full default charset, half pass.
		{"charset27_halfpass", "abcdefghijklmnopqrstuvwxyz ", 0.2},
	}

	offset := unpixel.Offset{X: 3, Y: 3}

	for _, tc := range cases {
		// Build a scorer that returns passFrac * threshold for every rune so
		// that all characters pass (score < threshold).
		scores := make(map[rune]float64, len(tc.charset))
		for _, ch := range tc.charset {
			scores[ch] = tc.passFrac
		}
		scorer := &scriptedScorer{charScore: scores}

		cfg := unpixel.Config{
			Charset:        tc.charset,
			MaxLength:      3, // keep bench duration bounded
			Threshold:      0.25,
			SpaceThreshold: 0.5,
		}

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var evals []unpixel.Eval
				search.GuidedDFS(context.Background(), scorer, cfg, offset, func(e unpixel.Eval) {
					evals = append(evals, e)
				})
				sinkEvals = evals
			}
		})
	}
}
