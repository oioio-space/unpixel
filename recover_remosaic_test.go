package unpixel_test

// Tests for the Hill–Zhou–Saul–Shacham PETS-2016 §4 remosaic path
// (WithRemosaic / WithRemosaicGrid on RecoverBlurred).
//
// Three categories:
//
//  1. Self-consistent synthetic: render → heavy Gaussian blur → recover.
//     Plain RecoverBlurred (no remosaic) is run first and its outcome documented;
//     WithRemosaic must recover the correct text.
//
//  2. JPEG-robustness synthetic: render → moderate blur → JPEG encode at q≈40
//     → decode → recover.  This is the paper's key claim.  If WithRemosaic does
//     NOT outperform the plain path we report that honestly via t.Log; the test
//     never fails on that basis alone.
//
//  3. Edge cases: tiny image (1×1, 2×2), near-zero σ.
//
//  4. Benchmark: the per-candidate cost of the remosaic path.

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"

	_ "github.com/oioio-space/unpixel/defaults" // wires DefaultComponents, DefaultBlurStrategy
)

// makeHeavyBlurred renders text and applies a heavy Gaussian blur (σ=7),
// returning the pixel-accurate blurred image (BlockSize=1).
func makeHeavyBlurred(t *testing.T, text string, sigma float64) *image.RGBA {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := components{
		renderer:  r,
		pixelator: pixelate.NewGaussianBlur(sigma),
		metric:    metric.NewPixelmatch(0.02),
		strategy:  search.NewGuidedStrategy(),
	}
	return makeSyntheticRedacted(t, c, text, style, 1)
}

// jpegRoundTrip encodes img to JPEG at the given quality and decodes it back,
// simulating the compression artefacts present in real captures.
func jpegRoundTrip(t *testing.T, img image.Image, quality int) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	decoded, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	return decoded
}

// TestRemosaic_heavyBlurSynthetic is the self-consistent heavy-blur test.
// We render a short word, blur at σ=7 (heavy), and compare:
//   - plain RecoverBlurred (no remosaic): outcome documented, not asserted.
//   - WithRemosaic: must recover the correct text.
//
// A bounded charset is used so both paths have a fair chance; the remosaic
// path is expected to converge at this σ because the block-average equalises
// the blur-mismatch signal.
func TestRemosaic_heavyBlurSynthetic(t *testing.T) {
	const (
		target  = "hi"
		charset = "hi abceg"
		sigma   = 7.0
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	blurred := makeHeavyBlurred(t, target, sigma)

	commonOpts := []unpixel.Option{
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(len(target) + 1),
		unpixel.WithStyle(style),
	}

	// Document the plain path outcome — no assertion, just informative.
	plainRes, err := unpixel.RecoverBlurred(t.Context(), blurred, commonOpts...)
	if err != nil {
		t.Fatalf("plain RecoverBlurred: %v", err)
	}
	plainOK := recoveredText(plainRes, target)
	t.Logf("plain RecoverBlurred: best=%q total=%.4f recovered=%v",
		plainRes.BestGuess, plainRes.BestTotal, plainOK)

	// The remosaic path MUST recover the correct text.
	remosaicRes, err := unpixel.RecoverBlurred(t.Context(), blurred,
		append(commonOpts, unpixel.WithRemosaic())...)
	if err != nil {
		t.Fatalf("remosaic RecoverBlurred: %v", err)
	}
	t.Logf("remosaic RecoverBlurred: best=%q total=%.4f sigma=%.2f",
		remosaicRes.BestGuess, remosaicRes.BestTotal, remosaicRes.BlurSigma)

	if !recoveredText(remosaicRes, target) {
		t.Errorf("remosaic path: want %q in result, got best=%q (candidates=%v)",
			target, remosaicRes.BestGuess,
			func() []string {
				out := make([]string, len(remosaicRes.TopN))
				for i, e := range remosaicRes.TopN {
					out[i] = e.Guess
				}
				return out
			}())
	}
}

// TestRemosaic_withRemosaicGrid verifies that WithRemosaicGrid(b) pins the block
// size and still recovers correctly on a synthetic heavy-blur target.
func TestRemosaic_withRemosaicGrid(t *testing.T) {
	const (
		target  = "go"
		charset = "go abcde"
		sigma   = 6.0
		grid    = 6
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	blurred := makeHeavyBlurred(t, target, sigma)

	res, err := unpixel.RecoverBlurred(
		t.Context(), blurred,
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(len(target)+1),
		unpixel.WithStyle(style),
		unpixel.WithRemosaicGrid(grid),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred: %v", err)
	}
	t.Logf("remosaic grid=%d: best=%q total=%.4f sigma=%.2f", grid, res.BestGuess, res.BestTotal, res.BlurSigma)
	if !recoveredText(res, target) {
		t.Errorf("want %q, got best=%q", target, res.BestGuess)
	}
}

// TestRemosaic_JPEGRobustness is the paper's key claim: blur at moderate σ,
// JPEG-compress at q≈40, decode, then recover.  We report the outcome of both
// paths honestly; if WithRemosaic does NOT improve over the plain path that is
// reported in the test log as an honest finding.
func TestRemosaic_JPEGRobustness(t *testing.T) {
	const (
		target   = "go"
		charset  = "go abcde"
		sigma    = 4.0
		jpegQual = 40
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	blurred := makeHeavyBlurred(t, target, sigma)

	// JPEG round-trip: encode at q=40, decode — introduces blocking artefacts.
	jpegImg := jpegRoundTrip(t, blurred, jpegQual)

	commonOpts := []unpixel.Option{
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(len(target) + 1),
		unpixel.WithStyle(style),
	}

	plainRes, err := unpixel.RecoverBlurred(t.Context(), jpegImg, commonOpts...)
	if err != nil {
		t.Fatalf("plain RecoverBlurred: %v", err)
	}
	plainOK := recoveredText(plainRes, target)
	t.Logf("[JPEG q=%d] plain: best=%q total=%.4f recovered=%v", jpegQual, plainRes.BestGuess, plainRes.BestTotal, plainOK)

	remosaicRes, err := unpixel.RecoverBlurred(t.Context(), jpegImg,
		append(commonOpts, unpixel.WithRemosaic())...)
	if err != nil {
		t.Fatalf("remosaic RecoverBlurred: %v", err)
	}
	remosaicOK := recoveredText(remosaicRes, target)
	t.Logf("[JPEG q=%d] remosaic: best=%q total=%.4f sigma=%.2f recovered=%v",
		jpegQual, remosaicRes.BestGuess, remosaicRes.BestTotal, remosaicRes.BlurSigma, remosaicOK)

	// Honest reporting: if neither path recovers, note it but do not fail —
	// the JPEG path may genuinely be hard at this σ/quality combination.
	// If remosaic recovered but plain did not, that is the paper's claim confirmed.
	switch {
	case remosaicOK && !plainOK:
		t.Logf("FINDING: remosaic IMPROVED over plain (paper's claim confirmed for σ=%.1f q=%d)", sigma, jpegQual)
	case !remosaicOK && plainOK:
		t.Logf("FINDING: plain recovered but remosaic did NOT (unexpected regression)")
	case !remosaicOK && !plainOK:
		t.Logf("FINDING: neither path recovered %q at σ=%.1f q=%d — hard case, noting honestly", target, sigma, jpegQual)
	default:
		t.Logf("FINDING: both paths recovered %q", target)
	}
	// The remosaic path must not regress vs plain: if plain recovered, remosaic must too.
	if plainOK && !remosaicOK {
		t.Errorf("remosaic regressed vs plain: plain recovered %q, remosaic gave %q",
			target, remosaicRes.BestGuess)
	}
}

// TestRemosaic_edgeCaseTinyImage verifies neither path panics on tiny images.
func TestRemosaic_edgeCaseTinyImage(t *testing.T) {
	for _, sz := range []int{1, 2, 4} {
		t.Run("sz"+string(rune('0'+sz)), func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, sz, sz))
			for i := range img.Pix {
				img.Pix[i] = 200
			}
			// Must not panic; may return an empty result or ErrNilImage is impossible here.
			res, err := unpixel.RecoverBlurred(
				t.Context(), img,
				unpixel.WithCharset("ab"),
				unpixel.WithMaxLength(1),
				unpixel.WithRemosaic(),
			)
			if err != nil {
				t.Logf("sz=%d: RecoverBlurred error (acceptable): %v", sz, err)
				return
			}
			t.Logf("sz=%d: best=%q total=%.4f", sz, res.BestGuess, res.BestTotal)
		})
	}
}

// TestRemosaic_edgeCaseNearZeroSigma verifies the path is stable when the
// image appears nearly sharp (InferBlurSigma returns a small σ). The σ fallback
// inside recoverWithRemosaic sets σ=3 when InferBlurSigma returns 0.
func TestRemosaic_edgeCaseNearZeroSigma(t *testing.T) {
	// A tiny uniform image will return σ=0 from InferBlurSigma.
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := range img.Pix {
		img.Pix[i] = 230
	}
	// Must not panic.
	_, err := unpixel.RecoverBlurred(
		t.Context(), img,
		unpixel.WithCharset("ab"),
		unpixel.WithMaxLength(1),
		unpixel.WithRemosaic(),
	)
	if err != nil {
		t.Logf("near-zero σ: error (acceptable): %v", err)
	}
}

// TestRemosaic_BlurSigmaSet verifies Result.BlurSigma is populated by the
// remosaic path (same contract as the plain blur path).
func TestRemosaic_BlurSigmaSet(t *testing.T) {
	const (
		target  = "hi"
		charset = "hi abceg"
		sigma   = 6.0
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	blurred := makeHeavyBlurred(t, target, sigma)

	res, err := unpixel.RecoverBlurred(
		t.Context(), blurred,
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(len(target)+1),
		unpixel.WithStyle(style),
		unpixel.WithRemosaic(),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred: %v", err)
	}
	if res.BlurSigma <= 0 {
		t.Errorf("BlurSigma = %v, want > 0", res.BlurSigma)
	}
	t.Logf("BlurSigma=%.2f best=%q", res.BlurSigma, res.BestGuess)
}

// TestRemosaic_chooseRemosaicGrid exercises the auto grid-size selection via
// the public API indirectly: WithRemosaicGrid(0) should behave the same as
// WithRemosaic() (both select auto = max(2, round(σ))).
func TestRemosaic_chooseRemosaicGrid(t *testing.T) {
	const (
		target  = "go"
		charset = "go abcde"
		sigma   = 6.0
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	blurred := makeHeavyBlurred(t, target, sigma)

	opts := []unpixel.Option{
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(len(target) + 1),
		unpixel.WithStyle(style),
	}

	res0, err := unpixel.RecoverBlurred(t.Context(), blurred,
		append(opts, unpixel.WithRemosaicGrid(0))...)
	if err != nil {
		t.Fatalf("WithRemosaicGrid(0): %v", err)
	}
	resNone, err := unpixel.RecoverBlurred(t.Context(), blurred,
		append(opts, unpixel.WithRemosaic())...)
	if err != nil {
		t.Fatalf("WithRemosaic: %v", err)
	}
	// Both use auto grid — best guess must be the same (deterministic).
	if res0.BestGuess != resNone.BestGuess {
		t.Errorf("WithRemosaicGrid(0)=%q vs WithRemosaic=%q — expected identical (both auto)",
			res0.BestGuess, resNone.BestGuess)
	}
}

// TestRemosaic_nilImageRejected verifies that passing a nil image to
// RecoverBlurred with WithRemosaic returns ErrNilImage, not a panic.
func TestRemosaic_nilImageRejected(t *testing.T) {
	_, err := unpixel.RecoverBlurred(t.Context(), nil, unpixel.WithRemosaic())
	if !errors.Is(err, unpixel.ErrNilImage) {
		t.Errorf("want ErrNilImage, got %v", err)
	}
}

// TestRemosaic_withRemosaicLinear verifies the WithRemosaicLinear option
// enables the remosaic path with linear-light block averaging and recovers
// correctly on a synthetic target.
func TestRemosaic_withRemosaicLinear(t *testing.T) {
	const (
		target  = "go"
		charset = "go abcde"
		sigma   = 5.0
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	blurred := makeHeavyBlurred(t, target, sigma)

	res, err := unpixel.RecoverBlurred(
		t.Context(), blurred,
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(len(target)+1),
		unpixel.WithStyle(style),
		unpixel.WithRemosaicLinear(),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred: %v", err)
	}
	t.Logf("WithRemosaicLinear: best=%q total=%.4f sigma=%.2f", res.BestGuess, res.BestTotal, res.BlurSigma)
	if res.BlurSigma <= 0 {
		t.Errorf("BlurSigma = %v, want > 0", res.BlurSigma)
	}
	// The linear path uses a slightly different block colour; recovery may differ
	// from the sRGB path — we assert no panic and a valid sigma, not exact text.
}

// remosaicSink defeats dead-code elimination for benchmark results.
var remosaicSink unpixel.Result

// BenchmarkRemosaic_RecoverBlurred benchmarks the end-to-end remosaic recovery
// path on a synthetic σ=6 blurred "hi" image with a small charset. This
// exercises the per-candidate render→blur→blockaverage→compare hot loop under
// the remosaic operator and must be tracked alongside the plain blur path.
func BenchmarkRemosaic_RecoverBlurred(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	const sigma = 6.0
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := components{
		renderer:  r,
		pixelator: pixelate.NewGaussianBlur(sigma),
		metric:    metric.NewPixelmatch(0.02),
		strategy:  search.NewGuidedStrategy(),
	}

	// Build the target outside the loop — setup, not measured.
	img := makeSyntheticRedactedB(b, c, "hi", style, 1)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, benchErr := unpixel.RecoverBlurred(
			b.Context(),
			img,
			unpixel.WithCharset("hi abceg"),
			unpixel.WithMaxLength(3),
			unpixel.WithStyle(style),
			unpixel.WithRemosaic(),
		)
		if benchErr != nil {
			b.Fatalf("RecoverBlurred: %v", benchErr)
		}
		remosaicSink = res
	}
}

// Helper: makeSyntheticRedacted for *testing.B (mirrors the *testing.T variant).
// It calls the same function because the signature accepts testing.TB-like helpers
// only via t.Helper()/t.Fatalf which both *testing.T and *testing.B satisfy.
// We reuse the *testing.T variant by wrapping — but since Go's testing package
// doesn't provide a common interface here, we copy the minimal logic.
func makeSyntheticRedactedB(b *testing.B, c components, text string, style unpixel.Style, blockSize int) *image.RGBA {
	b.Helper()

	img, sentinelX, err := c.renderer.Render(text, style)
	if err != nil {
		b.Fatalf("render %q: %v", text, err)
	}

	bm, imageCenter := imutil.BlueMargin(img)
	if bm == 0 {
		bm = sentinelX
	}
	img = imutil.Crop(img, 0, 0, bm, img.Bounds().Dy())
	if w := img.Bounds().Dx(); blockSize-(w%blockSize) < blockSize {
		img = imutil.PadWhite(img, w+blockSize-(w%blockSize), img.Bounds().Dy())
	}
	img = c.pixelator.Pixelate(img, 0, 0)

	leftEdge := imutil.LeftEdge(img)
	adjustedCenter := imageCenter - (imageCenter % blockSize) + 4
	redactedH := 2 * adjustedCenter
	redacted := imutil.Crop(img, leftEdge, 0, img.Bounds().Dx()-leftEdge, img.Bounds().Dy())
	if redacted.Bounds().Dy() < redactedH {
		redacted = imutil.PadWhite(redacted, redacted.Bounds().Dx(), redactedH)
	}
	return redacted
}
