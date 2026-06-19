// Package defaults wires the standard internal components into an unpixel.Config.
//
// The four standard components are:
//   - XImage renderer — rasterises text via the golang.org/x/image font stack.
//   - BlockAverage pixelator — replaces each block with its mean RGBA colour.
//   - Pixelmatch metric — measures pixel-level distance with a 0.02 threshold.
//   - GuidedDFS strategy — guided depth-first search over the candidate alphabet.
//
// Import this package for its side-effect alone to make Engine.Run work with a
// zero-value Config:
//
//	import _ "github.com/oioio-space/unpixel/defaults"
//
// This package exists solely to break the import cycle between the root unpixel
// package and its internal implementations. Applications that supply all four
// component fields in Config explicitly do not need to import it.
//
// To pick a non-default search strategy, assign one of the exported strategy
// constructors to Config.Strategy:
//
//	cfg := unpixel.Config{Strategy: defaults.BeamStrategy(0)}
package defaults

import (
	"fmt"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

func init() {
	unpixel.DefaultComponents = Wire
}

// Wire fills any nil component fields in cfg with the standard implementations.
// It is called automatically by Engine.Run via the DefaultComponents hook when
// this package is imported for its side-effect. It may also be called directly
// to pre-initialise a Config before passing it to New.
// Wire returns an error if the XImage renderer cannot be initialised, which
// indicates a font-parsing failure in the embedded font data.
func Wire(cfg *unpixel.Config) error {
	if cfg.Renderer == nil {
		r, err := render.NewXImage()
		if err != nil {
			return fmt.Errorf("init default renderer: %w", err)
		}
		cfg.Renderer = r
	}
	if cfg.Pixelator == nil {
		cfg.Pixelator = pixelate.NewBlockAverage(cfg.BlockSize)
	}
	if cfg.Metric == nil {
		// faithful: Jimp.diff uses threshold 0.02
		cfg.Metric = metric.NewPixelmatch(0.02)
	}
	if cfg.Strategy == nil {
		cfg.Strategy = search.NewGuidedStrategy()
	}
	return nil
}

// RendererFromFonts returns an XImage renderer that rasterises candidate text
// with the given TrueType/OpenType font data instead of the embedded Liberation
// Sans default, ready to assign to Config.Renderer (or pass via WithRenderer).
//
// Use it to match the exact typeface of a redaction — for example a user-
// supplied Consolas font for source-code screenshots, which keeps any font
// licensing on the caller's side:
//
//	reg, _ := os.ReadFile("Consolas.ttf")
//	r, _ := defaults.RendererFromFonts(reg, nil)
//	res, _ := unpixel.Recover(ctx, img, unpixel.WithRenderer(r),
//	    unpixel.WithStyle(unpixel.Style{FontSize: 24, LetterSpacing: -0.2}))
//
// regularTTF is required; boldTTF may be nil to reuse the regular font for bold.
func RendererFromFonts(regularTTF, boldTTF []byte) (unpixel.Renderer, error) {
	return render.NewXImageFromFonts(regularTTF, boldTTF)
}

// BlockAverage returns the faithful mosaic pixelator (per-block mean RGBA) for
// the given block size, ready to assign to Config.Pixelator. It is the same
// operator Wire installs by default; name it explicitly to pair with a non-zero
// Config.BlockSize, or alongside GaussianBlur when selecting the redaction mode.
func BlockAverage(blockSize int) unpixel.Pixelator {
	return pixelate.NewBlockAverage(blockSize)
}

// GaussianBlur returns a Gaussian-blur redaction operator (sigma in pixels) as
// an unpixel.Pixelator, for recovering blurred — rather than mosaic-pixelated —
// text. Assign it to Config.Pixelator with Config.BlockSize = 1 (blur has no
// grid), then run the normal search:
//
//	cfg := unpixel.Config{Pixelator: defaults.GaussianBlur(6), BlockSize: 1}
//
// Like mosaic, blur is a deterministic function of its input, so the same
// generate-and-test attack applies (render → blur → compare).
func GaussianBlur(sigma float64) unpixel.Pixelator {
	return pixelate.NewGaussianBlur(sigma)
}

// FastBlur returns a fast box-approximated Gaussian blur (sigma in pixels) as an
// unpixel.Pixelator. It is O(1) per pixel regardless of sigma — much cheaper than
// GaussianBlur for large radii — at a small fidelity cost; for generate-and-test
// the ranking is preserved, so it is a good default for the blur sweep:
//
//	cfg := unpixel.Config{Pixelator: defaults.FastBlur(6), BlockSize: 1}
func FastBlur(sigma float64) unpixel.Pixelator {
	return pixelate.NewFastBlur(sigma)
}

// GuidedStrategy returns the guided depth-first search strategy as an
// unpixel.Strategy, ready to assign to Config.Strategy. It is the same strategy
// Wire installs when Config.Strategy is nil; call it explicitly only for
// symmetry with BeamStrategy or to make the choice visible at the call site.
func GuidedStrategy() unpixel.Strategy {
	return search.NewGuidedStrategy()
}

// BeamStrategy returns the beam-search strategy as an unpixel.Strategy, ready to
// assign to Config.Strategy. width caps the number of candidates retained per
// depth level; pass 0 to defer to Config.BeamWidth (or DefaultBeamWidth when
// that is also unset).
//
// Beam search bounds the branching factor for a faster, lower-recall search than
// the default guided DFS. It memoises the shared render→pixelate→crop prefix
// work in an LRU cache sized by Config.CacheSize (zero disables the cache):
//
//	cfg := unpixel.Config{Strategy: defaults.BeamStrategy(0)}
func BeamStrategy(width int) unpixel.Strategy {
	return search.NewBeamStrategy(width)
}

// PixelmatchMetric returns the faithful default image-distance metric (a YIQ
// perceptual pixel-difference, matching the original Jimp.diff) as an
// unpixel.Metric, ready to assign to Config.Metric.
func PixelmatchMetric() unpixel.Metric {
	return metric.NewPixelmatch(0.02)
}

// SSIMMetric returns a structural-similarity image metric as an unpixel.Metric.
// window is the SSIM comparison-window side length; pass 0 for the default.
//
// SSIM compares local structure rather than exact pixels, so it tolerates the
// anti-aliasing/hinting differences between rendering engines. Its score scale
// differs from the pixel-fraction default, so a search using it usually needs
// its own Config.Threshold:
//
//	cfg := unpixel.Config{Metric: defaults.SSIMMetric(0)}
func SSIMMetric(window int) unpixel.Metric {
	return metric.NewSSIM(window)
}
