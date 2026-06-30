package multiframe

import (
	"image"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// DiscoverPhases returns a copy of frames with each frame's OffsetX/OffsetY
// set to the detected mosaic grid phase (in [0, block)). It does not mutate
// the input. Deterministic.
//
// Detection is based on within-block luma variance: a mosaic pixelated at
// phase p is block-constant within blocks aligned to p, so within-block
// variance is minimal at the true phase and high at wrong phases. X and Y
// phases are discovered independently (the grid is separable), each requiring
// a single pass over the image per candidate phase value.
//
// For near-constant images, within-block variance is ~0 at every candidate
// phase; the detected phase then defaults to 0, which may not equal the true
// encoding phase. This is harmless for fusion (a constant block carries no
// sub-block detail to recover regardless of phase).
//
// When block < 1 a copy of frames is returned unchanged.
func DiscoverPhases(frames []Frame, block int) []Frame {
	out := make([]Frame, len(frames))
	copy(out, frames)

	if block < 1 {
		return out
	}

	for i, f := range frames {
		if f.Img == nil {
			continue
		}
		rgba := imutil.ToRGBA(f.Img)
		ox, oy := discoverPhase(rgba, block)
		out[i].OffsetX = ox
		out[i].OffsetY = oy
	}
	return out
}

// discoverPhase returns the (x, y) phase in [0, block) that minimises
// within-block luma variance for the given image.
func discoverPhase(img *image.RGBA, block int) (ox, oy int) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	// Build a flat luma array (BT.601) indexed [y*w+x] for fast row access.
	luma := make([]int, w*h)
	for y := range h {
		for x := range w {
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			luma[y*w+x] = imutil.Lum601(c.R, c.G, c.B)
		}
	}

	ox = argminVariance1D(luma, w, h, block, true)
	oy = argminVariance1D(luma, w, h, block, false)
	return ox, oy
}

// argminVariance1D finds the phase p in [0, block) that minimises total
// within-block variance along one axis. When horizontal is true it searches
// the X phase (column blocks); when false it searches the Y phase (row
// blocks). The perpendicular axis is always summed over in full so no
// information is discarded.
func argminVariance1D(luma []int, w, h, block int, horizontal bool) int {
	bestPhase := 0
	bestVar := withinBlockVariance1D(luma, w, h, block, 0, horizontal)

	for p := 1; p < block; p++ {
		if v := withinBlockVariance1D(luma, w, h, block, p, horizontal); v < bestVar {
			bestVar = v
			bestPhase = p
		}
	}
	return bestPhase
}

// withinBlockVariance1D computes the total within-block luma variance for
// phase p along the chosen axis. Each block spans [start, start+block) and
// the variance contribution is Σ(luma − blockMean)² over all pixels in the
// block, summed across all blocks.
//
// Using integer arithmetic throughout (luma values are in [0,255]) keeps
// this path allocation-free and avoids floating-point rounding.
func withinBlockVariance1D(luma []int, w, h, block, phase int, horizontal bool) int64 {
	start := gridStart(phase, block)

	var totalVar int64

	if horizontal {
		// Iterate over column blocks; for each block [bx, bx+block) sum over
		// all rows to produce a combined luma column-slice, then compute variance.
		for bx := start; bx < w; bx += block {
			x0, x1 := max(bx, 0), min(bx+block, w)
			if x0 >= x1 {
				continue
			}
			n := (x1 - x0) * h // total pixel count in this column band
			if n == 0 {
				continue
			}
			var sum int64
			for y := range h {
				rowBase := y * w
				for x := x0; x < x1; x++ {
					sum += int64(luma[rowBase+x])
				}
			}
			mean := sum / int64(n)
			var sqErr int64
			for y := range h {
				rowBase := y * w
				for x := x0; x < x1; x++ {
					d := int64(luma[rowBase+x]) - mean
					sqErr += d * d
				}
			}
			totalVar += sqErr
		}
	} else {
		// Iterate over row blocks; for each block [by, by+block) sum over
		// all columns to produce a combined luma row-slice, then compute variance.
		for by := start; by < h; by += block {
			y0, y1 := max(by, 0), min(by+block, h)
			if y0 >= y1 {
				continue
			}
			n := w * (y1 - y0)
			if n == 0 {
				continue
			}
			var sum int64
			for y := y0; y < y1; y++ {
				rowBase := y * w
				for x := range w {
					sum += int64(luma[rowBase+x])
				}
			}
			mean := sum / int64(n)
			var sqErr int64
			for y := y0; y < y1; y++ {
				rowBase := y * w
				for x := range w {
					d := int64(luma[rowBase+x]) - mean
					sqErr += d * d
				}
			}
			totalVar += sqErr
		}
	}

	return totalVar
}
