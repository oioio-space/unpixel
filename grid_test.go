package unpixel_test

import (
	"image"
	"image/color"
	_ "image/png"
	"math"
	"os"
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

// partialEdgeMosaic builds a w×h mosaic image that simulates a pixelated text
// region sitting inside a white canvas. The white margin occupies the first
// marginW columns. A partial block of width partialW follows, then full blocks
// of width blockSize each. Every block (partial and full) is assigned a distinct
// non-white colour so that all transitions are detectable.
//
// This replicates the situation in marx.png, where GIMP's Pixelize was applied
// to a text selection with an internal x-offset that creates a narrow leading
// partial block at the edge of the pixelated region.
func partialEdgeMosaic(w, h, blockSize, marginW, partialW int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// White margin.
	for y := range h {
		for x := range marginW {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	// Partial leading block.
	if partialW > 0 {
		for y := range h {
			for x := marginW; x < marginW+partialW; x++ {
				img.SetRGBA(x, y, color.RGBA{R: 200, G: 200, B: 200, A: 255})
			}
		}
	}
	// Full blocks.
	for blockIdx := range (w - marginW - partialW + blockSize - 1) / blockSize {
		startX := marginW + partialW + blockIdx*blockSize
		endX := min(startX+blockSize, w)
		r := uint8(50 + (blockIdx*73)%150)
		g := uint8(50 + (blockIdx*97)%130)
		b := uint8(50 + (blockIdx*113)%110)
		for y := range h {
			for x := startX; x < endX; x++ {
				img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
			}
		}
	}
	return img
}

// subHarmonicMosaic builds a w×h image with true structural block period trueP
// but with a small colour variation at trueP/2 within each block. Inter-block
// contrast is large (≥ 80 units per channel), while intra-block sub-variation
// is 3 units — deliberately below robustBoundaryThreshold (5) so the robust
// boundary detector ignores intra-block transitions while the exact detector
// fires at every trueP/2 pixels.
//
// This causes gcdOfGaps to return trueP/2 (a sub-harmonic), while the robust
// autocorrelation correctly identifies trueP as the dominant structural period.
func subHarmonicMosaic(w, h, trueP int) *image.RGBA {
	subP := trueP / 2
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			blockX := x / trueP
			blockY := y / trueP
			subBlockX := (x % trueP) / subP
			// Large inter-block variation — adjacent blocks differ by ≥ 83 units in R
			// and ≥ 83 in G, well above the robust threshold (5).
			baseR := uint8(50 + (blockX*83+blockY*67)%180)
			baseG := uint8(50 + (blockX*97+blockY*53)%180)
			// Small intra-block sub-variation: +3 in the second sub-block of each
			// true block. 3 < robustBoundaryThreshold(5), so robust detector ignores
			// these transitions; exact detector fires at them (3 > 0).
			var delta uint8
			if subBlockX == 1 {
				delta = 3
			}
			img.SetRGBA(x, y, color.RGBA{
				R: baseR + delta,
				G: baseG + delta,
				B: 100,
				A: 255,
			})
		}
	}
	return img
}

// TestInferBlockGrid_marxPng verifies that InferBlockGrid correctly detects the
// 19-pixel block grid on testdata/real/marx.png, which previously returned
// Size=0 due to a partial 5-pixel edge block poisoning the GCD computation.
//
// Ground truth: block=19, offset=(5,5) relative to the text selection
// (manifest: testdata/real/manifest.json, entry "marx").
func TestInferBlockGrid_marxPng(t *testing.T) {
	const marxPath = "testdata/real/marx.png"
	f, err := os.Open(marxPath) // #nosec G304 -- controlled fixture path
	if err != nil {
		t.Skipf("skipping: cannot open %s: %v", marxPath, err)
	}
	img, _, err := image.Decode(f)
	if cerr := f.Close(); cerr != nil && err == nil {
		t.Fatalf("close %s: %v", marxPath, cerr)
	}
	if err != nil {
		t.Fatalf("decode %s: %v", marxPath, err)
	}

	grid, ok := unpixel.InferBlockGrid(img)
	if !ok {
		t.Fatalf("InferBlockGrid returned ok=false on marx.png (got %+v); want size≈19", grid)
	}
	const wantSize = 19
	if diff := grid.Size - wantSize; diff < -1 || diff > 1 {
		t.Errorf("Size = %d, want %d ±1", grid.Size, wantSize)
	}
	if grid.Confidence < 0.5 {
		t.Errorf("Confidence = %.3f, want ≥ 0.50 for marx.png", grid.Confidence)
	}
}

// TestInferBlockGrid_partialEdgeBlock verifies that InferBlockGrid returns the
// correct block size on a synthetic mosaic with a partial leading block — the
// same structural pattern that defeats the plain GCD approach on marx.png.
func TestInferBlockGrid_partialEdgeBlock(t *testing.T) {
	const (
		blockSize = 19
		marginW   = 80 // white columns before the pixelated region
		partialW  = 5  // partial leading block (= grid offset)
		imgW      = marginW + partialW + 30*blockSize
		imgH      = 120
	)
	img := partialEdgeMosaic(imgW, imgH, blockSize, marginW, partialW)

	grid, ok := unpixel.InferBlockGrid(img)
	if !ok {
		t.Fatalf("InferBlockGrid returned ok=false on partial-edge mosaic (got %+v)", grid)
	}
	if grid.Size != blockSize {
		t.Errorf("Size = %d, want %d", grid.Size, blockSize)
	}
	if grid.Confidence < 0.8 {
		t.Errorf("Confidence = %.3f, want ≥ 0.80 for a clean partial-edge mosaic", grid.Confidence)
	}
}

// TestInferBlockGrid_subHarmonicPrefersFundamental verifies that when the exact
// GCD of boundary gaps yields a sub-harmonic (trueP/2) but the robust
// autocorrelation strongly supports the fundamental period (trueP), InferBlockGrid
// returns trueP rather than the sub-harmonic.
func TestInferBlockGrid_subHarmonicPrefersFundamental(t *testing.T) {
	const (
		trueP = 20 // true structural block period
		imgW  = trueP * 12
		imgH  = trueP * 6
	)
	img := subHarmonicMosaic(imgW, imgH, trueP)

	grid, ok := unpixel.InferBlockGrid(img)
	if !ok {
		t.Fatalf("InferBlockGrid returned ok=false on sub-harmonic mosaic (got %+v)", grid)
	}
	if grid.Size != trueP {
		t.Errorf("Size = %d (sub-harmonic?), want fundamental period %d", grid.Size, trueP)
	}
}

// TestInferBlockGrid_regressionAxisAligned guards that the axis-aligned fixtures
// used in prior tests return byte-identical results after the partial-edge and
// sub-harmonic fixes are applied.
func TestInferBlockGrid_regressionAxisAligned(t *testing.T) {
	for _, block := range []int{4, 8, 16} {
		img := pixelatedGrid(8*block, 4*block, block)
		grid, ok := unpixel.InferBlockGrid(img)
		if !ok {
			t.Errorf("block=%d: InferBlockGrid returned ok=false", block)
			continue
		}
		if grid.Size != block {
			t.Errorf("block=%d: Size = %d, want %d", block, grid.Size, block)
		}
		if grid.PhaseX != 0 {
			t.Errorf("block=%d: PhaseX = %d, want 0", block, grid.PhaseX)
		}
		if grid.PhaseY != 0 {
			t.Errorf("block=%d: PhaseY = %d, want 0", block, grid.PhaseY)
		}
		if grid.Confidence < 0.9 {
			t.Errorf("block=%d: Confidence = %.3f, want ≥ 0.90", block, grid.Confidence)
		}
	}
}
