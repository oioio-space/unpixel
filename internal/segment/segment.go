// Package segment partitions a mosaic/redaction image into text lines and
// words. It is the segmentation brick that lets the blind decoder collapse one
// N-character search into Σ short per-word searches (P6.1).
//
// Ink test: BT.601 luminance (299·R + 587·G + 114·B)/1000 < 244, matching the
// convention used throughout the unpixel pipeline (see internal/imutil and
// real_mosaic_test.go contentBounds).
//
// Usage:
//
//	lines := segment.Lines(pixelated)
//	for i, line := range lines {
//	    words := segment.Words(pixelated, line)
//	    fmt.Printf("line %d: %d words\n", i, len(words))
//	}
//
//	// Or the one-shot convenience wrapper:
//	allWords := segment.Segment(pixelated) // [][]image.Rectangle
package segment

import (
	"image"
	"math"
)

// lumThreshold is the BT.601 luminance ceiling below which a pixel is
// considered "ink". Matches the threshold used in real_mosaic_test.go
// (contentBounds) and the pipeline's general convention.
const lumThreshold = 244

// isInk reports whether the pixel at (x, y) is inked according to BT.601
// luminance. It reads directly from img.Pix for allocation-free hot-path use.
func isInk(img *image.RGBA, x, y int) bool {
	off := img.PixOffset(x, y)
	r := uint32(img.Pix[off])
	g := uint32(img.Pix[off+1])
	b := uint32(img.Pix[off+2])
	// BT.601: (299·R + 587·G + 114·B) / 1000
	return (299*r+587*g+114*b)/1000 < lumThreshold
}

// rowHasInk reports whether any pixel in row y of img (within x range
// [xMin, xMax)) is inked.
func rowHasInk(img *image.RGBA, y, xMin, xMax int) bool {
	for x := xMin; x < xMax; x++ {
		if isInk(img, x, y) {
			return true
		}
	}
	return false
}

// Lines returns the bounding rectangles of the horizontal ink bands (text
// lines) in img, ordered top-to-bottom. A line is a maximal run of rows that
// contain at least one inked pixel, separated from adjacent lines by one or
// more all-background rows. The returned rectangles are horizontally tight: the
// x-range spans only the inked columns within the band.
//
// An all-white image returns an empty (non-nil) slice.
func Lines(img *image.RGBA) []image.Rectangle {
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return []image.Rectangle{}
	}

	var result []image.Rectangle
	inBand := false
	bandY0 := 0

	for y := b.Min.Y; y < b.Max.Y; y++ {
		ink := rowHasInk(img, y, b.Min.X, b.Max.X)
		switch {
		case !inBand && ink:
			inBand = true
			bandY0 = y
		case inBand && !ink:
			inBand = false
			result = append(result, tightRect(img, bandY0, y))
		}
	}
	if inBand {
		result = append(result, tightRect(img, bandY0, b.Max.Y))
	}

	if result == nil {
		return []image.Rectangle{}
	}
	return result
}

// tightRect returns a rectangle covering rows [y0, y1) of img, with the
// x-extent narrowed to the leftmost and rightmost inked columns in that band.
func tightRect(img *image.RGBA, y0, y1 int) image.Rectangle {
	b := img.Bounds()
	xMin := b.Max.X
	xMax := b.Min.X

	for y := y0; y < y1; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if isInk(img, x, y) {
				if x < xMin {
					xMin = x
				}
				if x+1 > xMax {
					xMax = x + 1
				}
			}
		}
	}

	if xMin >= xMax {
		// No ink found — return a zero-width rect at y0.
		return image.Rect(b.Min.X, y0, b.Min.X, y1)
	}
	return image.Rect(xMin, y0, xMax, y1)
}

// wordBreakK is the proportionality constant used by WordBreakGap.
//
// Rationale for k = 0.15:
//
// After block-average pixelation at block size B, the inter-word space
// collapses to exactly one block (B pixels) when the space glyph advance is
// less than 2B — which holds for every combination of font size and block size
// in the unpixel test suite (e.g. 32 pt + B=8: space≈8px = 1 block;
// 32 pt + B=16: space≈16px = 1 block). The band height H ≈ font_size_px.
// For k to split a 1-block gap we need: k×H < B. Since B/H ≈ B/font_size_px,
// and the smallest realistic ratio is B=8, H=48 → B/H ≈ 0.167, choosing
// k = 0.15 (< 0.167) ensures the threshold stays below one block width across
// all typical (font, block-size) combinations while remaining well above
// zero-width intra-glyph gaps (which disappear entirely after block averaging).
//
// The spec suggested starting at k≈0.4, but that value exceeds one block width
// (B/H ≈ 0.167) and therefore never splits any word-space gap in a pixelated
// image. k = 0.15 is the correct calibrated value for the pixelated domain.
const wordBreakK = 0.15

// WordBreakGap returns the minimum column-gap width (in pixels) that is
// treated as a word boundary within a line band of height H. A gap strictly
// narrower than this value is merged (intra-word spacing); a gap of this width
// or wider starts a new word.
//
// The threshold is round(wordBreakK × H). The constant wordBreakK = 0.15 is
// calibrated for block-pixelated images; see the constant's comment for the
// derivation. The function is exported so tests can pin the concrete value
// without hard-coding the constant.
func WordBreakGap(h int) int {
	return int(math.Round(wordBreakK * float64(h)))
}

// Words returns the bounding rectangles of the words within a single line
// band, ordered left-to-right. The band is described by line (as returned by
// Lines). Adjacent inked column-runs are merged when the gap between them is
// narrower than WordBreakGap(line.Dy()); a gap at or above that threshold
// starts a new word.
//
// The column ink profile is computed once (allocation: one bool slice of width
// columns) and reused for the run-length scan.
func Words(img *image.RGBA, line image.Rectangle) []image.Rectangle {
	b := img.Bounds()
	x0 := max(line.Min.X, b.Min.X)
	x1 := min(line.Max.X, b.Max.X)
	y0 := max(line.Min.Y, b.Min.Y)
	y1 := min(line.Max.Y, b.Max.Y)

	w := x1 - x0
	h := line.Dy()
	if w <= 0 || h <= 0 {
		return []image.Rectangle{}
	}

	// Build column ink profile: colInk[i] == true when column x0+i has at
	// least one inked pixel in the y-band. Computed once, O(w·h).
	colInk := make([]bool, w)
	for y := y0; y < y1; y++ {
		for i := range w {
			if !colInk[i] && isInk(img, x0+i, y) {
				colInk[i] = true
			}
		}
	}

	threshold := WordBreakGap(h)

	// Run-length scan over the column profile to emit word rectangles.
	//
	// We track:
	//   inWord  — currently accumulating an inked run (or a sub-threshold gap).
	//   wordX0  — absolute x where the current word started.
	//   wordX1  — absolute x+1 of the last inked column seen (trailing edge).
	//   gapLen  — number of consecutive background columns since wordX1.
	//
	// While a background gap is narrower than threshold the word stays open
	// (intra-glyph / inter-glyph spacing). Once the gap reaches threshold the
	// word is closed at wordX1 and a new word begins on the next ink.
	var result []image.Rectangle
	inWord := false
	wordX0 := 0 // absolute x of current word start
	wordX1 := 0 // absolute x+1 of last inked column (right edge of word so far)
	gapLen := 0 // background columns seen since wordX1

	for i, ink := range colInk {
		ax := x0 + i
		if ink {
			if !inWord {
				// First ink column: start a new word.
				inWord = true
				wordX0 = ax
			}
			// Extend the right edge; reset the trailing gap counter.
			wordX1 = ax + 1
			gapLen = 0
		} else if inWord {
			gapLen++
			if gapLen >= threshold {
				// Gap reached the word-break threshold: close the current word.
				result = append(result, wordRect(wordX0, wordX1, y0, y1))
				inWord = false
			}
		}
	}
	// Handle word still open at end of profile.
	if inWord {
		result = append(result, wordRect(wordX0, wordX1, y0, y1))
	}

	if result == nil {
		return []image.Rectangle{}
	}
	return result
}

// wordRect returns a rectangle for a word spanning columns [xMin, xMax) and
// rows [y0, y1).
func wordRect(xMin, xMax, y0, y1 int) image.Rectangle {
	return image.Rect(xMin, y0, xMax, y1)
}

// Segment is the convenience wrapper: it calls Lines, then Words for each line,
// and returns a slice of slices where result[i] contains the word rectangles
// for line i.
func Segment(img *image.RGBA) [][]image.Rectangle {
	lines := Lines(img)
	if len(lines) == 0 {
		return [][]image.Rectangle{}
	}
	result := make([][]image.Rectangle, len(lines))
	for i, line := range lines {
		result[i] = Words(img, line)
	}
	return result
}
