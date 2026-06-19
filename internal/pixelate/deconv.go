// Package pixelate (deconv.go) provides RichardsonLucy, a spatial-domain
// iterative deconvolution that sharpens a Gaussian-blurred image.
package pixelate

import (
	"image"
	"sync"
)

// rlFloatPool pools the per-channel float64 scratch slices used by
// RichardsonLucy. Each entry holds w*h float64 values and is fully
// overwritten before any read, so no zeroing is needed between uses.
var rlFloatPool = sync.Pool{
	New: func() any { return new([]float64) },
}

// RichardsonLucy applies iterative Richardson-Lucy (RL) deconvolution to src,
// sharpening a Gaussian blur of the given sigma. It returns a freshly
// allocated *image.RGBA of the same bounds as src.
//
// Algorithm: Richardson (1972) / Lucy (1974). Given observed image g and a
// symmetric Gaussian PSF h of standard deviation sigma, the estimate f is
// refined iteratively in the spatial domain:
//
//	f_0    = g
//	f_{k+1} = f_k ⊙ ( h ⊛ ( g / (h ⊛ f_k) ) )
//
// where ⊛ is (separable) convolution, ⊙ is element-wise multiplication, and
// / is element-wise division. Because h is symmetric (Gaussian), its flip is
// identical, so only one kernel is needed. Each colour channel (R, G, B) is
// processed independently in float64; alpha is copied verbatim. Results are
// clamped to [0, 255] before conversion. Divide-by-zero is guarded with a
// small epsilon (1e-10).
//
// Scratch buffers are borrowed from a sync.Pool and returned before
// RichardsonLucy returns, keeping allocations to one fresh output image plus
// four pooled float64 slices (current estimate, temporary convolution buffer,
// the ratio, and the intermediate H pass), none of which escape.
//
// Contracts:
//   - iterations <= 0 or sigma <= 0: returns a pixel-for-pixel copy of src.
//   - Empty image (w or h == 0): returns an empty image with no panic.
//   - Allocates exactly one new *image.RGBA; does not modify src.
func RichardsonLucy(src *image.RGBA, sigma float64, iterations int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	if iterations <= 0 || sigma <= 0 || w == 0 || h == 0 {
		// Return a copy of src (including empty case).
		copy(dst.Pix, src.Pix)
		return dst
	}

	// Build the 1-D Gaussian kernel (same construction as GaussianBlur).
	gb := NewGaussianBlur(sigma)
	kernel := gb.kernel
	radius := gb.radius
	n := w * h

	// Borrow four float64 scratch slices from the pool.
	pF := rlFloatPool.Get().(*[]float64)   // current estimate f
	pG := rlFloatPool.Get().(*[]float64)   // observed g (constant)
	pTmp := rlFloatPool.Get().(*[]float64) // h ⊛ f (or intermediate)
	pRat := rlFloatPool.Get().(*[]float64) // ratio then corrector
	for _, p := range []*[]float64{pF, pG, pTmp, pRat} {
		if cap(*p) < n {
			*p = make([]float64, n)
		}
	}
	f := (*pF)[:n]
	g := (*pG)[:n]
	tmp := (*pTmp)[:n]
	rat := (*pRat)[:n]

	// Process each colour channel independently.
	for ch := range 3 {
		// Populate g and initialise f = g.
		for i := range n {
			v := float64(src.Pix[i*4+ch])
			g[i] = v
			f[i] = v
		}

		for range iterations {
			// tmp = h ⊛ f  (separable: horizontal then vertical).
			gaussConvH(f, tmp, kernel, radius, w, h)
			gaussConvV(tmp, rat, kernel, radius, w, h) // rat holds h⊛f temporarily

			// rat = g / (h⊛f), element-wise, epsilon-guarded.
			const eps = 1e-10
			for i := range n {
				denom := rat[i]
				if denom < eps {
					denom = eps
				}
				rat[i] = g[i] / denom
			}

			// tmp = h ⊛ rat  (correction factor; h symmetric so h_flip == h).
			gaussConvH(rat, tmp, kernel, radius, w, h)
			gaussConvV(tmp, rat, kernel, radius, w, h) // rat holds corrector

			// f_{k+1} = f_k ⊙ corrector, clamped to [0,255].
			for i := range n {
				v := f[i] * rat[i]
				if v < 0 {
					v = 0
				} else if v > 255 {
					v = 255
				}
				f[i] = v
			}
		}

		// Write channel back to dst; copy alpha from src.
		for i := range n {
			dst.Pix[i*4+ch] = round8(f[i])
		}
	}

	// Copy alpha channel verbatim.
	for i := range n {
		dst.Pix[i*4+3] = src.Pix[i*4+3]
	}

	rlFloatPool.Put(pF)
	rlFloatPool.Put(pG)
	rlFloatPool.Put(pTmp)
	rlFloatPool.Put(pRat)
	return dst
}

// gaussConvH writes to dst the horizontal separable Gaussian convolution of
// src (stored as a flat []float64, w×h, one channel) using the given kernel
// and radius. Edges are clamped (replicate-border).
func gaussConvH(src, dst []float64, kernel []float64, radius, w, h int) {
	for y := range h {
		rowOff := y * w
		for x := range w {
			var acc float64
			for k := -radius; k <= radius; k++ {
				sx := clamp(x+k, w)
				acc += src[rowOff+sx] * kernel[k+radius]
			}
			dst[rowOff+x] = acc
		}
	}
}

// gaussConvV writes to dst the vertical separable Gaussian convolution of src
// (same layout/contract as gaussConvH).
func gaussConvV(src, dst []float64, kernel []float64, radius, w, h int) {
	for y := range h {
		for x := range w {
			var acc float64
			for k := -radius; k <= radius; k++ {
				sy := clamp(y+k, h)
				acc += src[sy*w+x] * kernel[k+radius]
			}
			dst[y*w+x] = acc
		}
	}
}
