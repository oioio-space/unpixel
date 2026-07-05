// Package defaults wires the standard internal components into an unpixel.Config.
//
// The four standard components are:
//   - XImage renderer — rasterises text via the golang.org/x/image font stack.
//   - BlockAverage pixelator — replaces each block with its mean RGBA colour.
//   - Metric — auto-selected by pixelator type (see below).
//   - GuidedDFS strategy — guided depth-first search over the candidate alphabet.
//
// Metric auto-selection (zero-config, no quality loss):
//
//   - BlockAverage / LinearBlockAverage pixelators → PixelmatchFast (no-AA YIQ
//     pixel diff). Mosaic images are block-constant and contain no real
//     anti-aliasing, so skipping the AA neighbourhood scan is behaviourally
//     equivalent but ~2× faster on the dense-diff path.
//   - GaussianBlur / FastBlur / unknown pixelators → faithful Pixelmatch (AA
//     exclusion required for cross-rendering-engine robustness).
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
	"context"
	"fmt"
	"image"
	"image/draw"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/locate"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
	"github.com/oioio-space/unpixel/internal/secrets"
)

// alignPhaseStep is the sub-block phase increment used by alignedDist. Matches
// the bestDistance test sweep: at block=32 this gives 4 phase values per axis
// (0, 8, 16, 24); at block ≤ 8 only phase 0 is tried.
const alignPhaseStep = 8

// alignPosStep is the pixel-position slide increment used by alignedDist.
const alignPosStep = 4

// alignPosRange is the maximum slide distance (inclusive) in each axis.
// Matches the bestDistance test sweep: ox/oy ∈ {0, 4, 8, …, 64}.
const alignPosRange = 64

// blurDefaultBeamWidth is the beam width RecoverBlurred uses when the caller
// has not supplied WithStrategy or WithBeamWidth. 32 comfortably exceeds the
// typical blur charset (~10–27 chars), giving full per-level coverage for
// small alphabets while bounding work to O(length × 32) for wider ones.
const blurDefaultBeamWidth = 32

func init() {
	unpixel.DefaultComponents = Wire
	unpixel.DefaultBlurStrategy = func() unpixel.Strategy {
		return search.NewBeamStrategy(blurDefaultBeamWidth)
	}
	unpixel.DefaultLocateMosaicBand = locate.LocateMosaicBand
	unpixel.DefaultConstrainedStrategy = func(prefix string) unpixel.Strategy {
		return constrainedGuidedStrategy{prefix: prefix}
	}
	unpixel.DefaultFormatStrategy = func(f secrets.Format) unpixel.Strategy {
		return formatConstrainedStrategy{format: f}
	}
	unpixel.DefaultVerifyCore = verifyCore
	unpixel.DefaultVerifyImageCore = verifyImageCore
}

// alignTolerance is the per-pixel YIQ colour-difference threshold used by
// alignedDist. It matches the reference bestDistance test metric
// (metric.NewPixelmatch(0.1)) — lenient enough to tolerate the small
// floating-point rounding differences that arise when comparing our renderer's
// linear-light block averages to a GIMP/GEGL-generated target. The in-pipeline
// metric (PixelmatchFast at 0.02) is too strict for this cross-renderer context.
const alignTolerance = 0.10

// alignedDist renders cand with cfg's renderer and style, crops to its tight
// ink bounds, then sweeps sub-block phase shifts and pixel-position slides,
// returning the minimum metric distance found. It mirrors the exhaustive
// alignment used by the direct model (see bestDistance in real_mosaic_test.go)
// so that verifyCore can confirm candidates on real redactions whose block-grid
// origin and text position differ from the engine's default pipeline geometry.
//
// The metric used is metric.NewPixelmatch(alignTolerance) — the faithful
// AA-excluding comparison at a threshold matched to the reference implementation.
// This is intentionally independent of cfg.Metric: the configured metric is
// tuned for in-pipeline comparison (our renderer against itself); alignedDist
// compares our renderer against a real external image, where cross-renderer
// colour differences require a looser per-pixel tolerance.
//
// Cost is bounded: ceil(block/alignPhaseStep)² × ceil(alignPosRange/alignPosStep)²
// comparisons per candidate — at block=32 that is 16 × 289 = 4 624 metric calls,
// each operating on a canvas of target.Bounds() pixels.
//
// alignedDist returns 1.0 (worst) if cfg.Renderer or cfg.Pixelator is absent
// or if rendering fails.
func alignedDist(ctx context.Context, cand string, target *image.RGBA, cfg unpixel.Config) float64 {
	if cfg.Renderer == nil || cfg.Pixelator == nil {
		return 1.0
	}

	img, sentinelX, err := cfg.Renderer.Render(cand, cfg.Style)
	if err != nil {
		return 1.0
	}

	// Crop to tight ink bounds so that padding added by applyDefaults (PaddingTop,
	// PaddingLeft) does not inflate the candidate dimensions.
	bb := imutil.InkBounds(img, sentinelX)
	ink := imutil.Crop(img, bb.Min.X, bb.Min.Y, bb.Dx(), bb.Dy())

	block := max(1, cfg.BlockSize)
	tw := target.Bounds().Dx()
	th := target.Bounds().Dy()

	// Fixed metric for cross-renderer comparison: lenient AA-excluding pixelmatch,
	// same as the reference bestDistance implementation.
	m := metric.NewPixelmatch(alignTolerance)

	// Allocate the canvas once; reuse it across all (phase, position) pairs.
	canvas := image.NewRGBA(image.Rect(0, 0, tw, th))

	best := 1.0
	for px := 0; px < block; px += alignPhaseStep {
		for py := 0; py < block; py += alignPhaseStep {
			if ctx.Err() != nil {
				return best
			}
			// Shift the pixelation grid phase: prepend (px, py) pixels of white
			// so the pixelation grid origin is displaced relative to the ink.
			pad := image.NewRGBA(image.Rect(0, 0, ink.Bounds().Dx()+px, ink.Bounds().Dy()+py))
			imutil.FillWhite(pad)
			imutil.Compose(pad, ink, px, py)

			cPx := cfg.Pixelator.Pixelate(pad, 0, 0)
			cw := cPx.Bounds().Dx()
			ch := cPx.Bounds().Dy()
			if cw > tw || ch > th {
				continue
			}

			for ox := 0; ox+cw <= tw && ox <= alignPosRange; ox += alignPosStep {
				for oy := 0; oy+ch <= th && oy <= alignPosRange; oy += alignPosStep {
					if ctx.Err() != nil {
						return best
					}
					imutil.FillWhite(canvas)
					imutil.Compose(canvas, cPx, ox, oy)
					if d := m.Compare(canvas, target); d < best {
						best = d
					}
				}
			}
		}
	}
	return best
}

// verifyCore implements the DefaultVerifyCore hook. It scores each candidate
// via two complementary paths and takes the minimum distance:
//
//  1. Pipeline path: the existing render→BlueMargin→crop→pad→pixelate→LeftEdge
//     pipeline (TotalScore) at the best discovered block-grid offset. This
//     handles tight, pipeline-generated fixtures where the candidate's geometry
//     exactly matches the engine's crop/pad geometry.
//
//  2. Alignment path (alignedDist): render → ink-crop → sub-block-phase shift
//     + pixel-position slide. This handles real redactions whose block-grid
//     origin is not at pixel (0,0) and whose textual content is not at the
//     image's top-left corner — the geometry that verifyCore previously missed.
//
// Taking the min of both paths ensures that candidates correct for either
// geometry class score near 0, and that adding the alignment path cannot raise
// any existing passing distance.
func verifyCore(ctx context.Context, rgba *image.RGBA, cfg unpixel.Config, candidates []string) ([]unpixel.Verdict, error) {
	scorer := search.NewCachingScorer(search.NewPipelineScorer(rgba, cfg), cfg.CacheSize)
	offsets := search.DiscoverOffsets(ctx, scorer, cfg, func(unpixel.Progress) {})

	// Fall back to the zero offset when none survive the threshold gate.
	if len(offsets) == 0 {
		offsets = []unpixel.Offset{{}}
	}

	verdicts := make([]unpixel.Verdict, len(candidates))
	for i, cand := range candidates {
		// Pipeline path: best block-grid offset.
		dist := 1.0
		for _, off := range offsets {
			if d := scorer.TotalScore(ctx, cand, off); d < dist {
				dist = d
			}
		}
		// Alignment path: ink-crop + phase/position slide, run ONLY as a fallback
		// when the fast pipeline path did not already confirm a match. This keeps
		// the common/synthetic case (pipeline geometry) free of the ~block/8² ×
		// (range/step)² comparisons alignedDist costs, paying them only on real
		// external redactions whose geometry defeats the pipeline path. Its result
		// is applied only when it is itself a genuine physical confirmation (below
		// the match threshold): the lenient cross-renderer metric can coincidentally
		// lower near-miss candidates on small images, so gating on the threshold
		// makes it a confirmer only, never a discriminator between near-misses.
		if dist >= unpixel.VerifyMatchThreshold {
			if d := alignedDist(ctx, cand, rgba, cfg); d < unpixel.VerifyMatchThreshold && d < dist {
				dist = d
			}
		}
		verdicts[i] = unpixel.Verdict{
			Text:     cand,
			Distance: dist,
			Match:    dist < unpixel.VerifyMatchThreshold,
		}
	}
	return verdicts, nil
}

// verifyImageCore implements the DefaultVerifyImageCore hook. It re-applies the
// forward operator (cfg.Pixelator) to the restored image and compares it to the
// redaction (cfg.Metric), taking the minimum distance over grid phases — the
// image analogue of verifyCore's best-offset search. restored is resized to the
// redaction's pixel dimensions (pure-Go CatmullRom) when their sizes differ.
//
// The key invariant: cfg.Pixelator.Pixelate may pad its output to a block
// multiple (e.g. BlockAverage pads width to the next multiple of blockSize). If
// redacted's width is not a multiple of blockSize, reMosaic.Bounds() !=
// redacted.Bounds() and metric.Compare short-circuits on unequal bounds,
// returning 0.0 — a false accept for any input. To prevent this, verifyImageCore:
//  1. Normalises redacted to a zero-Min rectangle of size dx×dy (redZero).
//  2. Scales restored into the same dx×dy rectangle (rest).
//  3. After each Pixelate call, crops reMosaic back to dx×dy before Compare.
//
// This guarantees Compare always sees equal bounds over the real (unpadded)
// pixel region, so garbage restorations receive a genuinely high distance.
func verifyImageCore(ctx context.Context, redacted, restored *image.RGBA, cfg unpixel.Config) (unpixel.ImageVerdict, error) {
	rb := redacted.Bounds()
	dx, dy := rb.Dx(), rb.Dy()
	target := image.Rect(0, 0, dx, dy)

	// Step 1: normalise redacted to zero-Min so Crop and Compare operate on a
	// consistent rectangle regardless of the caller's image origin.
	redZero := redacted
	if rb.Min != (image.Point{}) {
		redZero = imutil.Crop(redacted, 0, 0, dx, dy)
	}

	// Step 2: scale restored into target (dx×dy, zero-Min).
	rest := image.NewRGBA(target)
	if restored.Bounds() == target {
		// Fast path: restored already has the right size and origin; copy to
		// preserve the invariant that rest is always a fresh zero-Min image.
		draw.Draw(rest, target, restored, image.Point{}, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(rest, target, restored, restored.Bounds(), xdraw.Over, nil)
	}

	block := cfg.BlockSize
	if block < 1 {
		block = 1
	}

	// best starts at 1.0, the metric's maximum (all pixels differ); metrics
	// return values in [0, 1] so any real comparison will drive it down.
	best := 1.0
	for oy := range block {
		for ox := range block {
			if ctx.Err() != nil {
				return unpixel.ImageVerdict{}, ctx.Err()
			}
			// Pixelate may pad reMosaic to a block multiple; crop it back to the
			// real region so metric.Compare sees equal bounds on both sides.
			reMosaic := cfg.Pixelator.Pixelate(rest, ox, oy)
			reCropped := imutil.Crop(reMosaic, 0, 0, dx, dy)
			if d := cfg.Metric.Compare(reCropped, redZero); d < best {
				best = d
			}
		}
	}

	return unpixel.ImageVerdict{Distance: best, Match: best < unpixel.VerifyMatchThreshold}, nil
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
		// Auto-select the fast no-AA metric for block-average mosaic pixelators:
		// mosaic images are block-constant and contain no real anti-aliasing, so
		// skipping the AA neighbourhood scan is behaviourally equivalent but ~2×
		// faster on the dense-diff path. For blur (GaussianBlur/FastBlur) or
		// unknown pixelators, keep the faithful Pixelmatch (AA exclusion required
		// for cross-rendering-engine robustness).
		if _, ok := cfg.Pixelator.(*pixelate.BlockAverage); ok {
			cfg.Metric = metric.NewPixelmatchFast(0.02)
		} else {
			cfg.Metric = metric.NewPixelmatch(0.02)
		}
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

// LinearBlockAverage returns a mosaic operator that averages each block in
// linear light (rather than sRGB), matching GIMP's GEGL Pixelize, CSS, and most
// image editors. Their block mean of dark text on a light background is lighter
// than the sRGB mean, so a redaction produced by those tools is reproduced
// faithfully only with this variant. Assign it to Config.Pixelator alongside a
// matching Config.BlockSize:
//
//	cfg := unpixel.Config{Pixelator: defaults.LinearBlockAverage(16), BlockSize: 16}
func LinearBlockAverage(blockSize int) unpixel.Pixelator {
	return pixelate.NewLinearBlockAverage(blockSize)
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

// Deblur sharpens a Gaussian-blurred image with pure-Go Richardson-Lucy
// deconvolution (a Gaussian point-spread function of the given sigma, run for the
// given number of iterations) and returns a fresh *image.RGBA. img is copied, so
// the input is never mutated; iterations <= 0 or sigma <= 0 returns an unmodified
// copy. Estimate sigma with [github.com/oioio-space/unpixel.InferBlurSigma].
//
// This is an exploratory preprocessing/inspection step, not part of the
// generate-and-test loop (which already reproduces blur on each candidate): for
// recovering blurred redactions the default render→blur→compare search is usually
// stronger. Deblur is useful to sharpen an image for visual inspection or as a
// front-end to other tooling. 5–30 iterations is the usual range.
func Deblur(img image.Image, sigma float64, iterations int) *image.RGBA {
	src, ok := img.(*image.RGBA)
	if !ok {
		b := img.Bounds()
		src = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	}
	return pixelate.RichardsonLucy(src, sigma, iterations)
}

// LanguageModel returns the bundled character-bigram plausibility scorer (higher
// = more plausible text), ready to assign to Config.LanguageModel or pass via
// WithLanguageModel. It breaks ties between candidates of equal image distance:
//
//	res, _ := unpixel.Recover(ctx, img, unpixel.WithLanguageModel(defaults.LanguageModel()))
func LanguageModel() func(string) float64 {
	return lang.Default().Score
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

// MonospaceStrategy returns the monospace fast-path strategy as an
// unpixel.Strategy. For fixed-advance fonts, cells are independent, so it
// classifies each position greedily with charset-wide parallelism — far cheaper
// than the backtracking DFS. Use it for code/secret redactions in a monospace
// face; recovery degrades if the font is actually proportional.
func MonospaceStrategy() unpixel.Strategy {
	return search.NewMonospaceStrategy()
}

// PixelmatchMetric returns the faithful default image-distance metric (a YIQ
// perceptual pixel-difference, matching the original Jimp.diff) as an
// unpixel.Metric, ready to assign to Config.Metric. It performs anti-aliasing
// neighbourhood exclusion, making it robust for cross-rendering-engine comparisons.
// Wire selects this automatically for GaussianBlur/FastBlur pixelators.
func PixelmatchMetric() unpixel.Metric {
	return metric.NewPixelmatch(0.02)
}

// PixelmatchFastMetric returns the no-AA image-distance metric as an
// unpixel.Metric, ready to assign to Config.Metric. It omits the anti-aliasing
// neighbourhood exclusion, which is equivalent for block-constant
// (mosaic-pixelated) images — and roughly 2× faster on the dense-diff path.
// Wire selects this automatically for BlockAverage/LinearBlockAverage pixelators.
func PixelmatchFastMetric() unpixel.Metric {
	return metric.NewPixelmatchFast(0.02)
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

// constrainedGuidedStrategy implements unpixel.Strategy using GuidedDFSConstrained.
// It is wired by the DefaultConstrainedStrategy hook when WithPrefix is active.
type constrainedGuidedStrategy struct {
	prefix string
}

// Search runs offset discovery then GuidedDFSConstrained per surviving offset,
// fanned out across cfg.Workers goroutines with a deterministic merge. It is
// byte-identical to GuidedStrategy when prefix is empty — GuidedDFSConstrained
// delegates to GuidedDFS when the constraint returns nil at every position.
func (s constrainedGuidedStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	scorer := search.NewCachingScorer(search.NewPipelineScorer(redacted, cfg), cfg.CacheSize)
	c := search.NewPrefixConstraint(s.prefix)
	dfs := func(ctx context.Context, sc search.Scorer, cfg unpixel.Config, offset unpixel.Offset, emit func(unpixel.Eval)) {
		search.GuidedDFSConstrained(ctx, sc, cfg, offset, c, emit)
	}
	search.Offsets(ctx, scorer, cfg, out, results, dfs)
}

// formatConstrainedStrategy implements unpixel.Strategy using
// GuidedDFSConstrained with a secrets.Format constraint, plus a leaf filter that
// drops complete-but-invalid candidates (failing Luhn/mod-97/date/phone rules)
// from the results competition. It is wired by the DefaultFormatStrategy hook
// when WithExpectedFormat is active.
type formatConstrainedStrategy struct {
	format secrets.Format
}

// Search runs offset discovery then, per surviving offset, GuidedDFSConstrained
// with the format constraint and a validity leaf filter. Dropping an emit only
// removes a candidate from the results race; child exploration is unaffected, so
// shorter invalid prefixes (e.g. a non-Luhn 16-digit card) do not prevent longer
// valid candidates (a 19-digit card) from being found.
func (s formatConstrainedStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	scorer := search.NewCachingScorer(search.NewPipelineScorer(redacted, cfg), cfg.CacheSize)
	dfs := func(ctx context.Context, sc search.Scorer, cfg unpixel.Config, offset unpixel.Offset, emit func(unpixel.Eval)) {
		c := search.NewFormatConstraint(s.format, cfg.MaxLength)
		filtered := func(ev unpixel.Eval) {
			if secrets.TerminalLen(s.format, len([]rune(ev.Guess))) && !secrets.Valid(s.format, ev.Guess) {
				return
			}
			emit(ev)
		}
		search.GuidedDFSConstrained(ctx, sc, cfg, offset, c, filtered)
	}
	search.Offsets(ctx, scorer, cfg, out, results, dfs)
}
