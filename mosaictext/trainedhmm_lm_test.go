package mosaictext_test

// trainedhmm_lm_test.go — TDD tests for roadmap item #3:
// language-model-in-decode (LM-weighted Viterbi) and offset robustness.
//
// Test plan:
//  1. TestLMWeightOptionExists        — WithTHMMLMWeight compiles and returns non-nil.
//  2. TestLMWeightZeroIdentity        — beta=0 is byte-identical to the no-LM default (same seed).
//  3. TestLMImprovesSentence          — LM fusion produces a lower edit-distance on a
//                                       short English sentence than the no-LM baseline.
//  4. TestDigitGateStillExact         — the digit gate ("3141592653") still passes with LM active
//                                       (beta=0 on digit-only charset, or small beta).
//  5. BenchmarkViterbiLMOverhead      — measures the ns/op overhead of LM-fused vs plain Viterbi
//                                       so callers can verify decode-phase cost is acceptable.

import (
	"image"
	"image/draw"
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/mosaictext"
)

// Imports note: thmmFindFont, thmmLoadPNG, thmmSavePNG, thmmEditDistance are
// defined in trainedhmm_test.go (same mosaictext_test package) and shared here.

// lmBenchSink defeats dead-code elimination of DecodeTrainedHMM results in benchmarks.
var lmBenchSink mosaictext.Result

// ---- 1. option-wiring smoke test ----

// TestLMWeightOptionExists verifies WithTHMMLMWeight compiles and returns a non-nil THMMOption.
func TestLMWeightOptionExists(t *testing.T) {
	t.Parallel()
	opt := mosaictext.WithTHMMLMWeight(1.0)
	if opt == nil {
		t.Error("WithTHMMLMWeight(1.0) returned nil option")
	}
	opt0 := mosaictext.WithTHMMLMWeight(0)
	if opt0 == nil {
		t.Error("WithTHMMLMWeight(0) returned nil option")
	}
}

// ---- 2. beta=0 identity ----

// TestLMWeightZeroIdentity proves that DecodeTrainedHMM with WithTHMMLMWeight(0)
// plus WithTHMMLanguage(English) produces byte-identical Text and Distance to the
// baseline (no LM options), given the same seed. This is the non-regression contract.
func TestLMWeightZeroIdentity(t *testing.T) {
	fontData := b4FindFont(t, "Liberation Mono")
	const (
		text  = "3141592653"
		fs    = 32.0
		block = 4
	)

	mosaic := b4SyntheticMosaic(t, text, fontData, fs, block)
	loaded := b4LoadPNG(t, b4SavePNG(t, mosaic))

	baseOpts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset("0123456789"),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(128),
		mosaictext.WithTHMMCorpus(200),
		mosaictext.WithTHMMSeed(42),
	}

	resBase, errBase := mosaictext.DecodeTrainedHMM(t.Context(), loaded, baseOpts...)
	if errBase != nil {
		t.Fatalf("baseline: %v", errBase)
	}

	lmZeroOpts := append(
		slices.Clone(baseOpts),
		mosaictext.WithTHMMLMWeight(0),
		mosaictext.WithTHMMLanguage(lang.English),
	)
	resLMZero, errLM := mosaictext.DecodeTrainedHMM(t.Context(), loaded, lmZeroOpts...)
	if errLM != nil {
		t.Fatalf("LM beta=0: %v", errLM)
	}

	if resLMZero.Text != resBase.Text {
		t.Errorf("beta=0 identity FAILED: got %q, want %q (baseline)", resLMZero.Text, resBase.Text)
	}
	if resLMZero.Distance != resBase.Distance {
		t.Errorf("beta=0 distance differs: got %.6f, want %.6f", resLMZero.Distance, resBase.Distance)
	}
}

// ---- 3. LM improves a sentence ----

// TestLMImprovesSentence renders a short English sentence with Liberation Sans,
// decodes it with and without LM fusion, and verifies that the LM-fused result
// has a strictly lower edit-distance than the no-LM baseline.
//
// Target "go run" at block=8, corpus=150: empirically gives +3–4 char improvement
// at beta=2.0, running in ~1 s. This is the key proof that LM fusion helps on
// natural-language text.
//
// Design note: the emission model at corpus=150 is deliberately noisy — the
// improvement comes purely from the LM prior steering Viterbi toward
// linguistically plausible transitions (e.g. "g"→"o" over "g"→"j").
func TestLMImprovesSentence(t *testing.T) {
	fontData := b4FindFont(t, "Liberation Sans")
	const (
		text    = "go run"
		charset = "abcdefghijklmnopqrstuvwxyz "
		fs      = 32.0
		block   = 8
		corpus  = 150
		beta    = 2.0
	)

	mosaic := b4SyntheticMosaic(t, text, fontData, fs, block)
	loaded := b4LoadPNG(t, b4SavePNG(t, mosaic))

	baseOpts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Sans"),
		mosaictext.WithTHMMCharset(charset),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(64),
		mosaictext.WithTHMMCorpus(corpus),
		mosaictext.WithTHMMSeed(7),
	}

	resBase, errBase := mosaictext.DecodeTrainedHMM(t.Context(), loaded, baseOpts...)
	if errBase != nil {
		t.Fatalf("baseline decode: %v", errBase)
	}

	lmOpts := append(
		slices.Clone(baseOpts),
		mosaictext.WithTHMMLanguage(lang.English),
		mosaictext.WithTHMMLMWeight(beta),
	)
	resLM, errLM := mosaictext.DecodeTrainedHMM(t.Context(), loaded, lmOpts...)
	if errLM != nil {
		t.Fatalf("LM decode: %v", errLM)
	}

	edBase := b4EditDistance(resBase.Text, text)
	edLM := b4EditDistance(resLM.Text, text)

	t.Logf("LM sentence test — target: %q (beta=%.1f)", text, beta)
	t.Logf("  no-LM baseline: %q  edit-distance=%d", resBase.Text, edBase)
	t.Logf("  LM-fused:       %q  edit-distance=%d", resLM.Text, edLM)
	t.Logf("  improvement: %+d chars (positive = LM better)", edBase-edLM)

	// Hard gate: LM must improve (not merely match) the baseline on this sentence.
	// The "go run" target with seed=7 / block=8 / corpus=150 was validated to
	// produce a consistent +3–4 char improvement; a regression here indicates the
	// LM accounting or charsetHasLetters guard is broken.
	if edLM >= edBase {
		t.Errorf("LM fusion did not improve: baseline ed=%d, LM ed=%d (want LM < baseline)",
			edBase, edLM)
	}
}

// ---- 4. digit gate non-regression ----

// TestDigitGateWithLM verifies the digit gate "3141592653" still passes when
// WithTHMMLMWeight is set. Because the digit charset contains no Unicode letters,
// charsetHasLetters returns false and the LM scorer is silently disabled — the
// decode must be byte-identical to the plain digit gate result.
//
// This mirrors TestDecodeTrainedHMM_DigitGate exactly; the only additions are
// WithTHMMLanguage and WithTHMMLMWeight.
func TestDigitGateWithLM(t *testing.T) {
	fontData := thmmFindFont(t, "Liberation Mono")
	const (
		text  = "3141592653"
		fs    = 32.0
		block = 4
		beta  = 0.5 // non-zero to exercise the guard; LM is suppressed by charsetHasLetters
	)

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}
	img, _, renderErr := r.Render(text, unpixel.Style{FontSize: fs})
	if renderErr != nil {
		t.Fatalf("render: %v", renderErr)
	}
	mosaicImg := defaults.BlockAverage(block).Pixelate(img, 0, 0)
	loaded := thmmLoadPNG(t, thmmSavePNG(t, mosaicImg))

	res, decErr := mosaictext.DecodeTrainedHMM(
		t.Context(), loaded,
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset("0123456789"),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(128),
		mosaictext.WithTHMMCorpus(2000),
		mosaictext.WithTHMMSeed(42),
		mosaictext.WithTHMMLanguage(lang.English),
		mosaictext.WithTHMMLMWeight(beta),
	)
	if decErr != nil {
		t.Fatalf("DecodeTrainedHMM with LM: %v", decErr)
	}

	ed := thmmEditDistance(res.Text, text)
	t.Logf("digit gate with LM (beta=%.1f): got %q (want %q) edit-distance=%d", beta, res.Text, text, ed)
	if res.Text != text {
		t.Errorf("DIGIT GATE WITH LM FAILED: got %q, want %q (edit-distance %d)", res.Text, text, ed)
	}
}

// ---- 5. benchmark ----

// BenchmarkViterbiLMOverhead compares the full DecodeTrainedHMM pipeline with and
// without LM fusion to measure the per-decode ns/op overhead of ViterbiLM.
// Run with:
//
//	scripts/gotest-caged.sh go test ./mosaictext/ -run '^$' -bench BenchmarkViterbiLMOverhead -benchtime 3x -count 3
func BenchmarkViterbiLMOverhead(b *testing.B) {
	var fontData []byte
	for _, f := range fonts.All() {
		if f.Name == "Liberation Mono" {
			fontData = f.Data
			break
		}
	}
	if fontData == nil {
		b.Skip("Liberation Mono not found")
	}
	const (
		text    = "31415"
		charset = "0123456789"
		fs      = 32.0
		block   = 4
		corpus  = 200
	)

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		b.Fatalf("renderer: %v", err)
	}
	rendered, sx, rErr := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: 16, PaddingLeft: 4})
	if rErr != nil {
		b.Fatalf("render: %v", rErr)
	}
	cropped := image.NewRGBA(image.Rect(0, 0, sx, rendered.Bounds().Dy()))
	draw.Draw(cropped, cropped.Bounds(), rendered, image.Point{}, draw.Src)
	mosaic := defaults.BlockAverage(block).Pixelate(cropped, 0, 0)

	baseOpts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset(charset),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(64),
		mosaictext.WithTHMMCorpus(corpus),
		mosaictext.WithTHMMSeed(42),
	}
	lmOpts := append(
		slices.Clone(baseOpts),
		mosaictext.WithTHMMLanguage(lang.English),
		mosaictext.WithTHMMLMWeight(2.0),
	)

	b.Run("plain", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			res, _ := mosaictext.DecodeTrainedHMM(b.Context(), mosaic, baseOpts...)
			lmBenchSink = res
		}
	})

	b.Run("lm-fused", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			res, _ := mosaictext.DecodeTrainedHMM(b.Context(), mosaic, lmOpts...)
			lmBenchSink = res
		}
	})
}
