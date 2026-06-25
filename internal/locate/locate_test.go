package locate_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/locate"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// sink defeats dead-code elimination in benchmarks.
var sink image.Rectangle

// newWhite returns a w×h opaque-white RGBA image.
func newWhite(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	return img
}

// paintRect fills a rectangular area of img with the given color.
func paintRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// syntheticMosaic builds a full-screen image with a solid-color background and
// a perfectly pixelated (block-constant) mosaic band inside large margins.
// It returns the image and the exact expected mosaic rectangle (block-aligned).
//
// The mosaic content is a two-color checkerboard of bs×bs blocks so that every
// block boundary is a hard color step — the sharpest possible mosaic signal.
func syntheticMosaic(bs, contentW, contentH, marginLeft, marginTop, marginRight, marginBot int) (*image.RGBA, image.Rectangle) {
	w := marginLeft + contentW + marginRight
	h := marginTop + contentH + marginBot
	full := newWhite(w, h)

	for by := range contentH / bs {
		for bx := range contentW / bs {
			c := color.RGBA{R: 80, G: 80, B: 80, A: 255}
			if (bx+by)%2 == 0 {
				c = color.RGBA{R: 200, G: 200, B: 200, A: 255}
			}
			r := image.Rect(
				marginLeft+bx*bs, marginTop+by*bs,
				marginLeft+(bx+1)*bs, marginTop+(by+1)*bs,
			)
			paintRect(full, r, c)
		}
	}

	want := image.Rect(marginLeft, marginTop, marginLeft+contentW, marginTop+contentH)
	return full, want
}

// TestLocateMosaicBand_syntheticCheckerboard verifies that the locator finds
// the exact block-aligned mosaic rectangle in a white-margined image.
func TestLocateMosaicBand_syntheticCheckerboard(t *testing.T) {
	const bs = 10
	tests := []struct {
		name               string
		contentW, contentH int
		mL, mT, mR, mB     int
	}{
		{"small_mosaic_large_margin", 5 * bs, 4 * bs, 100, 80, 100, 80},
		{"wide_margins", 8 * bs, 3 * bs, 200, 150, 200, 150},
		{"thin_margins", 6 * bs, 5 * bs, bs, bs, bs, bs},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img, want := syntheticMosaic(bs, tc.contentW, tc.contentH, tc.mL, tc.mT, tc.mR, tc.mB)
			got, ok := locate.LocateMosaicBand(img)
			if !ok {
				t.Fatalf("LocateMosaicBand: ok=false, want %v", want)
			}
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// TestLocateMosaicBand_allWhite asserts ok=false on a uniform image (no grid).
func TestLocateMosaicBand_allWhite(t *testing.T) {
	img := newWhite(200, 100)
	if _, ok := locate.LocateMosaicBand(img); ok {
		t.Error("LocateMosaicBand(all-white): ok=true, want false")
	}
}

// TestLocateMosaicBand_coloredBackground verifies detection on a non-white background.
func TestLocateMosaicBand_coloredBackground(t *testing.T) {
	const bs = 8
	const w, h = 300, 200
	bg := color.RGBA{R: 230, G: 220, B: 200, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, bg)
		}
	}

	const (
		mL   = 60
		mT   = 64
		cols = 5
		rows = 4
	)
	for by := range rows {
		for bx := range cols {
			c := color.RGBA{R: 40, G: 40, B: 40, A: 255}
			if (bx+by)%2 == 0 {
				c = color.RGBA{R: 180, G: 180, B: 180, A: 255}
			}
			r := image.Rect(mL+bx*bs, mT+by*bs, mL+(bx+1)*bs, mT+(by+1)*bs)
			paintRect(img, r, c)
		}
	}

	want := image.Rect(mL, mT, mL+cols*bs, mT+rows*bs)
	got, ok := locate.LocateMosaicBand(img)
	if !ok {
		t.Fatalf("LocateMosaicBand: ok=false, want %v", want)
	}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestLocateMosaicBand_renderedText is the regression test for the documented
// failure: the blur-based LocateRedaction truncated the trailing "!" of
// "Hello World !" (x≤985 vs real x≤1177). We render, pixelate, embed in a
// large canvas with white margins, and assert the full mosaic width is found.
func TestLocateMosaicBand_renderedText(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}

	const (
		text     = "Hello World !"
		fontSize = 32.0
		bs       = 10
		margin   = 80
	)

	style := unpixel.Style{FontSize: fontSize, PaddingTop: 8, PaddingLeft: 8}
	rendered, sentinelX, err := r.Render(text, style)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Crop to text-only width (exclude the blue sentinel).
	textW := sentinelX
	rb := rendered.Bounds()
	textH := rb.Dy()

	// Snap to block-grid boundaries so the mosaic region is perfectly aligned.
	snapW := ((textW + bs - 1) / bs) * bs
	snapH := ((textH + bs - 1) / bs) * bs

	content := newWhite(snapW, snapH)
	for y := range textH {
		for x := range textW {
			content.SetRGBA(x, y, rendered.RGBAAt(rb.Min.X+x, rb.Min.Y+y))
		}
	}

	px := pixelate.NewLinearBlockAverage(bs)
	mosaic := px.Pixelate(content, 0, 0)

	// Embed in a full-screen canvas with large white margins.
	mb := mosaic.Bounds()
	fullW := margin + mb.Dx() + margin
	fullH := margin + mb.Dy() + margin
	full := newWhite(fullW, fullH)
	for y := range mb.Dy() {
		for x := range mb.Dx() {
			full.SetRGBA(margin+x, margin+y, mosaic.RGBAAt(mb.Min.X+x, mb.Min.Y+y))
		}
	}

	got, ok := locate.LocateMosaicBand(full)
	if !ok {
		t.Fatalf("LocateMosaicBand: ok=false, want a mosaic band covering %q", text)
	}

	// The detected rect must cover the ink content — including the trailing "!"
	// that LocateRedaction (blur-based) truncated. Outer padding columns/rows
	// (partial blocks filled by PaddingLeft/Top) are indistinguishable from the
	// background margin and may be excluded; only ink-carrying blocks matter.
	//
	// The pixelated "Hello World !" mosaic has its leftmost ink at bx=9 (x=90)
	// and rightmost ink at bx=26 (x=260, last "!" stroke). The detected bounds
	// must contain this ink span and must sit inside the mosaic region.
	inkMinX := margin + 10           // first ink block (PaddingLeft=8 → bx=9 → x=margin+10)
	inkMaxX := margin + mb.Dx() - 10 // last ink block (PaddingRight leaves last block = bx=27 pure white)
	mosaicMaxX := margin + mb.Dx()
	if got.Max.X < inkMaxX {
		t.Errorf("Max.X=%d < inkMaxX=%d: trailing '!' truncated (LocateRedaction regression)", got.Max.X, inkMaxX)
	}
	if got.Min.X > inkMinX {
		t.Errorf("Min.X=%d > inkMinX=%d: left ink edge missed", got.Min.X, inkMinX)
	}
	if got.Max.X > mosaicMaxX {
		t.Errorf("Max.X=%d > mosaicMaxX=%d: right edge beyond mosaic", got.Max.X, mosaicMaxX)
	}
	if got.Min.X < margin {
		t.Errorf("Min.X=%d < margin=%d: left edge into background", got.Min.X, margin)
	}
	// Vertical must contain ink rows (rows 9–11 in block-space, y=90–120).
	if got.Min.Y < margin || got.Min.Y > margin+10 {
		t.Errorf("Min.Y=%d not in mosaic top band [%d,%d]", got.Min.Y, margin, margin+10)
	}
	if got.Max.Y < margin+mb.Dy()-10 || got.Max.Y > margin+mb.Dy() {
		t.Errorf("Max.Y=%d not in mosaic bottom band [%d,%d]", got.Max.Y, margin+mb.Dy()-10, margin+mb.Dy())
	}
	t.Logf("LocateMosaicBand(%q): got %v (mosaic %dx%d)", text, got, mb.Dx(), mb.Dy())
}

// BenchmarkLocateMosaicBand measures locate cost on a 1920×1080 canvas with a
// central mosaic band. Results are stored in sink to defeat dead-code elimination.
func BenchmarkLocateMosaicBand(b *testing.B) {
	const bs = 10
	img, _ := syntheticMosaic(bs, 50*bs, 5*bs, 735, 505, 685, 525)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sink, _ = locate.LocateMosaicBand(img)
	}
}
