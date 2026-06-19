// Package pixelate implements the block-average pixelation operation used by
// the unpixel pipeline. It is the operation being attacked: replace every
// blockSize×blockSize region with its mean RGBA.
package pixelate

import (
	"image"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// BlockAverage implements unpixel.Pixelator using per-block mean RGBA.
type BlockAverage struct {
	blockSize int
}

// NewBlockAverage returns a BlockAverage pixelator with the given block size.
func NewBlockAverage(blockSize int) *BlockAverage {
	return &BlockAverage{blockSize: blockSize}
}

// Pixelate replaces every blockSize×blockSize region of src (aligned to
// originX, originY) with the mean RGBA of that region. The image is first
// white-padded so its width is a multiple of blockSize. The result has the
// same or larger dimensions.
//
// faithful: main.ts pixelation loop — grid aligned to (originX,originY);
// partial blocks (near edges) still average only the pixels that exist.
func (b *BlockAverage) Pixelate(src *image.RGBA, originX, originY int) *image.RGBA {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// White-pad width to a multiple of blockSize (faithful: main.ts remainder logic).
	remainder := b.blockSize - (w % b.blockSize)
	paddedW := w
	if remainder < b.blockSize {
		paddedW = w + remainder
	}

	dst := image.NewRGBA(image.Rect(0, 0, paddedW, h))
	// Fill with white first so padded area is white.
	imutil.FillWhite(dst)
	// Copy src into dst row-by-row.
	rowBytes := w * 4
	for y := range h {
		srcOff := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		dstOff := dst.PixOffset(0, y)
		copy(dst.Pix[dstOff:dstOff+rowBytes], src.Pix[srcOff:srcOff+rowBytes])
	}

	// Block grid origins covering the image, aligned to (originX, originY).
	startX := originX - ((originX / b.blockSize) * b.blockSize) // ≡ originX % blockSize, but handles negative
	if startX > 0 {
		startX -= b.blockSize
	}
	startY := originY - ((originY / b.blockSize) * b.blockSize)
	if startY > 0 {
		startY -= b.blockSize
	}

	// Each block: sum its clamped region by indexing dst.Pix directly (no
	// per-pixel RGBAAt/bounds checks), then fill it by writing the first row and
	// memmoving it down. Same means and fill values as a per-pixel loop.
	bs := b.blockSize
	stride := dst.Stride
	pix := dst.Pix
	for by := startY; by < h; by += bs {
		y0, y1 := max(by, 0), min(by+bs, h)
		for bx := startX; bx < paddedW; bx += bs {
			x0, x1 := max(bx, 0), min(bx+bs, paddedW)
			if x0 >= x1 || y0 >= y1 {
				continue
			}
			var rSum, gSum, bSum, aSum int
			for y := y0; y < y1; y++ {
				off := y*stride + x0*4
				for x := x0; x < x1; x++ {
					rSum += int(pix[off])
					gSum += int(pix[off+1])
					bSum += int(pix[off+2])
					aSum += int(pix[off+3])
					off += 4
				}
			}
			n := (x1 - x0) * (y1 - y0)
			mr, mg, mb, ma := avg8(rSum, n), avg8(gSum, n), avg8(bSum, n), avg8(aSum, n)

			rowStart := y0*stride + x0*4
			rowLen := (x1 - x0) * 4
			for off := rowStart; off < rowStart+rowLen; off += 4 {
				pix[off], pix[off+1], pix[off+2], pix[off+3] = mr, mg, mb, ma
			}
			for y := y0 + 1; y < y1; y++ {
				dstRow := y*stride + x0*4
				copy(pix[dstRow:dstRow+rowLen], pix[rowStart:rowStart+rowLen])
			}
		}
	}
	return dst
}

// avg8 returns sum/n as a byte. Each summed channel is a uint8 and n>0, so the
// mean is always in [0,255]; the explicit & 0xFF mask documents that bound (and
// keeps the int→uint8 conversion provably overflow-free).
func avg8(sum, n int) uint8 {
	return uint8((sum / n) & 0xFF)
}
