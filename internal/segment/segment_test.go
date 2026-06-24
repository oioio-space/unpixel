package segment_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/segment"
)

// whiteImage returns an opaque white RGBA image of size w×h.
func whiteImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, white)
		}
	}
	return img
}

// fillRect paints a solid black rectangle on img.
func fillRect(img *image.RGBA, r image.Rectangle) {
	black := color.RGBA{A: 255}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, black)
		}
	}
}

// TestLines_TwoBands verifies that Lines detects two horizontal ink bands
// separated by a wide white gap and returns tight y-ranges for each.
func TestLines_TwoBands(t *testing.T) {
	img := whiteImage(200, 100)
	// Band 1: rows 10–29 (height 20).
	fillRect(img, image.Rect(20, 10, 160, 30))
	// Band 2: rows 60–79 (height 20). Gap rows 30–59 is all white.
	fillRect(img, image.Rect(30, 60, 140, 80))

	lines := segment.Lines(img)
	if len(lines) != 2 {
		t.Fatalf("Lines: got %d rects, want 2; rects = %v", len(lines), lines)
	}

	// Y-ranges must be tight to the inked rows.
	if got, want := lines[0].Min.Y, 10; got != want {
		t.Errorf("lines[0].Min.Y = %d, want %d", got, want)
	}
	if got, want := lines[0].Max.Y, 30; got != want {
		t.Errorf("lines[0].Max.Y = %d, want %d", got, want)
	}
	if got, want := lines[1].Min.Y, 60; got != want {
		t.Errorf("lines[1].Min.Y = %d, want %d", got, want)
	}
	if got, want := lines[1].Max.Y, 80; got != want {
		t.Errorf("lines[1].Max.Y = %d, want %d", got, want)
	}

	// X-ranges must include the inked columns.
	if lines[0].Min.X > 20 || lines[0].Max.X < 160 {
		t.Errorf("lines[0] x-range %v too narrow, want to include [20, 160)", lines[0])
	}
	if lines[1].Min.X > 30 || lines[1].Max.X < 140 {
		t.Errorf("lines[1] x-range %v too narrow, want to include [30, 140)", lines[1])
	}
}

// TestWords_TwoWords verifies that Words splits a line band into exactly 2
// words when the inter-word gap exceeds the threshold, and that small
// intra-word/inter-glyph gaps do NOT cause a split.
func TestWords_TwoWords(t *testing.T) {
	// Band height H = 40. WordBreakGap(40) = round(0.3*40) = 12.
	// Use a wide gap (16 px > 12) between the two words.
	// Within each word, use a 4-px gap (< 12) that must be merged.
	const H = 40
	img := whiteImage(300, H)

	// Word 1: blocks at x=[5, 35) and x=[39, 69); 4-px gap between them.
	fillRect(img, image.Rect(5, 5, 35, H-5))
	fillRect(img, image.Rect(39, 5, 69, H-5))

	// Inter-word gap: x=[69, 85) — 16 px, exceeds threshold.

	// Word 2: blocks at x=[85, 115) and x=[119, 149); 4-px gap between them.
	fillRect(img, image.Rect(85, 5, 115, H-5))
	fillRect(img, image.Rect(119, 5, 149, H-5))

	line := image.Rect(0, 0, 300, H)
	words := segment.Words(img, line)
	if len(words) != 2 {
		t.Fatalf("Words: got %d rects, want 2; rects = %v", len(words), words)
	}

	// Word 1 must span both of its blocks.
	if words[0].Min.X > 5 || words[0].Max.X < 69 {
		t.Errorf("words[0] x-range %v too narrow, want to include [5, 69)", words[0])
	}
	// Word 2 must span both of its blocks.
	if words[1].Min.X > 85 || words[1].Max.X < 149 {
		t.Errorf("words[1] x-range %v too narrow, want to include [85, 149)", words[1])
	}
}

// TestWords_ThresholdBoundary pins the chosen k = 0.15 constant.
// A gap of WordBreakGap(H)-1 stays merged; a gap of WordBreakGap(H) splits.
func TestWords_ThresholdBoundary(t *testing.T) {
	// k = 0.15, H = 40 → threshold = round(0.15×40) = 6.
	const H = 40
	threshold := segment.WordBreakGap(H)
	if threshold != 6 {
		t.Fatalf("WordBreakGap(40) = %d, want 6 (k=0.15)", threshold)
	}

	const blockW = 30 // width of each inked block

	runCase := func(t *testing.T, name string, gap, wantWords int) {
		t.Helper()
		img := whiteImage(200, H)
		fillRect(img, image.Rect(10, 5, 10+blockW, H-5))
		fillRect(img, image.Rect(10+blockW+gap, 5, 10+blockW+gap+blockW, H-5))
		line := image.Rect(0, 0, 200, H)
		words := segment.Words(img, line)
		if len(words) != wantWords {
			t.Errorf("%s: gap=%d, got %d words, want %d", name, gap, len(words), wantWords)
		}
	}

	runCase(t, "below threshold (merged)", threshold-1, 1)
	runCase(t, "at threshold (split)", threshold, 2)
}

// TestAllWhite confirms that Lines and Segment return empty slices (not nil
// panics) for an all-white image.
func TestAllWhite(t *testing.T) {
	img := whiteImage(100, 50)

	if lines := segment.Lines(img); len(lines) != 0 {
		t.Errorf("Lines(white): got %v, want []", lines)
	}
	if seg := segment.Segment(img); len(seg) != 0 {
		t.Errorf("Segment(white): got %v, want []", seg)
	}
}

// TestLines_zeroSizeImage verifies that Lines on a 0×0 image returns a
// non-nil empty slice without panicking (the Dx==0||Dy==0 early-return path).
func TestLines_zeroSizeImage(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 0, 0))
	got := segment.Lines(img)
	if got == nil {
		t.Error("Lines(0×0) returned nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("Lines(0×0) = %v, want []", got)
	}
}

// TestWords_lineOutsideImage verifies that Words returns an empty slice when
// the supplied line rectangle does not overlap the image bounds.
func TestWords_lineOutsideImage(t *testing.T) {
	t.Parallel()
	img := whiteImage(50, 50)
	// A line rect entirely below the image.
	outsideLine := image.Rect(0, 100, 50, 150)
	got := segment.Words(img, outsideLine)
	if len(got) != 0 {
		t.Errorf("Words(outsideLine): got %v, want []", got)
	}
}

// TestWords_allWhiteBand verifies that Words on a non-empty all-white band
// returns an empty (non-nil) slice — the result==nil guard path.
func TestWords_allWhiteBand(t *testing.T) {
	t.Parallel()
	img := whiteImage(80, 20)
	line := image.Rect(0, 0, 80, 20)
	got := segment.Words(img, line)
	if len(got) != 0 {
		t.Errorf("Words(allWhiteBand): got %v, want []", got)
	}
}

// TestSegment_RenderedText is the integration test: it renders "le chat dort"
// with the real font renderer, pixelates with block size 8, then segments.
// Expected: 1 line, 3 words.
//
// The test uses a font size of 32 pt which, at block size 8, produces a band
// of roughly 5–6 blocks tall — enough signal to separate the three words.
// Word count is asserted ==3; if proportional spacing ever makes the count
// ambiguous, this comment is the record and the assertion should be tightened,
// not relaxed.
func TestSegment_RenderedText(t *testing.T) {
	const (
		text      = "le chat dort"
		blockSize = 8
		fontSize  = 32.0
	)

	renderer, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}

	style := unpixel.Style{
		FontSize:    fontSize,
		PaddingLeft: 8,
		PaddingTop:  8,
	}
	rendered, _, err := renderer.Render(text, style)
	if err != nil {
		t.Fatalf("Render(%q): %v", text, err)
	}

	// Pixelate with sRGB block average (the default pipeline).
	pix := pixelate.NewBlockAverage(blockSize)
	pixelated := pix.Pixelate(rendered, 0, 0)

	lines := segment.Lines(pixelated)
	if len(lines) != 1 {
		t.Fatalf("Lines: got %d, want 1; lines = %v", len(lines), lines)
	}

	words := segment.Words(pixelated, lines[0])
	if len(words) != 3 {
		t.Errorf("Words: got %d, want 3; words = %v", len(words), words)
	}

	// Segment must agree with the two-step result.
	seg := segment.Segment(pixelated)
	if len(seg) != 1 {
		t.Fatalf("Segment: got %d lines, want 1", len(seg))
	}
	if len(seg[0]) != len(words) {
		t.Errorf("Segment line[0]: %d words, Lines+Words gave %d", len(seg[0]), len(words))
	}
}

// BenchmarkSegment measures the hot-path cost of segmenting a rendered and
// pixelated multi-word line. b.ReportAllocs is required by the benchmark rule.
func BenchmarkSegment(b *testing.B) {
	const (
		text      = "le chat dort"
		blockSize = 8
		fontSize  = 32.0
	)

	renderer, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: fontSize, PaddingLeft: 8, PaddingTop: 8}
	rendered, _, err := renderer.Render(text, style)
	if err != nil {
		b.Fatalf("Render: %v", err)
	}
	pixelated := pixelate.NewBlockAverage(blockSize).Pixelate(rendered, 0, 0)

	b.ReportAllocs()
	for b.Loop() {
		_ = segment.Segment(pixelated)
	}
}
