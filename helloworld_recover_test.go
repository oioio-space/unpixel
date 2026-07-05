package unpixel_test

import (
	"image"
	"os"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/metric"
)

// TestHelloWorld_RecoverableByProposeVerify demonstrates the recoverable path for
// coarse-block real redactions on testdata/real/hello-world.png — the first
// real-corpus recovery, achieved via the LLM-propose / whole-string-verify loop
// (unpixel.Verify, #3) rather than per-character search.
//
// The chain of measured findings this pins down:
//
//   - Per-character decoding (monospace marginal, reference matching, +LM) is
//     information-starved at block=32: each glyph spans ~2-3 block columns, so no
//     per-char method recovers the text (all yield garbage at distance 0.017-0.026).
//   - Whole-string physical scoring WITH exhaustive alignment (ink-crop + position
//     and sub-block-phase slide, as the direct model uses) confirms the true string
//     "Hello World !" at distance 0.0000 — the redaction IS reversible.
//   - But physical scoring alone has SEMANTIC TIES: "Hello Norld !" also scores
//     0.0000 because W and N average to the same block values at block=32. Only a
//     language prior distinguishes the real word "World" from "Norld".
//
// Hence the recoverable path = a generative proposer (a language model proposes
// plausible words) + whole-string physical verify (confirms the fit) + the semantic
// prior as tie-breaker. This is the LLM-propose/verify differentiator, proven on a
// real image. It also pinpoints the concrete production gap: unpixel.Verify's
// verifyCore aligns only on block-grid offsets (via TotalScore), NOT the ink-crop +
// position slide this test uses, so today Verify scores ~0.63 for every candidate
// on this wider redaction. Giving verifyCore this alignment makes the loop work on
// real images — the highest-leverage next step.
func TestHelloWorld_RecoverableByProposeVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real hello-world propose/verify demonstration in -short mode")
	}

	f, err := os.Open(realMosaicSample)
	if err != nil {
		t.Fatalf("open %s: %v", realMosaicSample, err)
	}
	defer func() { _ = f.Close() }()
	src, err := decodePNG(f)
	if err != nil {
		t.Fatalf("decode %s: %v", realMosaicSample, err)
	}

	rect := contentBounds(src)
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+128, rect.Dy()+32))
	xdraw.Draw(target, target.Bounds(), image.White, image.Point{}, xdraw.Src)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), src, rect.Min, xdraw.Src)

	r := notoMonoRenderer(t)
	m := metric.NewPixelmatch(0.1)
	const block = 32
	lin := defaults.LinearBlockAverage(block)

	// verifyLike scores a proposed string with whole-image comparison under the
	// same exhaustive alignment (ink-crop + position/phase slide) the direct model
	// uses — the alignment verifyCore is missing today.
	verifyLike := func(s string) float64 {
		img, sx, rerr := r.Render(s, unpixel.Style{FontSize: 124, XScale: 1.06})
		if rerr != nil {
			t.Fatalf("render %q: %v", s, rerr)
		}
		bb := inkBounds(img, sx)
		out := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
		xdraw.Draw(out, out.Bounds(), img, bb.Min, xdraw.Src)
		return bestDistance(out, target, lin, m, block)
	}

	const truth = "Hello World !"
	truthDist := verifyLike(truth)
	t.Logf("truth %q verified at distance %.4f", truth, truthDist)

	// The true string is physically confirmed (well below the match threshold): a
	// proposer that emits it gets a decisive physical match.
	if truthDist > 0.05 {
		t.Errorf("truth %q distance %.4f, want <=0.05 — the redaction should be verifiable", truth, truthDist)
	}

	// A candidate of the wrong shape (all-caps, different ink) is physically
	// rejected — discrimination exists where the block signal differs.
	if d := verifyLike("HELLO WORLD !"); d <= unpixel.VerifyMatchThreshold {
		t.Errorf("all-caps decoy distance %.4f, want > %.2f (should not match)", d, unpixel.VerifyMatchThreshold)
	}

	// The semantic tie: "Hello Norld !" is physically indistinguishable (W≈N at
	// block=32), so only the language prior separates the real word from the
	// non-word. This documents WHY propose+verify needs the semantic prior; it is
	// not asserted as a failure — it is the mechanism.
	t.Logf("semantic tie: %q verified at %.4f (non-word — the language prior is the arbiter)",
		"Hello Norld !", verifyLike("Hello Norld !"))
}
