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
// frame's grid phase: it infers the block size from the first image, detects
// each frame's sub-block phase via luma-variance minimisation, and then scores
// every candidate against ALL frames (render once, pixelate per phase, average
// distances). This is strictly more disambiguating than decoding the fused
// image because phase diversity stays in the objective rather than being
// collapsed by image averaging.
//
// imgs must be non-empty and every image non-nil; every image must contain
// frame-0's content region (they may differ in total size). With len(imgs)==1
// the result is byte-identical to calling
// [Decode] on that image. Options are forwarded to the underlying pipeline.
func DecodeMultiFrameAuto(ctx context.Context, imgs []image.Image, opts ...Option) (Result, error) {
	if len(imgs) == 0 {
		return Result{}, fmt.Errorf("mosaictext: no images provided")
	}
	for i, img := range imgs {
		if img == nil {
			return Result{}, fmt.Errorf("mosaictext: image %d is nil", i)
		}
	}

	// Single-frame fast path: skip phase discovery entirely —
	// byte-identical to Decode, preserving the equivalence contract.
	if len(imgs) == 1 {
		return Decode(ctx, imgs[0], opts...)
	}

	// Infer block size from the first image. Fall back to block=1 on failure;
	// DiscoverPhases still runs (returning phase 0 for a constant image), and
	// decodeFrames will report ErrNoMosaic normally.
	block := 1
	if g, ok := unpixel.InferBlockGrid(imgs[0]); ok && g.Size >= 2 {
		block = g.Size
	}

	// Build internal frames with zero offsets so DiscoverPhases fills them in.
	mfFrames := make([]multiframe.Frame, len(imgs))
	for i, img := range imgs {
		mfFrames[i] = multiframe.Frame{Img: img}
	}
	phased := multiframe.DiscoverPhases(mfFrames, block)

	phases := make([][2]int, len(phased))
	for i, f := range phased {
		phases[i] = [2]int{f.OffsetX, f.OffsetY}
	}
	return decodeFrames(ctx, imgs, phases, opts...)
}
