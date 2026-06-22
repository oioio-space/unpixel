// Package deblur provides image normalisation helpers that prepare real-world
// blurred-text captures (textured background, vignette, dark theme, JPEG
// blocking) for the generate-and-test recovery loop, which assumes a clean
// Gaussian blur on a flat-white background.
//
// The main entry point is [Normalize]. The morphological primitives ([Erode],
// [Dilate], [Open]) are separated into morph.go so they can be benchmarked and
// tested in isolation; they operate on a flat float64 luminance plane rather
// than RGBA pixels to keep the math clean.
package deblur

// Erode computes the grey-scale erosion of lum using a flat square
// structuring element of the given half-size radius. For each pixel the
// output is the minimum luminance value found in the (2·radius+1)² square
// neighbourhood. Out-of-bounds pixels are clamped to the nearest border value.
//
// lum is a row-major float64 slice of length w×h. The function does not
// modify lum; it returns a freshly allocated slice of the same length.
//
// Erosion is implemented as a separable pass: a horizontal 1-D minimum sweep
// followed by a vertical 1-D minimum sweep. Each pass is O(w·h) regardless of
// radius, giving O(w·h) total (not O(w·h·r²)).
func Erode(lum []float64, w, h, radius int) []float64 {
	if radius <= 0 || w == 0 || h == 0 {
		out := make([]float64, len(lum))
		copy(out, lum)
		return out
	}
	tmp := make([]float64, len(lum))
	out := make([]float64, len(lum))

	// Horizontal pass: for each pixel take the min of [x-r, x+r].
	for y := range h {
		row := y * w
		for x := range w {
			lo := max(0, x-radius)
			hi := min(w-1, x+radius)
			v := lum[row+lo]
			for xi := lo + 1; xi <= hi; xi++ {
				if lum[row+xi] < v {
					v = lum[row+xi]
				}
			}
			tmp[row+x] = v
		}
	}

	// Vertical pass: for each pixel take the min of [y-r, y+r].
	for y := range h {
		for x := range w {
			lo := max(0, y-radius)
			hi := min(h-1, y+radius)
			v := tmp[lo*w+x]
			for yi := lo + 1; yi <= hi; yi++ {
				if tmp[yi*w+x] < v {
					v = tmp[yi*w+x]
				}
			}
			out[y*w+x] = v
		}
	}
	return out
}

// Dilate computes the grey-scale dilation of lum using a flat square
// structuring element of the given half-size radius. For each pixel the
// output is the maximum luminance value found in the (2·radius+1)² square
// neighbourhood. Out-of-bounds pixels are clamped to the nearest border value.
//
// See [Erode] for the separable-pass contract; Dilate has the same O(w·h)
// complexity and does not modify lum.
func Dilate(lum []float64, w, h, radius int) []float64 {
	if radius <= 0 || w == 0 || h == 0 {
		out := make([]float64, len(lum))
		copy(out, lum)
		return out
	}
	tmp := make([]float64, len(lum))
	out := make([]float64, len(lum))

	// Horizontal pass.
	for y := range h {
		row := y * w
		for x := range w {
			lo := max(0, x-radius)
			hi := min(w-1, x+radius)
			v := lum[row+lo]
			for xi := lo + 1; xi <= hi; xi++ {
				if lum[row+xi] > v {
					v = lum[row+xi]
				}
			}
			tmp[row+x] = v
		}
	}

	// Vertical pass.
	for y := range h {
		for x := range w {
			lo := max(0, y-radius)
			hi := min(h-1, y+radius)
			v := tmp[lo*w+x]
			for yi := lo + 1; yi <= hi; yi++ {
				if tmp[yi*w+x] > v {
					v = tmp[yi*w+x]
				}
			}
			out[y*w+x] = v
		}
	}
	return out
}

// Open applies a morphological opening to lum: first [Erode] then [Dilate]
// with the same radius and structuring element. Opening removes bright thin
// features (strokes narrower than 2·radius pixels) while preserving large
// bright regions — making it an excellent background estimator: the text
// strokes erode away, leaving only the slowly-varying background.
//
// lum is not modified. Returns a freshly allocated slice of the same length.
func Open(lum []float64, w, h, radius int) []float64 {
	eroded := Erode(lum, w, h, radius)
	return Dilate(eroded, w, h, radius)
}
