package multiframe_test

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/multiframe"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// syntheticSource produces a w×h RGBA image with a simple high-frequency
// pattern (horizontal gradient bands) so that pixelation at different phases
// produces distinctly different block means.
func syntheticSource(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			// Distinct pattern: R varies by column, G by row, B constant.
			r := uint8((x * 255) / max(w-1, 1))
			g := uint8((y * 255) / max(h-1, 1))
			b := uint8(128)
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return img
}

// mse computes the mean squared error per channel between two same-size images.
func mse(a, b *image.RGBA) float64 {
	bounds := a.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	var sum float64
	for y := range h {
		for x := range w {
			ca := a.RGBAAt(x, y)
			cb := b.RGBAAt(x, y)
			dr := float64(ca.R) - float64(cb.R)
			dg := float64(ca.G) - float64(cb.G)
			db := float64(ca.B) - float64(cb.B)
			sum += dr*dr + dg*dg + db*db
		}
	}
	return sum / float64(w*h*3)
}

// TestFuseBeatsSingleFrame is the core proof: fusing F phase-diverse mosaics
// of a source image yields a lower MSE against the source than the best
// single-frame mosaic does.
func TestFuseBeatsSingleFrame(t *testing.T) {
	const (
		W     = 64
		H     = 64
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	// Build 4 frames at distinct sub-block phases (0,0), (2,0), (0,3), (3,3).
	phases := [][2]int{{0, 0}, {2, 0}, {0, 3}, {3, 3}}
	frames := make([]multiframe.Frame, len(phases))
	for i, ph := range phases {
		mosaic := pix.Pixelate(src, ph[0], ph[1])
		frames[i] = multiframe.Frame{
			Img:     mosaic,
			OffsetX: ph[0],
			OffsetY: ph[1],
		}
	}

	fused, err := multiframe.Fuse(frames, block)
	if err != nil {
		t.Fatalf("Fuse error: %v", err)
	}
	if fused == nil {
		t.Fatal("Fuse returned nil image")
	}

	// Check output bounds match the source.
	if got, want := fused.Bounds().Dx(), W; got != want {
		t.Errorf("fused width: got %d, want %d", got, want)
	}
	if got, want := fused.Bounds().Dy(), H; got != want {
		t.Errorf("fused height: got %d, want %d", got, want)
	}

	// Compute MSE of best single frame vs source.
	bestSingleMSE := math.MaxFloat64
	for _, f := range frames {
		rgba := toRGBA(f.Img, W, H)
		m := mse(src, rgba)
		if m < bestSingleMSE {
			bestSingleMSE = m
		}
	}

	fusedMSE := mse(src, fused)
	t.Logf("best single-frame MSE=%.4f  fused(F=4) MSE=%.4f", bestSingleMSE, fusedMSE)

	if fusedMSE >= bestSingleMSE {
		t.Errorf("fusion did not improve over single frame: fused MSE %.4f >= best single %.4f",
			fusedMSE, bestSingleMSE)
	}
}

// TestFuseImproveWithMoreFrames verifies that more phase-diverse frames
// monotonically improve reconstruction quality.
func TestFuseImproveWithMoreFrames(t *testing.T) {
	const (
		W     = 64
		H     = 64
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	// 8 distinct sub-block phases.
	allPhases := [][2]int{
		{0, 0},
		{1, 0},
		{2, 0},
		{3, 0},
		{0, 1},
		{1, 1},
		{2, 1},
		{3, 1},
	}
	allFrames := make([]multiframe.Frame, len(allPhases))
	for i, ph := range allPhases {
		mosaic := pix.Pixelate(src, ph[0], ph[1])
		allFrames[i] = multiframe.Frame{
			Img:     mosaic,
			OffsetX: ph[0],
			OffsetY: ph[1],
		}
	}

	counts := []int{1, 2, 4, 8}
	prevMSE := math.MaxFloat64
	for _, n := range counts {
		fused, err := multiframe.Fuse(allFrames[:n], block)
		if err != nil {
			t.Fatalf("Fuse(F=%d) error: %v", n, err)
		}
		m := mse(src, fused)
		t.Logf("F=%d  MSE=%.4f", n, m)
		if n > 1 && m >= prevMSE {
			t.Errorf("F=%d did not improve over F=%d: MSE %.4f >= %.4f", n, n/2, m, prevMSE)
		}
		prevMSE = m
	}
}

// TestFuseSingleFrameGraceful checks that Fuse of one frame returns content
// consistent with that frame (no invented detail, no error).
func TestFuseSingleFrameGraceful(t *testing.T) {
	const (
		W     = 32
		H     = 32
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)
	mosaic := pix.Pixelate(src, 0, 0)

	frames := []multiframe.Frame{{Img: mosaic, OffsetX: 0, OffsetY: 0}}
	fused, err := multiframe.Fuse(frames, block)
	if err != nil {
		t.Fatalf("Fuse single-frame error: %v", err)
	}
	if fused == nil {
		t.Fatal("Fuse returned nil image")
	}

	// The fused result should be block-constant (equal block means as the input).
	// Verify by re-pixelating the fused image and comparing to the original mosaic.
	refused := pix.Pixelate(fused, 0, 0)
	mosaicRGBA := toRGBA(mosaic, W, H)
	refusedRGBA := toRGBA(refused, W, H)

	m := mse(mosaicRGBA, refusedRGBA)
	t.Logf("single-frame consistency MSE=%.6f", m)
	// The re-pixelated fused should be very close to the original mosaic.
	if m > 1.0 {
		t.Errorf("single-frame fused re-pixelation MSE %.4f > 1.0: too much invented detail", m)
	}
}

// TestFuseDeterministic verifies that identical inputs produce identical outputs.
func TestFuseDeterministic(t *testing.T) {
	const (
		W     = 48
		H     = 48
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	phases := [][2]int{{0, 0}, {3, 0}, {0, 4}, {3, 4}}
	frames := make([]multiframe.Frame, len(phases))
	for i, ph := range phases {
		mosaic := pix.Pixelate(src, ph[0], ph[1])
		frames[i] = multiframe.Frame{Img: mosaic, OffsetX: ph[0], OffsetY: ph[1]}
	}

	fused1, err := multiframe.Fuse(frames, block)
	if err != nil {
		t.Fatalf("Fuse run 1 error: %v", err)
	}
	fused2, err := multiframe.Fuse(frames, block)
	if err != nil {
		t.Fatalf("Fuse run 2 error: %v", err)
	}

	m := mse(fused1, fused2)
	if m != 0 {
		t.Errorf("Fuse is non-deterministic: MSE between runs = %.6f", m)
	}
}

// TestFuseErrorCases checks input validation.
func TestFuseErrorCases(t *testing.T) {
	// Nil / empty frames slice.
	_, err := multiframe.Fuse(nil, 8)
	if err == nil {
		t.Error("Fuse(nil, 8): want error, got nil")
	}

	_, err = multiframe.Fuse([]multiframe.Frame{}, 8)
	if err == nil {
		t.Error("Fuse(empty, 8): want error, got nil")
	}

	// Block size < 1.
	src := syntheticSource(16, 16)
	pix := pixelate.NewBlockAverage(8)
	mosaic := pix.Pixelate(src, 0, 0)
	frames := []multiframe.Frame{{Img: mosaic, OffsetX: 0, OffsetY: 0}}

	_, err = multiframe.Fuse(frames, 0)
	if err == nil {
		t.Error("Fuse(frames, 0): want error, got nil")
	}

	// Nil image in frame.
	frames = []multiframe.Frame{{Img: nil, OffsetX: 0, OffsetY: 0}}
	_, err = multiframe.Fuse(frames, 8)
	if err == nil {
		t.Error("Fuse(nil img frame, 8): want error, got nil")
	}
}

// toRGBA converts an image.Image to *image.RGBA cropped/padded to w×h.
func toRGBA(img image.Image, w, h int) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		b := r.Bounds()
		if b.Dx() == w && b.Dy() == h {
			return r
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			dst.Set(x, y, img.At(x, y))
		}
	}
	return dst
}
