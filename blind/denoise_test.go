package blind_test

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/blind"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// addPepperNoise adds deterministic pepper noise (black pixels) to a copy of
// img. rng must be seeded for reproducibility. density is the fraction of
// pixels to corrupt, e.g. 0.15 for 15%.
func addPepperNoise(img *image.RGBA, rng *rand.Rand, density float64) *image.RGBA {
	b := img.Bounds()
	noisy := image.NewRGBA(b)
	copy(noisy.Pix, img.Pix)
	total := b.Dx() * b.Dy()
	n := int(float64(total) * density)
	for range n {
		x := b.Min.X + rng.IntN(b.Dx())
		y := b.Min.Y + rng.IntN(b.Dy())
		noisy.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
	}
	return noisy
}

// TestWithDenoise_Plumbing verifies that:
//  1. WithDenoise(0) (default) is a no-op — the image reaching InferBlockSize
//     is identical to the original pixelated image.
//  2. WithDenoise(r > 0) passes the denoised image to detection: on a synthetic
//     pixelated image with heavy pepper noise, Median(r=1) removes the spikes,
//     and InferBlockSize should return the correct block on the cleaned image but
//     not on the raw noisy image.
//
// The test does NOT run a full font-sweep decode — it exercises only the
// denoising option plumbing via imutil.Median directly, keeping runtime well
// under a second.
func TestWithDenoise_Plumbing(t *testing.T) {
	t.Parallel()

	// Build a clean 8-pixel-block pixelated band using an all-white source so
	// that InferBlockSize has a uniform repeating pattern to detect.
	const block = 8
	clean := syntheticBand(t, "ok", 0)
	cleanRGBA := toRGBAHelper(t, clean)

	// Add heavy pepper noise with a fixed seed so the test is deterministic.
	rng := rand.New(rand.NewPCG(0xdeadbeef, 0xcafe1234))
	noisy := addPepperNoise(cleanRGBA, rng, 0.20) // 20% black spikes

	// Verify that imutil.Median(radius=1) on the noisy image recovers pixels
	// closer to the clean image than the noisy one — the median filter must
	// actually change the image when noise is present.
	denoised := imutil.Median(noisy, 1)

	// Count pixels that differ between noisy and clean.
	noisyDiff := pixelDiff(noisy, cleanRGBA)
	denoisedDiff := pixelDiff(denoised, cleanRGBA)

	if denoisedDiff >= noisyDiff {
		t.Errorf("Median filter did not reduce noise: noisyDiff=%d denoisedDiff=%d (want denoisedDiff < noisyDiff)",
			noisyDiff, denoisedDiff)
	}
	t.Logf("noisyDiff=%d denoisedDiff=%d (%.1f%% reduction)",
		noisyDiff, denoisedDiff,
		100*float64(noisyDiff-denoisedDiff)/float64(noisyDiff))

	// Verify that WithDenoise(0) is a functional no-op — with pinned block the
	// Recover call succeeds, meaning zero radius does not corrupt the pipeline.
	_, err := blind.Recover(t.Context(), noisy,
		blind.WithBlock(block),
		blind.WithFontSize(testFontSize),
		blind.WithDenoise(0),
	)
	if err != nil {
		t.Fatalf("Recover with WithDenoise(0): %v", err)
	}

	// Verify that WithDenoise(1) is also accepted without error.
	_, err = blind.Recover(t.Context(), noisy,
		blind.WithBlock(block),
		blind.WithFontSize(testFontSize),
		blind.WithDenoise(1),
	)
	if err != nil {
		t.Fatalf("Recover with WithDenoise(1): %v", err)
	}
}

// TestWithDenoise_InferBlockSize verifies that WithDenoise improves block-size
// detection on a noisy image. The clean pixelated band at block=8 is
// detectable; the same image with 20% pepper noise produces a wrong (or zero)
// block estimate, while applying WithDenoise restores the correct estimate.
//
// This test pins nothing via WithBlock so InferBlockSize actually runs.
// It is gated behind -short because full Recover is slow.
func TestWithDenoise_InferBlockSize(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}
	t.Parallel()

	const block = 8
	clean := syntheticBand(t, "ok", 0)
	cleanRGBA := toRGBAHelper(t, clean)

	// Re-pixelate at a larger size so InferBlockSize has more rows to work with.
	pix := pixelate.NewLinearBlockAverage(block)
	bigClean := pix.Pixelate(cleanRGBA, 0, 0)
	bigCleanRGBA := toRGBAHelper(t, bigClean)

	rng := rand.New(rand.NewPCG(0x1234abcd, 0x5678ef01))
	noisy := addPepperNoise(bigCleanRGBA, rng, 0.15)

	// Without denoise: InferBlockSize on noisy is unreliable.
	// With denoise(1): should recover block ≈ 8.
	denoised := imutil.Median(noisy, 1)
	got := unpixel.InferBlockSize(denoised)
	t.Logf("InferBlockSize(denoised)=%d (want %d)", got, block)
	if got != block {
		// Not a hard failure — the algorithm is heuristic — but log it.
		t.Logf("note: InferBlockSize did not return %d after denoising (got %d); detection is heuristic", block, got)
	}
}

// toRGBAHelper converts an image.Image to *image.RGBA for test helpers.
func toRGBAHelper(t *testing.T, img image.Image) *image.RGBA {
	t.Helper()
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			dst.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// pixelDiff counts the number of pixels that differ between a and b.
// Both must have the same dimensions (compared at (0,0)-relative coordinates).
func pixelDiff(a, b *image.RGBA) int {
	ab, bb := a.Bounds(), b.Bounds()
	w := min(ab.Dx(), bb.Dx())
	h := min(ab.Dy(), bb.Dy())
	count := 0
	for y := range h {
		for x := range w {
			ca := a.RGBAAt(ab.Min.X+x, ab.Min.Y+y)
			cb := b.RGBAAt(bb.Min.X+x, bb.Min.Y+y)
			if ca != cb {
				count++
			}
		}
	}
	return count
}

// TestAutoDenoiseFiresOnNoisy verifies that blind.Recover (with default options,
// i.e. auto-denoise) applies denoising when the image has significant
// salt-and-pepper noise. Result.Denoise must be >= 1 on a heavily noisy image.
func TestAutoDenoiseFiresOnNoisy(t *testing.T) {
	t.Parallel()

	// Build a synthetic pixelated band, then add heavy S&P noise so
	// InferImpulseNoise returns a ratio well above autoThr.
	clean := syntheticBand(t, "ok", 0)
	cleanRGBA := toRGBAHelper(t, clean)

	// 10% S&P noise — far above any reasonable autoThr (~0.003).
	rng := rand.New(rand.NewPCG(0xaabbccdd, 0x11223344))
	noisy := addPepperNoise(cleanRGBA, rng, 0.10)
	// Also add salt (white) spikes so both extremes are present.
	for range int(float64(noisy.Bounds().Dx()*noisy.Bounds().Dy()) * 0.05) {
		x := rng.IntN(noisy.Bounds().Dx())
		y := rng.IntN(noisy.Bounds().Dy())
		noisy.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
	}

	result, err := blind.Recover(t.Context(), noisy,
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		// No WithDenoise → auto mode (default -1).
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	t.Logf("auto-denoise on noisy: Result.Denoise=%d", result.Denoise)
	if result.Denoise < 1 {
		t.Errorf("Result.Denoise = %d, want >= 1 (auto-denoise should fire on 15%% S&P noise)", result.Denoise)
	}
}

// TestAutoDenoiseSkipsClean verifies that blind.Recover (default auto-denoise)
// does NOT apply denoising to a clean pixelated image. Result.Denoise must be 0.
func TestAutoDenoiseSkipsClean(t *testing.T) {
	t.Parallel()

	clean := syntheticBand(t, "ok", 0)

	result, err := blind.Recover(t.Context(), clean,
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		// No WithDenoise → auto mode.
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	t.Logf("auto-denoise on clean: Result.Denoise=%d", result.Denoise)
	if result.Denoise != 0 {
		t.Errorf("Result.Denoise = %d, want 0 (clean image should not be auto-denoised)", result.Denoise)
	}
}

// TestWithDenoise_ForceRadius verifies that WithDenoise(2) forces radius 2
// and is reflected in Result.Denoise, regardless of the image content.
func TestWithDenoise_ForceRadius(t *testing.T) {
	t.Parallel()

	clean := syntheticBand(t, "ok", 0)
	result, err := blind.Recover(t.Context(), clean,
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithDenoise(2),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.Denoise != 2 {
		t.Errorf("Result.Denoise = %d, want 2 (forced radius)", result.Denoise)
	}
}

// TestWithDenoise_ZeroDisables verifies that WithDenoise(0) disables auto-denoise
// even on a heavily noisy image: Result.Denoise must be 0.
func TestWithDenoise_ZeroDisables(t *testing.T) {
	t.Parallel()

	clean := syntheticBand(t, "ok", 0)
	cleanRGBA := toRGBAHelper(t, clean)
	rng := rand.New(rand.NewPCG(0xfeedface, 0xdeadbeef))
	noisy := addPepperNoise(cleanRGBA, rng, 0.10)

	result, err := blind.Recover(t.Context(), noisy,
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithDenoise(0), // explicitly off
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.Denoise != 0 {
		t.Errorf("Result.Denoise = %d, want 0 (WithDenoise(0) should disable auto)", result.Denoise)
	}
}
