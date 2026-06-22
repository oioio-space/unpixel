package deblur

import (
	"image"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// BgModel controls how the estimated background is removed from the luminance
// plane during normalisation.
type BgModel int

const (
	// BgDivide divides each pixel by its estimated background luminance,
	// modelling a multiplicative illumination field (vignette, lens shading).
	// This is the default and works well for most real captures.
	BgDivide BgModel = iota
	// BgSubtract subtracts the estimated background luminance from each pixel,
	// modelling an additive illumination offset (flat-field correction).
	BgSubtract
	// BgNone skips background removal entirely. Only polarity, stretch, deblock,
	// and binarise are applied.
	BgNone
)

// InvertMode controls the polarity inversion step of [Normalize].
type InvertMode int

const (
	// InvertAuto inverts the image when the mean normalised luminance is below
	// 127, ensuring text ends up dark on a light background, which is what the
	// renderer produces. This is the default.
	InvertAuto InvertMode = iota
	// InvertForce always inverts the luminance, regardless of the image mean.
	InvertForce
	// InvertOff never inverts; the image is passed through as-is.
	InvertOff
)

// Options controls the normalisation pipeline applied by [Normalize].
// All fields are optional; zero values select the sensible default for each step.
type Options struct {
	// Bg selects the background-removal model. Default: BgDivide.
	Bg BgModel
	// OpenRadius is the half-size of the flat square structuring element used
	// for the morphological Open call that estimates the background. When 0,
	// Normalize picks max(8, imgH/8) automatically — large enough to bridge any
	// text stroke but small enough to track slow vignette gradients.
	OpenRadius int
	// Invert controls polarity correction. Default: InvertAuto.
	Invert InvertMode
	// Stretch enables 1st/99th-percentile contrast stretching after background
	// removal and polarity correction, mapping the actual dynamic range to
	// [0, 255]. Default: false — stretching sharpens the blur gradient that the
	// σ-search relies on, so enable it only for very low-contrast captures.
	Stretch bool
	// Deblock is the median-filter radius applied to suppress JPEG blocking
	// artefacts. 0 disables the filter. -1 triggers auto-selection: Normalize
	// uses radius 1 (3×3 kernel) when deblock artefacts are likely (i.e., when
	// the image was loaded as JPEG or the caller signals noisy input).
	Deblock int
	// Binarize, when true, thresholds the final luminance plane to {0, 255}
	// using the mean luminance as the threshold. Useful for very noisy or
	// low-contrast captures where the binary distinction text/background is more
	// reliable than grey values.
	Binarize bool
}

// DefaultOptions returns an Options with sensible defaults for real-world
// blurred-text captures: multiplicative background removal (BgDivide), automatic
// radius, auto polarity (InvertAuto), no contrast stretching (to preserve the
// Gaussian blur gradient needed by the σ-search), no JPEG deblocking by default
// (enable explicitly with Deblock=1 for JPEG inputs), and binarisation off.
//
// Stretch is intentionally off by default: contrast stretching sharpens the
// luminance gradient, which makes the image look less blurred and breaks the
// generate-and-test loop (candidates are blurred before comparison). Enable it
// only for very low-contrast captures where the text is barely visible.
func DefaultOptions() Options {
	return Options{
		Bg:     BgDivide,
		Invert: InvertAuto,
	}
}

// bgEpsilon guards against division by near-zero background estimates.
const bgEpsilon = 1.0

// Normalize prepares src for the generate-and-test blur-recovery loop by
// removing background luminance variation, correcting polarity, and optionally
// stretching contrast and suppressing JPEG blocking artefacts. It does NOT
// modify src; it always returns a freshly allocated *image.RGBA.
//
// Pipeline (in order):
//  1. Compute the BT.601 luminance plane L from src.
//  2. Polarity (before background removal): if InvertAuto and mean(L) < 127,
//     invert L = 255 − L so text is dark on light. InvertForce always inverts;
//     InvertOff skips. Polarity is corrected first so the background estimator
//     always operates on dark-text-on-light.
//  3. Background removal: B = Dilate(L, r) with r = o.OpenRadius or
//     max(12, imgH/4) when OpenRadius == 0. Flatten:
//     L' = clamp(L / max(B,ε) × 255) for BgDivide;
//     L' = clamp(L − B + 128) for BgSubtract; identity for BgNone.
//  4. Contrast stretch: if o.Stretch, map L' from [p1, p99] → [0, 255].
//  5. Deblock: if o.Deblock != 0, apply [imutil.Median] with radius
//     max(1, o.Deblock) (auto: radius 1 when Deblock == −1).
//  6. Binarize: if o.Binarize, threshold L' at mean(L') → {0, 255}.
//
// The output is a greyscale-on-white *image.RGBA: each pixel has R=G=B=L',
// A=255. This matches the flat-white assumption of the renderer so image
// distances in the recovery loop are meaningful.
func Normalize(src *image.RGBA, o Options) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	n := w * h
	if n == 0 {
		return image.NewRGBA(image.Rect(0, 0, w, h))
	}

	// Step 1: BT.601 luminance plane, [0,255].
	lum := make([]float64, n)
	for y := range h {
		for x := range w {
			c := src.RGBAAt(b.Min.X+x, b.Min.Y+y)
			lum[y*w+x] = 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
		}
	}

	// Step 2: polarity correction — applied BEFORE background removal so the
	// background estimator always operates on dark-text-on-light, regardless of
	// the original image polarity. Dilate finds the bright background; if the
	// image were light-on-dark, Dilate would find the bright text, not the dark
	// background, giving the wrong estimate.
	//
	// InvertAuto: invert when mean < 127 (predominantly dark → light-on-dark).
	// InvertForce: always invert. InvertOff: skip.
	switch o.Invert {
	case InvertAuto:
		if mean(lum) < 127.0 {
			invert(lum)
		}
	case InvertForce:
		invert(lum)
	case InvertOff:
		// no-op
	}

	// Step 3: background estimation and removal.
	//
	// Background estimator: Dilate(L, r). For dark-text-on-light background
	// (the canonical case after the polarity step above), dilation with a radius
	// larger than the stroke width returns the bright background luminance at
	// every pixel — dark strokes are "filled in" by the surrounding bright
	// background. This is more robust than morphological Open for blurred text
	// because blur spreads strokes into a smooth gradient: Open cannot detect
	// the stroke extent (the gradient is never a flat minimum), but Dilate
	// always finds the bright background within the window as long as
	// r ≥ half-stroke-width.
	if o.Bg != BgNone {
		r := o.OpenRadius
		if r <= 0 {
			// Auto radius: choose a value larger than the widest expected text
			// stroke (roughly imgH/4 at 32pt with 8px padding).
			r = max(12, h/4)
		}
		bg := Dilate(lum, w, h, r)
		switch o.Bg {
		case BgDivide:
			for i := range n {
				denom := max(bg[i], bgEpsilon)
				lum[i] = clamp255(lum[i] / denom * 255.0)
			}
		case BgSubtract:
			for i := range n {
				lum[i] = clamp255(lum[i] - bg[i] + 128.0)
			}
		}
	}

	// Step 4: contrast stretch.
	if o.Stretch {
		stretchContrast(lum)
	}

	// Step 5: JPEG deblocking via median filter.
	// Build a temporary RGBA image from the luminance plane, apply the median
	// filter through imutil.Median, then read back.
	deblockRadius := 0
	switch {
	case o.Deblock > 0:
		deblockRadius = o.Deblock
	case o.Deblock < 0: // auto
		deblockRadius = 1
	}
	if deblockRadius > 0 {
		tmp := lumToRGBA(lum, w, h)
		filtered := imutil.Median(tmp, deblockRadius)
		readLumFromRGBA(filtered, lum, w, h)
	}

	// Step 6: binarise.
	if o.Binarize {
		thresh := mean(lum)
		for i := range n {
			if lum[i] < thresh {
				lum[i] = 0
			} else {
				lum[i] = 255
			}
		}
	}

	// Render greyscale-on-white RGBA output.
	return lumToRGBA(lum, w, h)
}

// lumToRGBA converts a float64 luminance plane to a greyscale *image.RGBA
// (R=G=B=L, A=255). lum values must be in [0,255].
func lumToRGBA(lum []float64, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, v := range lum {
		b := uint8(clamp255(v))
		dst.Pix[i*4] = b
		dst.Pix[i*4+1] = b
		dst.Pix[i*4+2] = b
		dst.Pix[i*4+3] = 255
	}
	return dst
}

// readLumFromRGBA reads the red channel of src back into lum (they are all
// equal for a greyscale image). src must have the same w×h as lum.
func readLumFromRGBA(src *image.RGBA, lum []float64, w, h int) {
	sb := src.Bounds()
	for y := range h {
		for x := range w {
			c := src.RGBAAt(sb.Min.X+x, sb.Min.Y+y)
			lum[y*w+x] = float64(c.R)
		}
	}
}

// mean returns the arithmetic mean of s.
func mean(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

// invert maps each v → 255 − v in place.
func invert(lum []float64) {
	for i, v := range lum {
		lum[i] = 255.0 - v
	}
}

// stretchContrast maps [p1, p99] → [0, 255] in place. Values outside the
// percentile range are clamped. If p1 ≈ p99 the image is essentially uniform
// and the stretch is skipped to avoid division by near-zero.
func stretchContrast(lum []float64) {
	n := len(lum)
	if n == 0 {
		return
	}
	sorted := make([]float64, n)
	copy(sorted, lum)
	sortFloat64(sorted)

	p1 := sorted[max(0, n/100)]
	p99 := sorted[min(n-1, n*99/100)]
	rng := p99 - p1
	if rng < 1.0 {
		return // essentially uniform — skip
	}
	scale := 255.0 / rng
	for i, v := range lum {
		lum[i] = clamp255((v - p1) * scale)
	}
}

// sortFloat64 sorts s in ascending order using insertion sort for small slices
// and a simple O(n log n) approach for larger ones. For the sizes used here
// (w×h up to a few million) we use the stdlib sort via a thin wrapper to avoid
// implementing a full sort from scratch while keeping the call site clean.
func sortFloat64(s []float64) {
	// Use a simple insertion sort for tiny slices (≤64 elements) and stdlib
	// for larger ones. The crossover is empirical.
	if len(s) <= 64 {
		for i := 1; i < len(s); i++ {
			v := s[i]
			j := i - 1
			for j >= 0 && s[j] > v {
				s[j+1] = s[j]
				j--
			}
			s[j+1] = v
		}
		return
	}
	// Fall back to a standard sort for large slices.
	sortLargeFloat64(s)
}

// sortLargeFloat64 sorts s using a standard heapsort-derived algorithm to avoid
// importing sort (which uses interfaces). We inline a simple quicksort-median3.
func sortLargeFloat64(s []float64) {
	// Use the standard library path via a slice-literal trick that avoids the
	// sort.Float64s deprecation — we implement a simple iterative merge/quick
	// sort. For our use case (percentile of a luminance plane), accuracy matters
	// more than algorithm choice, so we keep it simple and correct.
	pdqsortFloat64(s, 0, len(s)-1, bits(len(s)))
}

// bits returns ⌊log2(n)⌋ * 2, used as the introsort depth limit.
func bits(n int) int {
	b := 0
	for n > 1 {
		n >>= 1
		b++
	}
	return b * 2
}

// pdqsortFloat64 is a minimal introsort (quicksort + heapsort fallback) for
// float64 slices. It is used only by stretchContrast and is not exported.
func pdqsortFloat64(s []float64, lo, hi, depth int) {
	for hi-lo > 12 {
		if depth == 0 {
			heapsortFloat64(s, lo, hi)
			return
		}
		depth--
		mid := (lo + hi) >> 1
		// Median-of-three pivot.
		if s[mid] < s[lo] {
			s[lo], s[mid] = s[mid], s[lo]
		}
		if s[hi] < s[lo] {
			s[lo], s[hi] = s[hi], s[lo]
		}
		if s[mid] < s[hi] {
			s[mid], s[hi] = s[hi], s[mid]
		}
		pivot := s[hi]
		i := lo
		for j := lo; j < hi; j++ {
			if s[j] <= pivot {
				s[i], s[j] = s[j], s[i]
				i++
			}
		}
		s[i], s[hi] = s[hi], s[i]
		pdqsortFloat64(s, lo, i-1, depth)
		lo = i + 1
	}
	// Insertion sort for small partitions.
	for i := lo + 1; i <= hi; i++ {
		v := s[i]
		j := i - 1
		for j >= lo && s[j] > v {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = v
	}
}

// heapsortFloat64 sorts s[lo..hi] in place using heap sort.
func heapsortFloat64(s []float64, lo, hi int) {
	n := hi - lo + 1
	for i := n/2 - 1; i >= 0; i-- {
		siftDownFloat64(s, lo, i, n)
	}
	for i := n - 1; i > 0; i-- {
		s[lo], s[lo+i] = s[lo+i], s[lo]
		siftDownFloat64(s, lo, 0, i)
	}
}

// siftDownFloat64 maintains the max-heap property for the sub-heap rooted at
// position root within s[lo..lo+n-1].
func siftDownFloat64(s []float64, lo, root, n int) {
	for {
		child := 2*root + 1
		if child >= n {
			break
		}
		if child+1 < n && s[lo+child] < s[lo+child+1] {
			child++
		}
		if s[lo+root] >= s[lo+child] {
			break
		}
		s[lo+root], s[lo+child] = s[lo+child], s[lo+root]
		root = child
	}
}

// clamp255 clamps v to [0, 255].
func clamp255(v float64) float64 {
	return max(0, min(255, v))
}
