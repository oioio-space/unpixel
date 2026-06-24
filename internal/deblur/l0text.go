// Package deblur (l0text.go) implements non-blind L0-regularised text
// deblurring following Pan, Hu, Su, Yang — "Deblurring Text Images via
// L0-Regularized Intensity and Gradient Prior", CVPR 2014.
// https://jspan.github.io/projects/text-deblurring/
//
// The method is a one-shot front-end for blur recovery: given a blurred image B
// and a known (or estimated) Gaussian PSF K of standard deviation σ, it
// recovers the latent sharp image L by minimising the MAP energy:
//
//	L* = argmin_L  ‖K*L − B‖²  +  λ‖∇L‖₀  +  μ‖L − {fg,bg}‖₀
//
// The first term enforces consistency with the blur model, the second enforces
// sparsity of gradients (sharp glyph edges), and the third enforces a two-tone
// intensity prior (text is dark on a light background). Both L0 regularisers
// are solved by half-quadratic splitting (HQS): the L-subproblem becomes a
// Wiener deconvolution solved in the frequency domain (FFT); the auxiliary
// variables for gradient sparsity and intensity two-tone are solved by
// hard-thresholding.
//
// This implementation uses a self-contained radix-2 Cooley-Tukey FFT (no
// external dependencies; pure Go, no CGO). Images are zero-padded to the next
// power-of-two size for the FFT and cropped back afterward.
package deblur

import (
	"image"
	"math"
	"math/cmplx"
)

// ── Public API ────────────────────────────────────────────────────────────────

// L0Options enables L0 text deblurring as a preprocessing step in blur
// recovery. When passed to [RecoverBlurredPreprocess], [TextL0] is applied to
// the input image before the σ-search. All fields are optional; zero values
// select sensible defaults.
type L0Options struct {
	// Sigma is the Gaussian blur standard deviation used as the PSF. When zero,
	// the caller should set it to the estimated or known σ before calling
	// RecoverBlurredPreprocess.
	Sigma float64
	// Lambda is the gradient L0 sparsity weight (‖∇L‖₀ term). Default: 2e-3.
	Lambda float64
	// Mu is the intensity two-tone prior weight (‖L−{fg,bg}‖₀ term). Default: 5e-4.
	Mu float64
	// Iterations is the number of outer HQS iterations. Default: 20.
	Iterations int
}

// L0Option is a functional option for [TextL0].
type L0Option func(*L0Options)

// WithL0Lambda sets the gradient L0 sparsity weight λ (the ‖∇L‖₀ term).
// Larger values impose stronger gradient sparsity (sharper edges, more
// aggressive L0 thresholding). The paper's default is 2×10⁻³.
func WithL0Lambda(lambda float64) L0Option {
	return func(o *L0Options) { o.Lambda = lambda }
}

// WithL0Mu sets the intensity two-tone prior weight μ (the ‖L−{fg,bg}‖₀ term).
// Larger values push pixel intensities harder toward the binary fg/bg values.
// The paper's default is 5×10⁻⁴.
func WithL0Mu(mu float64) L0Option {
	return func(o *L0Options) { o.Mu = mu }
}

// WithL0Iterations sets the number of outer HQS alternating-minimisation
// iterations. 10–30 is typical; more iterations sharpen further but cost more.
// Default: 20.
func WithL0Iterations(n int) L0Option {
	return func(o *L0Options) { o.Iterations = n }
}

// TextL0 sharpens src using the non-blind L0-regularised text-deblurring
// method of Pan et al. (CVPR 2014). The Gaussian PSF is parameterised by sigma
// (standard deviation in pixels); the caller must supply the known or estimated
// σ (e.g. from [unpixel.InferBlurSigma] or from the σ-search grid).
//
// The method minimises:
//
//	L* = argmin_L  ‖K*L − B‖²  +  λ‖∇L‖₀  +  μ‖L − {fg,bg}‖₀
//
// by half-quadratic splitting (HQS): the L-subproblem is a Wiener deconvolution
// solved via FFT; the gradient- and intensity-auxiliary subproblems are solved by
// hard thresholding. Operates on luminance; the result is greyscale-on-white.
//
// Constraints:
//   - Does not modify src; returns a freshly allocated *image.RGBA.
//   - Output is always greyscale (R=G=B) with A=255.
//   - Deterministic for the same inputs.
//   - sigma ≤ 0 returns a pixel-for-pixel copy of src.
//   - Empty image (w=0 or h=0) returns an empty image with no panic.
//
// Cite: Pan, Hu, Su, Yang. "Deblurring Text Images via L0-Regularized Intensity
// and Gradient Prior." CVPR 2014. https://jspan.github.io/projects/text-deblurring/
func TextL0(src *image.RGBA, sigma float64, opts ...L0Option) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	if sigma <= 0 || w == 0 || h == 0 {
		copy(dst.Pix, src.Pix)
		return dst
	}

	// Apply options.
	o := L0Options{
		Lambda:     2e-3,
		Mu:         5e-4,
		Iterations: 20,
	}
	for _, fn := range opts {
		fn(&o)
	}
	if o.Iterations <= 0 {
		o.Iterations = 20
	}

	// Extract luminance plane [0, 255].
	lum := extractLum(src, w, h)

	// Run the L0 HQS deblurring on luminance.
	sharp := l0Deblur(lum, w, h, sigma, o.Lambda, o.Mu, o.Iterations)

	// Write greyscale-on-white output.
	for i, v := range sharp {
		g := round8clamp(v)
		dst.Pix[i*4] = g
		dst.Pix[i*4+1] = g
		dst.Pix[i*4+2] = g
		dst.Pix[i*4+3] = 255
	}
	return dst
}

// RecoverBlurredPreprocess applies L0 text deblurring as an opt-in front-end
// for blur recovery. When opts is nil (default-off), it returns a fresh
// pixel-for-pixel copy of src — the existing blur path is byte-identical.
// When opts is non-nil, it calls [TextL0] with opts.Sigma, Lambda, Mu,
// and Iterations, and returns the sharpened image for use as the input to the
// σ-search/decode pipeline.
//
// Wire into [RecoverBlurred] via Config.l0deblur (set by [WithL0Deblur]) so
// the sharpened image is used throughout the σ-search and candidate generation.
func RecoverBlurredPreprocess(src *image.RGBA, opts *L0Options) *image.RGBA {
	if opts == nil {
		// Default-off: byte-identical copy.
		dst := image.NewRGBA(src.Bounds())
		copy(dst.Pix, src.Pix)
		return dst
	}
	return TextL0(
		src, opts.Sigma,
		WithL0Lambda(opts.Lambda),
		WithL0Mu(opts.Mu),
		WithL0Iterations(opts.Iterations),
	)
}

// ── Core L0-HQS algorithm ────────────────────────────────────────────────────

// l0Deblur implements the Pan et al. CVPR 2014 half-quadratic splitting loop.
//
// Energy:  E(L) = ‖K*L − B‖²  +  λ‖∇L‖₀  +  μ‖L − {fg,bg}‖₀
//
// HQS introduces auxiliary variables:
//   - g ≈ ∇L   (gradient proxy; solved by hard-threshold at √(λ/β₁))
//   - s ≈ L    (intensity proxy; solved by hard-threshold to {fg,bg} at √(μ/β₂))
//
// β₁, β₂ are penalty parameters that increase each iteration (2× per step).
// The L-subproblem is:
//
//	(‖K‖²_F + β₁‖D‖²_F + β₂) L̂ = K̄ B̂  +  β₁ D̄ ĝ  +  β₂ ŝ
//
// where D is the finite-difference operator, all operations are element-wise
// in the Fourier domain, and bar denotes complex conjugate.
func l0Deblur(lum []float64, w, h int, sigma, lambda, mu float64, iters int) []float64 {
	// Pad to next power-of-two for FFT efficiency.
	pw := nextPow2(w)
	ph := nextPow2(h)
	n := pw * ph

	// Build the 2D PSF K in spatial domain (same Gaussian as GaussianBlur).
	// Place the kernel centered at (0,0) with wrap-around (standard DFT convention).
	psf := make([]float64, n)
	buildGaussianPSF(psf, pw, ph, sigma)

	// Precompute K̂ (FFT of PSF) and |K̂|².
	kHat := realFFT2D(psf, pw, ph)
	kHatConj := make([]complex128, n)
	kHatSq := make([]float64, n)
	for i, v := range kHat {
		kHatConj[i] = cmplx.Conj(v)
		kHatSq[i] = real(v)*real(v) + imag(v)*imag(v)
	}

	// Precompute D̂ — the DFT of the finite-difference (gradient) operator.
	// In 2D we use horizontal (Dx) and vertical (Dy) separately.
	// D̂x[i,j] = exp(2πi·j/pw) − 1,  D̂y[i,j] = exp(2πi·i/ph) − 1.
	dxHat := make([]complex128, n)
	dyHat := make([]complex128, n)
	for fy := range ph {
		for fx := range pw {
			dxHat[fy*pw+fx] = cmplx.Exp(complex(0, 2*math.Pi*float64(fx)/float64(pw))) - 1
			dyHat[fy*pw+fx] = cmplx.Exp(complex(0, 2*math.Pi*float64(fy)/float64(ph))) - 1
		}
	}
	dxHatConj := conjSlice(dxHat)
	dyHatConj := conjSlice(dyHat)
	dxHatSq := absSqSlice(dxHat)
	dyHatSq := absSqSlice(dyHat)

	// Pad blurred image B and compute B̂.
	bPad := padImage(lum, w, h, pw, ph)
	bHat := realFFT2D(bPad, pw, ph)

	// Initialise latent image = blurred input (the HQS starting point).
	latent := make([]float64, n)
	copy(latent, bPad)

	// HQS penalty parameters; doubled each iteration.
	beta1 := 2 * lambda // gradient penalty
	beta2 := 2 * mu     // intensity penalty

	gx := make([]float64, n) // gradient auxiliary x
	gy := make([]float64, n) // gradient auxiliary y
	s := make([]float64, n)  // intensity auxiliary

	for range iters {
		// ── Step 1: update g (gradient auxiliary) by L0 hard-thresholding ──
		// g* = ∇latent  if (∂x·latent)²+(∂y·latent)² > λ/β₁, else 0.
		thresh1 := lambda / beta1
		computeGradients(latent, gx, gy, pw, ph)
		for i := range n {
			mag2 := gx[i]*gx[i] + gy[i]*gy[i]
			if mag2 <= thresh1 {
				gx[i] = 0
				gy[i] = 0
			}
		}

		// ── Step 2: update s (intensity auxiliary) by two-tone hard-threshold ──
		// Find fg (dark) and bg (light) as the two-cluster means of latent.
		// s* = nearest cluster centre if |latent[i]−cluster|² > μ/β₂, else latent[i].
		fg, bg := twoToneMeans(latent)
		thresh2 := mu / beta2
		for i, v := range latent {
			dFg := v - fg
			dBg := v - bg
			distFg := dFg * dFg
			distBg := dBg * dBg
			// Distance to the nearest cluster.
			nearDist := min(distFg, distBg)
			if nearDist > thresh2 {
				// Snap to nearest cluster.
				if distFg <= distBg {
					s[i] = fg
				} else {
					s[i] = bg
				}
			} else {
				s[i] = v
			}
		}

		// ── Step 3: update latent image via Wiener deconvolution in FFT ──
		// latent̂ = (K̄·B̂  +  β₁·(D̄x·ĝx + D̄y·ĝy)  +  β₂·ŝ)
		//           / (|K̂|²  +  β₁·(|D̂x|² + |D̂y|²)  +  β₂)
		gxHat := realFFT2D(gx, pw, ph)
		gyHat := realFFT2D(gy, pw, ph)
		sHat := realFFT2D(s, pw, ph)

		lHat := make([]complex128, n)
		for i := range n {
			num := kHatConj[i]*bHat[i] +
				complex(beta1, 0)*(dxHatConj[i]*gxHat[i]+dyHatConj[i]*gyHat[i]) +
				complex(beta2, 0)*sHat[i]
			denom := complex(kHatSq[i]+beta1*(dxHatSq[i]+dyHatSq[i])+beta2, 0)
			lHat[i] = num / denom
		}

		// Inverse FFT → new latent estimate, clamped to [0,255].
		lNew := realIFFT2D(lHat, pw, ph)
		for i, v := range lNew {
			latent[i] = max(0, min(255, v))
		}

		// Increase penalty parameters (geometric schedule: 2× per step).
		beta1 *= 2
		beta2 *= 2
	}

	// Crop back to original w×h.
	return cropImage(latent, pw, w, h)
}

// ── Helper: Gaussian PSF ──────────────────────────────────────────────────────

// buildGaussianPSF fills a pw×ph float64 buffer with a normalised Gaussian PSF
// centred at (0,0) with wrap-around (DFT convention: the PSF origin is at the
// top-left corner, i.e. using circular/wrap-around shift).
func buildGaussianPSF(psf []float64, pw, ph int, sigma float64) {
	radius := int(math.Ceil(3 * sigma))
	var sum float64
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			v := math.Exp(-float64(dx*dx+dy*dy) / (2 * sigma * sigma))
			// Wrap-around placement.
			px := (dx + pw) % pw
			py := (dy + ph) % ph
			psf[py*pw+px] += v
			sum += v
		}
	}
	if sum > 0 {
		for i := range psf {
			psf[i] /= sum
		}
	}
}

// ── Gradient and two-tone helpers ─────────────────────────────────────────────

// computeGradients fills gx and gy with the forward finite differences of pix
// (wrap-around boundary — consistent with the DFT frequency-domain operator).
func computeGradients(pix, gx, gy []float64, w, h int) {
	for y := range h {
		for x := range w {
			i := y*w + x
			xp1 := (x + 1) % w
			yp1 := (y + 1) % h
			gx[i] = pix[y*w+xp1] - pix[i]
			gy[i] = pix[yp1*w+x] - pix[i]
		}
	}
}

// twoToneMeans estimates the dark (fg) and light (bg) cluster means from pix
// using a split at the mean. Text images are bimodal: strokes are dark,
// background is light. The split at the mean gives a robust initial clustering.
func twoToneMeans(pix []float64) (fg, bg float64) {
	var sumFg, sumBg float64
	var nFg, nBg int
	// Compute the mean as the split threshold.
	var total float64
	for _, v := range pix {
		total += v
	}
	thresh := total / float64(len(pix))

	for _, v := range pix {
		if v < thresh {
			sumFg += v
			nFg++
		} else {
			sumBg += v
			nBg++
		}
	}
	if nFg > 0 {
		fg = sumFg / float64(nFg)
	}
	if nBg > 0 {
		bg = sumBg / float64(nBg)
	} else {
		bg = 255
	}
	return fg, bg
}

// ── Image padding / cropping ─────────────────────────────────────────────────

// padImage zero-pads a w×h float64 image into a pw×ph buffer (top-left aligned).
func padImage(src []float64, w, h, pw, ph int) []float64 {
	dst := make([]float64, pw*ph)
	for y := range h {
		copy(dst[y*pw:y*pw+w], src[y*w:(y+1)*w])
	}
	return dst
}

// cropImage extracts the top-left w×h region from a pw×ph flat buffer.
func cropImage(src []float64, pw, w, h int) []float64 {
	dst := make([]float64, w*h)
	for y := range h {
		copy(dst[y*w:y*w+w], src[y*pw:y*pw+w])
	}
	return dst
}

// extractLum extracts BT.601 luminance from an *image.RGBA into a float64 slice.
func extractLum(src *image.RGBA, w, h int) []float64 {
	b := src.Bounds()
	lum := make([]float64, w*h)
	for y := range h {
		for x := range w {
			c := src.RGBAAt(b.Min.X+x, b.Min.Y+y)
			lum[y*w+x] = 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
		}
	}
	return lum
}

// round8clamp rounds a float64 to uint8, clamped to [0, 255].
func round8clamp(v float64) uint8 {
	v += 0.5
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// ── Self-contained radix-2 Cooley-Tukey FFT ──────────────────────────────────
//
// This is a pure-Go, dependency-free 1D FFT and its 2D row-column extension.
// Sizes must be powers of two (callers pad to nextPow2 before calling).
// For sizes used here (≤512) the full O(N log N) computation is fast enough;
// no SIMD or assembly is needed.

// nextPow2 returns the smallest power of two ≥ n.
func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// fft1D computes the in-place DFT of x (length must be a power of two).
// Uses Cooley-Tukey decimation-in-time with bit-reversal permutation.
func fft1D(x []complex128) {
	n := len(x)
	if n <= 1 {
		return
	}
	// Bit-reversal permutation.
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	// Cooley-Tukey butterfly stages.
	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		// Twiddle factor: w = e^{-2πi/length}.
		wBase := cmplx.Exp(complex(0, -2*math.Pi/float64(length)))
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for k := range half {
				u := x[i+k]
				v := w * x[i+k+half]
				x[i+k] = u + v
				x[i+k+half] = u - v
				w *= wBase
			}
		}
	}
}

// ifft1D computes the in-place inverse DFT of x (length must be a power of two).
func ifft1D(x []complex128) {
	// Conjugate → FFT → conjugate → scale.
	for i, v := range x {
		x[i] = cmplx.Conj(v)
	}
	fft1D(x)
	n := complex(float64(len(x)), 0)
	for i, v := range x {
		x[i] = cmplx.Conj(v) / n
	}
}

// realFFT2D computes the 2D DFT of the real-valued pw×ph image stored row-major
// in src. Returns the complex spectrum as a []complex128 of length pw*ph.
func realFFT2D(src []float64, pw, ph int) []complex128 {
	spec := make([]complex128, pw*ph)
	for i, v := range src {
		spec[i] = complex(v, 0)
	}
	row := make([]complex128, pw)
	// Row-wise FFTs.
	for y := range ph {
		copy(row, spec[y*pw:(y+1)*pw])
		fft1D(row)
		copy(spec[y*pw:(y+1)*pw], row)
	}
	// Column-wise FFTs.
	col := make([]complex128, ph)
	for x := range pw {
		for y := range ph {
			col[y] = spec[y*pw+x]
		}
		fft1D(col)
		for y := range ph {
			spec[y*pw+x] = col[y]
		}
	}
	return spec
}

// realIFFT2D computes the 2D inverse DFT of spec (pw×ph complex spectrum) and
// returns the real part as a []float64 of length pw*ph.
func realIFFT2D(spec []complex128, pw, ph int) []float64 {
	x := make([]complex128, pw*ph)
	copy(x, spec)
	row := make([]complex128, pw)
	// Row-wise IFFTs.
	for y := range ph {
		copy(row, x[y*pw:(y+1)*pw])
		ifft1D(row)
		copy(x[y*pw:(y+1)*pw], row)
	}
	// Column-wise IFFTs.
	col := make([]complex128, ph)
	for xc := range pw {
		for y := range ph {
			col[y] = x[y*pw+xc]
		}
		ifft1D(col)
		for y := range ph {
			x[y*pw+xc] = col[y]
		}
	}
	out := make([]float64, pw*ph)
	for i, v := range x {
		out[i] = real(v)
	}
	return out
}

// conjSlice returns a new slice of complex conjugates.
func conjSlice(x []complex128) []complex128 {
	out := make([]complex128, len(x))
	for i, v := range x {
		out[i] = cmplx.Conj(v)
	}
	return out
}

// absSqSlice returns |x[i]|² as a []float64.
func absSqSlice(x []complex128) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = real(v)*real(v) + imag(v)*imag(v)
	}
	return out
}
