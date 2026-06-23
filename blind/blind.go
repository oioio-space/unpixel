// Package blind provides a zero-config API for blind recovery of mosaic-redacted
// text without knowing the font, block size, or grid offset in advance.
//
// Blind recovery works by auto-detecting the pixelation block size, calibrating
// the font size from the crop, sweeping bundled font renderers, and scoring each
// candidate line with the whole-line beam decoder from internal/blinddecode.
//
// # Usage
//
//	result, err := blind.Recover(ctx, img,
//	    blind.WithLanguage(blind.French),
//	    blind.WithBlock(8),
//	)
//	if err != nil { ... }
//	fmt.Println(result.Text)
package blind

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"strings"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/blinddecode"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// GammaMode controls which colour space is used for block averaging during
// blind recovery. GIMP, GEGL, and most editors average in linear light; the
// original unredacter / Jimp averaged in sRGB. Real captures vary, so
// GammaAuto (the default) tries both and keeps the one with the lower image
// distance.
type GammaMode int

const (
	// GammaAuto runs the decoder twice — once with linear-light averaging and
	// once with sRGB averaging — and keeps the result with the lower Dist.
	// On a tie, linear is preferred for determinism. This is the default when
	// neither WithGamma nor WithLinear is called.
	GammaAuto GammaMode = iota
	// GammaLinear forces linear-light block averaging (matches GIMP/GEGL
	// Pixelize, CSS pixel-average, and most modern editors).
	GammaLinear
	// GammaSRGB forces sRGB block averaging (matches the original unredacter /
	// Jimp behaviour and older tooling).
	GammaSRGB
)

// Result is the outcome of a blind recovery.
type Result struct {
	// Text is the recovered text. Lines are separated by newlines.
	Text string
	// Font is the name of the bundled font that produced the lowest image distance.
	Font string
	// Lang is the ISO 639-1 code of the language used (e.g. "en", "fr").
	Lang string
	// Block is the pixelation block size resolved for this recovery (in pixels).
	Block int
	// Dist is the mean whole-line image distance for the winning font (lower = better).
	Dist float64
	// Lines holds the per-line recovered text in reading order.
	Lines []string
	// Denoise is the median-filter radius actually applied before detection and
	// decoding: 0 means no filtering was done (either the image was clean and
	// auto-detect chose not to filter, or WithDenoise(0) disabled auto).
	// Positive values correspond to the imutil.Median radius (1 = 3×3, 2 = 5×5).
	Denoise int
	// Gamma is the colour space used for block averaging that produced this
	// result: "linear" (GIMP/GEGL Pixelize) or "srgb" (original unredacter /
	// Jimp). When GammaAuto is in effect, this records which space won; when
	// a specific mode is forced it reflects that choice.
	Gamma string
}

// config holds the resolved options for a Recover call.
type config struct {
	language      lang.Language
	block         int // 0 = auto
	offsetX       int
	offsetY       int
	fontSize      float64 // 0 = auto
	gamma         GammaMode
	fonts         []string
	metric        unpixel.Metric
	denoiseRadius int // -1 = auto (default), 0 = off, >0 = forced radius
}

// Language selects the dictionary and language prior used for scoring. The
// constants English and French are the supported values; ParseLanguage accepts
// their string forms (e.g. for a CLI flag).
type Language = lang.Language

// Supported languages, re-exported so callers need not import the internal
// language package.
const (
	English = lang.English
	French  = lang.French
)

// ParseLanguage accepts "en"/"english" and "fr"/"french"/"français"
// (case-insensitive); reports false otherwise.
func ParseLanguage(s string) (Language, bool) { return lang.ParseLanguage(s) }

// Option is a functional option for Recover.
type Option func(*config)

// WithLanguage sets the language model and dictionary used for scoring.
// Default: English.
func WithLanguage(l Language) Option {
	return func(c *config) { c.language = l }
}

// WithBlock pins the pixelation block size. When 0 (the default), Recover
// calls unpixel.InferBlockSize on the image to detect it automatically.
func WithBlock(n int) Option {
	return func(c *config) { c.block = n }
}

// WithOffset sets the grid phase used for re-pixelation during scoring.
// Defaults to (0, 0); DecodeLineWhole handles phase internally.
func WithOffset(x, y int) Option {
	return func(c *config) { c.offsetX = x; c.offsetY = y }
}

// WithFontSize pins the font size in points used to render candidates. When 0
// (the default), Recover calls unpixel.InferFontSize on the located crop.
func WithFontSize(px float64) Option {
	return func(c *config) { c.fontSize = px }
}

// WithGamma sets the colour space used for block averaging. GammaAuto (the
// default) runs both linear and sRGB and picks the one with lower image
// distance, recording the winner in Result.Gamma. GammaLinear and GammaSRGB
// force a specific space and skip the second pass.
func WithGamma(mode GammaMode) Option {
	return func(c *config) { c.gamma = mode }
}

// WithLinear controls whether block averaging is done in linear light (true,
// matching GIMP/GEGL Pixelize) or sRGB space (false).
//
// Deprecated: prefer WithGamma(GammaLinear) or WithGamma(GammaSRGB). The
// default (no option) is now GammaAuto, which tries both and picks the better
// result. WithLinear is kept for backward compatibility and maps true →
// GammaLinear, false → GammaSRGB.
func WithLinear(on bool) Option {
	return func(c *config) {
		if on {
			c.gamma = GammaLinear
		} else {
			c.gamma = GammaSRGB
		}
	}
}

// WithFonts sets the font-style filter for the bundled font sweep.
// Recognised style values: "sans", "serif", "mono". Pass no arguments to use
// the default of {"sans"}.
func WithFonts(styles ...string) Option {
	return func(c *config) { c.fonts = styles }
}

// WithMetric overrides the image-distance metric. Default: SSIM with window 7.
func WithMetric(m unpixel.Metric) Option {
	return func(c *config) { c.metric = m }
}

// DenoiseAuto and DenoiseOff are sentinel values for WithDenoise.
const (
	// DenoiseAuto instructs Recover to sample the image for impulse noise and
	// choose a median-filter radius automatically (0, 1, or 2). This is the
	// default when WithDenoise is not called.
	DenoiseAuto = -1
	// DenoiseOff disables median pre-filtering entirely, regardless of image
	// content. Equivalent to WithDenoise(0).
	DenoiseOff = 0
)

// autoDenoiseThreshold is the InferImpulseNoise ratio below which Recover
// does not apply any median filter in auto mode. Chosen conservatively at 0.3 %
// so that clean mosaic captures (block edges have ratio ≈ 0) and typical
// lossless PNG screenshots are never filtered, while even modest salt-and-pepper
// contamination (≥ 0.5 %) triggers radius-1 filtering.
const autoDenoiseThreshold = 0.003

// heavyDenoiseThreshold is the InferImpulseNoise ratio above which Recover
// upgrades from radius 1 to radius 2 (5×5 kernel) in auto mode. At 5 % noise
// density a 3×3 kernel starts leaving residual spikes because two adjacent
// corrupted pixels can survive the median; a 5×5 kernel is more robust.
const heavyDenoiseThreshold = 0.05

// WithDenoise controls the median pre-filter applied to the image before block-
// size detection and decoding. Three modes:
//   - default (no WithDenoise call) or WithDenoise(DenoiseAuto): auto-detect —
//     Recover calls unpixel.InferImpulseNoise and applies radius 1 or 2 only
//     when the image looks noisy (ratio ≥ autoDenoiseThreshold). Clean images
//     are unaffected.
//   - WithDenoise(DenoiseOff) or WithDenoise(0): disable — no filtering
//     regardless of image content.
//   - WithDenoise(r), r > 0: force radius r (1 = 3×3 kernel, 2 = 5×5, …).
//
// Useful for JPEG-compressed or noisy screen captures where salt-and-pepper
// speckle interferes with block-size detection. A radius of 1 removes single-
// pixel spikes; radius 2 handles larger speckle clusters.
func WithDenoise(radius int) Option {
	return func(c *config) { c.denoiseRadius = radius }
}

// defaultFontSize is the fallback font size when InferFontSize returns 0.
const defaultFontSize = 32.0

// Recover runs the full zero-config blind pipeline on a mosaic-redacted image.
//
// Steps:
//  1. Convert img to *image.RGBA.
//  2. Resolve the block size: WithBlock if set, else InferBlockSize; if still 0,
//     LocateRedaction and InferBlockSize on the crop.
//  3. Resolve the font size: WithFontSize if set, else InferFontSize on the
//     located crop, falling back to 32 pt.
//  4. Build a blinddecode.Options with the configured pixelator, metric,
//     dictionary, and prior.
//  5. Build bundled font renderers via BundledRenderers (filtered by WithFonts).
//  6. Call blinddecode.Recover (once for a forced gamma, twice for GammaAuto)
//     and return the result with the lower image distance.
//
// Recover honours ctx cancellation between phases. It returns a non-nil error
// when no usable font can be built or the image is unrecoverable.
func Recover(ctx context.Context, img image.Image, opts ...Option) (Result, error) {
	cfg := config{
		language:      lang.English,
		gamma:         GammaAuto, // default: try both, keep the better one
		fonts:         []string{"sans"},
		denoiseRadius: -1, // auto by default
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Convert to *image.RGBA.
	rgba := toRGBA(img)

	// Resolve the denoise radius from the three-state config field, then apply.
	//
	//   denoiseRadius < 0 (auto): sample the image for impulse noise and choose
	//     radius 0 (clean), 1 (modest noise), or 2 (heavy noise).
	//   denoiseRadius == 0 (off): skip filtering entirely.
	//   denoiseRadius > 0 (forced): use that radius directly.
	var appliedRadius int
	switch {
	case cfg.denoiseRadius > 0:
		appliedRadius = cfg.denoiseRadius
	case cfg.denoiseRadius < 0: // auto
		ratio := unpixel.InferImpulseNoise(rgba)
		switch {
		case ratio >= heavyDenoiseThreshold:
			appliedRadius = 2
		case ratio >= autoDenoiseThreshold:
			appliedRadius = 1
		}
	}
	if appliedRadius > 0 {
		rgba = imutil.Median(rgba, appliedRadius)
	}

	// Resolve block size.
	block := cfg.block
	cropRGBA := rgba
	if block <= 0 {
		block = unpixel.InferBlockSize(rgba)
	}
	if block <= 0 {
		// Try locating the redaction crop and inferring from it.
		if region, ok := unpixel.LocateRedaction(rgba); ok {
			b := rgba.Bounds()
			cropRGBA = imutil.Crop(rgba, region.Min.X-b.Min.X, region.Min.Y-b.Min.Y, region.Dx(), region.Dy())
			block = unpixel.InferBlockSize(cropRGBA)
		}
	}
	if block <= 0 {
		block = 8 // last-resort fallback
	}

	// Resolve font size.
	fontSize := cfg.fontSize
	if fontSize <= 0 {
		if fs := unpixel.InferFontSize(cropRGBA); fs >= 8 {
			fontSize = fs
		} else {
			fontSize = defaultFontSize
		}
	}

	// Build the metric (resolved once; shared across both gamma runs).
	m := cfg.metric
	if m == nil {
		m = metric.NewSSIM(7)
	}

	// Build renderers (resolved once; shared across both gamma runs).
	renderers, err := blinddecode.BundledRenderers(cfg.fonts...)
	if err != nil {
		return Result{}, fmt.Errorf("blind.Recover: build renderers: %w", err)
	}
	if len(renderers) == 0 {
		return Result{}, errors.New("blind.Recover: no usable font renderers for the requested styles")
	}

	// baseOpts carries everything that does not change between gamma passes.
	//
	// TopK=0: let DecodeLineWhole's budget-adaptive effectivePoolK choose the
	// per-tier cap automatically.  For a 2-word line this yields k≈235,
	// comfortably including low-frequency words like "cat" or "chat".  Longer
	// lines get a smaller k to keep the Cartesian-product render count under
	// maxCombinations (500 000).  A non-zero TopK would act as an upper cap on
	// the budget-derived k, which is useful to restrict the search further but
	// never necessary to enable it — so we leave it at 0 here.
	baseOpts := blinddecode.Options{
		Metric:   m,
		Dict:     lang.DictionaryFor(cfg.language),
		Prior:    lang.PriorFor(cfg.language),
		Block:    block,
		OffsetX:  cfg.offsetX,
		OffsetY:  cfg.offsetY,
		FontSize: fontSize,
		TopK:     0,
	}

	// runGamma is the per-pixelator inner call. It sets the Pixelator on a copy
	// of baseOpts (so baseOpts itself is immutable), checks ctx, then runs
	// blinddecode.Recover. linear selects LinearBlockAverage vs BlockAverage.
	runGamma := func(linear bool) (blinddecode.ImageResult, error) {
		if err := ctx.Err(); err != nil {
			return blinddecode.ImageResult{}, fmt.Errorf("blind.Recover: %w", err)
		}
		o := baseOpts
		if linear {
			o.Pixelator = pixelate.NewLinearBlockAverage(block)
		} else {
			o.Pixelator = pixelate.NewBlockAverage(block)
		}
		return blinddecode.Recover(cropRGBA, o, renderers), nil
	}

	// Select which gamma pass(es) to run and which label to attach.
	var imgResult blinddecode.ImageResult
	var gammaLabel string
	switch cfg.gamma {
	case GammaLinear:
		imgResult, err = runGamma(true)
		if err != nil {
			return Result{}, err
		}
		gammaLabel = "linear"
	case GammaSRGB:
		imgResult, err = runGamma(false)
		if err != nil {
			return Result{}, err
		}
		gammaLabel = "srgb"
	default: // GammaAuto: run both, keep the lower Dist; prefer linear on a tie.
		linResult, linErr := runGamma(true)
		if linErr != nil {
			return Result{}, linErr
		}
		srgbResult, srgbErr := runGamma(false)
		if srgbErr != nil {
			return Result{}, srgbErr
		}
		if srgbResult.Dist < linResult.Dist {
			imgResult, gammaLabel = srgbResult, "srgb"
		} else {
			imgResult, gammaLabel = linResult, "linear"
		}
	}

	lines := splitLines(imgResult.Text)
	return Result{
		Text:    imgResult.Text,
		Font:    imgResult.Font,
		Lang:    cfg.language.String(),
		Block:   block,
		Dist:    imgResult.Dist,
		Lines:   lines,
		Denoise: appliedRadius,
		Gamma:   gammaLabel,
	}, nil
}

// toRGBA returns img as *image.RGBA. If img is already *image.RGBA, it is
// returned directly; otherwise it is drawn into a fresh *image.RGBA.
func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// splitLines splits text on newlines, returning a slice of non-empty lines in
// reading order. Empty lines (consecutive newlines) are dropped.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.FieldsFunc(text, func(r rune) bool { return r == '\n' })
}
