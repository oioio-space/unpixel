// Package locate provides mosaic-aware region detection for the unpixel pipeline.
//
// The primary entry point is [LocateMosaicBand], which finds the block-aligned
// bounding box of a pixelated (block-constant) region embedded in a larger
// screenshot. Unlike [unpixel.LocateRedaction] — which targets Gaussian-blurred
// redactions by looking for low gradient — LocateMosaicBand exploits the
// piecewise-constant structure of a true mosaic: within each block every pixel
// shares the same colour, and hard colour steps appear exactly at block
// boundaries. This lets it recover the full extent of the mosaic including
// trailing punctuation that the blur-based locator misses.
//
// Detection uses two complementary criteria:
//
//  1. Content rows/cols: blocks are flat (block-constant) AND the flat-block
//     means span ≥ interBlockContrast luminance — distinguishing ink from
//     uniform background.
//
//  2. Padding rows/cols: blocks adjacent to a content cluster that are
//     uniformly flat (all blocks flat, no contrast) — these are the top/bottom
//     padding rows of a text mosaic that share the background colour but are
//     still part of the pixelated region.
//
// # Caveats
//
//   - Multiple disjoint mosaic regions: only the largest is returned.
//   - Heavily JPEG-compressed mosaics: intra-block variance is non-zero; the
//     flatThresh tolerance (currently 24 per channel) may need tuning.
//   - Very small mosaics (fewer than 2 blocks in either dimension): not detected.
package locate

import (
	"image"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// flatThresh is the maximum per-channel absolute deviation from a block's
// corner pixel that still qualifies the block as "flat" (block-constant).
// A perfect software mosaic has zero deviation; this headroom covers minor
// JPEG/quantisation noise.
const flatThresh = 24

// interBlockContrast is the minimum luminance range across the flat-block
// means in a row or column for that line to be treated as mosaic content
// (as opposed to uniform padding or background).
const interBlockContrast = 16

// mosaicFrac is the minimum fraction of flat blocks required for a row or
// column to be considered a mosaic band (either content or padding).
const mosaicFrac = 0.75

// kind classifies a line of blocks (row or column) in the mosaic grid.
type kind int

const (
	// kindNonFlat marks a line where too many blocks are not piecewise-constant
	// (e.g. a row of sharp surrounding text).
	kindNonFlat kind = iota
	// kindFlat marks a line where most blocks are flat but show no inter-block
	// luminance contrast — uniform background or padding.
	kindFlat
	// kindContent marks a line where most blocks are flat AND the flat-block
	// means span ≥ interBlockContrast luminance — mosaic ink content.
	kindContent
)

// LocateMosaicBand detects the grid-aligned bounding box of a pixelated
// (block-constant mosaic) region inside img.
//
// The algorithm:
//  1. Detect the block size and grid phase with [unpixel.InferBlockGrid].
//  2. Classify each block-row as "content" (flat + inter-block contrast) or
//     "flat-uniform" (flat but no contrast — padding or background).
//  3. Find the longest contiguous run of content rows, then grow it outward
//     through adjacent flat-uniform rows, stopping at non-flat rows (which
//     are the sharp surrounding text).
//  4. Re-score block-columns within the final vertical band the same way.
//  5. Return the result snapped to block boundaries.
//
// ok is false when no regular grid is found or when no mosaic band stands out
// from the background (e.g. an all-uniform or all-sharp image).
func LocateMosaicBand(img *image.RGBA) (image.Rectangle, bool) { //nolint:revive // LocateMosaicBand reads clearly at the call site; stutter is intentional.
	grid, ok := unpixel.InferBlockGrid(img)
	if !ok || grid.Size < 2 {
		return image.Rectangle{}, false
	}

	rgba := img // already *image.RGBA — no conversion needed
	b := rgba.Bounds()
	W, H := b.Dx(), b.Dy()
	S := grid.Size

	// First block-start position along each axis, derived from the grid phase.
	firstX := grid.PhaseX % S
	if firstX < 0 {
		firstX += S
	}
	firstY := grid.PhaseY % S
	if firstY < 0 {
		firstY += S
	}

	buildStarts := func(first, dim int) []int {
		starts := make([]int, 0, dim/S+2)
		for p := first; p < dim; p += S {
			starts = append(starts, p)
		}
		return starts
	}
	xStarts := buildStarts(firstX, W)
	yStarts := buildStarts(firstY, H)
	nBX, nBY := len(xStarts), len(yStarts)
	if nBX < 2 || nBY < 2 {
		return image.Rectangle{}, false
	}

	// blockPixEnd returns the exclusive pixel coordinate of block i along the
	// given starts, clamped to the image dimension.
	blockPixEnd := func(starts []int, i, dim int) int {
		if i+1 < len(starts) {
			return starts[i+1]
		}
		return dim
	}

	// blockStat returns the mean luminance of a block and whether it is flat.
	// Flat means every pixel's per-channel deviation from the corner pixel is
	// within flatThresh — the defining signature of a mosaic block.
	blockStat := func(bx, by int) (meanLum int, flat bool) {
		x0 := b.Min.X + xStarts[bx]
		x1 := b.Min.X + blockPixEnd(xStarts, bx, W)
		y0 := b.Min.Y + yStarts[by]
		y1 := b.Min.Y + blockPixEnd(yStarts, by, H)

		ref := rgba.RGBAAt(x0, y0)
		flat = true
		var lumSum, n int
		for y := y0; y < y1; y++ {
			off := rgba.PixOffset(x0, y)
			row := rgba.Pix[off : off+(x1-x0)*4 : off+(x1-x0)*4]
			for i := 0; i < len(row); i += 4 {
				if flat {
					dr := int(row[i]) - int(ref.R)
					dg := int(row[i+1]) - int(ref.G)
					db := int(row[i+2]) - int(ref.B)
					if dr > flatThresh || dr < -flatThresh ||
						dg > flatThresh || dg < -flatThresh ||
						db > flatThresh || db < -flatThresh {
						flat = false
					}
				}
				lumSum += imutil.Lum601(row[i], row[i+1], row[i+2])
				n++
			}
		}
		if n > 0 {
			meanLum = lumSum / n
		}
		return meanLum, flat
	}

	rowKind := func(by int) kind {
		var flatCount int
		lumMin, lumMax := 255, 0
		for bx := range nBX {
			lum, flat := blockStat(bx, by)
			if flat {
				flatCount++
				lumMin = min(lumMin, lum)
				lumMax = max(lumMax, lum)
			}
		}
		if float64(flatCount) < mosaicFrac*float64(nBX) {
			return kindNonFlat
		}
		if lumMax-lumMin >= interBlockContrast {
			return kindContent
		}
		return kindFlat
	}

	colKind := func(bx, byLo, byHi int) kind {
		bandLen := byHi - byLo
		var flatCount int
		lumMin, lumMax := 255, 0
		for by := byLo; by < byHi; by++ {
			lum, flat := blockStat(bx, by)
			if flat {
				flatCount++
				lumMin = min(lumMin, lum)
				lumMax = max(lumMax, lum)
			}
		}
		if float64(flatCount) < mosaicFrac*float64(bandLen) {
			return kindNonFlat
		}
		if lumMax-lumMin >= interBlockContrast {
			return kindContent
		}
		return kindFlat
	}

	// Classify every block-row.
	rowKinds := make([]kind, nBY)
	for by := range nBY {
		rowKinds[by] = rowKind(by)
	}

	// Find the longest contiguous run of content rows.
	contentRow := make([]bool, nBY)
	for by := range nBY {
		contentRow[by] = rowKinds[by] == kindContent
	}
	rowStart, rowEnd := longestRun(contentRow)
	if rowEnd <= rowStart {
		return image.Rectangle{}, false
	}

	// Classify every block-column within the content row band.
	colKinds := make([]kind, nBX)
	for bx := range nBX {
		colKinds[bx] = colKind(bx, rowStart, rowEnd)
	}

	// Build a content map and fill small flat-only gaps (spaces between letters,
	// near-white punctuation blocks). A gap of kindNonFlat still terminates the
	// run — that would be a sharp surrounding text column.
	// maxColGap is the maximum number of consecutive non-content (but flat)
	// block-columns that can be bridged inside a mosaic span.
	const maxColGap = 3
	contentCol := fillFlatGaps(colKinds, maxColGap)
	colStart, colEnd := longestRun(contentCol)
	if colEnd <= colStart {
		return image.Rectangle{}, false
	}

	x0 := b.Min.X + xStarts[colStart]
	x1 := b.Min.X + blockPixEnd(xStarts, colEnd-1, W)
	y0 := b.Min.Y + yStarts[rowStart]
	y1 := b.Min.Y + blockPixEnd(yStarts, rowEnd-1, H)

	return image.Rect(x0, y0, x1, y1), true
}

// fillFlatGaps returns a boolean slice where a run of ≤ maxGap consecutive
// kindFlat entries between two kindContent entries is bridged to true. Runs
// containing any kindNonFlat entry are never bridged — those mark sharp edges
// that bound the mosaic region.
func fillFlatGaps(kinds []kind, maxGap int) []bool {
	out := make([]bool, len(kinds))
	for i, k := range kinds {
		out[i] = k == kindContent
	}
	// Two-pass: scan forward to find gap extents, fill if both sides are content.
	i := 0
	for i < len(out) {
		if !out[i] {
			i++
			continue
		}
		// out[i] is true; scan forward for the end of this content run.
		j := i + 1
		for j < len(out) && out[j] {
			j++
		}
		// [i, j) is a content run. Look ahead for a gap of kindFlat only.
		gapStart := j
		k := j
		for k < len(out) && !out[k] && kinds[k] == kindFlat {
			k++
		}
		gapLen := k - gapStart
		// If the gap is small enough and the next entry is also content, bridge it.
		if gapLen > 0 && gapLen <= maxGap && k < len(out) && out[k] {
			for g := gapStart; g < k; g++ {
				out[g] = true
			}
		}
		i = k
	}
	return out
}

// longestRun returns [start, end) of the longest contiguous run of true values
// in flags. Returns (0, 0) when no true value exists.
func longestRun(flags []bool) (start, end int) {
	bestLen, curStart := 0, 0
	for i := 0; i <= len(flags); i++ {
		if i < len(flags) && flags[i] {
			continue
		}
		if runLen := i - curStart; runLen > bestLen {
			bestLen, start, end = runLen, curStart, i
		}
		curStart = i + 1
	}
	return start, end
}
