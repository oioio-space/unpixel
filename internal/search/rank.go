// Package search (rank.go) provides Top-N ranking and confidence scoring
// for the candidates produced by GuidedDFS.
package search

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/oioio-space/unpixel"
)

// totalScorePool bounds how many candidates (those with the lowest marginal
// score) are re-scored with the expensive whole-image TotalScore for final
// ranking. The true answer has a near-zero marginal score, so it sits among the
// lowest-marginal candidates and stays in this pool; the cap keeps the
// disambiguation cost flat regardless of how many candidates passed the
// threshold. It must be selected by marginal score alone — biasing the pool
// toward "more complete" strings would evict the real answer (which carries
// spaces, hence fewer glyphs) in favour of longer coincidental matches.
const totalScorePool = 512

// nonSpaceCount returns the number of non-whitespace runes in s.
func nonSpaceCount(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// compareEval is the total order used to rank candidates for the final answer.
// Lower image score wins first. At an equal score — which happens often, because
// a correct prefix scores ~0 on its marginal band just like the full string —
// the candidate explaining MORE of the redaction wins (more non-whitespace
// glyphs), so "go run" beats the prefix "go". Ties then prefer the shorter
// string (no trailing-space padding) and finally lexical order for determinism.
func compareEval(a, b unpixel.Eval) int {
	if c := cmp.Compare(a.Score, b.Score); c != 0 {
		return c
	}
	if c := cmp.Compare(nonSpaceCount(b.Guess), nonSpaceCount(a.Guess)); c != 0 {
		return c // more non-whitespace glyphs first
	}
	if c := cmp.Compare(len(a.Guess), len(b.Guess)); c != 0 {
		return c // shorter (less trailing padding) first
	}
	return cmp.Compare(a.Guess, b.Guess)
}

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
	slices.SortStableFunc(sorted, compareEval)
	return sorted[:min(n, len(sorted))]
}

// RankFinal ranks cands for the final answer at one offset, returning the top n
// and the whole-image TotalScore of the winner (used to compare offsets).
//
// It first drops all-whitespace candidates and orders the rest by marginal score
// (compareEval), then re-scores the best totalScorePool of them with ts.TotalScore
// and sorts that pool by whole-image fidelity. This resolves the ties that the
// marginal score leaves — a correct prefix and the complete string both score
// ~0 marginally, but only the complete string explains the whole redaction. The
// returned candidates keep their marginal Score; only their order reflects the
// total score. bestTotal is +1 (worst) when no substantive candidate exists.
// lm, when non-nil, scores linguistic plausibility (higher = better) and breaks
// ties between candidates whose whole-image totals are within lmTieBand.
func RankFinal(ctx context.Context, ts TotalScorer, cands []unpixel.Eval, offset unpixel.Offset, n int, lm func(string) float64) (top []unpixel.Eval, bestTotal float64) {
	subs := substantiveOnly(cands)
	if n <= 0 || len(subs) == 0 {
		return nil, 1
	}
	// Pool by marginal score alone (lowest first; neutral length/lexical
	// tie-break) so the real answer — low marginal score, but not the "most
	// complete" string — is never evicted before it can be total-scored.
	slices.SortStableFunc(subs, func(a, b unpixel.Eval) int {
		if c := cmp.Compare(a.Score, b.Score); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.Guess), len(b.Guess)); c != 0 {
			return c
		}
		return cmp.Compare(a.Guess, b.Guess)
	})
	pool := subs[:min(len(subs), totalScorePool)]

	type scored struct {
		e     unpixel.Eval
		total float64
		lm    float64
	}
	ranked := make([]scored, len(pool))
	for i, e := range pool {
		s := scored{e: e, total: ts.TotalScore(ctx, e.Guess, offset)}
		if lm != nil {
			s.lm = lm(e.Guess)
		}
		ranked[i] = s
	}
	// Rank by whole-image fidelity; within lmTieBand of equal total, the language
	// prior (higher = more plausible) breaks the tie, then compareEval.
	const lmTieBand = 0.01
	slices.SortStableFunc(ranked, func(a, b scored) int {
		if d := a.total - b.total; d < -lmTieBand || d > lmTieBand {
			return cmp.Compare(a.total, b.total)
		}
		if lm != nil {
			if c := cmp.Compare(b.lm, a.lm); c != 0 { // higher plausibility first
				return c
			}
		}
		return compareEval(a.e, b.e)
	})

	top = make([]unpixel.Eval, 0, min(n, len(ranked)))
	for i := 0; i < len(ranked) && i < n; i++ {
		top = append(top, ranked[i].e)
	}
	return top, ranked[0].total
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
