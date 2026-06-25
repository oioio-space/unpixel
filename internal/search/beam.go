// Package search (beam.go) adds BeamDFS, a beam-search variant of GuidedDFS that
// bounds the branching factor by retaining only the best cfg.BeamWidth candidates
// per depth level, and BeamStrategy, the unpixel.Strategy that drives it.
package search

import (
	"cmp"
	"context"
	"image"
	"math"
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
//
// When cfg.LanguageModel is set, beam selection uses a language-blended rank
// (see beamRank) so linguistically plausible prefixes survive beam pruning even
// when their image distances are comparable to less-plausible alternatives. This
// is essential for blur recovery, where per-character image signal is weak and
// the correct path can be pruned by image distance alone.
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
	beam := topBeamLM(level, width, cfg.LanguageModel)

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
		beam = topBeamLM(next, width, cfg.LanguageModel)
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

// beamLMBlend is the weight applied to the normalised language-model bonus when
// ranking beam candidates. The combined rank is:
//
//	rank = imageScore − beamLMBlend × max(0, lm(guess) − beamLMFloor)
//
// Typical image scores (pixelmatch, blur) are in [0, 0.25]. The normalised LM
// term is at most beamLMBlend × (0 − beamLMFloor) ≈ 0.35, so the prior can
// overcome a ~0.35 image-score disadvantage — large enough to flip "cennect"
// vs "connect" at σ≈3 (image gap ≈ 0) while staying smaller than the gap
// between a correct and a truly wrong character (typically ≥ 0.08).
const (
	beamLMBlend = 0.05
	// beamLMFloor is the log-prob floor (~e^-7 ≈ 0.09% plausibility) applied to
	// the language-model score before the beam-rank blend. It prevents OOV strings
	// from receiving an unbounded penalty while still giving plausible prefixes a
	// meaningful bonus over noise.
	beamLMFloor = -7.0
)

// beamRank returns the composite beam-selection score for a node: lower is
// better. Without a language model it equals the raw image score. With one,
// the LM bonus shifts linguistically plausible prefixes toward lower rank so
// they survive pruning even when the image signal is nearly flat (blur).
func beamRank(n node, lm func(string) float64) float64 {
	if lm == nil {
		return n.result.Score
	}
	lmScore := lm(n.guess)
	bonus := max(0, lmScore-beamLMFloor) // ≥ 0; higher plausibility → larger bonus
	return n.result.Score - beamLMBlend*bonus
}

// rankedNode pairs a node with its precomputed composite beam rank so the
// sort comparator pays one lm call per node rather than O(log N).
type rankedNode struct {
	n    node
	rank float64
}

// topBeamLM sorts nodes by their composite beam rank (image score blended with
// an optional language-model bonus) and returns at most width of them. Ties
// within floating-point equality break on guess for determinism.
//
// When lm is nil (the common mosaic path) nodes are sorted in place by
// (score, guess) with no rankedNode allocation. When lm is non-nil, beamRank
// is precomputed once per node before sorting so the comparator never calls
// lm(guess) more than once per node (vs. O(log N) calls with a naive
// in-comparator evaluation).
func topBeamLM(nodes []node, width int, lm func(string) float64) []node {
	if lm == nil {
		// Fast path: no language model — sort in place, no extra allocation.
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
	// Slow path: precompute composite rank once per node to avoid O(log N) lm calls.
	ranked := make([]rankedNode, len(nodes))
	for i, n := range nodes {
		ranked[i] = rankedNode{n: n, rank: beamRank(n, lm)}
	}
	slices.SortFunc(ranked, func(a, b rankedNode) int {
		if c := cmp.Compare(a.rank, b.rank); c != 0 {
			return c
		}
		return cmp.Compare(a.n.guess, b.n.guess)
	})
	if len(ranked) > width {
		ranked = ranked[:width]
	}
	// Write sorted nodes back into the input slice (reuse its backing array).
	nodes = nodes[:len(ranked)]
	for i, rn := range ranked {
		nodes[i] = rn.n
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
	candidates   []unpixel.Eval
	topN         []unpixel.Eval
	bestSeenEval unpixel.Eval // lowest-scored candidate ever evaluated, regardless of threshold
	confidence   float64
	ambiguity    float64
	bestTotal    float64 // whole-image score of topN[0]; ranks offsets against each other
	offset       unpixel.Offset
	done         bool
	belowThresh  bool // true when topN was populated from bestSeen (no candidate passed the gate)
}

// searchOffsets discovers grid offsets, runs dfs per surviving offset (fanned out
// across cfg.Workers goroutines), and emits one Result per offset followed by a
// terminal EventDone. Progress events stream live and may interleave, but the
// Results and the final best are merged deterministically in offset order,
// independent of goroutine scheduling. Both GuidedStrategy and BeamStrategy use
// this runner; the only difference is the scorer and the dfs passed in.
//
// Best-effort surfacing: the scorer is wrapped in a trackingScorer so the
// single lowest-scored candidate ever evaluated is retained regardless of
// whether it passed the threshold gate. When no candidate passes the gate for
// a given offset, topN/BestGuess are populated from that best-seen candidate
// and Result.BelowThreshold is set to true. When no offset survives discovery,
// one Result is emitted with the best-seen discovery candidate and
// BelowThreshold=true, so callers always get a non-empty guess to surface.
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

	// Wrap scorer so every Eval (discovery + DFS) is tracked for best-seen
	// promotion. The trackingScorer adds one atomic load + occasional CAS per
	// call — negligible beside the render→pixelate→metric work already done.
	globalSeen := newBestSeenTracker()
	tracked := trackingScorer{Scorer: scorer, seen: globalSeen}

	offsets := DiscoverOffsets(ctx, tracked, cfg, emit)
	offsetsTotal := len(offsets)

	// Shrink the outer-worker budget to the actual number of surviving offsets so
	// intraNodeWorkers (called inside each dfs invocation) distributes the idle
	// cores to intra-node child evaluation rather than keeping them unused.
	//
	// Example: 20 cores, cfg.Workers=20, 3 offsets survive discovery.
	//   Before: forEachIndex runs 3 goroutines; intraNodeWorkers = 20/20 = 1 (sequential).
	//   After:  cfg.Workers=3; forEachIndex still runs 3 goroutines; intraNodeWorkers = 20/3 = 6.
	//
	// cfg is a value copy; this write is local and does not affect callers.
	// Guard: when offsetsTotal == 0 the early-return below fires first; skip the
	// min so cfg.Workers never becomes 0 (which resolveWorkers would misread as
	// "use GOMAXPROCS").
	if offsetsTotal > 0 {
		cfg.Workers = min(resolveWorkers(cfg), offsetsTotal)
	}

	if ctx.Err() != nil || offsetsTotal == 0 {
		emit(unpixel.Progress{Kind: unpixel.EventDone, Done: true})
		// Best-effort: promote the best-seen discovery candidate so the caller
		// always gets a non-empty guess, even when no offset survived.
		bsGuess, bsScore := globalSeen.best()
		bsGuess = TrimEdgeSpaces(bsGuess)
		if bsGuess != "" && !math.IsInf(bsScore, 1) {
			bsEval := unpixel.Eval{Guess: bsGuess, Score: bsScore}
			conf, _ := Confidence([]unpixel.Eval{bsEval})
			results <- unpixel.Result{
				BestGuess:  bsGuess,
				BestScore:  bsScore,
				BestTotal:  1.0, // kept at 1.0: sub-threshold guess must not win inter-sigma comparisons
				TopN:       []unpixel.Eval{bsEval},
				Confidence: conf,
				// Note: Confidence here is 1-bsScore (low, since bsScore > threshold).
				BelowThreshold: true,
			}
		} else {
			results <- unpixel.Result{BestScore: 1.0, BestTotal: 1.0}
		}
		return
	}

	// The running best is shared only to populate advisory progress events; the
	// authoritative best is recomputed deterministically after the fan-out.
	var mu sync.Mutex
	bestScore := 1.0
	var bestGuess string
	evaluated := 0

	outcomes := make([]offsetOutcome, offsetsTotal)
	// cfg.Workers already holds min(resolveWorkers, offsetsTotal) from above.
	forEachIndex(ctx, offsetsTotal, cfg.Workers, func(i int) {
		offset := offsets[i]

		// Per-offset tracker: captures the best-seen for this offset's DFS so
		// we can fall back to it when no candidate passes the gate.
		offsetSeen := newBestSeenTracker()
		offsetTracked := trackingScorer{Scorer: tracked, seen: offsetSeen}

		var candidates []unpixel.Eval
		dfs(ctx, offsetTracked, cfg, offset, func(e unpixel.Eval) {
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
		//
		// Note: scorer (not offsetTracked) is used for TotalScore so the
		// type-assertion reaches the underlying PipelineScorer, not the wrapper.
		var topN []unpixel.Eval
		bestTotal := 1.0
		if ts, ok := scorer.(TotalScorer); ok {
			topN, bestTotal = RankFinal(ctx, ts, candidates, offset, cfg.TopN, cfg.LanguageModel)
		} else {
			topN = RankTopN(substantiveOnly(candidates), cfg.TopN)
			if len(topN) > 0 {
				bestTotal = topN[0].Score
			}
		}

		// Best-effort surfacing: when no candidate passed the gate, populate topN
		// from the offset-local best-seen so BestGuess is never silently empty.
		// bestTotal stays at 1.0 for below-threshold results so they never win
		// an inter-offset or inter-sigma comparison against a legitimate result
		// (whose BestTotal comes from a real whole-image TotalScore).
		belowThresh := false
		if len(topN) == 0 {
			bsGuess, bsScore := offsetSeen.best()
			bsGuess = TrimEdgeSpaces(bsGuess)
			if bsGuess != "" && !math.IsInf(bsScore, 1) {
				bsEval := unpixel.Eval{Guess: bsGuess, Score: bsScore}
				topN = []unpixel.Eval{bsEval}
				// bestTotal deliberately kept at 1.0 (set above): the marginal
				// score of a sub-threshold candidate is not a reliable whole-image
				// fidelity signal and must not displace a legitimate result.
				belowThresh = true
			}
		}

		conf, ambiguity := Confidence(topN)
		bsGuess, bsScore := offsetSeen.best()
		bsSeen := unpixel.Eval{Guess: bsGuess, Score: bsScore}
		outcomes[i] = offsetOutcome{
			candidates:   candidates,
			topN:         topN,
			bestSeenEval: bsSeen,
			confidence:   conf,
			ambiguity:    ambiguity,
			bestTotal:    bestTotal,
			offset:       offset,
			done:         true,
			belowThresh:  belowThresh,
		}
	})

	// Deterministic merge: the authoritative best is the winning offset's
	// top-ranked candidate, where offsets are compared by the whole-image
	// fidelity of their best candidate (bestTotal). This picks the full answer
	// over a correct prefix that ties on marginal score, and picks the right grid
	// origin over one that merely produced a low-marginal fluke. Ties break on
	// offset then discovery order, so the result never depends on scheduling.
	//
	// When at least one offset has above-threshold candidates, those win the
	// merge unconditionally; only if every offset is below-threshold does the
	// final answer carry BelowThreshold=true.
	finalScore := 1.0
	var finalGuess string
	bestTotal := 2.0 // worse than any score in [0, 1]
	anyAbove := false
	for _, oc := range outcomes {
		if !oc.done || len(oc.topN) == 0 {
			continue
		}
		if !oc.belowThresh {
			anyAbove = true
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
		// BelowThreshold is true for a result only when every participating
		// offset is below-threshold (no above-threshold winner was found).
		bt := oc.belowThresh && !anyAbove
		results <- unpixel.Result{
			BestGuess:      finalGuess,
			BestScore:      finalScore,
			BestTotal:      bestTotal,
			Candidates:     oc.candidates,
			TopN:           oc.topN,
			Confidence:     oc.confidence,
			Ambiguity:      oc.ambiguity,
			Offset:         oc.offset,
			BelowThreshold: bt,
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
