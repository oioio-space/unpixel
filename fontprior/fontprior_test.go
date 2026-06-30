package fontprior_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fontprior"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// mosaicOf renders text in the named bundled font and pixelates it in memory.
func mosaicOf(t *testing.T, fontName, text string, block int) image.Image {
	t.Helper()
	var data []byte
	for _, f := range fonts.All() {
		if f.Name == fontName {
			data = f.Data
			break
		}
	}
	if data == nil {
		t.Fatalf("bundled font %q not found", fontName)
	}
	r, err := render.NewXImageFromFonts(data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	rendered, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}
	return pixelate.NewBlockAverage(block).Pixelate(rendered, 0, 0)
}

func rankOf(ranked []fontprior.Ranked, name string) int {
	for i, r := range ranked {
		if r.Name == name {
			return i
		}
	}
	return -1
}

func TestHistogram_ranksTrueFontTop3(t *testing.T) {
	// Distinct-category fonts the histogram should separate reliably.
	cases := []struct{ font, text string }{
		{"Liberation Mono", "ABC123"},
		{"Liberation Serif", "The quick brown fox jumps"},
		{"Liberation Sans", "Mosaic"},
	}
	const block = 6
	top1, top3 := 0, 0
	for _, c := range cases {
		img := mosaicOf(t, c.font, c.text, block)
		ranked, err := fontprior.Histogram{}.Rank(t.Context(), img, block, fonts.All())
		if err != nil {
			t.Fatalf("Rank(%q): %v", c.font, err)
		}
		r := rankOf(ranked, c.font)
		if r == 0 {
			top1++
		}
		if r >= 0 && r < 3 {
			top3++
		} else {
			t.Errorf("font %q ranked %d (want top-3); order=%v", c.font, r, ranked)
		}
	}
	t.Logf("font prior: top1 %d/%d, top3 %d/%d", top1, len(cases), top3, len(cases))
}

func TestHistogram_emptyFontsReturnsNil(t *testing.T) {
	got, err := fontprior.Histogram{}.Rank(t.Context(), image.NewRGBA(image.Rect(0, 0, 10, 10)), 0, nil)
	if err != nil {
		t.Fatalf("Rank(nil fonts): %v", err)
	}
	if got != nil {
		t.Errorf("Rank(nil fonts) = %v; want nil", got)
	}
}
