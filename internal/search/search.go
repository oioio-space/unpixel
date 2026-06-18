// Package search implements offset discovery and the GuidedDFS search strategy
// for the unpixel pipeline.
package search

import (
	"cmp"
	"context"
	"slices"

	"github.com/oioio-space/unpixel"
)

// EvalResult carries the outcome of scoring one candidate string.
type EvalResult struct {
	// Score is the marginal-region diff score in [0, 1]. Lower is better.
	Score float64
	// TotalScore is the whole-image diff score (used for display only).
	TotalScore float64
	// TooBig indicates the rendered candidate is wider than the redacted image.
	TooBig bool
}

// Scorer evaluates a candidate string at a given grid offset.
// prevGuess is the parent guess whose rendered image is compared to find the
// marginal change region (empty string on the first call at each depth).
type Scorer interface {
	Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) EvalResult
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
			Guess:      s.guess,
			Score:      s.result.Score,
			TotalScore: s.result.TotalScore,
			TooBig:     s.result.TooBig,
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
			Guess:      child.guess,
			Score:      child.result.Score,
			TotalScore: child.result.TotalScore,
			TooBig:     child.result.TooBig,
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
	done := 0
	var offsets []unpixel.Offset

	for y := range bs {
		for x := range bs {
			if ctx.Err() != nil {
				return offsets
			}
			offset := unpixel.Offset{X: x, Y: y}
			bestScore := 1.0
			evaluated := 0
			for _, ch := range cfg.Charset {
				if ctx.Err() != nil {
					return offsets
				}
				res := scorer.Eval(ctx, string(ch), "", offset)
				evaluated++
				if res.Score < bestScore {
					bestScore = res.Score
				}
			}
			if bestScore < cfg.Threshold {
				offsets = append(offsets, unpixel.Offset{X: x, Y: y, Score: bestScore})
			}
			done++
			if emit != nil {
				emit(unpixel.Progress{
					Kind:         unpixel.EventOffsetProbed,
					Offset:       unpixel.Offset{X: x, Y: y, Score: bestScore},
					OffsetsDone:  done,
					OffsetsTotal: total,
					Evaluated:    evaluated,
				})
			}
		}
	}

	slices.SortFunc(offsets, func(a, b unpixel.Offset) int {
		return cmp.Compare(a.Score, b.Score)
	})
	return offsets
}
