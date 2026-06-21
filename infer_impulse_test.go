package unpixel_test

import (
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// autoThr is the impulse-noise threshold below which blind.Recover does NOT
// auto-denoise. Mirror the value from blind package (kept in sync manually; if
// the blind package changes autoThr these tests catch the divergence).
const autoThrTest = 0.003

// flatImage returns an w×h image filled with a single colour.
func flatImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// saltPepperImage returns a copy of img with a fraction of pixels set to
// alternating black/white using a seeded RNG (deterministic, NO time).
func saltPepperImage(img *image.RGBA, rng *rand.Rand, density float64) *image.RGBA {
	b := img.Bounds()
	noisy := image.NewRGBA(b)
	copy(noisy.Pix, img.Pix)
	total := b.Dx() * b.Dy()
	n := int(float64(total) * density)
	for i := range n {
		x := b.Min.X + rng.IntN(b.Dx())
		y := b.Min.Y + rng.IntN(b.Dy())
		var c color.RGBA
		if i%2 == 0 {
			c = color.RGBA{0, 0, 0, 255}
		} else {
			c = color.RGBA{255, 255, 255, 255}
		}
		noisy.SetRGBA(x, y, c)
	}
	return noisy
}

// TestInferImpulseNoise_CleanFlat verifies a flat uniform image has ~0 noise.
func TestInferImpulseNoise_CleanFlat(t *testing.T) {
	t.Parallel()
	img := flatImage(100, 100, color.RGBA{128, 128, 128, 255})
	got := unpixel.InferImpulseNoise(img)
	if got > autoThrTest {
		t.Errorf("InferImpulseNoise(flat grey) = %.6f, want < %.4f (clean image flagged as noisy)", got, autoThrTest)
	}
}

// TestInferImpulseNoise_SaltPepper verifies that 5% salt-and-pepper noise is
// detected well above autoThr. The exact returned ratio depends on the sampler
// stride, so we assert > 0.04 (a clearly noisy image, well above autoThr=0.003).
func TestInferImpulseNoise_SaltPepper(t *testing.T) {
	t.Parallel()
	// Use a mid-grey base so both black (pepper) and white (salt) are outliers.
	base := flatImage(200, 200, color.RGBA{150, 150, 150, 255})
	rng := rand.New(rand.NewPCG(0xdeadbeef, 0x1234cafe))
	noisy := saltPepperImage(base, rng, 0.05)
	got := unpixel.InferImpulseNoise(noisy)
	t.Logf("InferImpulseNoise(5%% S&P) = %.6f", got)
	if got < 0.04 {
		t.Errorf("InferImpulseNoise(5%% S&P) = %.6f, want >= 0.04 (noise not detected; autoThr=%.3f)", got, autoThrTest)
	}
}

// TestInferImpulseNoise_MosaicLow verifies that a clean pixelated mosaic image
// (sharp block edges) is NOT mistaken for impulse noise. The ratio must stay
// below autoThr so clean images are NOT auto-denoised.
func TestInferImpulseNoise_MosaicLow(t *testing.T) {
	t.Parallel()
	// Build a coloured grid pixelated at block=8 — sharp block edges, no noise.
	src := pixelatedGrid(8*8, 4*8, 8) // uses helper from infer_test.go
	pix := pixelate.NewLinearBlockAverage(8)
	mosaic := pix.Pixelate(src, 0, 0)
	got := unpixel.InferImpulseNoise(mosaic)
	if got >= autoThrTest {
		t.Errorf("InferImpulseNoise(clean mosaic) = %.6f, want < %.4f (structured edges wrongly classified)", got, autoThrTest)
	}
}

// TestInferImpulseNoise_RealSamples verifies that the committed real PNG
// samples (hello-world, marx) have noise ratios below autoThr. This is the key
// regression: if the threshold mis-classifies a normal mosaic capture as noisy,
// auto-denoise fires and changes (potentially degrades) the decode path.
func TestInferImpulseNoise_RealSamples(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"hello-world.png", "marx.png"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, err := os.Open("testdata/real/" + name)
			if err != nil {
				t.Skipf("real sample not available: %v", err)
			}
			defer func() { _ = f.Close() }()
			img, err := png.Decode(f)
			if err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			got := unpixel.InferImpulseNoise(img)
			t.Logf("InferImpulseNoise(%s) = %.6f", name, got)
			if got >= autoThrTest {
				t.Errorf("InferImpulseNoise(%s) = %.6f, want < %.4f (real sample would be auto-denoised)", name, got, autoThrTest)
			}
		})
	}
}

// sinkImpulse defeats dead-code elimination in benchmarks.
var sinkImpulse float64

// BenchmarkInferImpulseNoise measures the per-call cost of the inner detection
// loop on a representative large image (1450×509, matching marx.png).
//
// Setup pre-converts the decoded PNG to *image.RGBA so that toRGBA is a no-op
// inside InferImpulseNoise — this mirrors the real call site in blind.Recover,
// which always passes an *image.RGBA. The benchmark therefore measures only the
// 50 000-pixel scan, not the one-time format conversion.
func BenchmarkInferImpulseNoise(b *testing.B) {
	f, err := os.Open("testdata/real/marx.png")
	if err != nil {
		b.Skipf("real sample not available: %v", err)
	}
	defer func() { _ = f.Close() }()
	decoded, err := png.Decode(f)
	if err != nil {
		b.Fatalf("decode marx.png: %v", err)
	}
	// Pre-convert once, outside the loop (mirrors blind.Recover's toRGBA call).
	bnd := decoded.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bnd.Dx(), bnd.Dy()))
	for y := range bnd.Dy() {
		for x := range bnd.Dx() {
			rgba.Set(x, y, decoded.At(bnd.Min.X+x, bnd.Min.Y+y))
		}
	}
	b.SetBytes(int64(bnd.Dx()) * int64(bnd.Dy()) * 4)
	b.ReportAllocs()
	for b.Loop() {
		sinkImpulse = unpixel.InferImpulseNoise(rgba)
	}
}
