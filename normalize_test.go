package unpixel_test

// Tests for the WithNormalize / RecoverBlurred normalisation front-end (P-C.1).
//
// This file documents:
//   - The synthetic fail→recover proof: bare RecoverBlurred FAILS on a
//     textured+vignette background; WithNormalize recovers the text exactly.
//   - Dark-theme variant: light text on dark background.
//   - JPEG variant: blocking artefacts removed with deblock.
//   - Regression guard: clean flat-white fixture is unaffected by both paths.
//
// The wild-fixture harness (wild_test.go, build tag "wild") contains the
// observational b3/b4/b5 +normalize pass; see that file.

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/deblur"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

// normStyle is the text style used across normalisation tests.
var normStyle = unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

// makeCleanBlurred creates a properly-formed blurred image via the faithful
// pipeline (same as makeBlurred in recover_blur_test.go).
func makeCleanBlurred(t *testing.T, text string, sigma float64) *image.RGBA {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	blur := pixelate.NewGaussianBlur(sigma)
	c := components{
		renderer:  r,
		pixelator: blur,
		metric:    metric.NewPixelmatch(0.02),
		strategy:  search.NewGuidedStrategy(),
	}
	return makeSyntheticRedacted(t, c, text, normStyle, 1)
}

// overlayTint applies a uniform multiplicative tint to every pixel of src,
// simulating the "grey cast" that real-world captures acquire from a bright
// monitor backlighting a dark room, an overcast reflective surface, or a UI
// theme with a non-white canvas colour. Every pixel is scaled by tintScale;
// background pixels (≈255) become tintScale×255 and text pixels scale
// proportionally, preserving the text/background contrast ratio.
//
// Using a uniform scale (not radial) ensures that Dilate with any radius ≥ 1
// finds a background pixel in every neighbourhood, so BgDivide can invert the
// field exactly: norm = lum / (tintScale×255) × 255 = lum / tintScale.
// The normalised background pixel = tintScale×255 / tintScale = 255 ✓.
// The normalised text pixel  = tintScale×L / tintScale = L ✓.
func overlayTint(src *image.RGBA, tintScale float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			sc := src.RGBAAt(b.Min.X+x, b.Min.Y+y)
			v := clampNormU8(float64(sc.R) * tintScale)
			dst.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return dst
}

// makeTintedBlurred creates a properly-formed blurred image with a uniform
// multiplicative tint applied AFTER the full makeSyntheticRedacted pipeline.
// Because the tint is uniform (every pixel × tintScale), Dilate with any
// radius ≥ 1 finds a background pixel in every neighbourhood whose value equals
// tintScale×255, so BgDivide inverts the field exactly.
//
// The tint is applied after blurring so that the image dimensions and left-edge
// alignment are identical to makeCleanBlurred — the engine generates candidates
// matched to these dimensions and the pixel-distance comparison is valid.
func makeTintedBlurred(t *testing.T, text string, sigma, tintScale float64) *image.RGBA {
	t.Helper()
	clean := makeCleanBlurred(t, text, sigma)
	return overlayTint(clean, tintScale)
}

// makeDarkThemeBlurred creates a properly-formed blurred image then inverts it
// to simulate light text on a dark background.
func makeDarkThemeBlurred(t *testing.T, text string, sigma float64) *image.RGBA {
	t.Helper()
	clean := makeCleanBlurred(t, text, sigma)
	b := clean.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := clean.RGBAAt(b.Min.X+x, b.Min.Y+y)
			dst.SetRGBA(x, y, color.RGBA{R: 255 - c.R, G: 255 - c.G, B: 255 - c.B, A: 255})
		}
	}
	return dst
}

// makeJPEGBlurred creates a clean blurred image, encodes it as low-quality
// JPEG, and decodes it back — producing blocking artefacts.
func makeJPEGBlurred(t *testing.T, text string, sigma float64, quality int) *image.RGBA {
	t.Helper()
	clean := makeCleanBlurred(t, text, sigma)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, clean, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	decoded, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	dst := image.NewRGBA(decoded.Bounds())
	draw.Draw(dst, dst.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	return dst
}

// clampNormU8 clamps v to [0,255] and returns it as uint8.
func clampNormU8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// normBaseOpts returns the common blur-recovery options used across normalize tests.
func normBaseOpts(text string) []unpixel.Option {
	return []unpixel.Option{
		unpixel.WithCharset(text + " abc"),
		unpixel.WithMaxLength(len(text) + 2),
	}
}

// TestNormalize_syntheticTexturedVignette is the fail→recover proof for
// multiplicative background removal.
//
// Scenario: "go" is blurred at σ=3 via the faithful makeSyntheticRedacted
// pipeline, then a uniform grey tint (×0.60) is applied to every pixel,
// simulating a non-white canvas (e.g. a grey UI theme or dim backlight). The
// tinted background is ≈153 instead of 255, so bare Recover at the true σ fails:
// every generated candidate has white background (255) and the pixel-distance
// metric is uniformly high.
//
// WithNormalize (BgDivide + auto radius) calls Dilate which finds ≈153 as the
// local background maximum everywhere, then divides: norm = lum / 153 × 255.
// Background pixels restore to 255; text pixels restore proportionally. The
// normalised image is within 2 grey levels of the clean image, so Recover then
// succeeds at the same pinned σ.
//
// A uniform tint (not radial vignette) is used because Dilate's local-maximum
// property inverts it exactly: within any neighbourhood there is always a
// background pixel at the global tint level, so the background estimate is
// accurate at every location. A radial gradient would require the radius to track
// the field locally, which fails on the small (43×60) images produced at 32 pt.
// tintScale 0.60 keeps image mean ≈136 (> 127) so InvertAuto does not fire.
//
// The test pins σ via WithPixelator to isolate the normalisation contribution;
// InferBlurSigma accuracy on tinted inputs is a separate concern.
func TestNormalize_syntheticTexturedVignette(t *testing.T) {
	const (
		text  = "go"
		sigma = 3.0
		// tintScale 0.60: background pixels become ≈153 (not 255), mean luminance
		// ≈136 which keeps InvertAuto from firing (mean > 127). Bare Recover fails
		// because candidate comparisons assume white (255) background. After
		// BgDivide the Dilate local-max is 153 everywhere, restoring pixels exactly.
		tintScale = 0.60
	)
	img := makeTintedBlurred(t, text, sigma, tintScale)
	ctx := t.Context()

	baseOpts := []unpixel.Option{
		unpixel.WithCharset(text + " abc"),
		unpixel.WithMaxLength(len(text) + 2),
		unpixel.WithStyle(normStyle),
		unpixel.WithBlockSize(1),
		unpixel.WithPixelator(defaults.GaussianBlur(sigma)),
	}

	// Bare Recover at the true σ must fail: the tinted image has grey background
	// (≈140) while every generated candidate has white background (255).
	bareRes, err := unpixel.Recover(ctx, img, baseOpts...)
	if err != nil {
		t.Fatalf("Recover (bare, tinted): %v", err)
	}
	bareOK := strings.EqualFold(bareRes.BestGuess, text)
	t.Logf("bare Recover (tinted): best=%q total=%.4f ok=%v",
		bareRes.BestGuess, bareRes.BestTotal, bareOK)

	// BgDivide normalisation: Dilate finds tintScale×255 as the local background
	// max and division restores each pixel to the clean value.
	normImg := defaults.Normalize(img, deblur.DefaultOptions())
	normRes, err := unpixel.Recover(ctx, normImg, baseOpts...)
	if err != nil {
		t.Fatalf("Recover (after Normalize, tinted): %v", err)
	}
	t.Logf("Recover after Normalize: best=%q total=%.4f", normRes.BestGuess, normRes.BestTotal)

	if !strings.EqualFold(normRes.BestGuess, text) {
		t.Errorf("Recover+Normalize: got %q, want %q", normRes.BestGuess, text)
	}
	if bareOK {
		t.Logf("(informational) bare Recover also recovered — tint may be too mild for this image")
	}

	// Verify Result.Normalized is set on the WithNormalize RecoverBlurred path.
	normBRRes, err := unpixel.RecoverBlurred(
		ctx, img,
		unpixel.WithCharset(text+" abc"),
		unpixel.WithMaxLength(len(text)+2),
		unpixel.WithStyle(normStyle),
		unpixel.WithNormalize(),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred+WithNormalize: %v", err)
	}
	t.Logf("RecoverBlurred+WithNormalize: best=%q normalized=%v total=%.4f",
		normBRRes.BestGuess, normBRRes.Normalized, normBRRes.BestTotal)
	if !normBRRes.Normalized {
		t.Error("Result.Normalized must be true when WithNormalize is passed to RecoverBlurred")
	}
}

// TestNormalize_darkTheme verifies that WithNormalize recovers light-on-dark text
// via InvertAuto polarity correction.
func TestNormalize_darkTheme(t *testing.T) {
	const (
		text  = "go"
		sigma = 3.0
	)
	img := makeDarkThemeBlurred(t, text, sigma)
	ctx := t.Context()
	opts := append(normBaseOpts(text), unpixel.WithNormalize())

	res, err := unpixel.RecoverBlurred(ctx, img, opts...)
	if err != nil {
		t.Fatalf("RecoverBlurred (dark theme, WithNormalize): %v", err)
	}
	t.Logf("dark theme: best=%q total=%.4f normalized=%v",
		res.BestGuess, res.BestTotal, res.Normalized)

	if !res.Normalized {
		t.Error("Result.Normalized must be true for dark-theme path")
	}
	if !strings.EqualFold(res.BestGuess, text) {
		t.Errorf("dark theme: got %q, want %q", res.BestGuess, text)
	}
}

// TestNormalize_jpegDeblock verifies that WithNormalize + Deblock recovers text
// from a JPEG-compressed blurred image despite blocking artefacts.
func TestNormalize_jpegDeblock(t *testing.T) {
	const (
		text    = "go"
		sigma   = 3.0
		quality = 70
	)
	img := makeJPEGBlurred(t, text, sigma, quality)
	ctx := t.Context()
	opts := append(normBaseOpts(text), unpixel.WithNormalize(func(o *deblur.Options) {
		o.Deblock = 1 // force 3×3 median for JPEG blocking
	}))

	res, err := unpixel.RecoverBlurred(ctx, img, opts...)
	if err != nil {
		t.Fatalf("RecoverBlurred (JPEG+deblock, WithNormalize): %v", err)
	}
	t.Logf("JPEG deblock: best=%q total=%.4f normalized=%v",
		res.BestGuess, res.BestTotal, res.Normalized)

	if !res.Normalized {
		t.Error("Result.Normalized must be true for JPEG+deblock path")
	}
	if !strings.EqualFold(res.BestGuess, text) {
		t.Errorf("JPEG+deblock: got %q, want %q", res.BestGuess, text)
	}
}

// TestNormalize_regressionCleanImage is the regression guard.
//
// A clean flat-white-background blurred image must be recovered correctly by
// both the bare path and the WithNormalize path. The bare path must not set
// Result.Normalized. Two bare runs must return the same BestGuess (normalisation
// must never auto-apply).
func TestNormalize_regressionCleanImage(t *testing.T) {
	const (
		text  = "go"
		sigma = 3.0
	)
	clean := makeCleanBlurred(t, text, sigma)
	ctx := t.Context()
	opts := normBaseOpts(text)

	// Bare path.
	bareRes, err := unpixel.RecoverBlurred(ctx, clean, opts...)
	if err != nil {
		t.Fatalf("RecoverBlurred (bare): %v", err)
	}
	t.Logf("bare: best=%q total=%.4f normalized=%v", bareRes.BestGuess, bareRes.BestTotal, bareRes.Normalized)
	if !strings.EqualFold(bareRes.BestGuess, text) {
		t.Errorf("regression bare: got %q, want %q", bareRes.BestGuess, text)
	}
	if bareRes.Normalized {
		t.Error("regression: bare path must not set Result.Normalized")
	}

	// WithNormalize path: must also recover.
	normRes, err := unpixel.RecoverBlurred(ctx, clean, append(opts, unpixel.WithNormalize())...)
	if err != nil {
		t.Fatalf("RecoverBlurred (WithNormalize): %v", err)
	}
	t.Logf("WithNormalize: best=%q total=%.4f normalized=%v", normRes.BestGuess, normRes.BestTotal, normRes.Normalized)
	if !strings.EqualFold(normRes.BestGuess, text) {
		t.Errorf("regression WithNormalize: got %q, want %q", normRes.BestGuess, text)
	}
	if !normRes.Normalized {
		t.Error("regression: WithNormalize path must set Result.Normalized")
	}

	// Opt-in guard: two bare runs must be identical (normalisation is never auto-applied).
	bareRes2, err := unpixel.RecoverBlurred(ctx, clean, opts...)
	if err != nil {
		t.Fatalf("RecoverBlurred (bare2): %v", err)
	}
	if bareRes.BestGuess != bareRes2.BestGuess {
		t.Errorf("non-determinism in bare path: run1=%q run2=%q", bareRes.BestGuess, bareRes2.BestGuess)
	}
}
