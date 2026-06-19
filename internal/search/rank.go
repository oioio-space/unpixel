// Package search (rank.go) provides Top-N ranking and confidence scoring
// for the candidates produced by GuidedDFS.
package search

import (
	"cmp"
	"slices"
	"strings"

	"github.com/oioio-space/unpixel"
)

// Substantive reports whether guess contains at least one non-whitespace rune.
//
// An all-whitespace guess renders to a blank image, which the metric scores at
// ~0 against any blank region of the redacted image — a perfect-looking but
// meaningless match. Such candidates must never be selected as a recovery or
// reported in the Top-N (they would yield a misleading confidence of 1). They
// remain valid interior search nodes, however: a leading space is a legitimate
// prefix of a real answer such as " hello".
func Substantive(guess string) bool { return strings.TrimSpace(guess) != "" }

// substantiveOnly returns the candidates that pass Substantive, in their
// original order. It allocates a new slice and never aliases cands.
func substantiveOnly(cands []unpixel.Eval) []unpixel.Eval {
	var out []unpixel.Eval
	for _, e := range cands {
		if Substantive(e.Guess) {
			out = append(out, e)
		}
	}
	return out
}

// RankTopN returns the n candidates with the lowest Score from cands, sorted
// ascending by (Score, Guess) so results are deterministic when scores tie.
// The original cands slice is never mutated.
//
// If n <= 0 or cands is empty, RankTopN returns nil.
// If n >= len(cands), all candidates are returned (sorted).
func RankTopN(cands []unpixel.Eval, n int) []unpixel.Eval {
	if n <= 0 || len(cands) == 0 {
		return nil
	}
	// Clone so the caller's slice is not mutated.
	sorted := slices.Clone(cands)
	slices.SortStableFunc(sorted, func(a, b unpixel.Eval) int {
		if c := cmp.Compare(a.Score, b.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.Guess, b.Guess)
	})
	return sorted[:min(n, len(sorted))]
}

// Confidence derives a confidence score and an ambiguity score from a ranked
// Top-N slice as returned by RankTopN.
//
// conf is 1 − top[0].Score: a value in [0, 1] where 1 means a perfect match.
// ambiguity is top[1].Score − top[0].Score: the gap to the second-best candidate.
// A large ambiguity means the best guess is well-separated from the alternatives.
//
// Both values are 0 when top is empty. ambiguity is 0 when len(top) < 2.
func Confidence(top []unpixel.Eval) (conf, ambiguity float64) {
	if len(top) == 0 {
		return 0, 0
	}
	conf = 1 - top[0].Score
	if len(top) >= 2 {
		ambiguity = top[1].Score - top[0].Score
	}
	return conf, ambiguity
}
