package pixelate

import (
	"bytes"
	"image"
	"testing"
)

func edgeTestSrc() *image.RGBA {
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			c := byte(0)
			if x >= 8 {
				c = 255
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return src
}

func TestNewGaussianBlurEdge_clampMatchesDefault(t *testing.T) {
	src := edgeTestSrc()
	got := NewGaussianBlurEdge(2.0, EdgeClamp).Pixelate(src, 0, 0)
	want := NewGaussianBlur(2.0).Pixelate(src, 0, 0)
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Errorf("EdgeClamp output differs from NewGaussianBlur(2.0); want byte-identical")
	}
}

func TestNewGaussianBlurEdge_modesDifferAtBorder(t *testing.T) {
	src := edgeTestSrc()
	clamp := NewGaussianBlurEdge(2.0, EdgeClamp).Pixelate(src, 0, 0)
	reflect := NewGaussianBlurEdge(2.0, EdgeReflect).Pixelate(src, 0, 0)
	wrap := NewGaussianBlurEdge(2.0, EdgeWrap).Pixelate(src, 0, 0)
	// Interior pixel far from any border: all modes equal.
	cx := clamp.PixOffset(8, 8)
	if clamp.Pix[cx] != reflect.Pix[cx] || clamp.Pix[cx] != wrap.Pix[cx] {
		t.Errorf("interior pixel differs across edge modes; want equal")
	}
	// Left-border column 0: wrap pulls in the white right edge, so it differs from clamp.
	bx := clamp.PixOffset(0, 8)
	if clamp.Pix[bx] == wrap.Pix[bx] {
		t.Errorf("border pixel identical for clamp and wrap; want different")
	}
}
