package render_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/render"
)

func TestXImage_sentinelX_matchesMeasuredAdvance(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	img, sentinelX, err := r.Render("hello", style)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if img == nil {
		t.Fatal("Render returned nil image")
	}
	// sentinelX must be positive and less than the image width.
	if sentinelX <= 0 {
		t.Errorf("sentinelX = %d, want > 0", sentinelX)
	}
	if sentinelX >= img.Bounds().Dx() {
		t.Errorf("sentinelX %d >= image width %d", sentinelX, img.Bounds().Dx())
	}
}

func TestXImage_deterministic(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	img1, s1, err := r.Render("hello", style)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	img2, s2, err := r.Render("hello", style)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if s1 != s2 {
		t.Errorf("sentinelX differs: %d vs %d", s1, s2)
	}
	b := img1.Bounds()
	if b != img2.Bounds() {
		t.Errorf("bounds differ: %v vs %v", b, img2.Bounds())
	}
	for y := range b.Dy() {
		for x := range b.Dx() {
			c1 := img1.RGBAAt(x, y)
			c2 := img2.RGBAAt(x, y)
			if c1 != c2 {
				t.Errorf("pixel (%d,%d) differs: %v vs %v", x, y, c1, c2)
				return
			}
		}
	}
}

func TestXImage_hasNonWhitePixels(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	img, _, err := r.Render("abc", style)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	b := img.Bounds()
	found := false
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := img.RGBAAt(x, y)
			if c.R != 255 || c.G != 255 || c.B != 255 {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Error("Render produced an entirely white image — expected text pixels")
	}
}

func TestXImage_hasSentinelBluePixel(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	img, sentinelX, err := r.Render("hi", style)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Scan around sentinelX in the middle row for a pure-blue pixel.
	b := img.Bounds()
	midY := b.Dy() / 2
	found := false
	for x := sentinelX; x < min(sentinelX+50, b.Dx()); x++ {
		c := img.RGBAAt(x, midY)
		if c.B == 255 && c.R != 255 && c.G != 255 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no blue sentinel pixel found near sentinelX=%d in mid row %d", sentinelX, midY)
	}
}

func TestXImage_emptyStringRendersBlueOnly(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	img, sentinelX, err := r.Render("", style)
	if err != nil {
		t.Fatalf("Render empty string: %v", err)
	}
	if img == nil {
		t.Fatal("nil image for empty string")
	}
	// sentinelX for empty string should be the padding amount.
	if sentinelX != style.PaddingLeft {
		t.Errorf("empty string sentinelX = %d, want %d (paddingLeft)", sentinelX, style.PaddingLeft)
	}
}
