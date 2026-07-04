package render_test

import (
	"bytes"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/render"
)

// TestXImage_XScale_widerRaster verifies that Style.XScale=1.06 produces a
// rendered image whose text region is approximately 6% wider than XScale=1.0,
// and that sentinelX tracks the stretch.
func TestXImage_XScale_widerRaster(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}

	base := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	s106 := base
	s106.XScale = 1.06
	s100 := base
	s100.XScale = 1.0

	img106, sx106, err := r.Render("Hello World", s106)
	if err != nil {
		t.Fatalf("Render XScale=1.06: %v", err)
	}
	img100, sx100, err := r.Render("Hello World", s100)
	if err != nil {
		t.Fatalf("Render XScale=1.0: %v", err)
	}

	// Image with XScale=1.06 must be physically wider.
	if img106.Bounds().Dx() <= img100.Bounds().Dx() {
		t.Errorf("XScale=1.06 image width %d not wider than XScale=1.0 width %d",
			img106.Bounds().Dx(), img100.Bounds().Dx())
	}

	// sentinelX must also be wider.
	if sx106 <= sx100 {
		t.Errorf("XScale=1.06 sentinelX=%d not wider than XScale=1.0 sentinelX=%d", sx106, sx100)
	}

	// The text-region width should be ≈6% wider (allow ±2% for rounding).
	textW106 := sx106 - base.PaddingLeft
	textW100 := sx100 - base.PaddingLeft
	if textW100 == 0 {
		t.Fatal("XScale=1.0 textW is zero — unexpected")
	}
	ratio := float64(textW106) / float64(textW100)
	if ratio < 1.04 || ratio > 1.08 {
		t.Errorf("XScale=1.06 stretch ratio=%.3f, want 1.04…1.08 (≈1.06)", ratio)
	}
	t.Logf("XScale=1.06 sentinelX=%d, XScale=1.0 sentinelX=%d, ratio=%.3f", sx106, sx100, ratio)
}

// TestXImage_XScale_zeroEqualsOne verifies that XScale=0 (zero value) and
// XScale=1.0 both produce byte-identical output — the isotropic fast path.
func TestXImage_XScale_zeroEqualsOne(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}

	base := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	s0 := base // XScale zero-value → isotropic
	s1 := base
	s1.XScale = 1.0

	img0, sx0, err := r.Render("Hello World", s0)
	if err != nil {
		t.Fatalf("Render XScale=0: %v", err)
	}
	img1, sx1, err := r.Render("Hello World", s1)
	if err != nil {
		t.Fatalf("Render XScale=1.0: %v", err)
	}

	if sx0 != sx1 {
		t.Errorf("sentinelX: XScale=0 gives %d, XScale=1.0 gives %d — must be equal", sx0, sx1)
	}
	if img0.Bounds() != img1.Bounds() {
		t.Errorf("bounds: XScale=0 gives %v, XScale=1.0 gives %v — must be equal",
			img0.Bounds(), img1.Bounds())
	}
	if !bytes.Equal(img0.Pix, img1.Pix) {
		t.Error("pixel data: XScale=0 and XScale=1.0 are not byte-identical")
	}
}
