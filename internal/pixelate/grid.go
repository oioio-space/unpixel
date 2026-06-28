package pixelate

import (
	"image"

	"github.com/oioio-space/unpixel/internal/refmatch"
)

// PixelateToGrid computes the same per-block averages as [BlockAverage.Pixelate]
// followed by [refmatch.ExtractBlocksDirect], but writes each block's mean RGB
// directly into a compact grid without allocating a full-size intermediate image
// or filling each block's pixels. The returned grid is byte-identical to
// ExtractBlocksDirect(Pixelate(src, originX, originY)).
//
// This is the fused hot-path replacement for the render→pixelate→extract
// sequence inside mosaictext beam-search decoding. Callers that need the full
// pixelated image (e.g. for MSE scoring) should continue to use Pixelate.
func (b *BlockAverage) PixelateToGrid(src *image.RGBA, originX, originY int) [][]refmatch.BlockSig {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// White-pad width to a multiple of blockSize, identical to Pixelate.
	remainder := b.blockSize - (w % b.blockSize)
	paddedW := w
	if remainder < b.blockSize {
		paddedW = w + remainder
	}

	bs := b.blockSize
	bCols := paddedW / bs
	bRows := h / bs
	if bCols == 0 || bRows == 0 {
		return nil
	}

	// Block grid origins, identical to Pixelate's startX/startY computation.
	startX := originX - ((originX / bs) * bs)
	if startX > 0 {
		startX -= bs
	}
	startY := originY - ((originY / bs) * bs)
	if startY > 0 {
		startY -= bs
	}

	srcStride := src.Stride
	srcPix := src.Pix
	srcOffX := bounds.Min.X
	srcOffY := bounds.Min.Y

	// For each grid position (bc, br) that ExtractBlocksDirect would produce, find
	// the Pixelate block covering pixel (bc*bs, br*bs) in the padded dst, compute
	// its average, and store directly.
	//
	// The Pixelate block covering dst pixel px is:
	//   bx = startX + floor((px-startX)/bs)*bs
	// Because startX ≤ 0 ≤ px this simplifies to integer arithmetic without risk
	// of negative-numerator issues.
	grid := make([][]refmatch.BlockSig, bRows)
	for br := range bRows {
		row := make([]refmatch.BlockSig, bCols)
		py := br * bs
		// Block start y for the Pixelate block that contains py.
		by := startY + ((py - startY) / bs * bs)
		y0, y1 := max(by, 0), min(by+bs, h)

		for bc := range bCols {
			px := bc * bs
			bx := startX + ((px - startX) / bs * bs)
			x0, x1 := max(bx, 0), min(bx+bs, paddedW)

			if x0 >= x1 || y0 >= y1 {
				// Empty block: white (255, 255, 255). Store as float64.
				row[bc] = refmatch.BlockSig{R: 255, G: 255, B: 255}
				continue
			}

			// n is the total pixel count in the averaged region. Pixels in the
			// padded area (x >= w) are treated as white (255).
			n := (x1 - x0) * (y1 - y0)

			// xSrc is the boundary between real src pixels and white padding.
			// Pixels with x in [x0, xSrc) come from src; [xSrc, x1) are white.
			xSrc := min(x1, w)

			var mr, mg, mb uint8
			if b.gamma {
				var rL, gL, bL float64
				for y := y0; y < y1; y++ {
					// Real pixels from src.
					off := (srcOffY+y)*srcStride + (srcOffX+x0)*4
					for x := x0; x < xSrc; x++ {
						rL += srgbToLinear[srcPix[off]]
						gL += srgbToLinear[srcPix[off+1]]
						bL += srgbToLinear[srcPix[off+2]]
						off += 4
					}
					// White padding pixels: srgbToLinear[255] = 1.0.
					wCnt := float64(x1 - xSrc)
					rL += wCnt
					gL += wCnt
					bL += wCnt
				}
				nf := float64(n)
				mr = linearToSrgb8(rL / nf)
				mg = linearToSrgb8(gL / nf)
				mb = linearToSrgb8(bL / nf)
			} else {
				var rSum, gSum, bSum int
				// White padding contribution: 255 per pixel.
				padCount := (x1 - xSrc) * (y1 - y0)
				rSum += padCount * 255
				gSum += padCount * 255
				bSum += padCount * 255
				for y := y0; y < y1; y++ {
					off := (srcOffY+y)*srcStride + (srcOffX+x0)*4
					for x := x0; x < xSrc; x++ {
						rSum += int(srcPix[off])
						gSum += int(srcPix[off+1])
						bSum += int(srcPix[off+2])
						off += 4
					}
				}
				mr = avg8(rSum, n)
				mg = avg8(gSum, n)
				mb = avg8(bSum, n)
			}
			row[bc] = refmatch.BlockSig{R: float64(mr), G: float64(mg), B: float64(mb)}
		}
		grid[br] = row
	}
	return grid
}
