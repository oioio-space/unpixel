package mosaictext

import (
	"context"
	"errors"
	"fmt"
	"image"
)

// Frame is one observed mosaic of the same hidden content captured at a
// known sub-block grid phase offset.  Multiple Frame values with distinct
// (OffsetX, OffsetY) pairs allow [DecodeMultiFrame] to score each candidate
// against all phase-diverse observations, making the objective strictly more
// disambiguating than any single frame.
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

// DecodeMultiFrame recovers text from multiple phase-diverse mosaics of the
// same hidden content by scoring each candidate against ALL frames: the render
// is produced once and then pixelated at each frame's grid phase; the
// per-frame distances are averaged. A candidate that matches under one phase
// but not others is penalised, making the objective strictly more
// disambiguating than any single observation.
//
// frames must be non-empty and every Frame.Img must be non-nil; every image
// must contain frame-0's content region (frames may differ in total size but
// must share the same content area). Options are forwarded unchanged to the
// underlying decode pipeline.
//
// When len(frames)==1 the result is identical to calling [Decode] on that
// frame's image directly — callers can upgrade a single-frame call site to
// multi-frame by wrapping the existing image in a one-element slice.
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

	// Single-frame fast path: identical to Decode, preserving byte-exact equivalence.
	if len(frames) == 1 {
		return Decode(ctx, frames[0].Img, opts...)
	}

	imgs := make([]image.Image, len(frames))
	phases := make([][2]int, len(frames))
	for i, f := range frames {
		imgs[i] = f.Img
		phases[i] = [2]int{f.OffsetX, f.OffsetY}
	}
	return decodeFrames(ctx, imgs, phases, opts...)
}
