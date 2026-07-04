package unpixel

import (
	"image"
	"slices"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// dominantGapPeriod returns the most frequently occurring positive gap between
// consecutive positions in colPos and rowPos, provided it appears at least twice
// (across both slices combined). When two gap values tie in frequency, the
// larger one wins — this anti-sub-harmonic bias ensures that a true structural
// period is preferred over an accidental sub-multiple. It returns 0 when fewer
// than two boundary positions exist, or when no gap appears more than once.
//
// Unlike gcdOfGaps, dominantGapPeriod is robust to a single atypical gap such
// as a partial-edge block (width = phase offset < true period) that would
// otherwise drive the GCD to 1.
func dominantGapPeriod(colPos, rowPos []int) int {
	counts := make(map[int]int)
	addGaps := func(pos []int) {
		for i := 1; i < len(pos); i++ {
			if g := pos[i] - pos[i-1]; g > 0 {
				counts[g]++
			}
		}
	}
	addGaps(colPos)
	addGaps(rowPos)

	best, bestCount := 0, 0
	for g, c := range counts {
		if c > bestCount || (c == bestCount && g > best) {
			best, bestCount = g, c
		}
	}
	if bestCount < 2 {
		return 0
	}
	return best
}

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
// Block-size detection uses a three-stage cascade, each stage activating only
// when the previous one fails or produces an apparent sub-harmonic:
//
//  1. Exact GCD: the GCD of all inter-boundary gaps (fast, zero regression risk
//     for clean axis-aligned mosaics). Returns the true block size when every
//     detected boundary is a genuine grid line.
//
//  2. Dominant-gap fallback (partial-edge robustness): when the exact GCD is
//     < 2 — which happens when a partial-edge block creates a small gap (equal
//     to the grid phase offset) that poisons the GCD — the most frequent gap
//     value (the mode) is used instead. A single atypical gap cannot bias the
//     mode, so images like marx.png (19 px blocks, 5 px partial leading block)
//     are correctly recovered.
//
//  3. Sub-harmonic guard: after stages 1–2, if the robust autocorrelation of
//     the threshold-filtered boundary signal strongly supports a period that is
//     a strict multiple of the current candidate AND the robust signal does not
//     already support the current candidate, the larger period wins. The double
//     condition separates two cases: (a) sub-harmonic lock — the exact GCD is a
//     divisor of the true period and robust support at the GCD is near zero;
//     (b) clean mosaics — the exact GCD is correct and robust support at that
//     period is already high, so no upgrade is needed despite any coincidental
//     autocorrelation peaks from text-character spacing.
//
// PhaseX is the most common residue of boundary column positions modulo Size;
// PhaseY likewise for rows. Confidence is the fraction of boundary positions
// consistent with that modal residue, averaged over the X and Y axes.
func InferBlockGrid(img image.Image) (BlockGrid, bool) {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return BlockGrid{}, false
	}

	colPositions := detectBoundaryPositions(rgba, b, w, h, true)
	rowPositions := detectBoundaryPositions(rgba, b, w, h, false)

	// Stage 1: exact GCD — zero risk for clean, axis-aligned mosaics.
	size := gcdOfGaps(colPositions, rowPositions)
	if size < 2 {
		// Stage 2: dominant-gap fallback — robust to a single partial-edge block
		// whose gap (= phase offset) poisons the GCD.
		size = dominantGapPeriod(colPositions, rowPositions)
	}
	if size < 2 {
		return BlockGrid{}, false
	}

	// Stage 3: sub-harmonic guard — prefer the robust structural period when it
	// is a strict multiple of the current candidate and well-supported,
	// AND the robust signal does not already support the current candidate.
	//
	// The second condition is crucial: for clean mosaics the robust signal
	// has high support at the true block period (the exact GCD), so no upgrade
	// is needed. For sub-harmonic lock (e.g. GCD=10 on a 20 px grid with weak
	// intra-block variation), the robust signal has near-zero support at the
	// sub-harmonic period and high support at the true period — the upgrade is
	// warranted. Requiring low robust support at the current candidate prevents
	// text-structure autocorrelation peaks from causing false upgrades.
	//
	// Known sharp edge (heuristic): both sides compare against the single
	// RobustSupportThreshold with no dead-band, so an image whose robust support
	// straddles the threshold at BOTH periods (e.g. a heavily re-compressed JPEG
	// mosaic with content-driven transitions at the sub-harmonic) may decide
	// unstably. We keep one threshold rather than an unmeasured dead-band: no
	// fixture exercises the instability yet, and speculative constants would
	// violate the project's "prove behaviour changes, don't tune by feel" rule.
	robColPos := robustBoundaryPositions(rgba, b, w, h, true)
	robRowPos := robustBoundaryPositions(rgba, b, w, h, false)
	if acPeriod, acSupport := dominantPeriod(robColPos, w, robRowPos, h); acPeriod > size &&
		acPeriod%size == 0 &&
		acSupport >= RobustSupportThreshold &&
		robustSupportAvg(robColPos, w, robRowPos, h, size) < RobustSupportThreshold {
		size = acPeriod
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

// robustSupportAvg returns the mean normalised autocorrelation of the robust
// column and row boundary signals at the given period. It averages
// autocorrSupport over whichever axes have at least one boundary position and
// fit the period within the image dimension.
//
// This is used by Stage 3 of InferBlockGrid to test whether the current
// candidate block size is already well-supported by the robust signal. A high
// return value (≥ RobustSupportThreshold) means the candidate is the true
// period; a near-zero return value signals sub-harmonic lock.
func robustSupportAvg(robColPos []int, w int, robRowPos []int, h int, period int) float64 {
	sum, n := 0.0, 0
	if len(robColPos) > 0 && period < w {
		sum += autocorrSupport(robColPos, w, period)
		n++
	}
	if len(robRowPos) > 0 && period < h {
		sum += autocorrSupport(robRowPos, h, period)
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
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
