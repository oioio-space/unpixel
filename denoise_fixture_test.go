package unpixel_test

// denoise_fixture_test.go exercises the committed noisy fixture
// (testdata/real/hello-world-noisy.png) as a regression asset for the P7
// auto-denoise improvement.
//
// Three assertions:
//  1. InferImpulseNoise fires on the noisy fixture (≥ autoThrTest) and is ~0
//     on the clean original (< autoThrTest). Proves the detector distinguishes
//     the 4% S&P fixture from a normal mosaic capture.
//  2. Median(noisy, 1) reduces InferImpulseNoise to ~0, proving the median
//     filter eliminates the impulse noise the detector found.
//  3. LocateRedaction fails on the raw noisy image (noise destroys the
//     blur-gradient heuristic) but succeeds on Median(noisy, 1), showing that
//     one median pass restores downstream pipeline usability.

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

const (
	fixtureNoisy = "testdata/real/hello-world-noisy.png"
	fixtureClean = "testdata/real/hello-world.png"
)

// loadRGBA opens a PNG file and returns it as *image.RGBA.
func loadRGBA(tb testing.TB, path string) *image.RGBA {
	tb.Helper()
	f, err := os.Open(path)
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

// TestDenoise_InferImpulseNoise_NoisyVsClean verifies that the detector fires
// on the committed noisy fixture but not on the clean original. This is the
// key regression: the auto-denoise threshold must distinguish a 4% S&P image
// from a normal mosaic capture.
func TestDenoise_InferImpulseNoise_NoisyVsClean(t *testing.T) {
	t.Parallel()

	noisy := loadRGBA(t, fixtureNoisy)
	clean := loadRGBA(t, fixtureClean)

	gotNoisy := unpixel.InferImpulseNoise(noisy)
	gotClean := unpixel.InferImpulseNoise(clean)

	t.Logf("InferImpulseNoise(noisy) = %.6f (want >= %.4f)", gotNoisy, autoThrTest)
	t.Logf("InferImpulseNoise(clean) = %.6f (want <  %.4f)", gotClean, autoThrTest)

	if gotNoisy < autoThrTest {
		t.Errorf("InferImpulseNoise(noisy fixture) = %.6f, want >= %.4f: detector did not fire on 4%% S&P noise",
			gotNoisy, autoThrTest)
	}
	if gotClean >= autoThrTest {
		t.Errorf("InferImpulseNoise(clean fixture) = %.6f, want < %.4f: clean original would be auto-denoised",
			gotClean, autoThrTest)
	}
}

// TestDenoise_MedianEliminatesImpulseNoise verifies that one pass of
// imutil.Median(noisy, 1) brings InferImpulseNoise back below the auto-denoise
// threshold. This is the core functional requirement: after the median filter
// runs, the pipeline no longer classifies the image as noisy.
func TestDenoise_MedianEliminatesImpulseNoise(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping median filter in -short mode (1450×509 3×3 kernel)")
	}

	noisy := loadRGBA(t, fixtureNoisy)

	before := unpixel.InferImpulseNoise(noisy)
	denoised := imutil.Median(noisy, 1)
	after := unpixel.InferImpulseNoise(denoised)

	t.Logf("InferImpulseNoise: before=%.6f  after=%.6f  threshold=%.4f", before, after, autoThrTest)

	if before < autoThrTest {
		t.Fatalf("precondition failed: InferImpulseNoise(noisy)=%.6f, want >= %.4f (fixture must trigger detector)",
			before, autoThrTest)
	}
	if after >= autoThrTest {
		t.Errorf("InferImpulseNoise after Median(noisy,1) = %.6f, want < %.4f: median did not eliminate impulse noise",
			after, autoThrTest)
	}
}

// TestDenoise_LocateRedactionRestoredByMedian verifies that LocateRedaction
// fails on the raw noisy image (4% S&P noise destroys the blur-gradient
// heuristic) but succeeds after one Median(noisy, 1) pass. This is the
// strongest downstream proof: a noisy capture that the pipeline cannot locate
// becomes processable after auto-denoise.
func TestDenoise_LocateRedactionRestoredByMedian(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping median filter in -short mode (1450×509 3×3 kernel)")
	}

	noisy := loadRGBA(t, fixtureNoisy)

	_, okNoisy := unpixel.LocateRedaction(noisy)
	denoised := imutil.Median(noisy, 1)
	rectDenoised, okDenoised := unpixel.LocateRedaction(denoised)

	t.Logf("LocateRedaction: noisy ok=%v  denoised ok=%v rect=%v", okNoisy, okDenoised, rectDenoised)

	if okNoisy {
		t.Errorf("LocateRedaction(noisy) = ok=true, want false: 4%% S&P noise should break the blur-gradient heuristic")
	}
	if !okDenoised {
		t.Errorf("LocateRedaction(Median(noisy,1)) = ok=false, want true: median should restore locatability")
	}
	if okDenoised && (rectDenoised.Dx() < 200 || rectDenoised.Dy() < 10) {
		t.Errorf("LocateRedaction(Median(noisy,1)) = %v, too small (dx=%d dy=%d): want dx>=200 dy>=10",
			rectDenoised, rectDenoised.Dx(), rectDenoised.Dy())
	}
}
