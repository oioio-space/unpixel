package unpixel_test

import (
	"image"
	"os"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
)

// TestVerify_RealHelloWorld confirms that unpixel.Verify confirms "Hello World !"
// on the content-cropped real GIMP mosaic sample (testdata/real/hello-world.png),
// using the exhaustive alignment path added to verifyCore (render → ink-crop →
// sub-block-phase sweep + pixel-position slide). This is the production integration
// test for the LLM-propose/physical-verify loop on real, non-pipeline-generated
// images.
//
// A clearly-different-shape decoy ("HELLO WORLD !") must not match, demonstrating
// that physical discrimination works where block signal differs. Semantic ties
// (e.g. "Hello Norld !") may also match — that is expected; the language prior
// disambiguates those, not the physical score.
func TestVerify_RealHelloWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-mosaic Verify integration in -short mode")
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

	// Crop to content bounds + margin, matching the direct-model test geometry
	// used in TestRealMosaic_HelloWorld and TestHelloWorld_RecoverableByProposeVerify.
	rect := contentBounds(src)
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+128, rect.Dy()+32))
	xdraw.Draw(target, target.Bounds(), image.White, image.Point{}, xdraw.Src)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), src, rect.Min, xdraw.Src)

	r := notoMonoRenderer(t)

	vs, err := unpixel.Verify(
		t.Context(),
		target,
		[]string{"Hello World !", "HELLO WORLD !"},
		unpixel.WithRenderer(r),
		unpixel.WithPixelator(defaults.LinearBlockAverage(32)),
		unpixel.WithBlockSize(32),
		unpixel.WithStyle(unpixel.Style{FontSize: 124, XScale: 1.06}),
	)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	byText := make(map[string]unpixel.Verdict, len(vs))
	for _, v := range vs {
		byText[v.Text] = v
	}

	const τ = unpixel.VerifyMatchThreshold

	truth := byText["Hello World !"]
	t.Logf("truth %q: distance=%.4f match=%v", "Hello World !", truth.Distance, truth.Match)
	if truth.Distance > τ {
		t.Errorf("truth %q: distance %.4f > τ %.2f, want Match=true", "Hello World !", truth.Distance, τ)
	}
	if !truth.Match {
		t.Errorf("truth %q: Match=false (distance %.4f)", "Hello World !", truth.Distance)
	}

	decoy := byText["HELLO WORLD !"]
	t.Logf("decoy %q: distance=%.4f match=%v", "HELLO WORLD !", decoy.Distance, decoy.Match)
	if decoy.Match {
		t.Errorf("decoy %q: Match=true (distance %.4f), want no-match", "HELLO WORLD !", decoy.Distance)
	}
}
