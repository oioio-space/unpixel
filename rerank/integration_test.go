//go:build !ml

package rerank_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire Renderer/Pixelator/Metric
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/rerank"
)

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

func TestRerank_endToEnd(t *testing.T) {
	const block = 6
	img := mosaicOf(t, "Liberation Sans", "the", block)
	cands := []string{"the", "tho", "thy"}

	// weight 0: physical order; the true text should verify well.
	phys, err := rerank.Rerank(t.Context(), img, cands,
		unpixel.WithBlockSize(block), unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz"))
	if err != nil {
		t.Fatalf("Rerank(0): %v", err)
	}
	if len(phys) != len(cands) {
		t.Fatalf("ranked len = %d; want %d", len(phys), len(cands))
	}

	// weight > 0: still returns all candidates, best-first by blended score.
	fused, err := rerank.Rerank(t.Context(), img, cands,
		unpixel.WithBlockSize(block), unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz"),
		unpixel.WithRerankWeight(0.08))
	if err != nil {
		t.Fatalf("Rerank(0.08): %v", err)
	}
	if len(fused) != len(cands) {
		t.Errorf("fused len = %d; want %d", len(fused), len(cands))
	}
	// Blended is sorted ascending.
	for i := 1; i < len(fused); i++ {
		if fused[i].Blended < fused[i-1].Blended {
			t.Errorf("not sorted by Blended at %d: %v", i, fused)
		}
	}
}
