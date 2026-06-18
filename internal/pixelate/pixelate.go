// Package pixelate implements the block-average pixelation operation used by
// the unpixel pipeline. It is the operation being attacked: replace every
// blockSize×blockSize region with its mean RGBA.
package pixelate

import (
	"image"
	"image/color"

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

	// Compute block grid boundaries relative to origin.
	// Each pixel (x,y) belongs to the block whose top-left is at:
	//   bx = originX + floor((x-originX)/blockSize)*blockSize
	//   by = originY + floor((y-originY)/blockSize)*blockSize
	// We iterate over all distinct block origins covering [0,paddedW)×[0,h).

	startX := originX - ((originX / b.blockSize) * b.blockSize) // ≡ originX % blockSize, but handles negative
	if startX > 0 {
		startX -= b.blockSize
	}
	startY := originY - ((originY / b.blockSize) * b.blockSize)
	if startY > 0 {
		startY -= b.blockSize
	}

	for by := startY; by < h; by += b.blockSize {
		for bx := startX; bx < paddedW; bx += b.blockSize {
			mean := b.blockMean(dst, bx, by, paddedW, h)
			// Fill every pixel in this block with the mean.
			for dy := range b.blockSize {
				for dx := range b.blockSize {
					px, py := bx+dx, by+dy
					if px < 0 || px >= paddedW || py < 0 || py >= h {
						continue
					}
					dst.SetRGBA(px, py, mean)
				}
			}
		}
	}
	return dst
}

// blockMean computes the mean RGBA over the blockSize×blockSize region
// starting at (bx,by) in img, skipping pixels outside [0,w)×[0,h).
func (b *BlockAverage) blockMean(img *image.RGBA, bx, by, w, h int) color.RGBA {
	var rSum, gSum, bSum, aSum, n int
	for dy := range b.blockSize {
		for dx := range b.blockSize {
			x, y := bx+dx, by+dy
			if x < 0 || x >= w || y < 0 || y >= h {
				continue
			}
			c := img.RGBAAt(x, y)
			rSum += int(c.R)
			gSum += int(c.G)
			bSum += int(c.B)
			aSum += int(c.A)
			n++
		}
	}
	if n == 0 {
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	return color.RGBA{
		R: avg8(rSum, n),
		G: avg8(gSum, n),
		B: avg8(bSum, n),
		A: avg8(aSum, n),
	}
}

// avg8 returns sum/n as a byte. Each summed channel is a uint8 and n>0, so the
// mean is always in [0,255]; the explicit & 0xFF mask documents that bound (and
// keeps the int→uint8 conversion provably overflow-free).
func avg8(sum, n int) uint8 {
	return uint8((sum / n) & 0xFF)
}
