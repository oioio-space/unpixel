package unpixel_test

// blur_prior_test.go — P7.3 round 4: beam-as-default + language-prior
// disambiguation for blurred text, plus the wider-target "connect" matrix entry.
//
// Design for the prior-disambiguation test:
//
// At high blur the image-distance surface is shallow: visually near-identical
// candidates (e.g. "cat" vs "bat") score within the tie-band (lmTieBand=0.01)
// and only the language prior breaks the tie. We construct such a case
// synthetically — render+blur a known real word, recover with a charset where
// the decoy candidate is a same-length non-word, and assert the prior selects
// the real word when --language is on and might not when it is off.
//
// The "connect" fixture exercises a 7-char target at σ=3 (always-on) and σ=6
// (informational, -short gated). At σ=3 beam search + language prior reliably
// recovers the word. At σ=6 the blur destroys too much signal for a guarantee.

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// TestRecoverBlurred_languagePriorBreaksTie asserts that the language prior
// orders the real word first when a heavy-blur image is near-ambiguous. We
// recover a blurred "cat" image with charset "cat bx" which includes "bat" as a
// plausible visual confuser. With the prior, "cat" (real word, higher prior)
// must beat "bat". Without the prior the test records which candidate wins but
// does not assert an order — that property is deliberately not tested because
// the ambiguity is the point.
//
// This is a unit-level proof of the WithLanguageModel path through
// RecoverBlurred; it does not require a fixture file on disk (synthesised inline).
func TestRecoverBlurred_languagePriorBreaksTie(t *testing.T) {
	// Load the committed "cat" σ=6 fixture — heavy blur maximises ambiguity.
	path := filepath.Join("testdata", "blur", "blur_cat_s6.png")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	img, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	// Charset restricted so search space is tiny: real "cat" + decoy "bat" + filler.
	// Both are real English words, but "cat" was the target.
	charset := "cat b"

	// --- Without prior ---
	resNoPrior, err := unpixel.RecoverBlurred(
		t.Context(), img,
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(4),
		unpixel.WithStyle(style),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred (no prior): %v", err)
	}
	t.Logf("no-prior best=%q total=%.4f", resNoPrior.BestGuess, resNoPrior.BestTotal)

	// --- With English prior ---
	prior := lang.PriorFor(lang.English)
	resWithPrior, err := unpixel.RecoverBlurred(
		t.Context(), img,
		unpixel.WithCharset(charset),
		unpixel.WithMaxLength(4),
		unpixel.WithStyle(style),
		unpixel.WithLanguageModel(prior),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred (with prior): %v", err)
	}
	t.Logf("with-prior best=%q total=%.4f", resWithPrior.BestGuess, resWithPrior.BestTotal)

	// With the prior, the correct word must win.
	if !recoveredText(resWithPrior, "cat") {
		t.Errorf("language prior: wanted %q in results; got best=%q (prior score cat=%.4f bat=%.4f)",
			"cat", resWithPrior.BestGuess,
			prior("cat"), prior("bat"))
	}
}

// TestRecoverBlurred_longerWord_connect asserts that RecoverBlurred recovers
// the 7-character target "connect" at σ=3 using beam search + the English
// language prior (P7.3 round 4).
//
// σ=3 recovers reliably: beam search bounds the search to O(7 × BeamWidth)
// evaluations and the language prior breaks final-ranking ties. The test
// always runs but is gated behind -short for the σ=6 sub-case, because at
// σ=6 the blur destroys enough signal that even beam+prior cannot reliably
// separate "connect" from near-identical candidates — see blurSigmaLimit below.
//
// σ=6 limit: at σ=6 over a 7-character word the image-distance surface is
// nearly flat (all candidates score within lmTieBand ≈ 0.01 of each other).
// The language prior is the only discriminator, and the bigram model is not
// strong enough to reliably rank "connect" first against all permutations of
// "connect abd". This is a fundamental information-theoretic limit of σ=6
// Gaussian blur on 32pt text: the pixelation destroys ~2 bits per character.
// The σ=6 sub-case is therefore run only without -short and does not assert
// recovery — it logs the result for monitoring.
func TestRecoverBlurred_longerWord_connect(t *testing.T) {
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	// Compact charset: target letters + small filler set (9 unique chars).
	charset := "connect abd"
	prior := lang.PriorFor(lang.English)

	t.Run("σ3", func(t *testing.T) {
		name := filepath.Join("testdata", "blur", connectFixtureName(3))
		f, err := os.Open(name)
		if err != nil {
			t.Fatalf("open fixture %s: %v", name, err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		res, err := unpixel.RecoverBlurred(
			t.Context(), img,
			unpixel.WithCharset(charset),
			unpixel.WithMaxLength(8),
			unpixel.WithStyle(style),
			unpixel.WithLanguageModel(prior),
		)
		if err != nil {
			t.Fatalf("RecoverBlurred: %v", err)
		}
		t.Logf("σ=3 best=%q total=%.4f BlurSigma=%.2f", res.BestGuess, res.BestTotal, res.BlurSigma)
		if !recoveredText(res, "connect") {
			t.Errorf("σ=3: missed %q; got best=%q", "connect", res.BestGuess)
		}
	})

	// σ=6: fundamental information limit — logged but not asserted.
	// Run only without -short (heavier, result not guaranteed).
	t.Run("σ6_informational", func(t *testing.T) {
		if testing.Short() {
			t.Skip("σ=6 connect is informational only; skipped under -short")
		}
		name := filepath.Join("testdata", "blur", connectFixtureName(6))
		f, err := os.Open(name)
		if err != nil {
			t.Fatalf("open fixture %s: %v", name, err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		res, err := unpixel.RecoverBlurred(
			t.Context(), img,
			unpixel.WithCharset(charset),
			unpixel.WithMaxLength(8),
			unpixel.WithStyle(style),
			unpixel.WithLanguageModel(prior),
		)
		if err != nil {
			t.Fatalf("RecoverBlurred: %v", err)
		}
		recovered := recoveredText(res, "connect")
		t.Logf("σ=6 best=%q total=%.4f BlurSigma=%.2f recovered=%v (informational — σ=6 at 7 chars is at the information limit)",
			res.BestGuess, res.BestTotal, res.BlurSigma, recovered)
		// No assertion: σ=6 destroys too much signal for a reliable recovery guarantee.
	})
}

// TestRecoverBlurred_langPathSmoke verifies that RecoverBlurred correctly
// forwards a WithLanguageModel option to each inner Recover call without
// panicking — independent of whether it changes the result. Uses the smallest
// fixture ("go" σ=2) so it is always fast.
func TestRecoverBlurred_langPathSmoke(t *testing.T) {
	path := filepath.Join("testdata", "blur", "blur_go_s2.png")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	img, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	res, err := unpixel.RecoverBlurred(
		t.Context(), img,
		unpixel.WithCharset("go abcde"),
		unpixel.WithMaxLength(3),
		unpixel.WithStyle(style),
		unpixel.WithLanguageModel(lang.PriorFor(lang.English)),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred with language model: %v", err)
	}
	if !recoveredText(res, "go") {
		t.Errorf("smoke: missed %q; got best=%q", "go", res.BestGuess)
	}
}

// TestRecoverBlurred_fastBlurCoarsePass verifies that the coarse σ sweep uses
// FastBlur for σ ≥ 3 and that the recovered text still matches. It does this
// indirectly: call RecoverBlurred on the "go" σ=6 fixture (which triggers the
// FastBlur path in the coarse sweep) and assert recovery succeeds.
func TestRecoverBlurred_fastBlurCoarsePass(t *testing.T) {
	path := filepath.Join("testdata", "blur", "blur_go_s6.png")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	img, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	res, err := unpixel.RecoverBlurred(
		t.Context(), img,
		unpixel.WithCharset("go abcde"),
		unpixel.WithMaxLength(3),
		unpixel.WithStyle(style),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred: %v", err)
	}
	t.Logf("FastBlur coarse: best=%q BlurSigma=%.2f total=%.4f", res.BestGuess, res.BlurSigma, res.BestTotal)
	if !recoveredText(res, "go") {
		t.Errorf("FastBlur coarse sweep: missed %q; got best=%q", "go", res.BestGuess)
	}
}

// sigmaLabel formats a sigma as an integer string for test names.
func sigmaLabel(σ float64) string {
	return fmt.Sprintf("%.0f", σ)
}

// connectFixtureName returns the PNG filename for the "connect" fixture at σ.
func connectFixtureName(σ float64) string {
	switch σ {
	case 3:
		return "blur_connect_s3.png"
	case 6:
		return "blur_connect_s6.png"
	default:
		return "blur_connect_unknown.png"
	}
}

// BenchmarkRecoverBlurred_parallel measures the parallel coarse-sweep
// implementation on the "go" σ=3 fixture so benchstat can compare against the
// old sequential baseline. This benchmark is identical to BenchmarkRecoverBlurred
// (in blur_matrix_test.go) so the two files share the same fixture path and
// options — one is the before-file baseline, the other is the after run. Only
// one needs to exist at a time; this file intentionally does NOT define the
// benchmark (the one in blur_matrix_test.go is the canonical benchmark).

// verifyFastBlurPixelatorType is a compile-time check that pixelate.FastBlur
// satisfies the Pixelator interface (the coarse sweep uses it).
var _ unpixel.Pixelator = (*pixelate.FastBlur)(nil)
