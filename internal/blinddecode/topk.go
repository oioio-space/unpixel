package blinddecode

import (
	"cmp"
	"slices"
)

// topKByPrior scores each word in words with prior, sorts descending by score,
// and returns the top k entries (or all entries when len(words) ≤ k). The
// returned slice is a fresh allocation; words is not modified. Deduplication is
// the caller's responsibility.
//
// It is used by wordPool (per rune-length tier) and elisionCandidates (per
// prefix+delta tier) to avoid duplicating the score→sort→cap pattern.
func topKByPrior(words []string, k int, prior func(string) float64) []string {
	type scored struct {
		word  string
		score float64
	}
	pairs := make([]scored, len(words))
	for i, w := range words {
		pairs[i] = scored{word: w, score: prior(w)}
	}
	slices.SortFunc(pairs, func(a, b scored) int {
		return cmp.Compare(b.score, a.score) // descending
	})
	lim := min(k, len(pairs))
	out := make([]string, lim)
	for i := range lim {
		out[i] = pairs[i].word
	}
	return out
}
