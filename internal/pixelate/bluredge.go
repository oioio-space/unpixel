package pixelate

import "math"

// Edge selects how a blur samples beyond the image border.
type Edge uint8

// Edge border-handling constants. EdgeClamp is the zero value and matches the
// behaviour of NewGaussianBlur exactly.
const (
	EdgeClamp   Edge = iota // repeat the border pixel (default; == NewGaussianBlur)
	EdgeReflect             // mirror across the border
	EdgeWrap                // wrap to the opposite border
)

// sampleIndex maps an out-of-range pixel index i to a valid one in [0, n)
// according to the chosen edge mode.
//
//   - EdgeClamp:   clamp to [0, n-1].
//   - EdgeReflect: mirror at each border (e.g. -1→0, n→n-1, -2→1).
//   - EdgeWrap:    wrap modulo n.
func sampleIndex(i, n int, edge Edge) int {
	switch edge {
	case EdgeReflect:
		for i < 0 || i >= n {
			if i < 0 {
				i = -i - 1
			} else {
				i = 2*n - i - 1
			}
		}
		return i
	case EdgeWrap:
		i %= n
		if i < 0 {
			i += n
		}
		return i
	default: // EdgeClamp
		if i < 0 {
			return 0
		}
		if i >= n {
			return n - 1
		}
		return i
	}
}

// newGaussianKernel builds the normalised 1-D Gaussian kernel for the given
// sigma, returning (kernel, radius). Shared by NewGaussianBlur and
// NewGaussianBlurEdge to keep both constructors DRY.
func newGaussianKernel(sigma float64) (kernel []float64, radius int) {
	radius = int(math.Ceil(3 * sigma))
	kernel = make([]float64, 2*radius+1)
	var sum float64
	for i := -radius; i <= radius; i++ {
		w := math.Exp(-float64(i*i) / (2 * sigma * sigma))
		kernel[i+radius] = w
		sum += w
	}
	for i := range kernel {
		kernel[i] /= sum
	}
	return kernel, radius
}

// NewGaussianBlurEdge returns a separable Gaussian blur (sigma in px) using
// the given border mode. EdgeClamp is byte-identical to NewGaussianBlur(sigma).
func NewGaussianBlurEdge(sigma float64, edge Edge) *GaussianBlur {
	if sigma < 0.1 {
		sigma = 0.1
	}
	kernel, radius := newGaussianKernel(sigma)
	return &GaussianBlur{kernel: kernel, sigma: sigma, radius: radius, edge: edge}
}
