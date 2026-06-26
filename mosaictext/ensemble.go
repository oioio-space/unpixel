package mosaictext

// ensemble.go — verification-selected decoder ensemble.
//
// DecodeEnsemble runs a configurable set of decoders on the same image and
// returns the result with the lowest [Result.Distance]. The safety property:
// the ensemble result distance is always ≤ min(Distance_i) over all
// non-skipped decoders — it can only match or beat any single decoder.
//
// Score comparability:
//
// Every decoder in this package that returns [Result] stores the same metric
// in [Result.Distance]: the whole-image block-value MSE of the best
// reconstruction (render the guessed text → pixelate with the inferred block
// size → mseRGB against the padded content crop). Because all decoders derive
// the same target from the same input (contentBounds + 24-px pad, block size
// from InferBlockGrid), the distances are directly comparable without a
// re-render step.

import (
	"context"
	"image"
	"math"
)

// EnsembleDecoder is a function that decodes a mosaic redaction and returns a
// [Result]. It matches the signature of [Decode], [DecodeHMM],
// [DecodeWindowHMM], and [DecodeTrainedHMM] so each can be wrapped without an
// adapter:
//
//	mosaictext.EnsembleDecoder(mosaictext.Decode)
//
// A decoder that returns an error or a [Result] with an empty Text is silently
// skipped by [DecodeEnsemble].
type EnsembleDecoder func(ctx context.Context, img image.Image) (Result, error)

// DecodeEnsemble runs each decoder in decoders on img, scores the results by
// [Result.Distance] (whole-image MSE; lower is better), and returns the result
// with the lowest distance. Decoders that error or return an empty Text are
// skipped. Ties are broken by decoder order — the first decoder in the slice
// wins on equal distance. If no decoder produces a non-empty result,
// [ErrNoContent] is returned.
//
// The ensemble can only match or beat the best individual decoder: its returned
// distance is always ≤ min(Distance_i) over all non-skipped results.
//
// Decoders run sequentially in slice order. Pass a context with a deadline or
// cancellation to bound total runtime; a cancelled context is detected between
// decoder calls and returned immediately.
func DecodeEnsemble(ctx context.Context, img image.Image, decoders []EnsembleDecoder) (Result, error) {
	best := Result{Distance: math.Inf(1)}
	found := false
	for _, dec := range decoders {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		res, err := dec(ctx, img)
		if err != nil || res.Text == "" {
			continue
		}
		// Strict less-than preserves first-wins tie-break.
		if !found || res.Distance < best.Distance {
			best = res
			found = true
		}
	}
	if !found {
		return Result{}, ErrNoContent
	}
	return best, nil
}
