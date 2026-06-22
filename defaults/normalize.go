package defaults

import (
	"image"

	"github.com/oioio-space/unpixel/internal/deblur"
)

// Normalize prepares img for blur recovery by removing background luminance
// variation, correcting polarity, and optionally stretching contrast and
// suppressing JPEG blocking artefacts.
//
// It is a thin wrapper around [deblur.Normalize] for callers that import the
// defaults package and want to apply normalisation as a standalone step (e.g.
// to inspect the preprocessed image) rather than through [unpixel.WithNormalize].
//
// img is converted to *image.RGBA if needed; the original is not modified.
// The returned *image.RGBA is always a fresh allocation.
func Normalize(img image.Image, o deblur.Options) *image.RGBA {
	src, ok := img.(*image.RGBA)
	if !ok {
		b := img.Bounds()
		src = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				src.Set(x-b.Min.X, y-b.Min.Y, img.At(x, y))
			}
		}
	}
	return deblur.Normalize(src, o)
}
