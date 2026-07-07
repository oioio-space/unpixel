//go:build ml

package fontprior_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fontprior"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// TestMLPrior_ranksFonts checks the trained ML prior returns a well-formed,
// score-sorted ranking of the bundled fonts (no error), and the contract cases.
func TestMLPrior_ranksFonts(t *testing.T) {
	all := fonts.All()
	img := image.NewRGBA(image.Rect(0, 0, 64, 16))
	imutil.FillWhite(img)

	ranked, err := fontprior.Default().Rank(t.Context(), img, 6, all)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(ranked) != len(all) {
		t.Fatalf("len(ranked) = %d, want %d", len(ranked), len(all))
	}
	for i := 1; i < len(ranked); i++ {
		if ranked[i-1].Score > ranked[i].Score {
			t.Errorf("ranked not sorted: [%d]=%.4f > [%d]=%.4f", i-1, ranked[i-1].Score, i, ranked[i].Score)
		}
	}
	if r, _ := fontprior.Default().Rank(t.Context(), img, 6, nil); r != nil {
		t.Errorf("Rank(empty fonts) = %v, want nil", r)
	}
	if r, _ := fontprior.Default().Rank(t.Context(), nil, 6, all); r != nil {
		t.Errorf("Rank(nil img) = %v, want nil", r)
	}
}

// TestMLPrior_topKAccuracy renders each bundled font (with text unseen in training)
// through the mosaic operator and checks the trained classifier ranks the true font
// in its top-3 more often than chance — evidence it learned the render→pixelate
// font signature rather than emitting noise.
func TestMLPrior_topKAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ML font-ID accuracy in -short mode")
	}
	all := fonts.All()
	const block = 6
	px := defaults.BlockAverage(block)

	top3 := 0
	for _, f := range all {
		r, err := defaults.RendererFromFonts(f.Data, nil)
		if err != nil {
			t.Fatalf("renderer %s: %v", f.Name, err)
		}
		img, _, rerr := r.Render("verify redaction sample", unpixel.Style{FontSize: 30})
		if rerr != nil {
			t.Fatalf("render %s: %v", f.Name, rerr)
		}
		mosaic := px.Pixelate(imutil.ToRGBA(img), 0, 0)

		ranked, err := fontprior.Default().Rank(t.Context(), mosaic, block, all)
		if err != nil {
			t.Fatalf("Rank %s: %v", f.Name, err)
		}
		for i := 0; i < 3 && i < len(ranked); i++ {
			if ranked[i].Name == f.Name {
				top3++
				break
			}
		}
		t.Logf("font=%-18s top1=%q", f.Name, ranked[0].Name)
	}
	// Chance top-3 over 9 fonts ≈ 3/9; training is deterministic (zero-init
	// full-batch GD), so the score is reproducible. featurize adds a spatial SHAPE
	// signature (vertical ink-density profile + ink run-length histogram) to
	// lumHist's ink-density histogram, which resolves most of the residual
	// same-family confusions (mono↔mono, sans↔sans) that ink density alone missed.
	const want = 8
	if top3 < want {
		t.Errorf("top-3 accuracy = %d/%d, want >= %d (well above chance)", top3, len(all), want)
	}
	t.Logf("ML font-ID top-3 accuracy: %d/%d", top3, len(all))
}
