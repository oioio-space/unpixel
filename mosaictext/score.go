package mosaictext

// score.go — calibrated candidate scoring for mosaic-pixelated text.
//
// ScoreCandidates runs the same auto-calibration as Decode (block size,
// colorspace, font size, grid phase) and scores each candidate string
// against the observed mosaic using the MSE forward model. This is the
// correct primitive for an LLM-propose / physical-verify workflow: the
// caller proposes candidate strings and ScoreCandidates ranks them by how
// well each reconstructs the original pixels.
//
// Concretely, for each candidate text s:
//
//	rendered(s) → pixelate(block, colorspace) → mseRGB(observed)
//
// Unlike Decode's inner loop (which fixes a single character count and
// sweeps the charset), ScoreCandidates adjusts the horizontal tracking
// factor per candidate so that every candidate — regardless of its length —
// spans the full content width. This is the correct comparison: the
// question is "does this string, rendered at the size and tracking implied
// by its length, reproduce the observed mosaic?", not "does this string at
// a fixed width-per-character match?".

import (
	"context"
	"image"
	"math"
	"sync"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"

	xdraw "golang.org/x/image/draw"
)

// ScoreCandidates scores each candidate string against the mosaic in img
// and returns the MSE distances indexed parallel to candidates (lower
// distance means the candidate reconstructs the observed mosaic better).
//
// It runs the same zero-configuration calibration as [Decode]:
//   - block grid and colorspace are inferred from img,
//   - font size and grid phase are calibrated by sweeping all bundled fonts
//     and picking the one whose probe render best explains the observed
//     redaction height and width.
//
// The scoring forward model per candidate s is:
//
//	rendered(s, stretchForLen(s)) → pixelate(block, colorspace) → mseRGB(observed)
//
// The horizontal tracking factor is adjusted per candidate so every string
// spans the full content width regardless of character count. This is the
// physical comparison: "does this text, rendered at the size implied by
// its length, reproduce the observed mosaic?" Applying a fixed stretch
// (calibrated for a different length) causes over-wide renders to be
// clipped before comparison, making all such candidates score identically.
//
// Fundamental limit: for long proportional-font sentences, per-block signal
// mixes across character boundaries at 8 px block sizes, so candidates that
// differ only in interior characters may score very similarly even after the
// fix. Short strings (words, tokens, digit sequences) discriminate reliably.
//
// Returns [ErrNoMosaic] when no block grid can be detected and [ErrNoContent]
// when the image has no redacted content. An empty candidates slice returns
// a nil slice and no error.
func ScoreCandidates(ctx context.Context, img image.Image, candidates []string, opts ...Option) ([]float64, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	rgba := toRGBA(img)

	grid, ok := unpixel.InferBlockGrid(img)
	if !ok || grid.Size < 2 {
		return nil, ErrNoMosaic
	}
	block := grid.Size

	rect := contentBounds(rgba)
	if rect.Empty() {
		return nil, ErrNoContent
	}

	// Build the same padded content crop as Decode and DecodeReference.
	const pad = 24
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+pad, rect.Dy()+pad))
	imutil.FillWhite(target)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), rgba, rect.Min, xdraw.Src)
	tW, tH := rect.Dx(), rect.Dy()

	// Downscale to targetBlockPx for the calibration sweep, matching Decode
	// phase-1 (keeps calibration fast on large blocks).
	f := max(1, block/targetBlockPx)
	coarseTarget, coarseBlock, coarseTW, coarseTH := target, block, tW, tH
	if f > 1 {
		coarseBlock = block / f
		coarseTarget = downscaleBox(target, f)
		coarseTW, coarseTH = tW/f, tH/f
	}

	// Sweep all bundled fonts × {sRGB, linear} at coarse resolution to find
	// the calibration whose phase probe best explains the observed mosaic.
	// This mirrors the phase-1 sweep in Decode exactly.
	rs, err := fonts.Renderers()
	if err != nil {
		return nil, err
	}

	type calibResult struct {
		d      *decoder
		linear bool
		pox    int
		fitMSE float64
	}
	frameBytes := coarseTarget.Bounds().Dx() * coarseTarget.Bounds().Dy() * 4
	workers, coarseCap := cfg.plan(frameBytes, len(rs)*2)
	sem := make(chan struct{}, workers)

	results := make([]calibResult, len(rs)*2)
	var wg sync.WaitGroup
	for fi := range rs {
		for li := range 2 {
			wg.Go(func() {
				if ctx.Err() != nil {
					return
				}
				sem <- struct{}{}
				defer func() { <-sem }()
				linear := li == 1
				d := &decoder{
					r:        rs[fi],
					target:   coarseTarget,
					tW:       coarseTW,
					tH:       coarseTH,
					block:    coarseBlock,
					pixelate: pixelatorFor(coarseBlock, linear),
					cacheCap: coarseCap,
				}
				nRef, _, _, ok := d.calibrate()
				if !ok {
					return
				}
				pox, fit := d.phase(d.stretchForN(nRef), nRef)
				results[fi*2+li] = calibResult{d: d, linear: linear, pox: pox, fitMSE: fit}
			})
		}
	}
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Pick the calibration with the lowest phase-probe MSE.
	best := calibResult{fitMSE: math.Inf(1)}
	for _, r := range results {
		if r.d != nil && r.fitMSE < best.fitMSE {
			best = r
		}
	}
	if best.d == nil {
		return nil, ErrNoContent
	}

	// Upgrade to full-resolution decoder when the target was downscaled,
	// mirroring the phase-2b upgrade in Decode.
	scoreDec := best.d
	scorePox := best.pox * f
	if f > 1 {
		hi := &decoder{
			r:        best.d.r,
			target:   target,
			tW:       tW,
			tH:       tH,
			block:    block,
			pixelate: pixelatorFor(block, best.linear),
			cacheCap: minCacheEntries,
		}
		if _, _, _, ok := hi.calibrate(); ok {
			scoreDec = hi
		}
	}

	// Score each candidate at the calibrated (fs, pox) but with a
	// per-candidate stretch so that every string — regardless of length —
	// spans the full content width before pixelation.
	//
	// A fixed stretch calibrated for nRef chars causes candidates longer
	// than nRef to render wider than the target canvas, making placed()
	// skip the draw (it guards against out-of-bounds compositing). All
	// over-wide candidates then compare a pure-white frame against the
	// target and score identically, regardless of their glyph content.
	// Adjusting stretch per candidate eliminates that width-clipping path.
	scoreDec.cache = newRenderCache(minCacheEntries)

	dists := make([]float64, len(candidates))
	for i, c := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		n := max(1, len([]rune(c)))
		dists[i] = scoreDec.dist(c, scoreDec.fs, scoreDec.stretchForN(n), scorePox)
	}
	return dists, nil
}
