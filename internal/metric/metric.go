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
