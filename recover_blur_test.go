package unpixel_test

import (
	"image"
	"slices"
	"strconv"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

// makeBlurred renders text and redacts it with a true Gaussian blur, mirroring
// the scorer's blur pipeline (BlockSize=1). It is the blur analogue of
// makeSyntheticRedacted and the generator for the blur recovery matrix.
func makeBlurred(t *testing.T, r *render.XImage, blur unpixel.Pixelator, text string, style unpixel.Style) *image.RGBA {
	t.Helper()
	c := components{renderer: r, pixelator: blur, metric: metric.NewPixelmatch(0.02), strategy: search.NewGuidedStrategy()}
	return makeSyntheticRedacted(t, c, text, style, 1)
}

// TestRecover_blurMatrix is the blur quality guard: redact several texts at
// several sigmas with a true Gaussian, then recover each through the engine.
// It pins blur recovery so later perf changes (box blur, reduced-resolution
// compare) can be proven not to regress recall.
func TestRecover_blurMatrix(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	cases := []struct {
		text, charset string
		sigma         float64
	}{
		{"go", "go abc", 2},
		{"go", "go abc", 4},
		{"cat", "cat eoab", 3},
		{"hi", "hi abcontinue", 6},
	}
	for _, c := range cases {
		t.Run(c.text+"_σ"+strconv.Itoa(int(c.sigma)), func(t *testing.T) {
			redacted := makeBlurred(t, r, pixelate.NewGaussianBlur(c.sigma), c.text, style)
			res, err := unpixel.Recover(
				t.Context(), redacted,
				unpixel.WithCharset(c.charset),
				unpixel.WithMaxLength(len(c.text)+1),
				unpixel.WithBlockSize(1),
				unpixel.WithPixelator(pixelate.NewGaussianBlur(c.sigma)),
				unpixel.WithStyle(style),
			)
			if err != nil {
				t.Fatalf("Recover: %v", err)
			}
			if !recoveredText(res, c.text) {
				t.Errorf("blur σ=%v: missed %q; best=%q", c.sigma, c.text, res.BestGuess)
			}
		})
	}
}

// TestRecover_fastBlurSelfConsistent checks FastBlur (#3) is exact for its own
// operator: redact and recover with FastBlur at several sigmas — always works.
func TestRecover_fastBlurSelfConsistent(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	for _, sigma := range []float64{3, 6, 10} {
		t.Run("σ"+strconv.Itoa(int(sigma)), func(t *testing.T) {
			redacted := makeBlurred(t, r, pixelate.NewFastBlur(sigma), "go", style)
			res, err := unpixel.Recover(
				t.Context(), redacted,
				unpixel.WithCharset("go abc"),
				unpixel.WithMaxLength(3),
				unpixel.WithBlockSize(1),
				unpixel.WithPixelator(pixelate.NewFastBlur(sigma)),
				unpixel.WithStyle(style),
			)
			if err != nil {
				t.Fatalf("Recover: %v", err)
			}
			if !recoveredText(res, "go") {
				t.Errorf("FastBlur σ=%v: missed %q; best=%q", sigma, "go", res.BestGuess)
			}
		})
	}
}

// TestRecover_fastBlurApproximatesGaussianAtLargeSigma documents the trade-off:
// against a TRUE Gaussian target, the box approximation preserves the ranking at
// large sigma (the expensive case where FastBlur pays off), so recovery still
// succeeds without the exact operator. (At small sigma the exact GaussianBlur is
// already cheap, so FastBlur is not used there — see resolveBlur.)
func TestRecover_fastBlurApproximatesGaussianAtLargeSigma(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	const sigma = 8.0
	redacted := makeBlurred(t, r, pixelate.NewGaussianBlur(sigma), "go", style) // true Gaussian
	res, err := unpixel.Recover(
		t.Context(), redacted,
		unpixel.WithCharset("go abc"),
		unpixel.WithMaxLength(3),
		unpixel.WithBlockSize(1),
		unpixel.WithPixelator(pixelate.NewFastBlur(sigma)), // approximate
		unpixel.WithStyle(style),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !recoveredText(res, "go") {
		t.Errorf("FastBlur vs Gaussian σ=%v: missed %q; best=%q", sigma, "go", res.BestGuess)
	}
}

// recoveredText reports whether text appears as the best guess or any candidate.
func recoveredText(res unpixel.Result, text string) bool {
	if res.BestGuess == text {
		return true
	}
	for _, e := range res.Candidates {
		if e.Guess == text {
			return true
		}
	}
	return slices.ContainsFunc(res.TopN, func(e unpixel.Eval) bool { return e.Guess == text })
}
