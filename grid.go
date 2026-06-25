package unpixel

import (
	"image"
	"slices"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// BlockGrid describes a detected axis-aligned mosaic lattice.
//
// A mosaic image has piecewise-constant blocks of size Size×Size pixels.
// The block boundaries fall at positions x ≡ PhaseX (mod Size) in X and
// y ≡ PhaseY (mod Size) in Y. Confidence measures how consistently the
// detected colour-change positions agree with this lattice.
type BlockGrid struct {
	// Size is the block side length in pixels, equal to InferBlockSize.
	Size int
	// PhaseX is the horizontal grid origin offset: block boundaries fall at
	// column positions x ≡ PhaseX (mod Size).
	PhaseX int
	// PhaseY is the vertical grid origin offset: block boundaries fall at
	// row positions y ≡ PhaseY (mod Size).
	PhaseY int
	// Confidence is the fraction of detected boundary positions whose residue
	// (mod Size) matches the modal residue, averaged across X and Y. It is in
	// [0, 1]: 1.0 for a perfectly axis-aligned mosaic; near 0 for a rotated or
	// irregular image.
	Confidence float64
}

// InferBlockGrid detects the mosaic lattice — block size and grid origin phase
// — of a pixelated image. It returns the detected BlockGrid and ok=true when a
// regular axis-aligned grid is found, or the zero BlockGrid and ok=false when
// the image is too small, uniform, or has no regular grid (same cases where
// InferBlockSize returns 0).
//
// The block size is derived from the GCD of the gaps between colour-change
// columns (and rows). PhaseX is the most common residue of those column
// positions modulo Size; PhaseY likewise for rows. Confidence is the fraction
// of boundary positions consistent with that modal residue, averaged over X
// and Y axes.
func InferBlockGrid(img image.Image) (BlockGrid, bool) {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return BlockGrid{}, false
	}

	colPositions := detectBoundaryPositions(rgba, b, w, h, true)
	rowPositions := detectBoundaryPositions(rgba, b, w, h, false)

	size := gcdOfGaps(colPositions, rowPositions)
	if size < 2 {
		return BlockGrid{}, false
	}

	phaseX, confX := modalResidue(colPositions, size)
	phaseY, confY := modalResidue(rowPositions, size)

	confidence := (confX + confY) / 2
	if len(colPositions) == 0 {
		confidence = confY
	}
	if len(rowPositions) == 0 {
		confidence = confX
	}

	return BlockGrid{
		Size:       size,
		PhaseX:     phaseX,
		PhaseY:     phaseY,
		Confidence: confidence,
	}, true
}

// detectBoundaryPositions collects the x-positions (columns=true) or
// y-positions (columns=false) where a colour change is detected in the image.
func detectBoundaryPositions(img *image.RGBA, b image.Rectangle, w, h int, columns bool) []int {
	var positions []int
	if columns {
		for x := 1; x < w; x++ {
			if columnDiffers(img, b, x, h) {
				positions = append(positions, x)
			}
		}
	} else {
		for y := 1; y < h; y++ {
			if rowDiffers(img, b, y, w) {
				positions = append(positions, y)
			}
		}
	}
	return positions
}

// gcdOfGaps returns the GCD of the gaps between consecutive positions in
// colPos and rowPos combined. It returns 0 when no gaps exist (fewer than
// two positions total across both slices), and 1 when the GCD reduces to 1
// (no regular spacing). Values < 2 signal an undetectable grid.
func gcdOfGaps(colPositions, rowPositions []int) int {
	g := 0
	prev := -1
	for _, x := range colPositions {
		if prev >= 0 {
			g = gcd(g, x-prev)
		}
		prev = x
	}
	prev = -1
	for _, y := range rowPositions {
		if prev >= 0 {
			g = gcd(g, y-prev)
		}
		prev = y
	}
	return g
}

// modalResidue returns the most common value of (pos mod size) among
// positions, along with the fraction of positions that have that residue
// (the confidence). When positions is empty it returns (0, 1.0) so a missing
// axis does not penalise confidence.
func modalResidue(positions []int, size int) (residue int, confidence float64) {
	if len(positions) == 0 {
		return 0, 1.0
	}
	counts := make([]int, size)
	for _, p := range positions {
		r := p % size
		if r < 0 {
			r += size
		}
		counts[r]++
	}
	best := slices.Index(counts, slices.Max(counts))
	return best, float64(counts[best]) / float64(len(positions))
}
