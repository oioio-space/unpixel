// Package pixelate (fastblur.go) provides FastBlur, a 3-pass box-blur
// approximation of a Gaussian (Kovesi, "Fast Almost-Gaussian Filtering"). Each
// box pass is O(1) per pixel via a running sum, so the cost is independent of
// sigma — unlike the exact separable GaussianBlur whose cost grows with radius.
//
// For generate-and-test the absolute score matters less than the ranking: every
// candidate is blurred the same way, so the correct text still blurs closest to
// a (true-Gaussian) target. The blur recovery matrix proves recall is preserved.
package pixelate

import (
	"image"
	"math"
)

// FastBlur implements unpixel.Pixelator as a 3-box-pass Gaussian approximation.
type FastBlur struct {
	radii [3]int
	sigma float64
}

// NewFastBlur returns a fast (box-approximated) Gaussian blur for the given
// standard deviation. The three box radii are chosen so the passes approximate a
// Gaussian of that sigma. Sigma is clamped to a small positive minimum.
func NewFastBlur(sigma float64) *FastBlur {
	if sigma < 0.1 {
		sigma = 0.1
	}
	return &FastBlur{radii: boxRadiiForGauss(sigma), sigma: sigma}
}

// Sigma returns the approximated blur's standard deviation in pixels.
func (f *FastBlur) Sigma() float64 { return f.sigma }

// boxRadiiForGauss returns three box-blur radii whose successive application
// approximates a Gaussian of the given sigma (Kovesi's closed form for n=3).
func boxRadiiForGauss(sigma float64) [3]int {
	const n = 3.0
	wIdeal := math.Sqrt(12*sigma*sigma/n + 1)
	wl := int(math.Floor(wIdeal))
	if wl%2 == 0 {
		wl--
	}
	wu := wl + 2
	mIdeal := (12*sigma*sigma - n*float64(wl*wl) - 4*n*float64(wl) - 3*n) / (-4*float64(wl) - 4)
	m := int(math.Round(mIdeal))

	var radii [3]int
	for i := range 3 {
		w := wu
		if i < m {
			w = wl
		}
		radii[i] = max(0, (w-1)/2)
	}
	return radii
}

// Pixelate returns a box-approximated Gaussian blur of src (same bounds). The
// origin arguments are ignored: blur has no grid.
func (f *FastBlur) Pixelate(src *image.RGBA, _, _ int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return dst
	}

	// Work on a tight copy of src (origin 0,0) ping-ponged between two buffers.
	a := make([]uint8, w*h*4)
	for y := range h {
		so := src.PixOffset(b.Min.X, b.Min.Y+y)
		copy(a[y*w*4:y*w*4+w*4], src.Pix[so:so+w*4])
	}
	buf := make([]uint8, w*h*4)

	for _, r := range f.radii {
		if r <= 0 {
			continue
		}
		boxBlurH(a, buf, w, h, r)
		boxBlurV(buf, a, w, h, r)
	}
	copy(dst.Pix, a)
	return dst
}

// boxBlurH writes to dst the horizontal box blur of src (interleaved RGBA,
// w×h, radius r) with clamped edges, using a running sum (O(1) per pixel).
func boxBlurH(src, dst []uint8, w, h, r int) {
	win := 2*r + 1
	for y := range h {
		row := y * w * 4
		for c := range 4 {
			// Initial window sum at x=0 with left edge clamped to src[row+c].
			sum := (r + 1) * int(src[row+c])
			for x := 1; x <= r; x++ {
				sum += int(src[row+clampN(x, w)*4+c])
			}
			for x := range w {
				dst[row+x*4+c] = clampByteI(sum / win)
				add := src[row+clampN(x+r+1, w)*4+c]
				rem := src[row+clampN(x-r, w)*4+c]
				sum += int(add) - int(rem)
			}
		}
	}
}

// boxBlurV writes to dst the vertical box blur of src (same layout/contract).
func boxBlurV(src, dst []uint8, w, h, r int) {
	win := 2*r + 1
	stride := w * 4
	for x := range w {
		col := x * 4
		for c := range 4 {
			sum := (r + 1) * int(src[col+c])
			for y := 1; y <= r; y++ {
				sum += int(src[clampN(y, h)*stride+col+c])
			}
			for y := range h {
				dst[y*stride+col+c] = clampByteI(sum / win)
				add := src[clampN(y+r+1, h)*stride+col+c]
				rem := src[clampN(y-r, h)*stride+col+c]
				sum += int(add) - int(rem)
			}
		}
	}
}

// clampByteI clamps a non-negative-ish int average to a uint8 in [0, 255]. The
// box average is mathematically in range; the clamp also satisfies the overflow
// checker and guards against any rounding drift.
func clampByteI(v int) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// clampN clamps i to [0, n-1].
func clampN(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}
