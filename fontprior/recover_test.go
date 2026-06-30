//go:build !ml

package fontprior_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fontprior"
	"github.com/oioio-space/unpixel/fonts"
)

func TestRecoverWithPrior_reorderMatchesMultiFontWinner(t *testing.T) {
	const block = 6
	img := mosaicOf(t, "Liberation Mono", "GO2024", block)

	// Reorder-only (no top-K): winner must equal the plain multi-font winner.
	pri, err := fontprior.RecoverWithPrior(t.Context(), img, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("RecoverWithPrior: %v", err)
	}
	if len(pri) == 0 || pri[0].Font == "" {
		t.Fatalf("RecoverWithPrior returned no named result")
	}

	// Build the same sweep without the prior, via the library multi-font path.
	rs := bundledRenderers(t)
	multi, err := unpixel.RecoverMultiFont(t.Context(), img, rs, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("RecoverMultiFont: %v", err)
	}
	if got, want := pri[0].Result.BestGuess, multi[0].Result.BestGuess; got != want {
		t.Errorf("prior winner %q != multi-font winner %q (reorder must preserve the winner)", got, want)
	}
}

func TestRecoverWithPrior_topKLimitsDecodes(t *testing.T) {
	const block = 6
	img := mosaicOf(t, "Liberation Serif", "Redacted", block)
	res, err := fontprior.RecoverWithPrior(t.Context(), img,
		unpixel.WithBlockSize(block), unpixel.WithFontPriorTopK(2))
	if err != nil {
		t.Fatalf("RecoverWithPrior topK: %v", err)
	}
	if len(res) > 2 {
		t.Errorf("top-K=2 returned %d font results; want <= 2", len(res))
	}
}

func bundledRenderers(t *testing.T) []unpixel.Renderer {
	t.Helper()
	rs, err := fonts.Renderers()
	if err != nil {
		t.Fatalf("fonts.Renderers: %v", err)
	}
	return rs
}
