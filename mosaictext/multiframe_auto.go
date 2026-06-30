package mosaictext

import (
	"context"
	"fmt"
	"image"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/multiframe"
)

// DecodeMultiFrameAuto decodes the hidden text from multiple phase-diverse
// mosaics of the same content WITHOUT requiring the caller to know each
// frame's grid phase: it infers the block size from the first image,
// detects each frame's sub-block phase, fuses them via iterative
// back-projection, and decodes the result with the same pipeline as Decode.
//
// imgs must be non-empty and every image non-nil. With len(imgs)==1 the
// result is byte-identical to calling Decode on that image. Options are
// forwarded to Decode unchanged.
func DecodeMultiFrameAuto(ctx context.Context, imgs []image.Image, opts ...Option) (Result, error) {
	if len(imgs) == 0 {
		return Result{}, fmt.Errorf("mosaictext: no images provided")
	}
	for i, img := range imgs {
		if img == nil {
			return Result{}, fmt.Errorf("mosaictext: image %d is nil", i)
		}
	}

	// Single-frame fast path: skip phase discovery and fusion entirely —
	// identical to Decode, preserving the byte-exact equivalence contract.
	if len(imgs) == 1 {
		return Decode(ctx, imgs[0], opts...)
	}

	// Infer block size from the first image.  Fall back to block=1 on failure,
	// which makes Fuse a plain copy and lets Decode report ErrNoMosaic normally.
	block := 1
	if g, ok := unpixel.InferBlockGrid(imgs[0]); ok && g.Size >= 2 {
		block = g.Size
	}

	// Build internal frames with zero offsets; DiscoverPhases will fill them in.
	frames := make([]multiframe.Frame, len(imgs))
	for i, img := range imgs {
		frames[i] = multiframe.Frame{Img: img}
	}

	phased := multiframe.DiscoverPhases(frames, block)

	fused, err := multiframe.Fuse(phased, block)
	if err != nil {
		return Result{}, fmt.Errorf("mosaictext: frame fusion: %w", err)
	}

	return Decode(ctx, fused, opts...)
}
