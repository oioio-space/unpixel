// Package search (strategy.go) implements unpixel.Strategy by composing
// DiscoverOffsets with GuidedDFS and emitting Progress events.
package search

import (
	"context"
	"image"

	"github.com/oioio-space/unpixel"
)

// GuidedStrategy implements unpixel.Strategy using offset discovery followed
// by GuidedDFS for each surviving offset.
type GuidedStrategy struct{}

// NewGuidedStrategy returns a GuidedStrategy.
func NewGuidedStrategy() GuidedStrategy { return GuidedStrategy{} }

// Search runs offset discovery then GuidedDFS per surviving offset, fanned out
// across cfg.Workers goroutines with a deterministic merge. It always emits a
// final EventDone event before returning.
//
// The pipeline scorer is wrapped in a CachingScorer (controlled by
// cfg.CacheSize; zero disables the cache) so the shared render→pixelate→crop
// prefix work is memoised across offset goroutines, matching BeamStrategy's
// construction.
func (GuidedStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	scorer := NewCachingScorer(NewPipelineScorer(redacted, cfg), cfg.CacheSize)
	searchOffsets(ctx, scorer, cfg, out, results, GuidedDFS)
}
