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
// [WithVarFontAxes], [WithVarFontLinear], and [WithVarFontCharset] to
// customise the decoder.
type VarFontOption func(*varFontConfig)

type varFontConfig struct {
	font      *varfont.Font
	style     unpixel.Style
	blockSize int
	linear    bool
	knownText string // empty → blind mode
	axes      []varfont.AxisSpec
	charset   string
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

	if cfg.knownText != "" {
		return fitKnownText(ctx, font, cfg, target, pix, m, blockSize)
	}
	return fitBlind(ctx, font, cfg, target, pix, m, blockSize)
}

// mkFitConfig builds a varfont.FitConfig from the shared decoder inputs.
// Only Text varies between the known-text and blind-mode call sites.
func mkFitConfig(font *varfont.Font, text string, cfg *varFontConfig, target *image.RGBA, pix unpixel.Pixelator, m unpixel.Metric, blockSize int) varfont.FitConfig {
	return varfont.FitConfig{
		Font:      font,
		Text:      text,
		Style:     cfg.style,
		Target:    target,
		Pixelator: pix,
		Metric:    m,
		BlockSize: blockSize,
		Axes:      cfg.axes,
	}
}

// fitKnownText runs FitAxes for the caller-supplied text and returns the
// fitted result. This is the fast, reliable calibration-mode path.
func fitKnownText(_ context.Context, font *varfont.Font, cfg *varFontConfig, target *image.RGBA, pix unpixel.Pixelator, m unpixel.Metric, blockSize int) (VarFontResult, error) {
	res, err := varfont.FitAxes(mkFitConfig(font, cfg.knownText, cfg, target, pix, m, blockSize))
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
func fitBlind(ctx context.Context, font *varfont.Font, cfg *varFontConfig, target *image.RGBA, pix unpixel.Pixelator, m unpixel.Metric, blockSize int) (VarFontResult, error) {
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
		res, err := varfont.FitAxes(mkFitConfig(font, cand, cfg, target, pix, m, blockSize))
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
