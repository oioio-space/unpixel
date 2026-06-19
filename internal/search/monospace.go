// Package search (monospace.go) provides MonospaceStrategy, a fast path for
// fixed-advance (monospace) fonts. In a monospace face every glyph occupies the
// same cell, so a character's pixels don't shift with what precedes it — the
// position-error cascade that forces a proportional font into a backtracking DFS
// disappears. MonospaceDFS therefore classifies each position greedily (no
// backtracking) and evaluates the whole charset for a position in parallel,
// turning an exponential search into O(positions × charset) with charset-wide
// concurrency. Best for code/secret redactions, which are usually monospace.
package search

import (
	"cmp"
	"context"
	"image"
	"slices"

	"github.com/oioio-space/unpixel"
)

// MonospaceStrategy implements unpixel.Strategy using offset discovery followed
// by greedy, per-position parallel classification (MonospaceDFS) per offset.
type MonospaceStrategy struct{}

// NewMonospaceStrategy returns a MonospaceStrategy.
func NewMonospaceStrategy() MonospaceStrategy { return MonospaceStrategy{} }

// Search runs offset discovery then MonospaceDFS per surviving offset.
func (MonospaceStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	searchOffsets(ctx, NewPipelineScorer(redacted, cfg), cfg, out, results, MonospaceDFS)
}

// monoBeam is how many best paths MonospaceDFS keeps per position. Monospace
// cells are near-independent, so a narrow beam recovers the line while staying
// far cheaper than a full DFS; >1 guards against a single greedy wrong turn.
const monoBeam = 3

// MonospaceDFS extends the guess position by position, keeping only the best
// monoBeam paths at each step (a narrow beam — valid because monospace cells are
// near-independent), and emits every evaluated candidate so the final ranking
// can arbitrate. The whole charset for a position is evaluated concurrently.
func MonospaceDFS(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	emit func(unpixel.Eval),
) {
	cfg = ensureThresholdFor(cfg)
	frontier := []string{""}
	for depth := 0; depth < cfg.MaxLength; depth++ {
		if ctx.Err() != nil {
			return
		}
		var next []node
		for _, prefix := range frontier {
			children := evalChildrenPar(ctx, scorer, cfg, offset, prefix)
			for _, c := range children {
				emit(unpixel.Eval{Guess: c.guess, Score: c.result.Score, TooBig: c.result.TooBig})
				if !c.result.TooBig {
					next = append(next, c)
				}
			}
		}
		if len(next) == 0 {
			return
		}
		slices.SortStableFunc(next, func(a, b node) int {
			return cmp.Compare(a.result.Score, b.result.Score)
		})
		frontier = frontier[:0]
		for i := 0; i < len(next) && i < monoBeam; i++ {
			frontier = append(frontier, next[i].guess)
		}
	}
}

// evalChildrenPar is the concurrent counterpart of evalChildren: it scores every
// charset character appended to parentGuess in parallel, keeps those below the
// threshold, and returns them sorted ascending by score (deterministically).
func evalChildrenPar(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parentGuess string,
) []node {
	chars := []rune(cfg.Charset)
	results := make([]*node, len(chars))
	forEachIndex(ctx, len(chars), resolveWorkers(cfg), func(i int) {
		if ctx.Err() != nil {
			return
		}
		ch := chars[i]
		next := parentGuess + string(ch)
		res := scorer.Eval(ctx, next, parentGuess, offset)
		if res.Score < cfg.ThresholdFor(ch) {
			results[i] = &node{guess: next, result: res}
		}
	})
	var children []node
	for _, r := range results {
		if r != nil {
			children = append(children, *r)
		}
	}
	slices.SortStableFunc(children, func(a, b node) int {
		return cmp.Compare(a.result.Score, b.result.Score)
	})
	return children
}
