package pixelate_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// makeGradient builds a w×h RGBA image where pixel (x,y) has R=x, G=y, B=0, A=255.
func makeGradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0, A: 255})
		}
	}
	return img
}

// blockMean computes the expected mean RGBA over the block starting at (bx,by)
// of size bs×bs in img. Pixels outside the image bounds are skipped.
func blockMean(img *image.RGBA, bx, by, bs int) color.RGBA {
	b := img.Bounds()
	var rSum, gSum, bSum, aSum, n int
	for dy := range bs {
		for dx := range bs {
			x, y := bx+dx, by+dy
			if x >= b.Max.X || y >= b.Max.Y {
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
		return color.RGBA{}
	}
	return color.RGBA{
		R: uint8(rSum / n),
		G: uint8(gSum / n),
		B: uint8(bSum / n),
		A: uint8(aSum / n),
	}
}

func TestBlockAverage_16x16_blockSize8(t *testing.T) {
	const bs = 8
	src := makeGradient(16, 16)
	p := pixelate.NewBlockAverage(bs)
	got := p.Pixelate(src, 0, 0)

	if got.Bounds().Dx() != 16 || got.Bounds().Dy() != 16 {
		t.Fatalf("output size = %v, want 16×16", got.Bounds().Size())
	}

	// Each block: all pixels within it must equal the computed mean.
	for blockRow := range 2 {
		for blockCol := range 2 {
			bx, by := blockCol*bs, blockRow*bs
			want := blockMean(src, bx, by, bs)
			for dy := range bs {
				for dx := range bs {
					c := got.RGBAAt(bx+dx, by+dy)
					if c != want {
						t.Errorf("block(%d,%d) pixel(%d,%d) = %v, want %v",
							blockCol, blockRow, bx+dx, by+dy, c, want)
					}
				}
			}
		}
	}
}

func TestBlockAverage_idempotent(t *testing.T) {
	// Pixelating an already-pixelated image at the same origin must be a no-op.
	const bs = 8
	src := makeGradient(16, 16)
	p := pixelate.NewBlockAverage(bs)
	once := p.Pixelate(src, 0, 0)
	twice := p.Pixelate(once, 0, 0)

	for y := range 16 {
		for x := range 16 {
			if once.RGBAAt(x, y) != twice.RGBAAt(x, y) {
				t.Errorf("idempotence failed at (%d,%d): once=%v twice=%v",
					x, y, once.RGBAAt(x, y), twice.RGBAAt(x, y))
			}
		}
	}
}

func TestBlockAverage_nonZeroOrigin(t *testing.T) {
	// With origin (3,3) the grid is aligned to multiples of 8 starting at (3,3).
	// Pixel at (3,3) belongs to block starting at (3,3); pixel at (2,2) belongs
	// to a partial block that still gets averaged correctly.
	const bs = 8
	src := makeGradient(24, 24)
	p := pixelate.NewBlockAverage(bs)
	got := p.Pixelate(src, 3, 3)

	// All pixels within the block that starts at (3,3) must share the same value.
	ref := got.RGBAAt(3, 3)
	for dy := range bs {
		for dx := range bs {
			c := got.RGBAAt(3+dx, 3+dy)
			if c != ref {
				t.Errorf("non-zero origin: pixel(%d,%d)=%v, want %v", 3+dx, 3+dy, c, ref)
			}
		}
	}
}

func TestBlockAverage_whitepadToBlockMultiple(t *testing.T) {
	// A 10×10 image with blockSize=8 → output width must be padded to 16.
	// The padded columns (10–15) are part of the second block (cols 8–15) and
	// will share that block's mean — they are NOT pure white after pixelation.
	// We only assert the output dimensions and that every pixel within each
	// block is uniform (the defining property of block-average).
	const bs = 8
	src := makeGradient(10, 10)
	p := pixelate.NewBlockAverage(bs)
	got := p.Pixelate(src, 0, 0)

	if got.Bounds().Dx() != 16 {
		t.Fatalf("padded width = %d, want 16", got.Bounds().Dx())
	}
	if got.Bounds().Dy() != 10 {
		t.Fatalf("height = %d, want 10", got.Bounds().Dy())
	}
	// Every pixel in each block must equal the first pixel of that block.
	for blockRow := range 2 { // rows 0–7 and 8–9 (partial)
		for blockCol := range 2 { // cols 0–7 and 8–15
			bx, by := blockCol*bs, blockRow*bs
			ref := got.RGBAAt(bx, by)
			for dy := range bs {
				for dx := range bs {
					x, y := bx+dx, by+dy
					if x >= 16 || y >= 10 {
						continue
					}
					c := got.RGBAAt(x, y)
					if c != ref {
						t.Errorf("block(%d,%d) pixel(%d,%d)=%v, want %v",
							blockCol, blockRow, x, y, c, ref)
					}
				}
			}
		}
	}
}
