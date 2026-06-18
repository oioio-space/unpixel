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
func (GuidedStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	searchOffsets(ctx, NewPipelineScorer(redacted, cfg), cfg, out, results, GuidedDFS)
}
