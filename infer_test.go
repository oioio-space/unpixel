package unpixel_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
)

// pixelatedGrid builds a w×h RGBA image partitioned into block×block cells, each
// filled with a distinct colour derived from its grid coordinates so that every
// adjacent cell boundary is detectable.
func pixelatedGrid(w, h, block int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			cx, cy := x/block, y/block
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(40 + 30*(cx%7)),
				G: uint8(40 + 30*(cy%7)),
				B: uint8(80 + 20*((cx+cy)%5)),
				A: 255,
			})
		}
	}
	return img
}

func TestInferBlockSize_detectsGrid(t *testing.T) {
	for _, block := range []int{4, 8, 16} {
		img := pixelatedGrid(8*block, 4*block, block)
		if got := unpixel.InferBlockSize(img); got != block {
			t.Errorf("InferBlockSize(block=%d) = %d, want %d", block, got, block)
		}
	}
}

func TestInferBlockSize_sharedColourBlocks(t *testing.T) {
	// Adjacent block-columns 1 and 2 share a colour, so the boundary at 2*block
	// is missing and the gap there becomes 2*block. The GCD of the gaps
	// (block and 2*block) must still recover the true block size.
	const block = 8
	img := pixelatedGrid(8*block, 4*block, block)
	shared := color.RGBA{R: 200, G: 200, B: 200, A: 255}
	for y := range img.Bounds().Dy() {
		for x := block; x < 3*block; x++ { // cells in columns 1 and 2
			img.SetRGBA(x, y, shared)
		}
	}
	if got := unpixel.InferBlockSize(img); got != block {
		t.Errorf("InferBlockSize with shared blocks = %d, want %d", got, block)
	}
}

func TestInferBlockSize_uniformReturnsZero(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	if got := unpixel.InferBlockSize(img); got != 0 {
		t.Errorf("InferBlockSize(uniform) = %d, want 0", got)
	}
}

func TestInferBlockSize_tinyReturnsZero(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if got := unpixel.InferBlockSize(img); got != 0 {
		t.Errorf("InferBlockSize(1x1) = %d, want 0", got)
	}
}

// TestNew_autoInfersBlockSize verifies that a non-positive Config.BlockSize is
// filled by inference rather than blindly defaulting to DefaultBlockSize.
func TestNew_autoInfersBlockSize(t *testing.T) {
	const block = 16
	img := pixelatedGrid(8*block, 4*block, block)
	eng, err := unpixel.New(img, unpixel.Config{}) // BlockSize unset → inferred
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := eng.Config().BlockSize; got != block {
		t.Errorf("New inferred BlockSize = %d, want %d", got, block)
	}
}
