package varfont

// CalibrateGeometry fits font SIZE and X-STRETCH to a crop of KNOWN visible
// text, complementing [CalibrateFromVisible] which fits font design-space axes
// (wght/wdth/opsz/…).
//
// Together they fill the two missing pieces of an exact forward model:
// axis calibration resolves stroke weight and glyph shape; geometry calibration
// resolves the physical scale (points per pixel) and horizontal compression or
// expansion applied before the screenshot was taken.
//
// # Why sharp text
//
// Block-average pixelation washes out sub-block detail: a 2 px size difference
// can be invisible after a 10 px mosaic. CalibrateGeometry therefore requires a
// SHARP (un-pixelated) target — the visible cleartext adjacent to the redacted
// region. Pass a non-nil Pixelator only when the visible crop is itself
// pixelated (rare; the objective will be weaker).
//
// # X-stretch semantics
//
// XStretch is a pure horizontal scale factor applied after glyph rendering:
// the rendered ink is resampled to (width × XStretch) × height using
// CatmullRom bicubic interpolation — the same kernel used by the mosaictext
// decode path, so the two pipelines are pixel-consistent.  1.0 means no
// stretch; < 1.0 compresses; > 1.0 expands.
//
// # Thread safety
//
// CalibrateGeometry is safe for concurrent use: each call creates its own
// rendering state and does not share mutable data with other calls.

import (
	"fmt"
	"image"
	"math"

	gtfont "github.com/go-text/typesetting/font"
	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// GeometryConfig holds all inputs for [CalibrateGeometry].
type GeometryConfig struct {
	// Font is the shared, read-only parsed variable font.
	Font *Font
	// Text is the known visible cleartext string to render and compare.
	Text string
	// Style is the base rendering style. FontSize is used as the search seed;
	// all other style fields (padding, letter-spacing) are held fixed.
	Style unpixel.Style
	// Target is the sharp visible-text crop to match against.
	// It is read-only; CalibrateGeometry never modifies it.
	Target *image.RGBA
	// Pixelator, when non-nil, is applied to each rendered+stretched candidate
	// before comparing against Target. Use nil for sharp visible text (the
	// default and strongly recommended path); set it only when the visible crop
	// is itself pixelated.
	Pixelator unpixel.Pixelator
	// Metric measures image distance between two RGBA images.
	Metric unpixel.Metric
	// MinSizePx and MaxSizePx are the font-size search bounds in pixels.
	// Zero values default to [0.5 × Style.FontSize, 2 × Style.FontSize],
	// clamped so that MinSizePx ≥ 1.
	MinSizePx, MaxSizePx float64
	// MinStretch and MaxStretch are the x-stretch search bounds.
	// Zero values default to [0.7, 1.4].
	MinStretch, MaxStretch float64
	// MaxIter is the maximum number of Nelder-Mead simplex operations.
	// Zero uses [DefaultMaxIter].
	MaxIter int
}

// GeometryResult is the output of [CalibrateGeometry].
type GeometryResult struct {
	// FontSizePx is the fitted font size in pixels (points at 72 DPI).
	FontSizePx float64
	// XStretch is the fitted horizontal scale factor (1.0 = no stretch).
	XStretch float64
	// Distance is the image-metric value at the optimum (lower is better;
	// 0 = pixel-perfect).
	Distance float64
	// Evals is the total number of render+stretch+metric evaluations performed.
	Evals int
}

// defaultGeometryStretchMin and defaultGeometryStretchMax are the x-stretch
// search bounds used when GeometryConfig.MinStretch/MaxStretch are zero.
const (
	defaultGeometryStretchMin = 0.7
	defaultGeometryStretchMax = 1.4
)

// CalibrateGeometry fits font size (pixels) and x-stretch (horizontal scale)
// to a crop of known visible text by minimising the image distance between the
// rendered+stretched candidate and Target.
//
// The optimiser is Nelder-Mead over the 2-parameter space [fontSize, xStretch],
// seeded at [GeometryConfig.Style.FontSize, 1.0] and bounded to
// [MinSizePx..MaxSizePx] × [MinStretch..MaxStretch].
//
// Feed the returned [GeometryResult.FontSizePx] and [GeometryResult.XStretch]
// into the decode pipeline to lock the forward model before running [FitAxes].
func CalibrateGeometry(cfg GeometryConfig) (GeometryResult, error) {
	if cfg.Font == nil {
		return GeometryResult{}, fmt.Errorf("varfont.CalibrateGeometry: Font must not be nil")
	}
	if cfg.Text == "" {
		return GeometryResult{}, fmt.Errorf("varfont.CalibrateGeometry: Text must not be empty")
	}
	if cfg.Target == nil {
		return GeometryResult{}, fmt.Errorf("varfont.CalibrateGeometry: Target must not be nil")
	}
	if cfg.Metric == nil {
		return GeometryResult{}, fmt.Errorf("varfont.CalibrateGeometry: Metric must not be nil")
	}

	// Resolve search bounds.
	seedSize := cfg.Style.FontSize
	if seedSize <= 0 {
		seedSize = 32
	}
	minSize := cfg.MinSizePx
	if minSize <= 0 {
		minSize = max(1.0, seedSize*0.5)
	}
	maxSize := cfg.MaxSizePx
	if maxSize <= 0 {
		maxSize = seedSize * 2.0
	}
	if minSize >= maxSize {
		maxSize = minSize + 1
	}

	minStretch := cfg.MinStretch
	if minStretch <= 0 {
		minStretch = defaultGeometryStretchMin
	}
	maxStretch := cfg.MaxStretch
	if maxStretch <= 0 {
		maxStretch = defaultGeometryStretchMax
	}
	if minStretch >= maxStretch {
		maxStretch = minStretch + 0.1
	}

	maxIter := cfg.MaxIter
	if maxIter <= 0 {
		maxIter = DefaultMaxIter
	}

	// Pixelator: nil → sharp passthrough.
	var pix unpixel.Pixelator = sharpPassthrough{}
	if cfg.Pixelator != nil {
		pix = cfg.Pixelator
	}

	// Crop target to its own bounds once — always compare against exactly this.
	targetCrop := imutil.ToRGBA(cfg.Target)

	// Allocate a single Face for the whole optimisation (reused across evals).
	face := gtfont.NewFace(cfg.Font.raw)

	// Normalise both parameters to [0,1] so that the single scalar initStep of
	// nelderMead perturbs each dimension proportionally regardless of the
	// difference in physical scale (px vs unit-less stretch factor).
	//
	// The AxisSpec bounds are [0,1]; decode/encode functions map between the
	// normalised unit cube and the physical ranges.
	specs := []AxisSpec{
		{Tag: "size", Min: 0, Max: 1},
		{Tag: "xstr", Min: 0, Max: 1},
	}

	// decodeSz/decodeSt map a normalised [0,1] value back to physical units.
	decodeSz := func(u float32) float64 { return minSize + float64(u)*(maxSize-minSize) }
	decodeSt := func(u float32) float64 { return minStretch + float64(u)*(maxStretch-minStretch) }

	// Style copy for the objective — FontSize is overwritten per-eval.
	evalStyle := cfg.Style

	evals := 0
	objectiveFn := func(p []float32) (float64, error) {
		evals++
		fontSize := decodeSz(p[0])
		xStretch := decodeSt(p[1])

		// Render at the candidate font size (axes held at font defaults).
		evalStyle.FontSize = fontSize
		img, sx := renderWithFace(face, cfg.Font, cfg.Text, evalStyle)

		// Crop to ink bounds then apply x-stretch (mirrors mosaictext pipeline).
		stretched := applyXStretch(img, sx, xStretch)

		// Optional pixelation.
		candidate := pix.Pixelate(stretched, 0, 0)

		// Both target and candidate are tight ink crops at origin (0,0).
		// Pad both to the union size on a white canvas so that size mismatches
		// contribute real signal: too-small → white where target has ink; too-large
		// → ink where target is white. This is symmetric and prevents the optimizer
		// from collapsing to a corner of the search box.
		//
		// Use MSE (not the caller-supplied Metric) as the objective: MSE is smooth
		// and varies continuously with font size and stretch, giving Nelder-Mead a
		// well-shaped bowl to descend. The caller's Metric is used for the final
		// Distance reported in GeometryResult (evaluated once at the optimum).
		tb := targetCrop.Bounds()
		cb := candidate.Bounds()
		w := max(tb.Dx(), cb.Dx())
		h := max(tb.Dy(), cb.Dy())

		padTarget := padToSize(targetCrop, w, h)
		padCand := padToSize(candidate, w, h)

		return mseCompare(padTarget, padCand), nil
	}

	// Phase 1: coarse 2-D grid scan to warm-start the Nelder-Mead seed.
	// Font-size quantisation creates sharp discontinuities: adjacent integer-px
	// values can differ in MSE by 0.10+, so Nelder-Mead started far from the
	// optimum cannot gradient-follow to the correct basin. A 9×7 grid (63 evals)
	// over the full [0,1]² normalised box is cheap relative to a full fit and
	// reliably locates the capture basin. Nelder-Mead then refines from there.
	const (
		gridSzPts = 9 // points along the font-size axis
		gridStPts = 7 // points along the stretch axis
	)
	bestGridSzN := float32((seedSize - minSize) / (maxSize - minSize))
	bestGridStN := float32((1.0 - minStretch) / (maxStretch - minStretch))
	bestGridMSE := math.MaxFloat64
	for si := range gridSzPts {
		szN := float32(si) / float32(gridSzPts-1) // 0 … 1
		for ti := range gridStPts {
			stN := float32(ti) / float32(gridStPts-1) // 0 … 1
			d, evalErr := objectiveFn([]float32{szN, stN})
			if evalErr != nil {
				return GeometryResult{}, fmt.Errorf("varfont.CalibrateGeometry: grid scan: %w", evalErr)
			}
			if d < bestGridMSE {
				bestGridMSE = d
				bestGridSzN = szN
				bestGridStN = stN
			}
		}
	}

	// Phase 2: Nelder-Mead from the warm-start seed.
	// initStep = 1/(gridSzPts-1) — one grid spacing in the normalised box, so
	// the initial simplex is local to the capture basin found by the scan.
	initStep := float32(1.0 / float64(gridSzPts-1))
	start := []float32{bestGridSzN, bestGridStN}

	best, _, nmEvals, err := nelderMead(specs, start, initStep, maxIter, objectiveFn)
	if err != nil {
		return GeometryResult{}, fmt.Errorf("varfont.CalibrateGeometry: %w", err)
	}
	_ = nmEvals // evals is tracked via objectiveFn closure

	bestFontSize := decodeSz(best[0])
	bestStretch := decodeSt(best[1])

	// Re-evaluate the optimum with the caller's Metric so Distance is on the
	// caller's scale (e.g. pixelmatch fraction), not the internal MSE scale.
	evalStyle.FontSize = bestFontSize
	finalImg, finalSX := renderWithFace(face, cfg.Font, cfg.Text, evalStyle)
	finalStretched := applyXStretch(finalImg, finalSX, bestStretch)
	finalCandidate := pix.Pixelate(finalStretched, 0, 0)
	tb := targetCrop.Bounds()
	cb := finalCandidate.Bounds()
	w := max(tb.Dx(), cb.Dx())
	h := max(tb.Dy(), cb.Dy())
	finalDist := cfg.Metric.Compare(padToSize(targetCrop, w, h), padToSize(finalCandidate, w, h))

	return GeometryResult{
		FontSizePx: bestFontSize,
		XStretch:   bestStretch,
		Distance:   finalDist,
		Evals:      evals,
	}, nil
}

// applyXStretch renders the ink region of img (up to sentinelX) and scales it
// horizontally by xStretch using CatmullRom bicubic interpolation — the same
// kernel used by the mosaictext decode path.
//
// The returned image has a white background. When xStretch is exactly 1.0, the
// ink crop is returned as-is (zero extra allocation for the scale step).
func applyXStretch(img *image.RGBA, sentinelX int, xStretch float64) *image.RGBA {
	bb := inkBoundsGeom(img, sentinelX)
	if bb.Empty() {
		// No ink — return a 1×1 white image so the metric has something to compare.
		out := image.NewRGBA(image.Rect(0, 0, 1, 1))
		imutil.FillWhite(out)
		return out
	}

	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)

	nw := int(math.Round(float64(bb.Dx()) * xStretch))
	if nw < 1 {
		nw = 1
	}
	if nw == bb.Dx() {
		// Unit stretch — return the ink crop directly.
		return ink
	}

	stretched := image.NewRGBA(image.Rect(0, 0, nw, bb.Dy()))
	xdraw.CatmullRom.Scale(stretched, stretched.Bounds(), ink, ink.Bounds(), xdraw.Over, nil)
	return stretched
}

// mseCompare returns the mean squared error between a and b, normalised to
// [0,1] (dividing by 255² so a channel difference of 255 contributes 1.0 per
// pixel). It requires a and b to have identical bounds.
//
// MSE is used as the internal geometry-optimisation objective because it is
// smooth and varies continuously with font size and stretch — unlike pixelmatch
// (a discrete count), which creates a spiky, multi-modal landscape that Nelder-
// Mead cannot reliably descend. The caller's Metric is used for the reported
// Distance at the optimum.
func mseCompare(a, b *image.RGBA) float64 {
	bounds := a.Bounds()
	n := bounds.Dx() * bounds.Dy()
	if n == 0 {
		return 0
	}
	const norm = 255.0 * 255.0 * 3.0 // 3 channels, max²
	var sum float64
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			ca := a.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			cb := b.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			dr := float64(ca.R) - float64(cb.R)
			dg := float64(ca.G) - float64(cb.G)
			db := float64(ca.B) - float64(cb.B)
			sum += dr*dr + dg*dg + db*db
		}
	}
	return sum / (float64(n) * norm)
}

// padToSize returns a w×h white-background image with src drawn at the top-left
// corner (origin 0,0). When src is already exactly w×h at origin, it is
// returned as-is (zero allocation). Used by CalibrateGeometry to compare
// ink-cropped images of different dimensions fairly.
func padToSize(src *image.RGBA, w, h int) *image.RGBA {
	b := src.Bounds()
	if b.Min.X == 0 && b.Min.Y == 0 && b.Dx() == w && b.Dy() == h {
		return src
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	imutil.FillWhite(out)
	xdraw.Draw(out, out.Bounds(), src, b.Min, xdraw.Src)
	return out
}

// inkLumCutoff is the per-channel luminance below which a pixel is considered
// ink in inkBoundsGeom. Must match the inkThreshold constant in geometry_test.go
// (both are 244) so that target generation and candidate rendering crop
// identically.
const inkLumCutoff = uint8(244)

// inkBoundsGeom returns the tight bounding rectangle of non-white pixels in img
// up to sentinelX (exclusive). It mirrors the mosaictext.inkBounds logic
// without the package-level dependency, using the same luminance threshold.
func inkBoundsGeom(img *image.RGBA, sentinelX int) image.Rectangle {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < min(b.Max.X, sentinelX); x++ {
			c := img.RGBAAt(x, y)
			// Pixel is "ink" when any channel is clearly below white.
			if c.R < inkLumCutoff || c.G < inkLumCutoff || c.B < inkLumCutoff {
				minX = min(minX, x)
				minY = min(minY, y)
				maxX = max(maxX, x+1)
				maxY = max(maxY, y+1)
			}
		}
	}
	if minX >= maxX || minY >= maxY {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}
