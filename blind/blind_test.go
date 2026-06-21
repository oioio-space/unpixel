package blind_test

import (
	"image"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/blind"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

const (
	testBlock = 8
	// testFontSize must be large enough that block=8 pixelation preserves a
	// blank column between words in a two-word phrase.  At 26 pt the gap between
	// "the" and "cat" (inkW=76 → pixW=80) collapses to zero after LinearBlockAverage,
	// giving segment.Words one word instead of two.  At 32 pt the rendered gap
	// (pixW=104, words at [0,48) and [56,96)) is one full block wide.
	testFontSize = 32.0
)

// sink defeats dead-code elimination in benchmarks.
var sink blind.Result

// syntheticBand renders phrase at testFontSize, crops to the ink bounding box,
// and pixelates it with LinearBlockAverage(testBlock) at offsetX.
//
// The result is a tight pixelated band with no extra margin.  blinddecode.Recover
// uses the full image width (ib.Dx()) for the line band, so any margin would
// shift block alignment for every word after the first and corrupt scoring.
func syntheticBand(t *testing.T, phrase string, offsetX int) image.Image {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	img, sx, err := r.Render(phrase, unpixel.Style{FontSize: testFontSize})
	if err != nil {
		t.Fatalf("render %q: %v", phrase, err)
	}
	ink := inkBounds(img, sx)
	inkImg := image.NewRGBA(image.Rect(0, 0, ink.Dx(), ink.Dy()))
	xdraw.Draw(inkImg, inkImg.Bounds(), img, ink.Min, xdraw.Src)
	pix := pixelate.NewLinearBlockAverage(testBlock)
	return pix.Pixelate(inkImg, offsetX, 0)
}

// syntheticBandB is the benchmark variant of syntheticBand.
func syntheticBandB(b *testing.B, phrase string, offsetX int) image.Image {
	b.Helper()
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	img, sx, err := r.Render(phrase, unpixel.Style{FontSize: testFontSize})
	if err != nil {
		b.Fatalf("render %q: %v", phrase, err)
	}
	ink := inkBounds(img, sx)
	inkImg := image.NewRGBA(image.Rect(0, 0, ink.Dx(), ink.Dy()))
	xdraw.Draw(inkImg, inkImg.Bounds(), img, ink.Min, xdraw.Src)
	pix := pixelate.NewLinearBlockAverage(testBlock)
	return pix.Pixelate(inkImg, offsetX, 0)
}

// inkBounds returns the tight bounding box of non-white pixels in [0, sentinelX).
func inkBounds(img *image.RGBA, sentinelX int) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := sentinelX, b.Dy(), 0, 0
	for y := range b.Dy() {
		for x := range sentinelX {
			c := img.RGBAAt(x, y)
			lum := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
			if lum < 240 {
				x0 = min(x0, x)
				y0 = min(y0, y)
				x1 = max(x1, x+1)
				y1 = max(y1, y+1)
			}
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return image.Rect(0, 0, 1, 1)
	}
	return image.Rect(x0, y0, x1, y1)
}

// TestWithLanguage_Plumbing verifies the Option plumbing and ParseLanguage
// round-trip without running a full decode.
func TestWithLanguage_Plumbing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input   string
		wantISO string
		wantOK  bool
	}{
		{"en", "en", true},
		{"english", "en", true},
		{"fr", "fr", true},
		{"french", "fr", true},
		{"français", "fr", true},
		{"de", "", false},
		{"", "", false},
	} {
		l, ok := lang.ParseLanguage(tc.input)
		if ok != tc.wantOK {
			t.Errorf("ParseLanguage(%q): ok=%v, want %v", tc.input, ok, tc.wantOK)
			continue
		}
		if ok && l.String() != tc.wantISO {
			t.Errorf("ParseLanguage(%q).String()=%q, want %q", tc.input, l.String(), tc.wantISO)
		}
	}
}

// TestDefaultOptions_BlockResolve verifies that WithBlock is forwarded into
// Result.Block and that Result.Lang matches the selected language code.
// No full decode is performed — the image is a tight pixelated band of "ok"
// with explicitly pinned block and font size, so InferBlockSize is bypassed.
func TestDefaultOptions_BlockResolve(t *testing.T) {
	t.Parallel()
	img := syntheticBand(t, "ok", 0)

	result, err := blind.Recover(t.Context(), img,
		blind.WithLanguage(lang.English),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result.Block != testBlock {
		t.Errorf("Block: got %d, want %d", result.Block, testBlock)
	}
	if result.Lang != "en" {
		t.Errorf("Lang: got %q, want %q", result.Lang, "en")
	}
}

// TestRecover_English verifies that blind.Recover recovers the exact text
// "the cat" from a synthetic whole-line mosaic band using the English model.
func TestRecover_English(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}

	const phrase = "the cat"
	img := syntheticBand(t, phrase, 0)

	result, err := blind.Recover(t.Context(), img,
		blind.WithLanguage(lang.English),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithFonts("sans"),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	t.Logf("English: text=%q font=%q dist=%.6f", result.Text, result.Font, result.Dist)
	if result.Text != phrase {
		t.Errorf("got %q, want %q", result.Text, phrase)
	}
}

// TestRecover_French verifies that blind.Recover recovers the exact text
// "le chat" from a synthetic whole-line mosaic band using the French model.
func TestRecover_French(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}

	const phrase = "le chat"
	img := syntheticBand(t, phrase, 0)

	result, err := blind.Recover(t.Context(), img,
		blind.WithLanguage(lang.French),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithFonts("sans"),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	t.Logf("French: text=%q font=%q dist=%.6f", result.Text, result.Font, result.Dist)
	if result.Text != phrase {
		t.Errorf("got %q, want %q", result.Text, phrase)
	}
}

// BenchmarkRecover measures the per-call cost of blind.Recover on a tiny
// synthetic band (block=8, "ok", sans fonts only).  Setup is outside the loop.
func BenchmarkRecover(b *testing.B) {
	img := syntheticBandB(b, "ok", 0)
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		var err error
		sink, err = blind.Recover(ctx, img,
			blind.WithLanguage(lang.English),
			blind.WithBlock(testBlock),
			blind.WithFontSize(testFontSize),
			blind.WithFonts("sans"),
		)
		if err != nil {
			b.Fatalf("Recover: %v", err)
		}
	}
}
