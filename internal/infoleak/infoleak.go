// Package infoleak quantifies how much exploitable information a block-average
// mosaic leaks under anti-aliased rendering and JPEG compression. It is a
// measurement/feasibility study, not a decoder: for a true block-average mosaic
// the only recoverable signal is the block values themselves (the "missing
// avalanche effect" the engine already exploits via generate-and-test). These
// primitives let the //go:build infoleak study runner put numbers on the
// information boundary; see docs/JOURNAL.md for the recorded findings.
package infoleak

import (
	"bytes"
	"image"
	"image/jpeg"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// Separability is the mean per-pixel absolute luminance difference between a and
// b, normalised to [0,1] (0 means indistinguishable). The images are compared
// over their common minimum width and height, so near-equal-width candidates
// (e.g. the confusable pair "rn"/"m") can be compared directly.
func Separability(a, b *image.RGBA) float64 {
	ab, bb := a.Bounds(), b.Bounds()
	w := min(ab.Dx(), bb.Dx())
	h := min(ab.Dy(), bb.Dy())
	if w == 0 || h == 0 {
		return 0
	}
	var sum int
	for y := range h {
		for x := range w {
			ao := a.PixOffset(ab.Min.X+x, ab.Min.Y+y)
			bo := b.PixOffset(bb.Min.X+x, bb.Min.Y+y)
			la := imutil.Lum601(a.Pix[ao], a.Pix[ao+1], a.Pix[ao+2])
			lb := imutil.Lum601(b.Pix[bo], b.Pix[bo+1], b.Pix[bo+2])
			d := la - lb
			if d < 0 {
				d = -d
			}
			sum += d
		}
	}
	return float64(sum) / (float64(w*h) * 255.0)
}

// JPEGRoundTrip encodes img as JPEG at the given quality (1..100) and decodes it
// back to RGBA, simulating a JPEG-compressed capture of a mosaic. Lower quality
// adds more signal-dependent noise to the (otherwise block-constant) values.
func JPEGRoundTrip(img *image.RGBA, quality int) (*image.RGBA, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	decoded, err := jpeg.Decode(&buf)
	if err != nil {
		return nil, err
	}
	return imutil.ToRGBA(decoded), nil
}

// binarizeHardEdge thresholds an anti-aliased render to two luminance levels —
// black (Lum < threshold) or white — removing the sub-pixel AA coverage so its
// contribution can be isolated by comparison against the AA original.
func binarizeHardEdge(img *image.RGBA, threshold int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			var v uint8 = 255
			if imutil.Lum601(img.Pix[o], img.Pix[o+1], img.Pix[o+2]) < threshold {
				v = 0
			}
			oo := out.PixOffset(x, y)
			out.Pix[oo], out.Pix[oo+1], out.Pix[oo+2], out.Pix[oo+3] = v, v, v, 255
		}
	}
	return out
}
