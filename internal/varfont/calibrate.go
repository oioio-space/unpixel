package varfont

// CalibrateFromVisible fits font axes (and optionally size, x-stretch,
// letter-spacing) to a crop of KNOWN visible text — the cleartext adjacent to
// a redaction. Because the text is sharp (un-pixelated), the objective is much
// stronger than the redaction objective: even a single-axis wght fit on two or
// three glyphs resolves to within ±50 design-space units.
//
// When Pixelator is nil the comparison is made directly against the sharp
// target (recommended for visible text). When Pixelator is non-nil the
// pipeline mirrors FitAxes: render → pixelate → metric, which is useful when
// the visible crop is itself a pixelated thumbnail.
//
// The returned CalibrateResult carries the fitted axis values; feed them as
// AxisSpec.Start values into FitAxes to lock the font and decode the redacted
// region with a strong warm-start.
//
// Thread safety: safe for concurrent use — each call clones its own Font Face.
//
// Example — calibrate on "Hello" adjacent to a redaction, then decode:
//
//	cal, err := varfont.CalibrateFromVisible(varfont.CalibrateConfig{
//	    Font:   font,
//	    Text:   "Hello",
//	    Style:  varfont.DefaultStyle(),
//	    Target: visibleCrop,  // sharp render adjacent to the redaction
//	    Metric: metric.NewPixelmatchFast(0.1),
//	})
//	// Lock the fitted wght and decode the redaction:
//	res, err := varfont.FitAxes(varfont.FitConfig{
//	    Axes: []varfont.AxisSpec{{Tag:"wght", Min:200, Max:900, Start: cal.Axes[0].Value}},
//	    ...
//	})

import (
	"fmt"
	"image"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// CalibrateConfig holds all inputs for [CalibrateFromVisible].
type CalibrateConfig struct {
	// Font is the shared, read-only parsed variable font.
	Font *Font
	// Text is the known cleartext string visible in Target.
	Text string
	// Style controls font size, padding, and letter-spacing.
	Style unpixel.Style
	// Target is the sharp (un-pixelated) visible-text crop to match against.
	// It is read-only; CalibrateFromVisible never modifies it.
	Target *image.RGBA
	// Pixelator, when non-nil, is applied to the rendered candidate before
	// comparing against Target. Use nil for sharp visible text (the default
	// and recommended path); set it to match a pre-pixelated visible crop.
	Pixelator unpixel.Pixelator
	// Metric measures image distance between two RGBA images.
	Metric unpixel.Metric
	// Axes specifies which font axes to optimise and their search ranges.
	// At least one AxisSpec is required.
	Axes []AxisSpec
	// MaxIter is the maximum number of optimiser rounds.
	// Zero uses [DefaultMaxIter].
	MaxIter int
	// InitStep is the initial step size in design-space units.
	// Zero uses [DefaultInitStep].
	InitStep float32
	// ShrinkFactor is the per-round step shrinkage factor in (0,1).
	// Zero uses [DefaultShrinkFactor].
	ShrinkFactor float32
	// Optimizer selects the search strategy.
	// Zero uses [OptimizerCoordDescent] (coordinate descent).
	Optimizer OptimizerKind
}

// CalibrateResult is the output of [CalibrateFromVisible].
// It mirrors [FitResult] so callers can use the same downstream logic.
type CalibrateResult = FitResult

// CalibrateFromVisible fits font axes to a crop of known visible text.
// Unlike [FitAxes], the comparison target is sharp (un-pixelated) by default —
// set CalibrateConfig.Pixelator when the visible crop is itself pixelated.
//
// The strong sharp-text objective resolves axis values faster and more
// precisely than fitting on a pixelated redaction. Use the returned
// [CalibrateResult.Axes] as warm-start [AxisSpec.Start] values when calling
// [FitAxes] on the adjacent redaction.
func CalibrateFromVisible(cfg CalibrateConfig) (CalibrateResult, error) {
	if cfg.Font == nil {
		return CalibrateResult{}, fmt.Errorf("varfont.CalibrateFromVisible: Font must not be nil")
	}
	if cfg.Target == nil {
		return CalibrateResult{}, fmt.Errorf("varfont.CalibrateFromVisible: Target must not be nil")
	}
	if cfg.Text == "" {
		return CalibrateResult{}, fmt.Errorf("varfont.CalibrateFromVisible: Text must not be empty")
	}
	if len(cfg.Axes) == 0 {
		return CalibrateResult{}, fmt.Errorf("varfont.CalibrateFromVisible: at least one AxisSpec required")
	}
	if cfg.Metric == nil {
		return CalibrateResult{}, fmt.Errorf("varfont.CalibrateFromVisible: Metric must not be nil")
	}

	// Delegate to FitAxes via a sharpFitConfig that wraps the sharp objective.
	// When Pixelator is nil we supply a no-op pixelator so FitAxes' signature
	// is satisfied, then strip it in the evaluate call via the sharpPixelator.
	var pix unpixel.Pixelator = sharpPassthrough{}
	if cfg.Pixelator != nil {
		pix = cfg.Pixelator
	}

	return FitAxes(FitConfig{
		Font:         cfg.Font,
		Text:         cfg.Text,
		Style:        cfg.Style,
		Target:       cfg.Target,
		Pixelator:    pix,
		Metric:       cfg.Metric,
		BlockSize:    0, // unused when Pixelator is sharpPassthrough
		Axes:         cfg.Axes,
		MaxIter:      cfg.MaxIter,
		InitStep:     cfg.InitStep,
		ShrinkFactor: cfg.ShrinkFactor,
		Optimizer:    cfg.Optimizer,
	})
}

// sharpPassthrough is a Pixelator that returns its input unchanged. It is used
// by CalibrateFromVisible when the caller provides no Pixelator — the render
// output is compared directly against the sharp visible crop.
type sharpPassthrough struct{}

// Pixelate implements [unpixel.Pixelator] by returning src unchanged.
// It is zero-alloc when the image is already *image.RGBA with origin (0,0).
func (sharpPassthrough) Pixelate(img *image.RGBA, _, _ int) *image.RGBA {
	return imutil.ToRGBA(img)
}
