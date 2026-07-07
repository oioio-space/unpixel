//go:build ml

package rerank_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/rerank"
)

// TestEmissionReranker_contract checks the trained emission reranker returns a
// well-formed, score-sorted ranking and honours the no-verdicts case.
func TestEmissionReranker_contract(t *testing.T) {
	verdicts := []unpixel.Verdict{
		{Text: "hunter2", Distance: 0.02},
		{Text: "hunterz", Distance: 0.02},
	}
	ranked, err := rerank.Default().Rerank(t.Context(), nil, verdicts, func(string) float64 { return 0 }, 0.1)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(ranked) != len(verdicts) {
		t.Fatalf("len(ranked) = %d, want %d", len(ranked), len(verdicts))
	}
	for i := 1; i < len(ranked); i++ {
		if ranked[i-1].Blended > ranked[i].Blended {
			t.Errorf("ranked not sorted by Blended: [%d]=%.4f > [%d]=%.4f", i-1, ranked[i-1].Blended, i, ranked[i].Blended)
		}
	}
	if r, _ := rerank.Default().Rerank(t.Context(), nil, nil, nil, 0.1); r != nil {
		t.Errorf("Rerank(no verdicts) = %v, want nil", r)
	}
}

// TestEmissionReranker_breaksTie renders a real monospace string, block-pixelates
// it, and checks the emission reranker uses the IMAGE to prefer the true text over
// a confusable decoy that ties it on physical distance — the discriminative signal
// the language-only reranker lacks.
func TestEmissionReranker_breaksTie(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping emission-reranker decode in -short mode")
	}
	var mono fonts.Font
	for _, f := range fonts.All() {
		if f.Name == "Liberation Mono" {
			mono = f
			break
		}
	}
	rend, err := defaults.RendererFromFonts(mono.Data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	const truth = "aB7kQ"
	img, _, rerr := rend.Render(truth, unpixel.Style{FontSize: 30})
	if rerr != nil {
		t.Fatalf("render: %v", rerr)
	}
	mosaic := defaults.BlockAverage(6).Pixelate(imutil.ToRGBA(img), 0, 0)

	// Truth and a confusable decoy tied on physical distance; only the image-based
	// emission model can separate them.
	verdicts := []unpixel.Verdict{
		{Text: "a87kQ", Distance: 0.010},
		{Text: truth, Distance: 0.010},
	}
	ranked, err := rerank.Default().Rerank(t.Context(), mosaic, verdicts, func(string) float64 { return 0 }, 1.0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	for _, r := range ranked {
		t.Logf("candidate %-8q blended=%.4f distance=%.4f", r.Text, r.Blended, r.Distance)
	}
	if ranked[0].Text != truth {
		t.Errorf("emission reranker Best = %q, want %q (image should break the physical tie)", ranked[0].Text, truth)
	}
}
