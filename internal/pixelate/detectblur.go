package pixelate

import (
	"image"
	"math"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// BlurKind classifies the redaction's forward operator family.
type BlurKind uint8

const (
	// BlurKindUnknown is returned when block < 2 or the image is degenerate.
	BlurKindUnknown BlurKind = iota
	// BlurKindMosaic indicates a block-average (mosaic/pixelate) redaction.
	BlurKindMosaic
	// BlurKindGaussian indicates a Gaussian (or box-approximate) blur redaction.
	BlurKindGaussian
)

// BlurKernel distinguishes a true Gaussian from a 3-pass box approximation.
type BlurKernel uint8

const (
	// BlurKernelUnknown means the kernel could not be determined.
	BlurKernelUnknown BlurKernel = iota
	// BlurKernelTrueGauss is an exact separable Gaussian convolution.
	BlurKernelTrueGauss
	// BlurKernelBox3 is a 3-pass box-blur approximation (FastBlur / GIMP).
	BlurKernelBox3
)

// BlurInfo is the result of [DetectBlur].
type BlurInfo struct {
	Kind   BlurKind
	Sigma  float64    // meaningful when Kind == BlurKindGaussian
	Kernel BlurKernel // meaningful when Kind == BlurKindGaussian
	Conf   float64    // confidence of Kind classification in [0,1]
}

// mosaicVarEps is the intra-block luminance variance threshold (8-bit² units)
// below which an image is classified as mosaic. A perfectly mosaiced block has
// variance 0; a value of 4.0 allows for JPEG noise while staying well below
// the variance of any meaningful blur.
const mosaicVarEps = 4.0

// confScale is the denominator for the tanh confidence curve. At variance
// distance 5.0 from mosaicVarEps, tanh(1)≈0.76; at 10, tanh(2)≈0.96.
const confScale = 5.0

// erf1090Factor is the ratio of the 10–90 % rise width of a Gaussian error
// function to its standard deviation σ. For erf: rise width = 2·Φ⁻¹(0.9)·σ
// where Φ⁻¹(0.9) ≈ 1.2816, giving factor ≈ 2·1.2816 = 2.563.
const erf1090Factor = 2.563

// DetectBlur classifies whether redacted is a mosaic or Gaussian-blur redaction,
// estimates sigma for the Gaussian case, and guesses the kernel family.
//
// block is the inferred mosaic block size (≥ 2). Pass 0 (or any value < 2) when
// unknown — mosaic detection then uses average intra-block variance computed over
// an 8-pixel default grid.
//
// The mosaic detector measures mean intra-block luminance variance: a perfect
// mosaic has variance 0 per block, while a blurred image retains smooth
// gradients. Decision boundary: meanIntraVar < mosaicVarEps ⇒ mosaic.
//
// The blur sigma estimator measures the 10–90 % rise width of the sharpest
// horizontal edge and converts it via sigma = riseWidth / 2.563.
//
// Confidence is tanh(|meanIntraVar − mosaicVarEps| / confScale), always in [0,1].
func DetectBlur(redacted *image.RGBA, block int) BlurInfo {
	if block < 2 {
		block = 8 // autocorrelation fallback grid size
	}
	b := redacted.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return BlurInfo{}
	}

	meanVar := meanIntraBlockVariance(redacted, block)
	conf := math.Tanh(math.Abs(meanVar-mosaicVarEps) / confScale)

	if meanVar < mosaicVarEps {
		return BlurInfo{Kind: BlurKindMosaic, Conf: conf}
	}

	// Blur branch: estimate sigma from the sharpest horizontal edge.
	sigma := estimateSigmaFromEdge(redacted)
	kernel := guessKernel(redacted, sigma)
	return BlurInfo{Kind: BlurKindGaussian, Sigma: sigma, Kernel: kernel, Conf: conf}
}

// meanIntraBlockVariance computes the mean per-block luminance variance over the
// block grid, using BT.601 luminance for each pixel.
func meanIntraBlockVariance(img *image.RGBA, block int) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	ox, oy := b.Min.X, b.Min.Y
	stride := img.Stride
	pix := img.Pix

	var totalVar float64
	nBlocks := 0

	for by := 0; by < h; by += block {
		y0, y1 := by, min(by+block, h)
		for bx := 0; bx < w; bx += block {
			x0, x1 := bx, min(bx+block, w)
			n := (x1 - x0) * (y1 - y0)
			if n == 0 {
				continue
			}

			// Two-pass variance: compute mean luminance then sum squared deviations.
			var lumSum int
			for y := oy + y0; y < oy+y1; y++ {
				off := y*stride + (ox+x0)*4
				for range x1 - x0 {
					r, g, bv := pix[off], pix[off+1], pix[off+2]
					lumSum += imutil.Lum601(r, g, bv)
					off += 4
				}
			}
			mean := float64(lumSum) / float64(n)

			var varSum float64
			for y := oy + y0; y < oy+y1; y++ {
				off := y*stride + (ox+x0)*4
				for range x1 - x0 {
					r, g, bv := pix[off], pix[off+1], pix[off+2]
					lum := float64(imutil.Lum601(r, g, bv))
					d := lum - mean
					varSum += d * d
					off += 4
				}
			}
			totalVar += varSum / float64(n)
			nBlocks++
		}
	}

	if nBlocks == 0 {
		return 0
	}
	return totalVar / float64(nBlocks)
}

// estimateSigmaFromEdge finds the sharpest horizontal luminance gradient row,
// measures its 10–90 % rise width, and converts to sigma via erf1090Factor.
func estimateSigmaFromEdge(img *image.RGBA) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 3 {
		return 1.0
	}
	ox, oy := b.Min.X, b.Min.Y
	stride := img.Stride
	pix := img.Pix

	// Collect per-row horizontal gradient magnitude sums and pick the max-gradient row.
	bestRow := -1
	bestGrad := 0.0
	for y := range h {
		var gradSum float64
		rowOff := (oy+y)*stride + ox*4
		for x := 1; x < w; x++ {
			off0 := rowOff + (x-1)*4
			off1 := rowOff + x*4
			r0, g0, b0 := pix[off0], pix[off0+1], pix[off0+2]
			r1, g1, b1 := pix[off1], pix[off1+1], pix[off1+2]
			d := float64(imutil.Lum601(r1, g1, b1)) - float64(imutil.Lum601(r0, g0, b0))
			gradSum += math.Abs(d)
		}
		if gradSum > bestGrad {
			bestGrad = gradSum
			bestRow = y
		}
	}
	if bestRow < 0 || bestGrad == 0 {
		return 1.0
	}

	// Extract the luminance profile for that row.
	lum := make([]float64, w)
	off := (oy+bestRow)*stride + ox*4
	for x := range w {
		r, g, bv := pix[off], pix[off+1], pix[off+2]
		lum[x] = float64(imutil.Lum601(r, g, bv))
		off += 4
	}

	// Find the global min and max to normalise the profile.
	lo, hi := lum[0], lum[0]
	for _, v := range lum[1:] {
		lo = min(lo, v)
		hi = max(hi, v)
	}
	rng := hi - lo
	if rng < 1 {
		return 1.0
	}

	// 10 % and 90 % thresholds (in luminance units).
	t10 := lo + 0.10*rng
	t90 := lo + 0.90*rng

	// Find leftmost x where lum >= t10 and leftmost x where lum >= t90,
	// handling both rising and falling edges by working with the profile's
	// direction (we search for the steepest transition).
	x10, x90 := -1, -1
	for x, v := range lum {
		if x10 < 0 && v >= t10 {
			x10 = x
		}
		if x90 < 0 && v >= t90 {
			x90 = x
		}
	}
	// Fallback: falling edge (white→black).
	if x10 < 0 || x90 < 0 || x10 == x90 {
		// Try reversed direction (falling edge).
		x10, x90 = -1, -1
		for x, v := range lum {
			if x10 < 0 && v <= hi-0.10*rng {
				x10 = x
			}
			if x90 < 0 && v <= hi-0.90*rng {
				x90 = x
			}
		}
	}
	if x10 < 0 || x90 < 0 {
		return 1.0
	}
	riseWidth := math.Abs(float64(x90 - x10))
	if riseWidth < 0.5 {
		riseWidth = 0.5
	}
	return riseWidth / erf1090Factor
}

// guessKernel re-blurs a synthetic step with NewGaussianBlur(sigma) and
// NewFastBlur(sigma), compares each edge profile's sum-of-squared differences
// to the observed profile, and returns the closer kernel.
func guessKernel(img *image.RGBA, sigma float64) BlurKernel {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 4 || h < 1 {
		return BlurKernelTrueGauss
	}

	// Build a synthetic step image (left half white, right half black).
	step := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := byte(255)
			if x >= w/2 {
				c = 0
			}
			i := step.PixOffset(x, y)
			step.Pix[i], step.Pix[i+1], step.Pix[i+2], step.Pix[i+3] = c, c, c, 255
		}
	}

	gaussImg := NewGaussianBlur(sigma).Pixelate(step, 0, 0)
	fastImg := NewFastBlur(sigma).Pixelate(step, 0, 0)

	// Compare the middle row of each blurred step to the observed middle row.
	midY := h / 2
	ox, oy := b.Min.X, b.Min.Y
	stride := img.Stride

	var ssdGauss, ssdFast float64
	off := (oy+midY)*stride + ox*4
	for x := range w {
		obsLum := float64(imutil.Lum601(img.Pix[off], img.Pix[off+1], img.Pix[off+2]))
		gOff := gaussImg.PixOffset(x, midY)
		fOff := fastImg.PixOffset(x, midY)
		gLum := float64(imutil.Lum601(gaussImg.Pix[gOff], gaussImg.Pix[gOff+1], gaussImg.Pix[gOff+2]))
		fLum := float64(imutil.Lum601(fastImg.Pix[fOff], fastImg.Pix[fOff+1], fastImg.Pix[fOff+2]))
		dg := obsLum - gLum
		df := obsLum - fLum
		ssdGauss += dg * dg
		ssdFast += df * df
		off += 4
	}

	if ssdFast < ssdGauss {
		return BlurKernelBox3
	}
	return BlurKernelTrueGauss
}
