package imutil_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// newRGBA builds an w×h white RGBA image.
func newWhite(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	return img
}

// --- Crop ---

func TestCrop_basic(t *testing.T) {
	img := newWhite(20, 10)
	// Paint a distinctive pixel at (5,3).
	img.SetRGBA(5, 3, color.RGBA{R: 1, G: 2, B: 3, A: 255})

	got := imutil.Crop(img, 2, 1, 10, 6)
	if got.Bounds().Dx() != 10 || got.Bounds().Dy() != 6 {
		t.Fatalf("Crop size = %v, want 10×6", got.Bounds().Size())
	}
	// Pixel originally at (5,3) is now at (3,2) in the crop.
	if c := got.RGBAAt(3, 2); c.R != 1 || c.G != 2 || c.B != 3 {
		t.Errorf("pixel at (3,2) = %v, want {1 2 3 255}", c)
	}
}

func TestCrop_clampToImage(t *testing.T) {
	img := newWhite(10, 10)
	// Request a crop that extends past the edge.
	got := imutil.Crop(img, 5, 5, 20, 20)
	if got.Bounds().Dx() != 5 || got.Bounds().Dy() != 5 {
		t.Errorf("clamped crop size = %v, want 5×5", got.Bounds().Size())
	}
}

// --- PadWhite ---

func TestPadWhite_growsWidth(t *testing.T) {
	img := newWhite(10, 8)
	img.SetRGBA(9, 4, color.RGBA{R: 100, G: 100, B: 100, A: 255})

	got := imutil.PadWhite(img, 16, 8)
	if got.Bounds().Dx() != 16 || got.Bounds().Dy() != 8 {
		t.Fatalf("PadWhite size = %v, want 16×8", got.Bounds().Size())
	}
	// Original pixel preserved.
	if c := got.RGBAAt(9, 4); c.R != 100 {
		t.Errorf("original pixel = %v, want R=100", c)
	}
	// Padded area is white.
	if c := got.RGBAAt(15, 4); c.R != 255 || c.G != 255 || c.B != 255 {
		t.Errorf("padded pixel = %v, want white", c)
	}
}

func TestPadWhite_sameSize(t *testing.T) {
	img := newWhite(8, 8)
	got := imutil.PadWhite(img, 8, 8)
	if got.Bounds() != img.Bounds() {
		t.Errorf("PadWhite same-size changed bounds: %v", got.Bounds())
	}
}

// --- CropInto ---

func TestCropInto_pixelIdenticalToCrop(t *testing.T) {
	img := newWhite(20, 10)
	img.SetRGBA(5, 3, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	img.SetRGBA(11, 7, color.RGBA{R: 50, G: 60, B: 70, A: 255})

	want := imutil.Crop(img, 2, 1, 10, 6)
	got := imutil.CropInto(nil, img, 2, 1, 10, 6)

	if got.Bounds() != want.Bounds() {
		t.Fatalf("CropInto bounds = %v, want %v", got.Bounds(), want.Bounds())
	}
	for y := range got.Bounds().Dy() {
		for x := range got.Bounds().Dx() {
			cg := got.RGBAAt(x, y)
			cw := want.RGBAAt(x, y)
			if cg != cw {
				t.Errorf("CropInto(%d,%d) = %v, Crop = %v", x, y, cg, cw)
			}
		}
	}
}

func TestCropInto_reusesBufferWhenSameSize(t *testing.T) {
	img := newWhite(20, 10)
	buf := imutil.CropInto(nil, img, 0, 0, 10, 5)
	origPix := &buf.Pix[0]

	// Same-size call must reuse the existing allocation.
	buf2 := imutil.CropInto(buf, img, 0, 0, 10, 5)
	if &buf2.Pix[0] != origPix {
		t.Error("CropInto with matching size allocated instead of reusing")
	}
}

func TestCropInto_clampToImage(t *testing.T) {
	img := newWhite(10, 10)
	got := imutil.CropInto(nil, img, 5, 5, 20, 20)
	if got.Bounds().Dx() != 5 || got.Bounds().Dy() != 5 {
		t.Errorf("CropInto clamped = %v, want 5×5", got.Bounds().Size())
	}
}

// --- PadWhiteInto ---

func TestPadWhiteInto_pixelIdenticalToPadWhite(t *testing.T) {
	img := newWhite(10, 8)
	img.SetRGBA(9, 4, color.RGBA{R: 100, G: 100, B: 100, A: 255})

	want := imutil.PadWhite(img, 16, 8)
	got := imutil.PadWhiteInto(nil, img, 16, 8)

	if got.Bounds() != want.Bounds() {
		t.Fatalf("PadWhiteInto bounds = %v, want %v", got.Bounds(), want.Bounds())
	}
	for y := range got.Bounds().Dy() {
		for x := range got.Bounds().Dx() {
			cg := got.RGBAAt(x, y)
			cw := want.RGBAAt(x, y)
			if cg != cw {
				t.Errorf("PadWhiteInto(%d,%d) = %v, PadWhite = %v", x, y, cg, cw)
			}
		}
	}
}

func TestPadWhiteInto_reusesBufferWhenSameSize(t *testing.T) {
	img := newWhite(10, 8)
	buf := imutil.PadWhiteInto(nil, img, 16, 8)
	origPix := &buf.Pix[0]

	buf2 := imutil.PadWhiteInto(buf, img, 16, 8)
	if &buf2.Pix[0] != origPix {
		t.Error("PadWhiteInto with matching size allocated instead of reusing")
	}
}

// --- Compose ---

func TestCompose_pastesSrc(t *testing.T) {
	dst := newWhite(20, 20)
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			src.SetRGBA(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	imutil.Compose(dst, src, 5, 7)
	if c := dst.RGBAAt(5, 7); c.R != 200 {
		t.Errorf("compose at (5,7) = %v, want R=200", c)
	}
	// Outside src paste area is still white.
	if c := dst.RGBAAt(4, 7); c.R != 255 {
		t.Errorf("pixel outside paste = %v, want white", c)
	}
}

// --- BlueMargin ---

func TestBlueMargin_findsBluePixel(t *testing.T) {
	// 30-wide × 10-high white image.
	// Blue block at columns 5–20, rows 2–7.
	// margin = 5 (first blue in mid row), scanX = 5+5 = 10 (inside the block).
	// topBlue = 2, botBlue = 8 → center = (2+8)/2 = 5.
	img := newWhite(30, 10)
	for y := 2; y < 8; y++ {
		for x := 5; x < 21; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 0, G: 0, B: 255, A: 255})
		}
	}
	margin, center := imutil.BlueMargin(img)
	if margin != 5 {
		t.Errorf("BlueMargin margin = %d, want 5", margin)
	}
	// topBlue=2, botBlue=8, center=(2+8)/2=5.
	if center != 5 {
		t.Errorf("BlueMargin center = %d, want 5", center)
	}
}

func TestBlueMargin_noBlue(t *testing.T) {
	img := newWhite(10, 10)
	margin, center := imutil.BlueMargin(img)
	if margin != 0 || center != 0 {
		t.Errorf("BlueMargin on white = (%d,%d), want (0,0)", margin, center)
	}
}

// --- LeftEdge ---

func TestLeftEdge_findsFirstNonWhite(t *testing.T) {
	img := newWhite(20, 10)
	// Column 5 has a dark pixel at row 3.
	img.SetRGBA(5, 3, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	got := imutil.LeftEdge(img)
	if got != 5 {
		t.Errorf("LeftEdge = %d, want 5", got)
	}
}

func TestLeftEdge_allWhiteReturnsZero(t *testing.T) {
	img := newWhite(10, 10)
	got := imutil.LeftEdge(img)
	if got != 0 {
		t.Errorf("LeftEdge all-white = %d, want 0", got)
	}
}

// --- Margins (red-diff left boundary) ---

func TestMargins_findsFirstRedColumn(t *testing.T) {
	// 20-wide × 10-high white image; red pixel at (8, 5).
	img := newWhite(20, 10)
	img.SetRGBA(8, 5, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	got := imutil.Margins(img)
	if got != 8 {
		t.Errorf("Margins = %d, want 8", got)
	}
}

func TestMargins_noRedReturnsZero(t *testing.T) {
	img := newWhite(10, 10)
	got := imutil.Margins(img)
	if got != 0 {
		t.Errorf("Margins no-red = %d, want 0", got)
	}
}

// --- ToRGBA ---

// TestToRGBA_alreadyRGBA verifies that ToRGBA returns the same pointer when
// the input is already *image.RGBA (zero allocation, no copy).
func TestToRGBA_alreadyRGBA(t *testing.T) {
	img := newWhite(4, 4)
	got := imutil.ToRGBA(img)
	if got != img {
		t.Error("ToRGBA(*image.RGBA): expected same pointer, got a copy")
	}
}

// TestToRGBA_nonRGBA verifies that ToRGBA converts a non-RGBA image into a
// fresh *image.RGBA with the same pixel content.
func TestToRGBA_nonRGBA(t *testing.T) {
	// image.NRGBA is not *image.RGBA, so ToRGBA must convert it.
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	src.SetNRGBA(1, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})

	got := imutil.ToRGBA(src)

	if got == nil {
		t.Fatal("ToRGBA returned nil")
	}
	if got.Bounds().Dx() != 3 || got.Bounds().Dy() != 2 {
		t.Errorf("ToRGBA dims: got %dx%d, want 3x2", got.Bounds().Dx(), got.Bounds().Dy())
	}
	// The converted pixel at (1,0) must match the source.
	c := got.RGBAAt(1, 0)
	if c.R != 10 || c.G != 20 || c.B != 30 || c.A != 255 {
		t.Errorf("ToRGBA pixel (1,0): got %v, want {10,20,30,255}", c)
	}
}

// --- FillWhite ---

// TestFillWhite_normal verifies that FillWhite sets every pixel to 0xFF.
func TestFillWhite_normal(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	imutil.FillWhite(img)
	for i, b := range img.Pix {
		if b != 0xFF {
			t.Errorf("Pix[%d] = %#x, want 0xFF", i, b)
			break
		}
	}
}

// TestFillWhite_empty verifies that FillWhite on a zero-size image does not
// panic (exercises the len(p)==0 early-return branch).
func TestFillWhite_empty(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 0, 0))
	imutil.FillWhite(img) // must not panic
}

// --- Lum601 ---

// TestLum601 verifies the BT.601 luminance formula for known reference values.
func TestLum601(t *testing.T) {
	tests := []struct {
		name       string
		r, g, b    uint8
		wantApprox int // expected value ± 1 (integer rounding)
	}{
		{name: "black", r: 0, g: 0, b: 0, wantApprox: 0},
		{name: "white", r: 255, g: 255, b: 255, wantApprox: 255},
		{name: "pure red", r: 255, g: 0, b: 0, wantApprox: 76},    // 299*255/1000 = 76
		{name: "pure green", r: 0, g: 255, b: 0, wantApprox: 149}, // 587*255/1000 = 149
		{name: "pure blue", r: 0, g: 0, b: 255, wantApprox: 29},   // 114*255/1000 = 29
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := imutil.Lum601(tc.r, tc.g, tc.b)
			if got < tc.wantApprox-1 || got > tc.wantApprox+1 {
				t.Errorf("Lum601(%d,%d,%d) = %d, want ~%d", tc.r, tc.g, tc.b, got, tc.wantApprox)
			}
		})
	}
}
