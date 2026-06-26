package mosaictext

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/multiframe"
)

// Frame is one observed mosaic of the same hidden content captured at a
// known sub-block grid phase offset.  Multiple Frame values with distinct
// (OffsetX, OffsetY) pairs allow [DecodeMultiFrame] to reconstruct
// sub-block detail that no single frame can reveal.
//
// OffsetX and OffsetY are the pixel offsets at which the pixelation grid
// was aligned when this frame was produced (e.g. the horizontal scroll
// position of the redaction within the pixelation tile).  Set both to
// zero when the offset is unknown or irrelevant — a single-frame call
// with (0, 0) is identical to calling [Decode] directly.
type Frame struct {
	// Img is the mosaic-pixelated observation.  Must be non-nil.
	Img image.Image
	// OffsetX is the horizontal sub-block grid phase in pixels.
	OffsetX int
	// OffsetY is the vertical sub-block grid phase in pixels.
	OffsetY int
}

// DecodeMultiFrame fuses multiple phase-diverse mosaics of the same hidden
// content via iterative back-projection (IBP) and then recovers the text
// using the same zero-configuration pipeline as [Decode].
//
// frames must be non-empty and every Frame.Img must be non-nil; all images
// must cover at least the same top-left region (the first frame's bounds
// determine the output size).  Options are forwarded unchanged to [Decode].
//
// When len(frames)==1 the IBP fusion is a no-op and the result is
// identical to calling [Decode] on that frame's image directly — callers
// can therefore upgrade a single-frame call site to multi-frame by
// wrapping the existing image in a one-element slice with no behaviour
// change.
//
// The function returns [ErrNoMosaic] or [ErrNoContent] under the same
// conditions as [Decode].
func DecodeMultiFrame(ctx context.Context, frames []Frame, opts ...Option) (Result, error) {
	if len(frames) == 0 {
		return Result{}, errors.New("mosaictext: no frames provided")
	}
	for i, f := range frames {
		if f.Img == nil {
			return Result{}, fmt.Errorf("mosaictext: frame %d has nil image", i)
		}
	}

	// Single-frame fast path: IBP with one frame converges in one pass to a
	// block-constant image identical to the input, but the uint8 round-trip in
	// Fuse can introduce sub-LSB noise that breaks InferBlockGrid.  Skip fusion
	// entirely and pass the raw image straight to Decode — this preserves the
	// "1-frame ≡ Decode" contract exactly.
	if len(frames) == 1 {
		return Decode(ctx, frames[0].Img, opts...)
	}

	// Translate public Frame slice to the internal multiframe.Frame slice.
	// The internal type lives in an internal package so callers cannot import
	// it directly; Frame is the public mirror.
	mf := make([]multiframe.Frame, len(frames))
	for i, f := range frames {
		mf[i] = multiframe.Frame{Img: f.Img, OffsetX: f.OffsetX, OffsetY: f.OffsetY}
	}

	// Infer the block size from the first frame so Fuse uses the correct grid.
	// InferBlockGrid is run again inside Decode on the fused image, but we need
	// it here to drive the IBP back-projection kernel size.  If inference fails
	// we fall back to block=1, which makes Fuse a plain copy of the first frame
	// and lets Decode report ErrNoMosaic normally.
	block := 1
	if g, ok := unpixel.InferBlockGrid(frames[0].Img); ok && g.Size >= 2 {
		block = g.Size
	}

	fused, err := multiframe.Fuse(mf, block)
	if err != nil {
		return Result{}, fmt.Errorf("mosaictext: frame fusion: %w", err)
	}

	return Decode(ctx, fused, opts...)
}
