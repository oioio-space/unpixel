package varfont

import (
	"fmt"
	"image"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// AxisSpec describes one font axis to optimise over during [FitAxes].
type AxisSpec struct {
	// Tag is the four-character OpenType axis tag (e.g. "wght", "wdth").
	Tag string
	// Min is the lower bound of the design-space range to search.
	Min float32
	// Max is the upper bound of the design-space range to search.
	Max float32
	// Start is the initial design-space value for this axis.
	Start float32
}

// FitConfig holds all inputs for [FitAxes].
type FitConfig struct {
	// Font is the shared, read-only parsed variable font.
	Font *Font
	// Text is the known cleartext to render and compare against Target.
	Text string
	// Style controls font size, padding, and letter-spacing.
	Style unpixel.Style
	// Target is the pixelated redaction crop to match.
	// It is read-only; FitAxes never modifies it.
	Target *image.RGBA
	// Pixelator re-applies the mosaic to each rendered candidate.
	Pixelator unpixel.Pixelator
	// Metric measures the image distance between two pixelated images.
	Metric unpixel.Metric
	// BlockSize is the mosaic block side length in pixels (used as pixelation
	// grid origin 0,0 for axis fitting; the offset search is a separate step).
	BlockSize int
	// Axes specifies which axes to optimise and their search ranges.
	// Each axis is optimised independently in round-robin (coordinate descent).
	Axes []AxisSpec
	// MaxIter is the maximum number of coordinate-descent rounds (full passes
	// over all axes). Zero uses [DefaultMaxIter].
	MaxIter int
	// InitStep is the initial step size in design-space units for the first
	// axis probe. Zero uses [DefaultInitStep].
	InitStep float32
	// ShrinkFactor is the factor by which the step size is reduced after each
	// round. Must be in (0,1); zero uses [DefaultShrinkFactor].
	ShrinkFactor float32
}

// FitResult is the output of [FitAxes].
type FitResult struct {
	// Axes holds the best-fit design-space coordinates found, in the same order
	// as FitConfig.Axes.
	Axes []Axis
	// Distance is the image-metric value at the best-fit axes (lower is better,
	// 0 = pixel-perfect).
	Distance float64
	// Evals is the total number of render+pixelate+metric evaluations performed.
	Evals int
}

// DefaultMaxIter is the default maximum number of coordinate-descent rounds.
// Each round probes every axis once with the current step size.
const DefaultMaxIter = 12

// DefaultInitStep is the default initial step size for each axis, in
// design-space units. For wght (range 200–900) this gives ±350 on the first
// probe, resolving coarsely before shrinking.
const DefaultInitStep = float32(175)

// DefaultShrinkFactor is the factor by which the step size is reduced after
// each round. Five rounds of 0.5 shrinkage takes 175 → 5.5 du, enough
// precision for weight fitting.
const DefaultShrinkFactor = float32(0.5)

// FitAxes runs coordinate descent over the font axes described by cfg.Axes to
// minimise the image distance between (render(cfg.Text, axes) → pixelate) and
// cfg.Target.
//
// Algorithm — per-axis golden-section / ternary line search within the current
// step neighbourhood, shrinking the step each round:
//
//  1. Start all axes at their AxisSpec.Start values.
//  2. For each axis in each round: probe current−step, current, current+step
//     (clamped to [Min,Max]), keep the value with the lowest distance.
//  3. Shrink step by ShrinkFactor.  Repeat for MaxIter rounds.
//
// The search is fully deterministic (no randomness). Each evaluation clones a
// lightweight Face from the shared read-only Font, so FitAxes is safe to call
// concurrently from multiple goroutines.
//
// Convergence is not guaranteed when the distance landscape over an axis is
// flat or multi-modal after pixelation (e.g. two wght values that produce
// identical mosaic blocks). The caller should inspect FitResult.Distance to
// decide whether the fit is useful.
func FitAxes(cfg FitConfig) (FitResult, error) {
	if cfg.Font == nil {
		return FitResult{}, fmt.Errorf("varfont.FitAxes: Font must not be nil")
	}
	if cfg.Target == nil {
		return FitResult{}, fmt.Errorf("varfont.FitAxes: Target must not be nil")
	}
	if len(cfg.Axes) == 0 {
		return FitResult{}, fmt.Errorf("varfont.FitAxes: at least one AxisSpec required")
	}

	maxIter := cfg.MaxIter
	if maxIter <= 0 {
		maxIter = DefaultMaxIter
	}
	initStep := cfg.InitStep
	if initStep <= 0 {
		initStep = DefaultInitStep
	}
	shrink := cfg.ShrinkFactor
	if shrink <= 0 || shrink >= 1 {
		shrink = DefaultShrinkFactor
	}

	// current holds the best design-space value per axis.
	current := make([]float32, len(cfg.Axes))
	for i, a := range cfg.Axes {
		current[i] = clampAxis(a.Start, a.Min, a.Max)
	}

	// Crop target to its own bounds once — compare always against exactly this.
	// imutil.ToRGBA returns img as-is when it is already *image.RGBA at (0,0).
	targetCrop := imutil.ToRGBA(cfg.Target)

	// axesScratch is a reusable slice passed to evaluate to avoid a per-eval
	// allocation inside the hot coordinate-descent loop.
	axesScratch := make([]Axis, len(cfg.Axes))

	var totalEvals int

	// Initial evaluation at the start point.
	bestDist, err := evaluate(cfg, current, targetCrop, axesScratch)
	if err != nil {
		return FitResult{}, fmt.Errorf("varfont.FitAxes: initial eval: %w", err)
	}
	totalEvals++

	step := initStep

	for range maxIter {
		for ai, spec := range cfg.Axes {
			// Probe three points: current−step, current, current+step (clamped).
			candidates := [3]float32{
				clampAxis(current[ai]-step, spec.Min, spec.Max),
				current[ai],
				clampAxis(current[ai]+step, spec.Min, spec.Max),
			}

			for _, v := range candidates {
				if v == current[ai] {
					// Already evaluated at this exact value in a prior probe.
					continue
				}
				prev := current[ai]
				current[ai] = v
				d, err := evaluate(cfg, current, targetCrop, axesScratch)
				if err != nil {
					return FitResult{}, fmt.Errorf("varfont.FitAxes: eval axis %s=%.1f: %w", spec.Tag, v, err)
				}
				totalEvals++
				if d < bestDist {
					bestDist = d
				} else {
					current[ai] = prev // revert if not an improvement
				}
			}
		}
		step *= shrink
	}

	result := make([]Axis, len(cfg.Axes))
	for i, spec := range cfg.Axes {
		result[i] = Axis{Tag: spec.Tag, Value: current[i]}
	}
	return FitResult{Axes: result, Distance: bestDist, Evals: totalEvals}, nil
}

// evaluate renders cfg.Text at the given axis values, pixelates the result,
// crops to the target size, and returns the metric distance against target.
// scratch is a caller-owned []Axis of len(cfg.Axes) reused across calls to
// avoid a per-evaluation allocation inside the coordinate-descent hot loop.
func evaluate(cfg FitConfig, current []float32, target *image.RGBA, scratch []Axis) (float64, error) {
	for i, spec := range cfg.Axes {
		scratch[i] = Axis{Tag: spec.Tag, Value: current[i]}
	}

	r := newFontVarRenderer(cfg.Font, scratch)
	img, _, err := r.Render(cfg.Text, cfg.Style)
	if err != nil {
		return 1, fmt.Errorf("render: %w", err)
	}

	pixed := cfg.Pixelator.Pixelate(img, 0, 0)

	// Crop both images to the overlapping top-left region for a fair comparison.
	// cropToSize is a zero-alloc fast path: it returns src directly when it
	// already has the exact requested dimensions at origin (0,0), which is the
	// common case for target; imutil.Crop always allocates a fresh image.
	tb := target.Bounds()
	pb := pixed.Bounds()
	w := min(tb.Dx(), pb.Dx())
	h := min(tb.Dy(), pb.Dy())

	tCrop := cropToSize(target, w, h)
	pCrop := cropToSize(pixed, w, h)

	return cfg.Metric.Compare(tCrop, pCrop), nil
}

// cropToSize returns a *image.RGBA containing the top-left w×h pixels of src.
// When src is already exactly w×h at origin (0,0), it is returned as-is (zero
// allocation). Otherwise imutil.Crop is used for its row-copy fast path.
func cropToSize(src *image.RGBA, w, h int) *image.RGBA {
	b := src.Bounds()
	if b.Min.X == 0 && b.Min.Y == 0 && b.Dx() == w && b.Dy() == h {
		return src
	}
	return imutil.Crop(src, 0, 0, w, h)
}

// clampAxis clamps v to [lo, hi].
func clampAxis(v, lo, hi float32) float32 {
	return min(max(v, lo), hi)
}
