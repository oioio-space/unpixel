package mosaictext

// DecodeVarFont recovers text from a mosaic redaction using a variable
// TrueType font whose design axes are fit to the redaction.
//
// # Integration choice
//
// Approach (A): a self-contained new decoder. It reuses the block-grid
// detection and content-bounds helpers shared by the other decoders, then
// delegates axis fitting to [varfont.FitAxes] and text recovery to the same
// render→pixelate→metric pipeline used by the rest of the package. Existing
// decoders (DecodeHMM, DecodeReference, DecodeWindowHMM, DecodeTrainedHMM)
// are entirely unaffected; this file adds no changes to them.
//
// # Calibration modes
//
// Known-text (calibration) mode — [WithVarFontText]:
//
//	The caller supplies a text string that is visible (or known) in the
//	redacted region. FitAxes minimises the render→pixelate→metric distance
//	between that text and the target, finding the best-fit axis values. The
//	fitted instance (Font + axes) is then available via [VarFontResult] for
//	the caller to use for further decoding with the recovered axes.
//	This is the Bishop Fox method: calibrate on a known fragment, decode the
//	rest with the fitted font instance.
//
// Blind mode (best-effort):
//
//	When no [WithVarFontText] is provided, DecodeVarFont attempts a joint
//	text+axis search: for each candidate text in the charset (up to
//	MaxBlindCandidates), it runs FitAxes and keeps the (text, axes) pair with
//	the lowest distance. This is tractable only for very short strings or tiny
//	charsets; callers should prefer the known-text mode for reliable results.
//	Blind mode is honest: if no candidate achieves a distance below
//	BlindDistanceGate it returns [ErrVarFontNoFit].
//
// # Thread safety
//
// DecodeVarFont is safe for concurrent use. Each call allocates its own
// pipeline state; the [varfont.Font] passed via [WithVarFont] is read-only
// and shared safely across calls.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
)

// ErrVarFontNoFit is returned when blind mode cannot find any (text, axes)
// pair whose distance falls below [BlindDistanceGate].
var ErrVarFontNoFit = errors.New("mosaictext: varfont decoder found no acceptable fit")

// BlindDistanceGate is the maximum distance accepted in blind mode. Candidates
// above this gate are considered non-matches. It is intentionally generous;
// callers that need stricter filtering should check [VarFontResult.Distance].
const BlindDistanceGate = 0.5

// MaxBlindCandidates is the maximum number of text candidates tried in blind
// mode. It caps the joint search to keep runtime tractable.
const MaxBlindCandidates = 64

// DefaultVarFontBlockSize is the block size used by DecodeVarFont when neither
// the caller nor the image provides one. It matches the most common GIMP/GEGL
// redaction size.
const DefaultVarFontBlockSize = 8

// VarFontResult is the output of [DecodeVarFont].
type VarFontResult struct {
	// Text is the recovered (or caller-supplied calibration) text.
	Text string
	// FittedAxes holds the best-fit design-space coordinates for each axis,
	// in the same order as the [AxisSpec] slice passed to [WithVarFontAxes].
	FittedAxes []varfont.Axis
	// Distance is the image-metric value at the best-fit (text, axes) pair.
	// Lower is better; ~0 means the candidate reproduces the redaction
	// near-exactly.
	Distance float64
	// Evals is the total number of render+pixelate+metric evaluations
	// performed by FitAxes across all candidates.
	Evals int
	// Linear reports whether linear-light block averaging was used (true =
	// GEGL/GIMP pixelation, false = sRGB).
	Linear bool
	// BlockSize is the mosaic block side length used for the fit.
	BlockSize int
}

// VarFontOption configures [DecodeVarFont]. Use [WithVarFont],
// [WithVarFontStyle], [WithVarFontBlockSize], [WithVarFontText],
// [WithVarFontAxes], [WithVarFontLinear], [WithVarFontCharset],
// [WithVarFontVisible], and [WithVarFontOptimizer] to customise the decoder.
type VarFontOption func(*varFontConfig)

type varFontConfig struct {
	font              *varfont.Font
	style             unpixel.Style
	blockSize         int
	linear            bool
	knownText         string // empty → blind mode
	axes              []varfont.AxisSpec
	charset           string
	visibleCrop       *image.RGBA // sharp crop for calibration; nil → skip
	visibleText       string      // cleartext matching visibleCrop
	calibrateGeometry bool        // recover font size + x-stretch from visibleCrop
	optimizer         varfont.OptimizerKind
}

// WithVarFont sets the parsed variable font to use. Required; DecodeVarFont
// returns an error when no font is supplied and the bundled Nunito default
// cannot be parsed.
func WithVarFont(f *varfont.Font) VarFontOption {
	return func(c *varFontConfig) { c.font = f }
}

// WithVarFontStyle sets the rendering style (font size, padding,
// letter-spacing). Defaults to [varfont.DefaultStyle] when not supplied.
func WithVarFontStyle(s unpixel.Style) VarFontOption {
	return func(c *varFontConfig) { c.style = s }
}

// WithVarFontBlockSize pins the mosaic block size in pixels. When 0 (the
// default), DecodeVarFont infers it from the image via [unpixel.InferBlockGrid].
func WithVarFontBlockSize(b int) VarFontOption {
	return func(c *varFontConfig) { c.blockSize = b }
}

// WithVarFontLinear enables linear-light block averaging (GEGL/GIMP Pixelize,
// CSS pixelate). Default false (sRGB averaging). When unsure, try both and
// keep the lower distance.
func WithVarFontLinear(linear bool) VarFontOption {
	return func(c *varFontConfig) { c.linear = linear }
}

// WithVarFontText enables calibration mode: FitAxes minimises the distance
// between the rendered text and the target. When not supplied, DecodeVarFont
// runs blind mode (joint text+axis search, tractable only for short strings).
func WithVarFontText(text string) VarFontOption {
	return func(c *varFontConfig) { c.knownText = text }
}

// WithVarFontAxes sets the axes to optimise and their search ranges. At least
// one AxisSpec is required.
func WithVarFontAxes(axes []varfont.AxisSpec) VarFontOption {
	return func(c *varFontConfig) { c.axes = axes }
}

// WithVarFontCharset sets the candidate character set for blind mode.
// Ignored in calibration mode (WithVarFontText). Defaults to [defaultCharset].
func WithVarFontCharset(cs string) VarFontOption {
	return func(c *varFontConfig) { c.charset = cs }
}

// WithVarFontVisible enables calibration-from-visible: before fitting the
// redaction, DecodeVarFont calls [varfont.CalibrateFromVisible] on the
// supplied sharp crop (visibleCrop) of the known text (visibleText) to find
// the best-fit axis values. Those fitted values are then used as warm-start
// [varfont.AxisSpec.Start] values when fitting the redaction, replacing the
// AxisSpec.Start values provided via [WithVarFontAxes].
//
// visibleCrop must be a sharp (un-pixelated) crop of the text adjacent to the
// redaction. The string visibleText must match the content of visibleCrop
// exactly (case-sensitive). Both arguments are required; passing a nil crop or
// an empty string is a no-op (calibration step is skipped).
//
// This is the high-value path: a strong, unambiguous objective on sharp glyphs
// resolves axis values much more precisely than fitting on a pixelated block.
// Non-regressive: existing calls without this option are unaffected.
func WithVarFontVisible(visibleCrop *image.RGBA, visibleText string) VarFontOption {
	return func(c *varFontConfig) {
		if visibleCrop != nil && visibleText != "" {
			c.visibleCrop = visibleCrop
			c.visibleText = visibleText
		}
	}
}

// WithVarFontCalibrateGeometry enables geometry calibration from the visible
// crop supplied via [WithVarFontVisible]. When set, [DecodeVarFont] calls
// [varfont.CalibrateGeometry] on the visible crop before axis fitting, and
// feeds the recovered font size and x-stretch into the decode pipeline so the
// forward model matches the exact physical rendering parameters of the source.
//
// Geometry calibration runs BEFORE axis fitting: it resolves scale first, then
// the axis fitter works on a geometry-correct render, which improves convergence
// when the true font size differs significantly from [varfont.DefaultStyle].
//
// Requires [WithVarFontVisible] to be set; the option is ignored otherwise.
// Non-regressive: absent this option, behaviour is unchanged.
func WithVarFontCalibrateGeometry() VarFontOption {
	return func(c *varFontConfig) { c.calibrateGeometry = true }
}

// WithVarFontOptimizer selects the search strategy used by FitAxes (and the
// calibration step when [WithVarFontVisible] is supplied). The default
// ([varfont.OptimizerCoordDescent]) is stable and fast for a single axis;
// [varfont.OptimizerNelderMead] is better suited to coupled multi-axis
// landscapes. Non-regressive: existing calls without this option use the
// default (coordinate descent).
func WithVarFontOptimizer(opt varfont.OptimizerKind) VarFontOption {
	return func(c *varFontConfig) { c.optimizer = opt }
}

// wrapStretch returns a [stretchPixelator] when stretch ≠ 1.0, or inner
// unchanged when stretch is unity (no-op fast path).
func wrapStretch(inner unpixel.Pixelator, stretch float64) unpixel.Pixelator {
	if stretch == 1.0 {
		return inner
	}
	return stretchPixelator{inner: inner, stretch: stretch}
}

// stretchPixelator wraps an [unpixel.Pixelator] and applies a horizontal
// x-stretch to every rendered image before pixelating. It is used when
// geometry calibration has recovered a non-unity x-stretch: the forward
// model must replicate the stretch so that axis fitting operates on
// geometry-correct candidates.
//
// The stretch is applied using CatmullRom bicubic interpolation — the same
// kernel used by [varfont.CalibrateGeometry] — so the two pipelines are
// pixel-consistent.
type stretchPixelator struct {
	inner   unpixel.Pixelator
	stretch float64 // > 0; 1.0 = no-op
}

// Pixelate applies the x-stretch and then delegates to the inner pixelator.
func (s stretchPixelator) Pixelate(img *image.RGBA, originX, originY int) *image.RGBA {
	if s.stretch == 1.0 {
		return s.inner.Pixelate(img, originX, originY)
	}
	b := img.Bounds()
	nw := int(math.Round(float64(b.Dx()) * s.stretch))
	if nw < 1 {
		nw = 1
	}
	var scaled *image.RGBA
	if nw == b.Dx() {
		scaled = img
	} else {
		scaled = image.NewRGBA(image.Rect(0, 0, nw, b.Dy()))
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, b, xdraw.Over, nil)
	}
	return s.inner.Pixelate(scaled, originX, originY)
}

// DecodeVarFont recovers text from a mosaic-pixelated redaction using a
// variable TrueType font whose design axes are fit to the target image.
//
// In calibration mode ([WithVarFontText] supplied), it fits the font axes to
// the known text and returns the fitted instance; the caller uses
// [VarFontResult.FittedAxes] to decode the rest of the redaction.
//
// In blind mode (no [WithVarFontText]), it attempts a joint text+axis search
// over [WithVarFontCharset] candidates (up to [MaxBlindCandidates]) and
// returns [ErrVarFontNoFit] when no candidate achieves a distance below
// [BlindDistanceGate].
//
// The axis fit uses the same render→pixelate→metric pipeline as the rest of
// the package, so the fitted instance is the one that best explains the
// redaction under the same objective function.
func DecodeVarFont(ctx context.Context, img image.Image, opts ...VarFontOption) (VarFontResult, error) {
	cfg := &varFontConfig{
		style:   varfont.DefaultStyle(),
		charset: defaultCharset,
	}
	for _, o := range opts {
		o(cfg)
	}

	if len(cfg.axes) == 0 {
		return VarFontResult{}, errors.New("mosaictext: DecodeVarFont requires at least one axis via WithVarFontAxes")
	}

	// Resolve the variable font: use the caller-supplied one, or parse the
	// bundled Nunito default.
	font := cfg.font
	if font == nil {
		var parseErr error
		font, parseErr = varfont.ParseFont(bytes.NewReader(vfembed.NunitoVFWght))
		if parseErr != nil {
			return VarFontResult{}, fmt.Errorf("mosaictext: parse bundled varfont: %w", parseErr)
		}
	}

	// Resolve block size: caller-supplied or inferred from the image.
	blockSize := cfg.blockSize
	if blockSize <= 0 {
		grid, ok := unpixel.InferBlockGrid(img)
		if !ok || grid.Size < 2 {
			return VarFontResult{}, ErrNoMosaic
		}
		blockSize = grid.Size
	}

	// Convert to RGBA and use the image as-is as the FitAxes target.
	// We do not apply contentBounds here: the caller supplies the redaction
	// region directly (possibly already pixelated), and FitAxes handles
	// size normalisation internally via cropToSize. Cropping by luminance
	// would include blue sentinel blocks from VarRenderer outputs and break
	// the comparison.
	target := toRGBA(img)
	if target.Bounds().Empty() {
		return VarFontResult{}, ErrNoContent
	}

	// Build the pixelator and metric matching the rest of the package.
	var pix unpixel.Pixelator
	if cfg.linear {
		pix = pixelate.NewLinearBlockAverage(blockSize)
	} else {
		pix = pixelate.NewBlockAverage(blockSize)
	}
	m := metric.NewPixelmatchFast(0.1)

	// Calibration-from-visible: when the caller supplies a sharp visible crop
	// alongside its known text, optionally run CalibrateGeometry first (to
	// recover exact font size and x-stretch), then run CalibrateFromVisible to
	// warm-start the axis values. Both steps are non-fatal: a failure falls
	// through to the original style / axes.
	axes := cfg.axes
	style := cfg.style // local copy so geometry updates don't mutate cfg
	xStretch := 1.0
	if cfg.visibleCrop != nil && cfg.visibleText != "" {
		// Step 1 (opt-in): geometry calibration — recover FontSizePx + XStretch.
		if cfg.calibrateGeometry {
			geomResult, geomErr := varfont.CalibrateGeometry(varfont.GeometryConfig{
				Font:   font,
				Text:   cfg.visibleText,
				Style:  style,
				Target: cfg.visibleCrop,
				Metric: m,
			})
			if geomErr == nil {
				style.FontSize = geomResult.FontSizePx
				xStretch = geomResult.XStretch
			}
			// Non-fatal: failed geometry calibration → keep original style.
		}

		// Step 2: axis calibration on the geometry-corrected render.
		calResult, calErr := varfont.CalibrateFromVisible(varfont.CalibrateConfig{
			Font:      font,
			Text:      cfg.visibleText,
			Style:     style,
			Target:    cfg.visibleCrop,
			Pixelator: nil, // sharp text — compare directly
			Metric:    m,
			Axes:      cfg.axes,
			Optimizer: cfg.optimizer,
		})
		if calErr == nil {
			// Promote fitted values to warm-start positions for the redaction fit.
			warmed := make([]varfont.AxisSpec, len(axes))
			copy(warmed, axes)
			for i, a := range calResult.Axes {
				warmed[i].Start = a.Value
			}
			axes = warmed
		}
		// Non-fatal: if calibration fails we fall through with the original axes.
	}

	// When geometry calibration recovered a non-unity x-stretch, wrap the
	// pixelator so the FitAxes forward model stretches candidates before
	// pixelating — matching the physical rendering pipeline.
	activePix := wrapStretch(pix, xStretch)

	// Pass the (possibly geometry-updated) style down to the fit functions via
	// a shallow config copy so the original cfg is not mutated.
	fitCfg := cfg
	fitCfg.style = style

	if fitCfg.knownText != "" {
		return fitKnownText(ctx, font, fitCfg, axes, target, activePix, m, blockSize)
	}
	return fitBlind(ctx, font, fitCfg, axes, target, activePix, m, blockSize)
}

// mkFitConfig builds a varfont.FitConfig from the shared decoder inputs.
// Only Text varies between the known-text and blind-mode call sites.
func mkFitConfig(font *varfont.Font, text string, cfg *varFontConfig, axes []varfont.AxisSpec, target *image.RGBA, pix unpixel.Pixelator, m unpixel.Metric, blockSize int) varfont.FitConfig {
	return varfont.FitConfig{
		Font:      font,
		Text:      text,
		Style:     cfg.style,
		Target:    target,
		Pixelator: pix,
		Metric:    m,
		BlockSize: blockSize,
		Axes:      axes,
		Optimizer: cfg.optimizer,
	}
}

// fitKnownText runs FitAxes for the caller-supplied text and returns the
// fitted result. This is the fast, reliable calibration-mode path.
func fitKnownText(_ context.Context, font *varfont.Font, cfg *varFontConfig, axes []varfont.AxisSpec, target *image.RGBA, pix unpixel.Pixelator, m unpixel.Metric, blockSize int) (VarFontResult, error) {
	res, err := varfont.FitAxes(mkFitConfig(font, cfg.knownText, cfg, axes, target, pix, m, blockSize))
	if err != nil {
		return VarFontResult{}, fmt.Errorf("mosaictext: FitAxes: %w", err)
	}
	return VarFontResult{
		Text:       cfg.knownText,
		FittedAxes: res.Axes,
		Distance:   res.Distance,
		Evals:      res.Evals,
		Linear:     cfg.linear,
		BlockSize:  blockSize,
	}, nil
}

// fitBlind runs a joint text+axis search over the charset candidates. For each
// candidate text it runs FitAxes and keeps the (text, axes) pair with the
// lowest distance. Returns [ErrVarFontNoFit] when no candidate clears
// [BlindDistanceGate].
func fitBlind(ctx context.Context, font *varfont.Font, cfg *varFontConfig, axes []varfont.AxisSpec, target *image.RGBA, pix unpixel.Pixelator, m unpixel.Metric, blockSize int) (VarFontResult, error) {
	charset := []rune(cfg.charset)
	// Cap the search to MaxBlindCandidates single-character candidates (the
	// tractable case for blind mode).
	candidates := make([]string, 0, min(len(charset), MaxBlindCandidates))
	for i, ch := range charset {
		if i >= MaxBlindCandidates {
			break
		}
		candidates = append(candidates, string(ch))
	}

	bestDist := math.Inf(1)
	bestText := ""
	var bestAxes []varfont.Axis
	totalEvals := 0

	for _, cand := range candidates {
		if err := ctx.Err(); err != nil {
			return VarFontResult{}, fmt.Errorf("mosaictext: fitBlind: %w", err)
		}
		res, err := varfont.FitAxes(mkFitConfig(font, cand, cfg, axes, target, pix, m, blockSize))
		if err != nil {
			continue
		}
		totalEvals += res.Evals
		if res.Distance < bestDist {
			bestDist = res.Distance
			bestText = cand
			bestAxes = res.Axes
		}
	}

	if bestDist > BlindDistanceGate || bestText == "" {
		return VarFontResult{}, ErrVarFontNoFit
	}
	return VarFontResult{
		Text:       bestText,
		FittedAxes: bestAxes,
		Distance:   bestDist,
		Evals:      totalEvals,
		Linear:     cfg.linear,
		BlockSize:  blockSize,
	}, nil
}
