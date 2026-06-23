package unpixel_test

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"github.com/oioio-space/unpixel"
)

// realBlurSample is a real-world redaction of "Hello World !" (1450×509, with
// large flat margins), a hand-contributed sample committed under testdata/fixtures
// (exempt from the manifest cross-check; see handContributedFixtures). It
// exercises the locate/infer preprocessing path on genuine input
// rather than synthetic data. The full forward-model decode of this sample lives
// in real_mosaic_test.go; this test only pins the region-location and inference
// helpers behave on a real image with wide margins.
const realBlurSample = "testdata/real/hello-world.png"

// loadRealSample decodes the committed real-world blur sample.
func loadRealSample(tb testing.TB) image.Image {
	tb.Helper()
	f, err := os.Open(realBlurSample)
	if err != nil {
		tb.Fatalf("open %s: %v", realBlurSample, err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		tb.Fatalf("decode %s: %v", realBlurSample, err)
	}
	return img
}

// subImage copies rect out of img into a fresh RGBA at the origin.
func subImage(img image.Image, rect image.Rectangle) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(out, out.Bounds(), img, rect.Min, draw.Src)
	return out
}

// TestRealBlur_locateAndInfer verifies the crop-first preprocessing design on a
// real blurred redaction: LocateRedaction finds the blurred text band, and
// InferBlurSigma returns a positive (non-zero) estimate on the crop — confirming
// that the estimator detects real blur on a genuine input rather than returning
// zero (which would suppress the σ-sweep entirely). The density-adaptive
// percentile formula gives a consistent positive estimate for both the crop and
// the whole image on this mildly-blurred sample; we pin both as positive and
// check that the crop estimate lies within the plausible range for mild blur.
// This pins the behaviour the synthetic locate tests only approximate.
func TestRealBlur_locateAndInfer(t *testing.T) {
	img := loadRealSample(t)

	rect, ok := unpixel.LocateRedaction(img)
	if !ok {
		t.Fatal("LocateRedaction: ok=false, want the blurred text band")
	}
	if rect.Dx() < 200 || rect.Dy() < 20 {
		t.Errorf("located band %v too small for the text", rect)
	}

	whole := unpixel.InferBlurSigma(img)
	crop := unpixel.InferBlurSigma(subImage(img, rect))
	t.Logf("whole sigma=%.2f crop sigma=%.2f", whole, crop)
	// Both estimates must be positive: a zero would suppress the σ-sweep entirely.
	if whole <= 0 {
		t.Errorf("whole-image sigma = %.2f, want > 0", whole)
	}
	if crop <= 0 {
		t.Errorf("crop sigma = %.2f, want > 0", crop)
	}
	// Crop estimate must be plausible for mild Gaussian blur (0 < σ ≤ 4).
	if crop > 4.0 {
		t.Errorf("crop sigma = %.2f, want ≤ 4.0 (mild blur)", crop)
	}
	if got := unpixel.InferFontSize(subImage(img, rect)); got <= 0 {
		t.Errorf("InferFontSize on the band = %.1f, want > 0", got)
	}
}

// BenchmarkRealBlur_locateAndInfer measures the real-image preprocessing path
// (locate the blurred band, then estimate its blur sigma) on the committed
// sample, so its cost is tracked over time on genuine input. Full recovery of
// this image is out of scope for the suite — its large (~104pt) glyphs and
// 11-character plaintext make a brute-force search impractically slow.
func BenchmarkRealBlur_locateAndInfer(b *testing.B) {
	img := loadRealSample(b)
	b.ReportAllocs()
	for b.Loop() {
		rect, ok := unpixel.LocateRedaction(img)
		if !ok {
			b.Fatal("LocateRedaction: ok=false")
		}
		_ = unpixel.InferBlurSigma(subImage(img, rect))
	}
}
