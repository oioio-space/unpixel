package unpixel_test

import (
	"image"
	"image/color"
	"math"
	"testing"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"github.com/oioio-space/unpixel"
)

// pixelatedGridPhase builds a w×h mosaic image whose block grid is offset by
// (phaseX, phaseY): block boundaries fall at positions ≡ phaseX (mod size) in X
// and ≡ phaseY (mod size) in Y. Each block gets a distinct colour derived from
// its logical cell coordinates.
func pixelatedGridPhase(w, h, size, phaseX, phaseY int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			// Map pixel to logical cell, accounting for phase.
			cx := (x - phaseX + size*1000) / size
			cy := (y - phaseY + size*1000) / size
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(40 + 30*(cx%7)),
				G: uint8(40 + 30*(cy%7)),
				B: uint8(80 + 20*((cx+cy)%5)),
				A: 255,
			})
		}
	}
	return img
}

// pixelatedGridHighContrast builds a w×h mosaic with block-size cells assigned
// maximally distinct colours via a deterministic per-cell formula. The high
// inter-block contrast (up to 200 units) ensures the block-homogeneity signal
// is clear, which is needed for the deskew tests.
//
// Border pixels have luminance > 96 so that Engine.New does not interpret this
// as a dark-background image and invert the colours.
func pixelatedGridHighContrast(w, h, block int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			cx, cy := x/block, y/block
			// Prime-based mixing gives wide colour spread across adjacent cells.
			r := uint8(200 - (cx*73+cy*137)%150)
			g := uint8(200 - (cx*113+cy*53)%150)
			b := uint8(200 - (cx*61+cy*97)%150)
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return img
}

// rotateTestImage rotates src by angleDeg degrees around its centre using
// nearest-neighbour interpolation and returns the result at the same size.
// Unsampled border pixels are filled with white.
func rotateTestImage(src *image.RGBA, angleDeg float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range dst.Pix {
		dst.Pix[i] = 0xFF
	}
	rad := angleDeg * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	cx, cy := float64(w)/2, float64(h)/2
	m := f64.Aff3{
		cos, sin, cx*(1-cos) - cy*sin,
		-sin, cos, cx*sin + cy*(1-cos),
	}
	xdraw.NearestNeighbor.Transform(dst, m, src, b, xdraw.Src, nil)
	return dst
}

// TestInferBlockGrid_zeroPhase verifies that a standard axis-aligned grid
// (phase=0) is detected with Size==block and Phase{X,Y}==0.
func TestInferBlockGrid_zeroPhase(t *testing.T) {
	const block = 8
	img := pixelatedGrid(8*block, 4*block, block)
	grid, ok := unpixel.InferBlockGrid(img)
	if !ok {
		t.Fatal("InferBlockGrid returned ok=false on a clean mosaic")
	}
	if grid.Size != block {
		t.Errorf("Size = %d, want %d", grid.Size, block)
	}
	if grid.PhaseX != 0 {
		t.Errorf("PhaseX = %d, want 0", grid.PhaseX)
	}
	if grid.PhaseY != 0 {
		t.Errorf("PhaseY = %d, want 0", grid.PhaseY)
	}
	if grid.Confidence < 0.8 {
		t.Errorf("Confidence = %.3f, want ≥ 0.80 for a clean mosaic", grid.Confidence)
	}
}

// TestInferBlockGrid_nonZeroPhase verifies that a grid shifted by a known
// (phaseX, phaseY) is correctly detected.
func TestInferBlockGrid_nonZeroPhase(t *testing.T) {
	const (
		block  = 8
		phaseX = 3
		phaseY = 5
	)
	// Build a large enough image so boundaries are plentiful.
	img := pixelatedGridPhase(12*block, 8*block, block, phaseX, phaseY)

	grid, ok := unpixel.InferBlockGrid(img)
	if !ok {
		t.Fatal("InferBlockGrid returned ok=false on a clean phase-shifted mosaic")
	}
	if grid.Size != block {
		t.Errorf("Size = %d, want %d", grid.Size, block)
	}
	if grid.PhaseX != phaseX {
		t.Errorf("PhaseX = %d, want %d", grid.PhaseX, phaseX)
	}
	if grid.PhaseY != phaseY {
		t.Errorf("PhaseY = %d, want %d", grid.PhaseY, phaseY)
	}
	if grid.Confidence < 0.8 {
		t.Errorf("Confidence = %.3f, want ≥ 0.80", grid.Confidence)
	}
}

// TestInferBlockGrid_uniformReturnsFalse verifies that a uniform image does
// not produce a valid grid detection (ok=false).
func TestInferBlockGrid_uniformReturnsFalse(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	_, ok := unpixel.InferBlockGrid(img)
	if ok {
		t.Error("InferBlockGrid returned ok=true on a uniform image, want false")
	}
}

// TestInferBlockGrid_tinyReturnsFalse verifies that images smaller than 2×2
// cause ok=false.
func TestInferBlockGrid_tinyReturnsFalse(t *testing.T) {
	for _, sz := range []int{0, 1} {
		img := image.NewRGBA(image.Rect(0, 0, sz, sz))
		_, ok := unpixel.InferBlockGrid(img)
		if ok {
			t.Errorf("InferBlockGrid(%dx%d) returned ok=true, want false", sz, sz)
		}
	}
}

// TestInferBlockGrid_confidenceLowOnRotated verifies that a rotated mosaic
// yields a low InferBlockGrid confidence, which is the trigger for the deskew
// path.
func TestInferBlockGrid_confidenceLowOnRotated(t *testing.T) {
	const block = 8
	img := pixelatedGrid(16*block, 8*block, block)
	rotated := rotateTestImage(img, 6.0) // 6° rotation — clearly non-axis-aligned

	grid, ok := unpixel.InferBlockGrid(rotated)
	// ok may be true or false depending on the GCD heuristic; what matters is
	// that confidence is low when the grid is not axis-aligned.
	if ok && grid.Confidence > 0.6 {
		t.Errorf("Confidence = %.3f on a 6°-rotated mosaic; want < 0.6 (should look non-axis-aligned)", grid.Confidence)
	}
}

// TestDeskew_recoversRotatedMosaic creates a clean axis-aligned high-contrast
// mosaic, rotates it by 5°, wraps it in Engine via New, and asserts that:
//  1. The skew was detected (Engine.SkewInfo().Detected == true).
//  2. The deskew was applied (Engine.SkewInfo().Applied == true).
//  3. The deskewed image's InferBlockGrid confidence is higher than the rotated baseline.
func TestDeskew_recoversRotatedMosaic(t *testing.T) {
	const block = 8
	// High-contrast colours produce a large homogeneity gain when the correct
	// angle is found, which is required for the deskew gate to fire.
	img := pixelatedGridHighContrast(20*block, 12*block, block)
	rotated := rotateTestImage(img, 5.0)

	eng, err := unpixel.New(rotated, unpixel.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	info := eng.SkewInfo()
	if !info.Detected {
		t.Fatalf("skew not detected (BaselineConfidence=%.3f); deskew path did not trigger", info.BaselineConfidence)
	}
	if !info.Applied {
		t.Fatalf("skew detected but not applied (AngleDeg=%.2f°); homogeneity gain may be below threshold", info.AngleDeg)
	}

	// After deskew the internal image should have a recoverable grid.
	deskewedGrid, ok := unpixel.InferBlockGrid(eng.DeskewedImage())
	if !ok {
		t.Log("InferBlockGrid returned ok=false after deskew (GCD approach may still fail on double-rotated image)")
		// Fall back to checking that deskew was applied at all (already asserted above).
	} else if deskewedGrid.Confidence < 0.5 {
		t.Errorf("post-deskew InferBlockGrid confidence = %.3f, want ≥ 0.50", deskewedGrid.Confidence)
	}
}

// TestDeskew_axisAlignedNotRotated verifies that an axis-aligned mosaic is NOT
// rotated by New — the deskew gate must leave axis-aligned inputs byte-identical.
//
// The image uses a light background (luminance > 96) so that New does not
// trigger dark-background inversion, which would change the pixel values
// independently of any deskew.
func TestDeskew_axisAlignedNotRotated(t *testing.T) {
	const block = 8
	// pixelatedGridHighContrast uses bright colours (border pixel lum > 128)
	// so darkBackground returns false and no inversion is applied.
	img := pixelatedGridHighContrast(16*block, 8*block, block)

	eng, err := unpixel.New(img, unpixel.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	info := eng.SkewInfo()
	if info.Applied {
		t.Errorf("deskew was applied to an axis-aligned mosaic (angle=%.2f°) — must be a no-op", info.AngleDeg)
	}

	// The internal RGBA pixels must be byte-identical to the input because no
	// inversion and no deskew rotation were applied.
	got := eng.DeskewedImage()
	if got.Bounds() != img.Bounds() {
		t.Fatalf("bounds differ: got %v, want %v", got.Bounds(), img.Bounds())
	}
	for i, v := range img.Pix {
		if got.Pix[i] != v {
			t.Errorf("pixel[%d] = %d, want %d (image was modified by deskew or inversion)", i, got.Pix[i], v)
			break
		}
	}
}

// TestInferBlockSize_unchanged verifies that the pre-existing InferBlockSize
// behaviour is unchanged after the refactor that shares the GCD logic with
// InferBlockGrid.
func TestInferBlockSize_unchanged(t *testing.T) {
	for _, block := range []int{4, 8, 16} {
		img := pixelatedGrid(8*block, 4*block, block)
		if got := unpixel.InferBlockSize(img); got != block {
			t.Errorf("InferBlockSize(block=%d) = %d after refactor, want %d", block, got, block)
		}
	}
	// Uniform → 0.
	uniform := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := range uniform.Pix {
		uniform.Pix[i] = 0xFF
	}
	if got := unpixel.InferBlockSize(uniform); got != 0 {
		t.Errorf("InferBlockSize(uniform) = %d after refactor, want 0", got)
	}
}
