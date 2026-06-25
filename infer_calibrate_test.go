package unpixel_test

// Tests for the typographic calibration estimators:
//   - InferGridPhase: estimate (x,y) phase of the block grid for a known block size.
//   - InferXStretch: estimate anisotropic horizontal stretch from mosaic content geometry.

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// sinkPhase and sinkStretch defeat dead-code elimination in benchmarks.
var (
	sinkPhase   image.Point
	sinkStretch float64
)

// buildPhaseShiftedMosaic creates a w×h mosaic image where block-grid boundaries
// fall at x ≡ phaseX (mod size) and y ≡ phaseY (mod size). Each cell gets a
// distinct colour so every boundary is detectable.
func buildPhaseShiftedMosaic(w, h, size, phaseX, phaseY int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			cx := (x - phaseX + size*1000) / size
			cy := (y - phaseY + size*1000) / size
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(50 + 40*(cx%6)),
				G: uint8(50 + 40*(cy%6)),
				B: uint8(100 + 30*((cx+cy)%5)),
				A: 255,
			})
		}
	}
	return img
}

// TestInferGridPhase_zeroPhase checks that a grid with no offset returns (0,0).
func TestInferGridPhase_zeroPhase(t *testing.T) {
	const size = 8
	img := buildPhaseShiftedMosaic(8*size, 4*size, size, 0, 0)
	got, ok := unpixel.InferGridPhase(img, size)
	if !ok {
		t.Fatal("InferGridPhase returned ok=false on a clean zero-phase mosaic")
	}
	if got.X != 0 || got.Y != 0 {
		t.Errorf("InferGridPhase zero-phase = (%d,%d), want (0,0)", got.X, got.Y)
	}
}

// TestInferGridPhase_nonZeroPhase checks that a phase-shifted grid is detected correctly.
func TestInferGridPhase_nonZeroPhase(t *testing.T) {
	cases := []struct {
		size, phaseX, phaseY int
	}{
		{8, 3, 5},
		{16, 7, 2},
		{4, 1, 3},
	}
	for _, c := range cases {
		img := buildPhaseShiftedMosaic(8*c.size, 4*c.size, c.size, c.phaseX, c.phaseY)
		got, ok := unpixel.InferGridPhase(img, c.size)
		if !ok {
			t.Errorf("size=%d phase=(%d,%d): InferGridPhase returned ok=false", c.size, c.phaseX, c.phaseY)
			continue
		}
		if got.X != c.phaseX || got.Y != c.phaseY {
			t.Errorf("size=%d phase=(%d,%d): InferGridPhase = (%d,%d), want (%d,%d)",
				c.size, c.phaseX, c.phaseY, got.X, got.Y, c.phaseX, c.phaseY)
		}
	}
}

// TestInferGridPhase_uniformReturnsFalse confirms ok=false on a uniform image.
func TestInferGridPhase_uniformReturnsFalse(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	_, ok := unpixel.InferGridPhase(img, 8)
	if ok {
		t.Error("InferGridPhase returned ok=true on uniform image, want false")
	}
}

// TestInferGridPhase_tinyReturnsFalse confirms ok=false on sub-2×2 images.
func TestInferGridPhase_tinyReturnsFalse(t *testing.T) {
	for _, sz := range []int{0, 1} {
		img := image.NewRGBA(image.Rect(0, 0, sz, sz))
		_, ok := unpixel.InferGridPhase(img, 8)
		if ok {
			t.Errorf("InferGridPhase(%dx%d) returned ok=true, want false", sz, sz)
		}
	}
}

// TestInferGridPhase_agreesWithInferBlockGrid verifies that InferGridPhase
// returns the same phase as InferBlockGrid for the same mosaic image.
func TestInferGridPhase_agreesWithInferBlockGrid(t *testing.T) {
	const size, phaseX, phaseY = 8, 5, 3
	img := buildPhaseShiftedMosaic(8*size, 4*size, size, phaseX, phaseY)

	grid, ok := unpixel.InferBlockGrid(img)
	if !ok {
		t.Fatal("InferBlockGrid returned ok=false")
	}

	phase, ok := unpixel.InferGridPhase(img, size)
	if !ok {
		t.Fatal("InferGridPhase returned ok=false")
	}

	if phase.X != grid.PhaseX || phase.Y != grid.PhaseY {
		t.Errorf("InferGridPhase=(%d,%d) disagrees with InferBlockGrid=(%d,%d)",
			phase.X, phase.Y, grid.PhaseX, grid.PhaseY)
	}
}

// TestInferXStretch_noStretch verifies that a mosaic rendered at stretch=1.0
// yields an estimate near 1.0.
//
// The rendered image is cropped to [0, sentinelX) before pixelating so the
// blue sentinel block is excluded from both the mosaic content and the
// reference width, keeping the comparison apples-to-apples.
func TestInferXStretch_noStretch(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	const (
		text     = "Hello"
		fontSize = 32.0
		block    = 8
	)
	style := unpixel.Style{FontSize: fontSize, PaddingTop: 8, PaddingLeft: 8}
	rendered, sentinelX, err := r.Render(text, style)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Crop out the sentinel so the mosaic and refWidth both cover text+padding only.
	textOnly := cropRGBA(rendered, sentinelX)
	pix := pixelate.NewBlockAverage(block)
	mosaic := pix.Pixelate(textOnly, 0, 0)

	stretch, ok := unpixel.InferXStretch(mosaic, block, sentinelX)
	if !ok {
		t.Fatal("InferXStretch returned ok=false on a clean mosaic")
	}
	// Expect near 1.0 (±10%).
	const tol = 0.10
	if stretch < 1.0-tol || stretch > 1.0+tol {
		t.Errorf("InferXStretch (no stretch) = %.3f, want 1.0 ± %.2f", stretch, tol)
	}
}

// TestInferXStretch_withStretch verifies that a mosaic whose content is
// synthetically stretched ~6% is estimated within tolerance.
//
// The sentinel is excluded from the mosaic (cropped before stretch+pixelate)
// so refWidthPx = sentinelX (the un-stretched text advance) is the right
// reference; the estimator then returns observedWidth/refWidthPx ≈ 1.06.
func TestInferXStretch_withStretch(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	const (
		text     = "Hello"
		fontSize = 32.0
		block    = 8
	)
	style := unpixel.Style{FontSize: fontSize, PaddingTop: 8, PaddingLeft: 8}
	rendered, sentinelX, err := r.Render(text, style)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Crop to text+padding only before stretching.
	textOnly := cropRGBA(rendered, sentinelX)

	// Simulate 6% horizontal stretch: scale the cropped image width by 1.06.
	const wantStretch = 1.06
	origW := textOnly.Bounds().Dx()
	origH := textOnly.Bounds().Dy()
	stretchedW := int(float64(origW) * wantStretch)
	stretched := image.NewRGBA(image.Rect(0, 0, stretchedW, origH))
	for y := range origH {
		for x := range stretchedW {
			srcX := int(float64(x) / wantStretch)
			if srcX >= origW {
				srcX = origW - 1
			}
			stretched.SetRGBA(x, y, textOnly.RGBAAt(srcX, y))
		}
	}

	pix := pixelate.NewBlockAverage(block)
	mosaic := pix.Pixelate(stretched, 0, 0)

	stretch, ok := unpixel.InferXStretch(mosaic, block, sentinelX)
	if !ok {
		t.Fatal("InferXStretch returned ok=false on stretched mosaic")
	}
	// Expect near wantStretch (±15%): block quantisation introduces ±block/sentinelX
	// edge uncertainty (~6% for block=8, sentinelX≈130).
	const tol = 0.15
	if stretch < wantStretch-tol || stretch > wantStretch+tol {
		t.Errorf("InferXStretch (stretch=%.2f) = %.3f, want %.2f ± %.2f",
			wantStretch, stretch, wantStretch, tol)
	}
}

// TestInferXStretch_invalidInputReturnsFalse verifies degenerate inputs.
func TestInferXStretch_invalidInputReturnsFalse(t *testing.T) {
	img := pixelatedGrid(64, 32, 8)

	// Zero refWidthPx → undeterminable.
	if _, ok := unpixel.InferXStretch(img, 8, 0); ok {
		t.Error("InferXStretch(refWidth=0) returned ok=true, want false")
	}

	// Tiny image → undeterminable.
	tiny := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if _, ok := unpixel.InferXStretch(tiny, 8, 100); ok {
		t.Error("InferXStretch(1x1 image) returned ok=true, want false")
	}
}

// BenchmarkInferGridPhase measures the cost of phase detection on a typical mosaic.
func BenchmarkInferGridPhase(b *testing.B) {
	img := buildPhaseShiftedMosaic(160, 80, 8, 3, 5)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p, _ := unpixel.InferGridPhase(img, 8)
		sinkPhase = p
	}
}

// BenchmarkInferXStretch measures the cost of stretch estimation on a typical mosaic.
func BenchmarkInferXStretch(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("NewXImage: %v", err)
	}
	rendered, sentinelX, err := r.Render("Hello World", unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8})
	if err != nil {
		b.Fatalf("Render: %v", err)
	}
	textOnly := cropRGBA(rendered, sentinelX)
	pix := pixelate.NewBlockAverage(8)
	mosaic := pix.Pixelate(textOnly, 0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s, _ := unpixel.InferXStretch(mosaic, 8, sentinelX)
		sinkStretch = s
	}
}
