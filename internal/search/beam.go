// Package search (beam.go) adds BeamDFS, a beam-search variant of GuidedDFS that
// bounds the branching factor by retaining only the best cfg.BeamWidth candidates
// per depth level, and BeamStrategy, the unpixel.Strategy that drives it.
package search

import (
	"cmp"
	"context"
	"image"
	"slices"
	"sync"
	"time"

	"github.com/oioio-space/unpixel"
)

// BeamDFS runs a breadth-first beam search over candidate strings. At each depth
// level it evaluates every charset extension of the current beam, emits every
// extension that passes the threshold gate, then retains only the best
// cfg.BeamWidth candidates (lowest score, ties broken by guess) to expand at the
// next level. cfg.BeamWidth <= 0 falls back to unpixel.DefaultBeamWidth.
//
// Unlike GuidedDFS, which expands every passing candidate depth-first, BeamDFS
// caps the work per level to BeamWidth parents, trading recall for a fixed
// branching factor; a larger width recovers more of GuidedDFS's recall.
func BeamDFS(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	emit func(unpixel.Eval),
) {
	cfg = ensureThresholdFor(cfg)
	width := cfg.BeamWidth
	if width <= 0 {
		width = unpixel.DefaultBeamWidth
	}

	// Depth 1: seed the beam with every single character that passes the gate.
	level := evalChildren(ctx, scorer, cfg, offset, "")
	emitNodes(level, emit)
	beam := topBeam(level, width)

	for len(beam) > 0 {
		if ctx.Err() != nil {
			return
		}
		var next []node
		for _, parent := range beam {
			if ctx.Err() != nil {
				return
			}
			// faithful: prune candidates that are already too big or maxed out.
			if parent.result.TooBig || len(parent.guess) >= cfg.MaxLength {
				continue
			}
			children := evalChildren(ctx, scorer, cfg, offset, parent.guess)
			emitNodes(children, emit)
			next = append(next, children...)
		}
		beam = topBeam(next, width)
	}
}

// emitNodes reports each node as an unpixel.Eval, preserving slice order.
func emitNodes(nodes []node, emit func(unpixel.Eval)) {
	for _, n := range nodes {
		emit(unpixel.Eval{
			Guess:  n.guess,
			Score:  n.result.Score,
			TooBig: n.result.TooBig,
		})
	}
}

// topBeam sorts nodes ascending by score (ties broken by guess for determinism)
// and returns at most width of them. It sorts nodes in place.
func topBeam(nodes []node, width int) []node {
	slices.SortFunc(nodes, func(a, b node) int {
		if c := cmp.Compare(a.result.Score, b.result.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.guess, b.guess)
	})
	if len(nodes) > width {
		nodes = nodes[:width]
	}
	return nodes
}

// BeamStrategy implements unpixel.Strategy using offset discovery followed by
// BeamDFS for each surviving offset. It wraps the scorer in a CachingScorer so
// the shared render→pixelate→crop prefix work is memoised across the search
// (controlled by cfg.CacheSize; zero disables the cache).
type BeamStrategy struct {
	// width overrides cfg.BeamWidth when positive; 0 defers to the Config value
	// (or unpixel.DefaultBeamWidth when that is also unset).
	width int
}

// NewBeamStrategy returns a BeamStrategy. A positive width overrides
// cfg.BeamWidth; pass 0 to defer to the Config (or the default).
func NewBeamStrategy(width int) BeamStrategy { return BeamStrategy{width: width} }

// Search runs offset discovery then BeamDFS per surviving offset.
// It always emits a final EventDone event before returning.
func (s BeamStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	if s.width > 0 {
		cfg.BeamWidth = s.width
	}
	if cfg.BeamWidth <= 0 {
		cfg.BeamWidth = unpixel.DefaultBeamWidth
	}
	scorer := NewCachingScorer(NewPipelineScorer(redacted, cfg), cfg.CacheSize)
	searchOffsets(ctx, scorer, cfg, out, results, BeamDFS)
}

// dfsFunc is the shared signature of GuidedDFS and BeamDFS, letting a strategy
// reuse the offset-discovery / progress-emitting scaffolding for either search.
type dfsFunc func(ctx context.Context, scorer Scorer, cfg unpixel.Config, offset unpixel.Offset, emit func(unpixel.Eval))

// offsetOutcome holds the per-offset search result, produced concurrently and
// merged deterministically after all offsets complete.
type offsetOutcome struct {
	candidates []unpixel.Eval
	topN       []unpixel.Eval
	confidence float64
	ambiguity  float64
	bestTotal  float64 // whole-image score of topN[0]; ranks offsets against each other
	offset     unpixel.Offset
	done       bool
}

// searchOffsets discovers grid offsets, runs dfs per surviving offset (fanned out
// across cfg.Workers goroutines), and emits one Result per offset followed by a
// terminal EventDone. Progress events stream live and may interleave, but the
// Results and the final best are merged deterministically in offset order,
// independent of goroutine scheduling. Both GuidedStrategy and BeamStrategy use
// this runner; the only difference is the scorer and the dfs passed in.
func searchOffsets(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
	dfs dfsFunc,
) {
	start := time.Now()
	emit := func(p unpixel.Progress) {
		p.Elapsed = time.Since(start)
		switch p.Kind {
		case unpixel.EventNewBest, unpixel.EventDone:
			select {
			case out <- p:
			case <-ctx.Done():
			}
		default:
			select {
			case out <- p:
			default:
			}
		}
	}

	offsets := DiscoverOffsets(ctx, scorer, cfg, emit)
	offsetsTotal := len(offsets)

	if ctx.Err() != nil || offsetsTotal == 0 {
		emit(unpixel.Progress{Kind: unpixel.EventDone, Done: true})
		results <- unpixel.Result{BestScore: 1.0, BestTotal: 1.0}
		return
	}

	// The running best is shared only to populate advisory progress events; the
	// authoritative best is recomputed deterministically after the fan-out.
	var mu sync.Mutex
	bestScore := 1.0
	var bestGuess string
	evaluated := 0

	outcomes := make([]offsetOutcome, offsetsTotal)
	forEachIndex(ctx, offsetsTotal, resolveWorkers(cfg), func(i int) {
		offset := offsets[i]
		var candidates []unpixel.Eval
		dfs(ctx, scorer, cfg, offset, func(e unpixel.Eval) {
			candidates = append(candidates, e)

			mu.Lock()
			evaluated++
			ev := evaluated
			improved := e.Score < bestScore
			if improved {
				bestScore = e.Score
				bestGuess = e.Guess
			}
			bg, bs := bestGuess, bestScore
			mu.Unlock()

			emit(unpixel.Progress{
				Kind:         unpixel.EventCandidate,
				Guess:        e.Guess,
				Score:        e.Score,
				Depth:        len(e.Guess),
				Offset:       offset,
				Evaluated:    ev,
				OffsetsTotal: offsetsTotal,
				BestGuess:    bg,
				BestScore:    bs,
			})
			if improved {
				emit(unpixel.Progress{
					Kind:         unpixel.EventNewBest,
					BestGuess:    bg,
					BestScore:    bs,
					Guess:        e.Guess,
					Score:        e.Score,
					Depth:        len(e.Guess),
					Offset:       offset,
					Evaluated:    ev,
					OffsetsTotal: offsetsTotal,
				})
			}
		})
		// Rank for the final answer. With a whole-image scorer, disambiguate the
		// candidates that tie at ~0 marginal score (correct prefixes, flukes) by
		// total fidelity so the complete string wins; otherwise fall back to the
		// marginal ranking. All-whitespace candidates are dropped either way.
		var topN []unpixel.Eval
		bestTotal := 1.0
		if ts, ok := scorer.(TotalScorer); ok {
			topN, bestTotal = RankFinal(ctx, ts, candidates, offset, cfg.TopN)
		} else {
			topN = RankTopN(substantiveOnly(candidates), cfg.TopN)
			if len(topN) > 0 {
				bestTotal = topN[0].Score
			}
		}
		conf, ambiguity := Confidence(topN)
		outcomes[i] = offsetOutcome{
			candidates: candidates,
			topN:       topN,
			confidence: conf,
			ambiguity:  ambiguity,
			bestTotal:  bestTotal,
			offset:     offset,
			done:       true,
		}
	})

	// Deterministic merge: the authoritative best is the winning offset's
	// top-ranked candidate, where offsets are compared by the whole-image
	// fidelity of their best candidate (bestTotal). This picks the full answer
	// over a correct prefix that ties on marginal score, and picks the right grid
	// origin over one that merely produced a low-marginal fluke. Ties break on
	// offset then discovery order, so the result never depends on scheduling.
	finalScore := 1.0
	var finalGuess string
	bestTotal := 2.0 // worse than any score in [0, 1]
	for _, oc := range outcomes {
		if !oc.done || len(oc.topN) == 0 {
			continue
		}
		if oc.bestTotal < bestTotal {
			bestTotal = oc.bestTotal
			finalGuess = oc.topN[0].Guess
			finalScore = oc.topN[0].Score
		}
	}
	if bestTotal > 1 {
		bestTotal = 1 // no winner selected: report the worst-case distance
	}
	for _, oc := range outcomes {
		if !oc.done {
			continue
		}
		results <- unpixel.Result{
			BestGuess:  finalGuess,
			BestScore:  finalScore,
			BestTotal:  bestTotal,
			Candidates: oc.candidates,
			TopN:       oc.topN,
			Confidence: oc.confidence,
			Ambiguity:  oc.ambiguity,
			Offset:     oc.offset,
		}
	}

	emit(unpixel.Progress{
		Kind:         unpixel.EventDone,
		Done:         true,
		BestGuess:    finalGuess,
		BestScore:    finalScore,
		Evaluated:    evaluated,
		OffsetsTotal: offsetsTotal,
	})
}
