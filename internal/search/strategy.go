// Package search (strategy.go) implements unpixel.Strategy by composing
// DiscoverOffsets with GuidedDFS and emitting Progress events.
package search

import (
	"context"
	"image"
	"time"

	"github.com/oioio-space/unpixel"
)

// GuidedStrategy implements unpixel.Strategy using offset discovery followed
// by GuidedDFS for each surviving offset.
type GuidedStrategy struct{}

// NewGuidedStrategy returns a GuidedStrategy.
func NewGuidedStrategy() GuidedStrategy { return GuidedStrategy{} }

// Search runs offset discovery then GuidedDFS per surviving offset.
// It always emits a final EventDone event before returning.
func (GuidedStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	start := time.Now()
	scorer := NewPipelineScorer(redacted, cfg)

	// emit sends a Progress event, blocking with ctx for high-priority events.
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

	var (
		bestGuess string
		bestScore = 1.0
		evaluated int
	)

	offsets := DiscoverOffsets(ctx, scorer, cfg, emit)
	offsetsTotal := len(offsets)

	if ctx.Err() != nil || offsetsTotal == 0 {
		emit(unpixel.Progress{Kind: unpixel.EventDone, Done: true})
		results <- unpixel.Result{BestGuess: bestGuess, BestScore: bestScore}
		return
	}

	for offsetsDone, offset := range offsets {
		if ctx.Err() != nil {
			break
		}
		var candidates []unpixel.Eval

		GuidedDFS(ctx, scorer, cfg, offset, func(e unpixel.Eval) {
			evaluated++
			candidates = append(candidates, e)

			emit(unpixel.Progress{
				Kind:         unpixel.EventCandidate,
				Guess:        e.Guess,
				Score:        e.Score,
				Depth:        len(e.Guess),
				Offset:       offset,
				Evaluated:    evaluated,
				OffsetsDone:  offsetsDone,
				OffsetsTotal: offsetsTotal,
				BestGuess:    bestGuess,
				BestScore:    bestScore,
			})

			if e.Score < bestScore {
				bestScore = e.Score
				bestGuess = e.Guess
				emit(unpixel.Progress{
					Kind:         unpixel.EventNewBest,
					BestGuess:    bestGuess,
					BestScore:    bestScore,
					Guess:        e.Guess,
					Score:        e.Score,
					Depth:        len(e.Guess),
					Offset:       offset,
					Evaluated:    evaluated,
					OffsetsDone:  offsetsDone,
					OffsetsTotal: offsetsTotal,
				})
			}
		})

		topN := RankTopN(candidates, cfg.TopN)
		conf, ambiguity := Confidence(topN)
		results <- unpixel.Result{
			BestGuess:  bestGuess,
			BestScore:  bestScore,
			Candidates: candidates,
			TopN:       topN,
			Confidence: conf,
			Ambiguity:  ambiguity,
			Offset:     offset,
		}
	}

	emit(unpixel.Progress{
		Kind:         unpixel.EventDone,
		Done:         true,
		BestGuess:    bestGuess,
		BestScore:    bestScore,
		Evaluated:    evaluated,
		OffsetsTotal: offsetsTotal,
	})
}
