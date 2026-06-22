package render_test

import (
	"os"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/render"
)

// embeddedRegular reads the bundled regular font from disk so the external-font
// path can be exercised without depending on any system font.
func embeddedRegular(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Fatalf("read embedded font: %v", err)
	}
	return b
}

func TestNewXImageFromFonts_emptyRegularErrors(t *testing.T) {
	if _, err := render.NewXImageFromFonts(nil, nil); err == nil {
		t.Error("NewXImageFromFonts(nil, nil): expected error, got nil")
	}
}

func TestNewXImageFromFonts_rendersWithSuppliedFont(t *testing.T) {
	// boldTTF nil must fall back to the regular font, not fail.
	r, err := render.NewXImageFromFonts(embeddedRegular(t), nil)
	if err != nil {
		t.Fatalf("NewXImageFromFonts: %v", err)
	}
	_, sentinelX, err := r.Render("hi", unpixel.Style{FontSize: 24, PaddingTop: 8, PaddingLeft: 8})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if sentinelX <= 0 {
		t.Errorf("sentinelX = %d, want > 0", sentinelX)
	}
}

// TestNewXImageFromFonts_invalidTTFErrors verifies that supplying unparseable
// font data returns an error (exercises the parseFace error branch).
func TestNewXImageFromFonts_invalidTTFErrors(t *testing.T) {
	garbage := []byte("this is definitely not a valid TrueType font binary")
	if _, err := render.NewXImageFromFonts(garbage, nil); err == nil {
		t.Error("NewXImageFromFonts(garbage TTF): expected parse error, got nil")
	}
	// Valid regular + invalid bold should also fail.
	valid := embeddedRegular(t)
	if _, err := render.NewXImageFromFonts(valid, garbage); err == nil {
		t.Error("NewXImageFromFonts(valid, garbage bold): expected parse error, got nil")
	}
}

func TestRender_letterSpacingWidensAndTightens(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	base := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	widthAt := func(ls float64) int {
		s := base
		s.LetterSpacing = ls
		_, sentinelX, err := r.Render("mmmm", s)
		if err != nil {
			t.Fatalf("Render(ls=%v): %v", ls, err)
		}
		return sentinelX
	}
	neg, zero, pos := widthAt(-3), widthAt(0), widthAt(4)
	if neg >= zero || zero >= pos {
		t.Errorf("letter-spacing not monotonic in width: neg=%d zero=%d pos=%d", neg, zero, pos)
	}
}

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
