// Package search implements offset discovery and the GuidedDFS search strategy
// for the unpixel pipeline.
package search

import (
	"cmp"
	"context"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/oioio-space/unpixel"
)

// resolveWorkers returns the concurrency level for offset fan-out: the configured
// value when positive, otherwise runtime.GOMAXPROCS. Engine.Run already fills
// Workers via applyDefaults, but DiscoverOffsets and the strategies are also
// callable directly (tests, benchmarks, custom drivers) with an unset Workers,
// so the fallback is resolved here too rather than assumed upstream.
func resolveWorkers(cfg unpixel.Config) int {
	if cfg.Workers > 0 {
		return cfg.Workers
	}
	return runtime.GOMAXPROCS(0)
}

// forEachIndex runs fn(i) for i in [0, n) using up to workers goroutines, or
// sequentially when workers <= 1. It returns after every invocation completes.
// fn must be safe for concurrent use and must only touch storage unique to its
// index so no further synchronisation is needed.
func forEachIndex(ctx context.Context, n, workers int, fn func(i int)) {
	if workers <= 1 || n <= 1 {
		for i := range n {
			if ctx.Err() != nil {
				return
			}
			fn(i)
		}
		return
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range n {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			fn(i)
		})
	}
	wg.Wait()
}

// EvalResult carries the outcome of scoring one candidate string.
type EvalResult struct {
	// Score is the marginal-region diff score in [0, 1]. Lower is better.
	Score float64
	// TooBig indicates the rendered candidate is wider than the redacted image.
	TooBig bool
}

// Scorer evaluates a candidate string at a given grid offset.
// prevGuess is the parent guess whose rendered image is compared to find the
// marginal change region (empty string on the first call at each depth).
type Scorer interface {
	Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) EvalResult
}

// TotalScorer is an optional Scorer capability: it scores the WHOLE rendered
// candidate against the WHOLE redacted image (no marginal cropping). The search
// uses it only to rank the final answer — a correct prefix or a coincidental
// glyph match drives the marginal Eval score to ~0, so marginal score cannot
// tell "go" or a fluke from the complete "go run", but the total score can.
// A Scorer that does not implement it falls back to marginal-score ranking.
type TotalScorer interface {
	TotalScore(ctx context.Context, guess string, offset unpixel.Offset) float64
}

// node is an internal DFS stack entry.
type node struct {
	guess  string
	result EvalResult
}

// ensureThresholdFor returns cfg with ThresholdFor set to a sensible default
// if the caller did not supply one, without mutating the original.
func ensureThresholdFor(cfg unpixel.Config) unpixel.Config {
	if cfg.ThresholdFor == nil {
		t, st := cfg.Threshold, cfg.SpaceThreshold
		cfg.ThresholdFor = func(ch rune) float64 {
			if ch == ' ' {
				return st
			}
			return t
		}
	}
	return cfg
}

// GuidedDFS runs a depth-first search over candidate strings, calling emit for
// every candidate that passes the threshold gate.
//
// faithful: preload.ts guessRecursive — render parent; if tooBig prune;
// append each charset char; keep score < threshold (< spaceThreshold for ' ');
// sort ascending; recurse.
func GuidedDFS(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	emit func(unpixel.Eval),
) {
	cfg = ensureThresholdFor(cfg)
	// Seed: evaluate every single character.
	seeds := evalChildren(ctx, scorer, cfg, offset, "")
	for _, s := range seeds {
		emit(unpixel.Eval{
			Guess:  s.guess,
			Score:  s.result.Score,
			TooBig: s.result.TooBig,
		})
	}
	for _, s := range seeds {
		if ctx.Err() != nil {
			return
		}
		guessRecursive(ctx, scorer, cfg, offset, s, emit)
	}
}

// guessRecursive extends the given node depth-first.
// The node's result already contains tooBig, so no extra parent eval is needed.
func guessRecursive(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parent node,
	emit func(unpixel.Eval),
) {
	if ctx.Err() != nil {
		return
	}
	// faithful: preload.ts — if tooBig prune immediately.
	if parent.result.TooBig {
		return
	}
	if len(parent.guess) >= cfg.MaxLength {
		return
	}

	children := evalChildren(ctx, scorer, cfg, offset, parent.guess)
	for _, child := range children {
		emit(unpixel.Eval{
			Guess:  child.guess,
			Score:  child.result.Score,
			TooBig: child.result.TooBig,
		})
	}
	for _, child := range children {
		guessRecursive(ctx, scorer, cfg, offset, child, emit)
	}
}

// evalChildren scores each character in cfg.Charset appended to parentGuess,
// keeps those below the threshold, and returns them sorted ascending by score.
// parentGuess is passed as prevGuess to the scorer for marginal-region diffing.
func evalChildren(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parentGuess string,
) []node {
	var children []node
	for _, ch := range cfg.Charset {
		if ctx.Err() != nil {
			return children
		}
		next := parentGuess + string(ch)
		res := scorer.Eval(ctx, next, parentGuess, offset)
		if res.Score < cfg.ThresholdFor(ch) {
			children = append(children, node{guess: next, result: res})
		}
	}
	slices.SortFunc(children, func(a, b node) int {
		return cmp.Compare(a.result.Score, b.result.Score)
	})
	return children
}

// DiscoverOffsets probes all blockSize² grid origins and returns those whose
// best single-character score is below cfg.Threshold, sorted ascending.
// emit is called with an EventOffsetProbed event after each origin is scored;
// pass nil to suppress progress events.
//
// faithful: preload.ts offset discovery loop — 8×8=64 origins, keep best < threshold.
func DiscoverOffsets(ctx context.Context, scorer Scorer, cfg unpixel.Config, emit func(unpixel.Progress)) []unpixel.Offset {
	cfg = ensureThresholdFor(cfg)
	bs := cfg.BlockSize
	total := bs * bs
	if total == 0 {
		return nil
	}

	// One slot per grid origin, indexed i = y*bs + x, so concurrent probes write
	// disjoint storage and the survivor scan stays deterministic.
	scored := make([]unpixel.Offset, total)
	survived := make([]bool, total)
	var done atomic.Int64

	forEachIndex(ctx, total, resolveWorkers(cfg), func(i int) {
		if ctx.Err() != nil {
			return
		}
		x, y := i%bs, i/bs
		offset := unpixel.Offset{X: x, Y: y}
		bestScore := 1.0
		evaluated := 0
		for _, ch := range cfg.Charset {
			if ctx.Err() != nil {
				return
			}
			res := scorer.Eval(ctx, string(ch), "", offset)
			evaluated++
			if res.Score < bestScore {
				bestScore = res.Score
			}
		}
		probed := unpixel.Offset{X: x, Y: y, Score: bestScore}
		scored[i] = probed
		survived[i] = bestScore < cfg.Threshold
		if emit != nil {
			emit(unpixel.Progress{
				Kind:         unpixel.EventOffsetProbed,
				Offset:       probed,
				OffsetsDone:  int(done.Add(1)),
				OffsetsTotal: total,
				Evaluated:    evaluated,
			})
		}
	})

	var offsets []unpixel.Offset
	for i := range total {
		if survived[i] {
			offsets = append(offsets, scored[i])
		}
	}
	slices.SortFunc(offsets, func(a, b unpixel.Offset) int {
		return cmp.Compare(a.Score, b.Score)
	})
	return offsets
}
