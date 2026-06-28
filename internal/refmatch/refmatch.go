// Package refmatch implements Depix-style reference-matching decoding for
// mosaic-pixelated text.
//
// The algorithm works by building a reference table: for each glyph in the
// candidate charset, the glyph is rendered on a white background at the
// calibrated font size, then pixelated with the same block-average filter used
// to produce the target redaction. Each glyph's reference is stored as a grid
// of block signatures (mean RGB per block). Decoding walks the target image
// left-to-right in character cells, picking at each position the reference
// glyph whose block signatures best match the target blocks at that position.
//
// Unlike the LM-guided beam search (mosaictext.DecodeHMM), this approach:
//
//   - Works for arbitrary content (passwords, code, random strings) because it
//     needs no language model — the image alone drives the decision.
//   - Works with proportional fonts: each matched glyph contributes its own
//     pixel advance rather than a fixed monospace pitch.
//   - Is exact when the rendering font matches the redaction font exactly.
package refmatch

import (
	"context"
	"math"
)

// BlockSig is the mean RGB signature of one pixelated block. Values are in
// [0, 255]. Alpha is ignored — mosaic pixelation of dark text on a white
// background produces opaque blocks.
type BlockSig struct {
	R, G, B float64
}

// rgbDiag is the diagonal of the RGB cube: √(3 · 255²). Used to normalise
// block distances to [0, 1].
const rgbDiag = 255 * 1.7320508075688772 // 255 * √3

// Dist returns the Euclidean RGB distance between two block signatures,
// normalised to [0, 1] by dividing by the diagonal of the RGB cube (√(3·255²)).
func (s BlockSig) Dist(o BlockSig) float64 {
	dr := s.R - o.R
	dg := s.G - o.G
	db := s.B - o.B
	return math.Sqrt(dr*dr+dg*dg+db*db) / rgbDiag
}

// GlyphRef holds the pixelated reference for one character. Blocks is a
// row-major grid [blockRow][blockCol] of block signatures. Cols is the
// comparison width in block columns; Advance is the cursor step after a match.
// They differ when a glyph's ink extends into a shared block at its boundary:
// the comparison includes the shared block but the advance skips only the
// non-shared prefix so the next glyph can claim the shared block.
type GlyphRef struct {
	// Rune is the Unicode code point this reference represents.
	Rune rune
	// Blocks is the pixelated block grid for this glyph, indexed [row][col].
	// Rows correspond to block rows from top to bottom; cols to left-to-right
	// block columns within the glyph's comparison window.
	Blocks [][]BlockSig
	// Cols is the number of block columns used for comparison (len(Blocks[0])).
	Cols int
	// Advance is the number of block columns the cursor moves after this glyph
	// is matched. When zero, Cols is used as the advance (backward-compatible).
	// Advance ≤ Cols so that the last (partially-shared) block is re-evaluated
	// for the next glyph rather than silently consumed.
	Advance int
}

// ExtractBlocks extracts block signatures from a raw RGBA pixel buffer.
// pix is the Pix slice of an image.RGBA; stride is its Stride field; w and h
// are the image width and height in pixels; blockSize is the block side length.
// Incomplete trailing blocks (pixels that do not fill a complete block) are
// discarded. The returned grid is indexed [blockRow][blockCol] and is nil when
// the image is smaller than one block in either dimension.
func ExtractBlocks(pix []byte, stride, w, h, blockSize int) [][]BlockSig {
	bCols := w / blockSize
	bRows := h / blockSize
	if bCols == 0 || bRows == 0 {
		return nil
	}
	grid := make([][]BlockSig, bRows)
	for br := range bRows {
		row := make([]BlockSig, bCols)
		for bc := range bCols {
			var rSum, gSum, bSum float64
			area := float64(blockSize * blockSize)
			for dy := range blockSize {
				y := br*blockSize + dy
				for dx := range blockSize {
					x := bc*blockSize + dx
					off := y*stride + x*4
					rSum += float64(pix[off])
					gSum += float64(pix[off+1])
					bSum += float64(pix[off+2])
				}
			}
			row[bc] = BlockSig{R: rSum / area, G: gSum / area, B: bSum / area}
		}
		grid[br] = row
	}
	return grid
}

// ExtractBlocksDirect extracts block signatures from a pre-pixelated RGBA
// buffer by reading only the top-left pixel of each block instead of averaging
// all block²  pixels. This is byte-identical to [ExtractBlocks] when every
// block is already uniform — i.e. when pix is the output of [Pixelate] — because
// mean(N identical uint8 values) == the value itself, and the uint8→float64
// conversion is exact.
//
// Calling this on a non-pixelated image produces wrong results; callers are
// responsible for ensuring the precondition. On a pixelated 264×40 image at
// block=8 (33×5 = 165 blocks) the O(bRows×bCols) read is ~64× cheaper than
// the O(bRows×bCols×block²) loop in ExtractBlocks.
func ExtractBlocksDirect(pix []byte, stride, w, h, blockSize int) [][]BlockSig {
	bCols := w / blockSize
	bRows := h / blockSize
	if bCols == 0 || bRows == 0 {
		return nil
	}
	grid := make([][]BlockSig, bRows)
	for br := range bRows {
		row := make([]BlockSig, bCols)
		for bc := range bCols {
			off := (br*blockSize)*stride + (bc*blockSize)*4
			row[bc] = BlockSig{
				R: float64(pix[off]),
				G: float64(pix[off+1]),
				B: float64(pix[off+2]),
			}
		}
		grid[br] = row
	}
	return grid
}

// BlockRowDist returns the mean Euclidean RGB distance between two equal-length
// rows of block signatures. Returns +Inf when the rows have different lengths.
func BlockRowDist(a, b []BlockSig) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}
	if len(a) == 0 {
		return 0
	}
	var sum float64
	for i := range a {
		sum += a[i].Dist(b[i])
	}
	return sum / float64(len(a))
}

// glyphDist computes the mean block-signature distance between a glyph
// reference and a window of columns in the target grid starting at targetCol.
// Returns +Inf when the glyph would extend beyond the target width.
// Row counts need not match: only the rows present in the reference are
// compared, starting from row 0 of both target and reference (both have
// white rows stripped so row 0 is the first ink row).
func glyphDist(target [][]BlockSig, targetCol int, ref *GlyphRef) float64 {
	if len(target) == 0 || ref.Cols < 1 || len(ref.Blocks) == 0 {
		return math.Inf(1)
	}
	bCols := len(target[0])
	if targetCol+ref.Cols > bCols {
		return math.Inf(1)
	}
	compareRows := min(len(target), len(ref.Blocks))
	var sum float64
	var n int
	for r := range compareRows {
		refRow := ref.Blocks[r]
		tgtRow := target[r]
		for c := range min(ref.Cols, len(refRow)) {
			sum += refRow[c].Dist(tgtRow[targetCol+c])
			n++
		}
	}
	if n == 0 {
		return math.Inf(1)
	}
	return sum / float64(n)
}

// Match decodes the target block grid by walking left-to-right, at each
// position selecting the GlyphRef with the lowest block-distance to the target
// blocks at that position. Advance is proportional: each matched glyph advances
// by its own Cols count, supporting proportional fonts.
//
// It returns the decoded string and a per-character cell distance slice (one
// entry per matched glyph). The context is checked between character cells;
// cancellation returns whatever partial result has been accumulated.
func Match(ctx context.Context, target [][]BlockSig, refs []*GlyphRef, _ int) (text string, cellDists []float64) {
	if len(target) == 0 || len(refs) == 0 {
		return "", nil
	}
	bCols := len(target[0])
	if bCols == 0 {
		return "", nil
	}

	runes := make([]rune, 0, bCols)
	dists := make([]float64, 0, bCols)

	for col := 0; col < bCols; {
		if ctx.Err() != nil {
			break
		}

		bestRune := rune(0)
		bestDist := math.Inf(1)
		bestAdvance := 1

		for _, ref := range refs {
			if ref.Cols < 1 {
				continue
			}
			d := glyphDist(target, col, ref)
			if d < bestDist {
				bestDist = d
				bestRune = ref.Rune
				// Use Advance for cursor step when set; fall back to Cols.
				// Advance ≤ Cols allows the last (shared-boundary) block to
				// be re-evaluated for the next glyph rather than consumed.
				if ref.Advance > 0 {
					bestAdvance = ref.Advance
				} else {
					bestAdvance = ref.Cols
				}
			}
		}

		if bestRune != 0 {
			runes = append(runes, bestRune)
			dists = append(dists, bestDist)
		}
		col += max(1, bestAdvance)
	}

	return string(runes), dists
}
