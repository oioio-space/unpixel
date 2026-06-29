package leak

import (
	"bytes"
	"image"
	"image/png"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// minBlockFraction is the minimum fraction of image pixels that must form a
// near-uniform dark region for it to qualify as a redaction block.
const minBlockFraction = 0.02

// darkThreshold is the maximum per-channel value (0–255) considered "dark".
const darkThreshold = 64

// uniformTolerance is the maximum per-channel spread within a candidate block
// that still qualifies as "near-constant".
const uniformTolerance = 20

// partial surfaces caller-supplied visible text when the PNG image contains a
// plausible solid redaction block (a near-uniform rectangular dark region that
// differs from the page background).
//
// If visibleText is empty the detector abstains immediately — auto-OCR is
// out of scope (Tier-2). On any decode error the detector also abstains.
func partial(data []byte, visibleText string) (Result, bool) {
	if visibleText == "" {
		return Result{}, false
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, false
	}

	if !hasSolidBlock(imutil.ToRGBA(img)) {
		return Result{}, false
	}

	return Result{
		Source:     SourcePartial,
		Text:       visibleText,
		Confidence: 0.5,
		Notes:      []string{"surfaced caller-supplied visible text; a solid redaction block is present"},
	}, true
}

// hasSolidBlock returns true when rgba contains a near-uniform dark rectangular
// region covering at least minBlockFraction of the image area. The scan looks
// for a contiguous horizontal run of rows that share a near-constant dark colour
// across a wide band of columns.
func hasSolidBlock(rgba *image.RGBA) bool {
	bounds := rgba.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return false
	}
	minPixels := max(1, int(float64(w*h)*minBlockFraction))

	// For each row, find the longest horizontal run of dark near-uniform pixels.
	// Then look for a vertical stack of such runs that together exceed minPixels.
	type run struct{ x0, x1 int } // half-open [x0, x1)

	rowRun := func(y int) run {
		ox, oy := bounds.Min.X, bounds.Min.Y
		// Find first dark pixel.
		start := -1
		var rRef, gRef, bRef uint8
		for x := range w {
			i := rgba.PixOffset(ox+x, oy+y)
			r, g, b := rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2]
			if int(r)+int(g)+int(b) <= int(darkThreshold)*3 {
				start = x
				rRef, gRef, bRef = r, g, b
				break
			}
		}
		if start < 0 {
			return run{}
		}
		end := start + 1
		for x := start + 1; x < w; x++ {
			i := rgba.PixOffset(ox+x, oy+y)
			r, g, b := rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2]
			dr := absDiff(r, rRef)
			dg := absDiff(g, gRef)
			db := absDiff(b, bRef)
			if dr > uniformTolerance || dg > uniformTolerance || db > uniformTolerance {
				break
			}
			end = x + 1
		}
		return run{start, end}
	}

	// Sweep rows: accumulate a vertical band whose horizontal extent is the
	// intersection of consecutive row-runs.
	bandX0, bandX1 := 0, w
	pixelCount := 0

	for y := range h {
		r := rowRun(y)
		if r.x0 == r.x1 { // row has no dark run — reset band
			bandX0, bandX1 = 0, w
			pixelCount = 0
			continue
		}
		newX0 := max(bandX0, r.x0)
		newX1 := min(bandX1, r.x1)
		if newX0 >= newX1 { // intersection empty — start fresh from this row
			bandX0, bandX1 = r.x0, r.x1
			pixelCount = r.x1 - r.x0
		} else {
			bandX0, bandX1 = newX0, newX1
			pixelCount += newX1 - newX0
		}
		if pixelCount >= minPixels {
			return true
		}
	}
	return false
}

func absDiff(a, b uint8) uint8 {
	if a >= b {
		return a - b
	}
	return b - a
}
