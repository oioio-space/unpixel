// Package metric provides image distance metrics for the unpixel pipeline.
// All metrics implement unpixel.Metric and return a value in [0, 1].
package metric

import (
	"image"

	"github.com/orisano/pixelmatch"
)

// RGB is a simple per-pixel metric: the fraction of pixels whose RGB values
// differ by any amount between the two images.
type RGB struct{}

// NewRGB returns an RGB metric.
func NewRGB() RGB { return RGB{} }

// Compare returns the fraction of pixels that differ between a and b.
// Images must have the same bounds.
func (RGB) Compare(a, b *image.RGBA) float64 {
	bounds := a.Bounds()
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return 0
	}
	var diffCount int
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			ca := a.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			cb := b.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			if ca.R != cb.R || ca.G != cb.G || ca.B != cb.B {
				diffCount++
			}
		}
	}
	return float64(diffCount) / float64(total)
}

// Pixelmatch wraps github.com/orisano/pixelmatch (a faithful Go port of
// mapbox/pixelmatch) to implement the unpixel.Metric interface. It uses a YIQ
// perceptual colour-difference threshold, matching Jimp.diff behaviour in the
// original TypeScript source.
type Pixelmatch struct {
	// threshold is the per-pixel colour-difference tolerance passed to pixelmatch.
	threshold float64
}

// NewPixelmatch returns a Pixelmatch metric with the given threshold.
// threshold=0.1 is a reasonable default; the original uses 0.02 for diffs.
func NewPixelmatch(threshold float64) Pixelmatch {
	return Pixelmatch{threshold: threshold}
}

// Compare returns the fraction of pixels that differ beyond the YIQ threshold.
// Images must have the same bounds.
func (m Pixelmatch) Compare(a, b *image.RGBA) float64 {
	bounds := a.Bounds()
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return 0
	}
	count, err := pixelmatch.MatchPixel(a, b, pixelmatch.Threshold(m.threshold))
	if err != nil {
		// Dimensions mismatch or other error — return 1 (maximally different)
		// so the caller prunes this candidate rather than silently accepting it.
		return 1
	}
	return float64(count) / float64(total)
}

// DefaultSSIMWindow is the side length, in pixels, of each SSIM comparison
// window when NewSSIM is called with a non-positive size.
const DefaultSSIMWindow = 8

// SSIM is a structural-similarity image metric. Unlike the per-pixel RGB and
// Pixelmatch metrics, it compares local luminance, contrast, and structure over
// small windows, which makes it tolerant of the sub-pixel anti-aliasing and
// hinting differences that arise between rendering engines (e.g. x/image vs
// Chromium). It is offered as an alternative to the faithful default; because
// its score scale differs from a pixel-fraction, a search using it generally
// needs its own Threshold tuning.
type SSIM struct {
	// window is the side length of each non-overlapping comparison window.
	window int
}

// NewSSIM returns an SSIM metric using square windows of the given side length.
// A window <= 0 uses DefaultSSIMWindow. For images smaller than the window the
// window is clamped to the image size.
func NewSSIM(window int) SSIM {
	if window <= 0 {
		window = DefaultSSIMWindow
	}
	return SSIM{window: window}
}

// Compare returns a structural-dissimilarity distance in [0, 1], computed as
// 1 - the mean SSIM over non-overlapping windows of the two images (0 = identical,
// 1 = maximally dissimilar). Images should have the same bounds; the comparison
// uses the overlapping top-left region when they differ. Pixels in a trailing
// partial window (when a dimension is not a multiple of the window) are ignored.
func (m SSIM) Compare(a, b *image.RGBA) float64 {
	w := min(a.Bounds().Dx(), b.Bounds().Dx())
	h := min(a.Bounds().Dy(), b.Bounds().Dy())
	if w == 0 || h == 0 {
		return 0
	}
	win := min(m.window, w, h)

	ga := grayscale(a, w, h)
	gb := grayscale(b, w, h)

	// Stabilising constants for dynamic range L=255 (Wang et al. 2004).
	const (
		c1 = (0.01 * 255) * (0.01 * 255)
		c2 = (0.03 * 255) * (0.03 * 255)
	)

	var sumSSIM float64
	var windows int
	for y0 := 0; y0+win <= h; y0 += win {
		for x0 := 0; x0+win <= w; x0 += win {
			sumSSIM += windowSSIM(ga, gb, w, x0, y0, win, c1, c2)
			windows++
		}
	}
	if windows == 0 {
		return 0
	}
	d := 1 - sumSSIM/float64(windows)
	return min(max(d, 0), 1)
}

// windowSSIM returns the SSIM of the win×win block at (x0, y0) in two grayscale
// buffers of width w.
func windowSSIM(ga, gb []float64, w, x0, y0, win int, c1, c2 float64) float64 {
	n := float64(win * win)
	var muA, muB float64
	for yy := range win {
		row := (y0 + yy) * w
		for xx := range win {
			muA += ga[row+x0+xx]
			muB += gb[row+x0+xx]
		}
	}
	muA /= n
	muB /= n

	var varA, varB, cov float64
	for yy := range win {
		row := (y0 + yy) * w
		for xx := range win {
			da := ga[row+x0+xx] - muA
			db := gb[row+x0+xx] - muB
			varA += da * da
			varB += db * db
			cov += da * db
		}
	}
	varA /= n
	varB /= n
	cov /= n

	return ((2*muA*muB + c1) * (2*cov + c2)) / ((muA*muA + muB*muB + c1) * (varA + varB + c2))
}

// grayscale returns the Rec.601 luminance of the top-left w×h region of img as a
// row-major slice of length w*h.
func grayscale(img *image.RGBA, w, h int) []float64 {
	b := img.Bounds()
	out := make([]float64, w*h)
	for y := range h {
		for x := range w {
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			out[y*w+x] = 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
		}
	}
	return out
}
