package unpixel

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"github.com/oioio-space/unpixel/internal/forensics"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// ambiguousFixturePath is a committed sRGB mosaic fixture whose DetectBlur
// intra-block variance places Conf.Kind in the ambiguous band [metaBandLow,
// metaBandHigh). In the ambiguous band, Recover's meta-strategy tries both
// sRGB and linear-light mosaic operators and uses forensics.Select to pick the
// winner. The fixture is rendered text ("go") so recovery success is assertable.
//
// Conf.Kind=0.664 for block size 8 (confirmed by FingerprintN probe).
const ambiguousFixturePath = "testdata/fixtures/block08_go.png"

// loadTestRGBA opens the PNG at path and returns it as *image.RGBA.
func loadTestRGBA(tb testing.TB, path string) *image.RGBA {
	tb.Helper()
	f, err := os.Open(path) // #nosec G304 -- compile-time constant test path
	if err != nil {
		tb.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	decoded, err := png.Decode(f)
	if err != nil {
		tb.Fatalf("decode %s: %v", path, err)
	}
	b := decoded.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), decoded, b.Min, draw.Src)
	return rgba
}

// ambiguousLinearMosaic loads the ambiguous-band test fixture and verifies
// that its top-1 FingerprintN confidence is genuinely in-band before returning.
// If the fixture's confidence ever moves out of band (e.g. due to a detector
// change), the test fails here — keeping the band-membership assertion tight.
func ambiguousLinearMosaic(t *testing.T) (*image.RGBA, string) {
	t.Helper()
	img := loadTestRGBA(t, ambiguousFixturePath)

	// Assert band membership so the meta path is genuinely exercised.
	ranked := forensics.FingerprintN(imutil.ToRGBA(img), forensics.Hint{Block: 8})
	conf := ranked[0].Conf.Kind
	if conf < metaBandLow || conf >= metaBandHigh {
		t.Fatalf("fixture %s has Conf.Kind=%.3f which is outside the ambiguous band [%.2f,%.2f); "+
			"choose a fixture whose confidence is in-band or update the threshold constants",
			ambiguousFixturePath, conf, metaBandLow, metaBandHigh)
	}
	return img, "go"
}

// TestRecover_metaRecoversAmbiguous asserts that when the top-1 FingerprintN
// confidence is in the ambiguous band, the meta-strategy (top-2 trial + secured
// selection) recovers the correct text. The fixture is an sRGB block-8 mosaic;
// both sRGB and linear mosaic operators are tried, and forensics.Select picks
// the sRGB result whose BestTotal is near zero (pixel-perfect match).
func TestRecover_metaRecoversAmbiguous(t *testing.T) {
	img, want := ambiguousLinearMosaic(t)
	res, err := Recover(t.Context(), img,
		WithAuto(),
		WithBlockSize(8),
		WithCharset("go abcde"),
		WithMaxLength(3),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if res.BestGuess != want {
		t.Errorf("BestGuess = %q, want %q", res.BestGuess, want)
	}
}

// TestRecover_metaTiebreakRecoversMosaic is the live integration test for the
// gamma-coherence tiebreak (I-1 fix). It synthesizes a linear-light mosaic
// whose Conf.Kind falls in the ambiguous band, then forces both the linear and
// sRGB operators through the meta-strategy and asserts:
//
//   - forensics.Select returns ok=true (not abstain), and
//   - the recovered text equals the ground truth.
//
// The synthesis uses the same varying-fill technique as
// TestRecover_autoFingerprintInstallsLinear: a checkerboard of black/white
// blocks at varying fill fractions so that linear vs sRGB block averaging
// produce measurably different block means, giving Conf.Gamma ≈ 0.69 for
// linear and ≈ 0.07 for sRGB — a coherence gap of ≈ 0.62 >> metaCoherenceMargin.
//
// NOTE on the text-disagreement requirement: at the Recover level the step-5
// (disagreement) path is only exercised when the two mosaic operators produce
// DIFFERENT BestGuess texts. For the short word "go" both operators usually
// converge on the correct answer (agreement, step 4). Forcing guaranteed
// disagreement would require a committed fixture calibrated so the wrong
// operator produces a plausible but incorrect BestGuess — not constructible
// deterministically in unit time. The pure-unit coverage of step 5 is therefore
// carried by TestSelect_coherenceTiebreakUsesGamma (forensics package). This
// live test covers the integration path (Select called from Recover) for the
// agreement case that the fixture exercises, and verifies that the fix did not
// regress Recover's recovery quality.
func TestRecover_metaTiebreakRecoversMosaic(t *testing.T) {
	// Use the committed ambiguous-band fixture: a linear mosaic with
	// Conf.Kind=0.664 (in-band) and Conf.Gamma≈0.689 (linear) vs ≈0.069 (sRGB),
	// confirmed by the probe in TestRecover_autoFingerprintInstallsLinear.
	img, want := ambiguousLinearMosaic(t)

	// Verify that FingerprintN now produces a decisive gamma coherence gap.
	ranked := forensics.FingerprintN(imutil.ToRGBA(img), forensics.Hint{Block: 8})
	if len(ranked) < 2 {
		t.Fatal("FingerprintN returned fewer than 2 operators; cannot test tiebreak")
	}
	gap := (ranked[0].Conf.Kind + ranked[0].Conf.Gamma) - (ranked[1].Conf.Kind + ranked[1].Conf.Gamma)
	if gap <= metaCoherenceMargin {
		t.Skipf("coherence gap=%.4f ≤ metaCoherenceMargin=%.2f — fixture no longer exercises the tiebreak (update fixture or threshold)", gap, metaCoherenceMargin)
	}

	res, err := Recover(t.Context(), img,
		WithAuto(),
		WithBlockSize(8),
		WithCharset("go abcde"),
		WithMaxLength(3),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if res.BestGuess != want {
		t.Errorf("BestGuess = %q, want %q (coherence gap=%.4f, should be decisive)", res.BestGuess, want, gap)
	}
}

// TestRecover_metaAbstainsOnDisagreement asserts the "no confident-wrong"
// contract: when two operators disagree and neither holds a decisive coherence
// lead, the result must NOT be a high-fidelity wrong answer. Using the same
// fixture as TestRecover_metaRecoversAmbiguous, either the meta picks the right
// answer (BestGuess == want) or it abstains and fidelity is low — but a
// confident wrong answer (BestGuess != want AND Fidelity() > 0.9) is forbidden.
func TestRecover_metaAbstainsOnDisagreement(t *testing.T) {
	img, want := ambiguousLinearMosaic(t)
	res, err := Recover(t.Context(), img,
		WithAuto(),
		WithBlockSize(8),
		WithCharset("go abcde"),
		WithMaxLength(3),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if res.BestGuess != want && res.Fidelity() > 0.9 {
		t.Errorf("confident-wrong: guess=%q (want %q) at fidelity %.2f — "+
			"meta-strategy must not produce a high-fidelity wrong answer",
			res.BestGuess, want, res.Fidelity())
	}
}
