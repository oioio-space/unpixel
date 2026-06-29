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
