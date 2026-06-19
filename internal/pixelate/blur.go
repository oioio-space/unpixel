// Package pixelate (blur.go) adds a Gaussian-blur redaction operator alongside
// the block-average pixelator. Blur is the other common way text is obscured,
// and — like mosaic — it is a deterministic function of its input, so the same
// generate-and-test attack applies: render a candidate, blur it with the same
// sigma, and compare. It implements unpixel.Pixelator so it drops into the
// existing pipeline (use BlockSize=1, which makes the grid/padding steps no-ops).
package pixelate

import (
	"image"
	"math"
)

// GaussianBlur implements unpixel.Pixelator as a separable Gaussian blur with
// clamped edges. The Pixelate origin arguments are ignored (blur has no grid).
type GaussianBlur struct {
	kernel []float64 // normalised 1-D weights, length 2*radius+1
	sigma  float64
	radius int
}

// NewGaussianBlur returns a Gaussian blur operator for the given standard
// deviation (in pixels). Sigma is clamped to a small positive minimum so the
// kernel is always well-formed; the radius is ceil(3*sigma), capturing >99.7%
// of the Gaussian's mass.
func NewGaussianBlur(sigma float64) *GaussianBlur {
	if sigma < 0.1 {
		sigma = 0.1
	}
	radius := int(math.Ceil(3 * sigma))
	kernel := make([]float64, 2*radius+1)
	var sum float64
	for i := -radius; i <= radius; i++ {
		w := math.Exp(-float64(i*i) / (2 * sigma * sigma))
		kernel[i+radius] = w
		sum += w
	}
	for i := range kernel {
		kernel[i] /= sum
	}
	return &GaussianBlur{kernel: kernel, sigma: sigma, radius: radius}
}

// Sigma returns the blur's standard deviation in pixels.
func (g *GaussianBlur) Sigma() float64 { return g.sigma }

// Pixelate returns a Gaussian-blurred copy of src (same bounds, origin 0,0).
// originX and originY are ignored: blur has no block grid. faithful to the
// B = K*L convolution model, with K a separable Gaussian and edges clamped.
func (g *GaussianBlur) Pixelate(src *image.RGBA, _, _ int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return dst
	}

	// Horizontal pass: src → tmp (float, 4 channels interleaved).
	tmp := make([]float64, w*h*4)
	for y := range h {
		rowOff := src.PixOffset(b.Min.X, b.Min.Y+y)
		for x := range w {
			var r, gg, bb, a float64
			for k := -g.radius; k <= g.radius; k++ {
				sx := clamp(x+k, w)
				p := rowOff + sx*4
				wk := g.kernel[k+g.radius]
				r += float64(src.Pix[p]) * wk
				gg += float64(src.Pix[p+1]) * wk
				bb += float64(src.Pix[p+2]) * wk
				a += float64(src.Pix[p+3]) * wk
			}
			t := (y*w + x) * 4
			tmp[t], tmp[t+1], tmp[t+2], tmp[t+3] = r, gg, bb, a
		}
	}

	// Vertical pass: tmp → dst.
	for y := range h {
		for x := range w {
			var r, gg, bb, a float64
			for k := -g.radius; k <= g.radius; k++ {
				sy := clamp(y+k, h)
				t := (sy*w + x) * 4
				wk := g.kernel[k+g.radius]
				r += tmp[t] * wk
				gg += tmp[t+1] * wk
				bb += tmp[t+2] * wk
				a += tmp[t+3] * wk
			}
			d := dst.PixOffset(x, y)
			dst.Pix[d] = round8(r)
			dst.Pix[d+1] = round8(gg)
			dst.Pix[d+2] = round8(bb)
			dst.Pix[d+3] = round8(a)
		}
	}
	return dst
}

// clamp returns i clamped to [0, n-1] (edge extension for the convolution).
func clamp(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// round8 rounds a float channel value to a uint8, clamped to [0, 255].
func round8(v float64) uint8 {
	v += 0.5
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}
