package fontrank_test

import (
	"math"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fontrank"
	"github.com/oioio-space/unpixel/internal/render"
)

// rendererFor returns an XImage renderer for the named bundled font.
func rendererFor(t *testing.T, fontName string) *render.XImage {
	t.Helper()
	for _, f := range fonts.All() {
		if f.Name == fontName {
			r, err := render.NewXImageFromFonts(f.Data, nil)
			if err != nil {
				t.Fatalf("build renderer for %s: %v", fontName, err)
			}
			return r
		}
	}
	t.Fatalf("font %q not in bundle", fontName)
	return nil
}

// TestFingerprintFromGlyphs_TrueFontRanksFirst renders known text with a
// chosen font, fingerprints the result, and asserts the true font ranks #1
// (or #2 in documented near-tie cases) among the bundled candidates.
func TestFingerprintFromGlyphs_TrueFontRanksFirst(t *testing.T) {
	const (
		targetFont = "Liberation Mono"
		knownText  = "Hello World"
		fontSize   = 32.0
	)

	r := rendererFor(t, targetFont)
	img, sentinelX, err := r.Render(knownText, unpixel.Style{FontSize: fontSize})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	fp, err := fontrank.FingerprintFromGlyphs(img, knownText, sentinelX)
	if err != nil {
		t.Fatalf("FingerprintFromGlyphs: %v", err)
	}
	t.Logf("fingerprint: xHeightRatio=%.4f capHeightRatio=%.4f meanAdvanceRatio=%.4f",
		fp.XHeightRatio, fp.CapHeightRatio, fp.MeanAdvanceRatio)

	named := namedFonts()
	ranked := fontrank.RankByMetrics(fp, named)

	t.Log("metric fingerprint ranking (lower = better match):")
	for i, r := range ranked {
		marker := "   "
		if r.Name == targetFont {
			marker = ">>>"
		}
		t.Logf("  %s #%d  %-26s  dist=%.6f", marker, i+1, r.Name, r.Dist)
	}

	// Liberation Mono is a monospace font — its advance ratio is distinctly
	// different from proportional fonts, so it should rank #1.
	if len(ranked) == 0 || ranked[0].Name != targetFont {
		top := ""
		if len(ranked) > 0 {
			top = ranked[0].Name
		}
		t.Errorf("true font %q not ranked #1; got %q", targetFont, top)
	}
}

// TestFingerprintFromGlyphs_SansFont verifies that a proportional sans font
// ranks in the top-2 when its own rendered glyphs are fingerprinted.
// Liberation Sans and Carlito are documented near-ties (both ≈ Arial metrics),
// so top-2 is the assertion rather than strict #1.
func TestFingerprintFromGlyphs_SansFont(t *testing.T) {
	const (
		targetFont = "Liberation Sans"
		knownText  = "The quick brown fox"
		fontSize   = 28.0
	)

	r := rendererFor(t, targetFont)
	img, sentinelX, err := r.Render(knownText, unpixel.Style{FontSize: fontSize})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	fp, err := fontrank.FingerprintFromGlyphs(img, knownText, sentinelX)
	if err != nil {
		t.Fatalf("FingerprintFromGlyphs: %v", err)
	}
	t.Logf("fingerprint: xHeightRatio=%.4f capHeightRatio=%.4f meanAdvanceRatio=%.4f",
		fp.XHeightRatio, fp.CapHeightRatio, fp.MeanAdvanceRatio)

	named := namedFonts()
	ranked := fontrank.RankByMetrics(fp, named)

	t.Log("metric fingerprint ranking — sans target:")
	for i, r := range ranked {
		marker := "   "
		if r.Name == targetFont {
			marker = ">>>"
		}
		t.Logf("  %s #%d  %-26s  dist=%.6f", marker, i+1, r.Name, r.Dist)
	}

	const topK = 2
	inTopK := false
	for _, r := range ranked[:min(topK, len(ranked))] {
		if r.Name == targetFont {
			inTopK = true
		}
	}
	if !inTopK {
		t.Errorf("true font %q not in top-%d", targetFont, topK)
	}
}

// TestFingerprintFromGlyphs_NearTie documents that Liberation Sans and Carlito
// produce very similar fingerprints (both ≈ Arial) and may appear in either
// order. The test asserts both are in the top-3 and logs the scores for
// human inspection.
func TestFingerprintFromGlyphs_NearTie(t *testing.T) {
	const (
		fontA     = "Liberation Sans"
		fontB     = "Carlito"
		knownText = "Hello World"
		fontSize  = 32.0
	)

	rA := rendererFor(t, fontA)
	imgA, sxA, err := rA.Render(knownText, unpixel.Style{FontSize: fontSize})
	if err != nil {
		t.Fatalf("render %s: %v", fontA, err)
	}

	fp, err := fontrank.FingerprintFromGlyphs(imgA, knownText, sxA)
	if err != nil {
		t.Fatalf("FingerprintFromGlyphs: %v", err)
	}

	named := namedFonts()
	ranked := fontrank.RankByMetrics(fp, named)

	t.Log("near-tie: Liberation Sans vs Carlito ranking:")
	for i, r := range ranked {
		t.Logf("  #%d  %-26s  dist=%.6f", i+1, r.Name, r.Dist)
	}

	// Both near-tie fonts must appear in top-3; order is not asserted.
	const topK = 3
	inTopK := func(name string) bool {
		for _, r := range ranked[:min(topK, len(ranked))] {
			if r.Name == name {
				return true
			}
		}
		return false
	}
	if !inTopK(fontA) {
		t.Errorf("%s not in top-%d", fontA, topK)
	}
	if !inTopK(fontB) {
		t.Errorf("%s not in top-%d", fontB, topK)
	}

	// Document the distance gap between fontA and fontB (informational).
	distA, distB := math.NaN(), math.NaN()
	for _, r := range ranked {
		switch r.Name {
		case fontA:
			distA = r.Dist
		case fontB:
			distB = r.Dist
		}
	}
	t.Logf("distance gap |%s - %s| = %.6f (near-tie expected)", fontA, fontB, math.Abs(distA-distB))
}

// TestFingerprintFromGlyphs_InvalidInputs verifies that degenerate inputs
// (empty image, empty text, zero sentinelX) return errors or safe zero values
// rather than panicking.
func TestFingerprintFromGlyphs_InvalidInputs(t *testing.T) {
	t.Parallel()

	t.Run("empty text", func(t *testing.T) {
		t.Parallel()
		const (
			targetFont = "Liberation Sans"
			fontSize   = 32.0
		)
		r := rendererFor(t, targetFont)
		img, sx, err := r.Render("x", unpixel.Style{FontSize: fontSize})
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		_, err = fontrank.FingerprintFromGlyphs(img, "", sx)
		if err == nil {
			t.Error("expected error for empty knownText, got nil")
		}
	})

	t.Run("zero sentinel", func(t *testing.T) {
		t.Parallel()
		const (
			targetFont = "Liberation Sans"
			fontSize   = 32.0
		)
		r := rendererFor(t, targetFont)
		img, _, err := r.Render("Hello", unpixel.Style{FontSize: fontSize})
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		_, err = fontrank.FingerprintFromGlyphs(img, "Hello", 0)
		if err == nil {
			t.Error("expected error for sentinelX=0, got nil")
		}
	})
}

// TestRankByMetrics_EmptyFonts verifies RankByMetrics returns nil for an empty
// font list.
func TestRankByMetrics_EmptyFonts(t *testing.T) {
	t.Parallel()
	fp := fontrank.Fingerprint{XHeightRatio: 0.5, CapHeightRatio: 0.7, MeanAdvanceRatio: 0.6}
	ranked := fontrank.RankByMetrics(fp, nil)
	if ranked != nil {
		t.Errorf("got %v, want nil", ranked)
	}
}

// BenchmarkFingerprintFromGlyphs measures the cost of extracting a glyph
// metric fingerprint from a pre-rendered image.
func BenchmarkFingerprintFromGlyphs(b *testing.B) {
	const (
		targetFont = "Liberation Sans"
		knownText  = "Hello World"
		fontSize   = 32.0
	)
	all := fonts.All()
	var data []byte
	for _, f := range all {
		if f.Name == targetFont {
			data = f.Data
		}
	}
	r, err := render.NewXImageFromFonts(data, nil)
	if err != nil {
		b.Fatalf("build renderer: %v", err)
	}
	img, sx, err := r.Render(knownText, unpixel.Style{FontSize: fontSize})
	if err != nil {
		b.Fatalf("render: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		fp, err := fontrank.FingerprintFromGlyphs(img, knownText, sx)
		if err != nil {
			b.Fatal(err)
		}
		_ = fp
	}
}

// BenchmarkRankByMetrics measures the cost of ranking all bundled fonts by
// their metric fingerprint against a pre-computed image fingerprint.
func BenchmarkRankByMetrics(b *testing.B) {
	const (
		targetFont = "Liberation Sans"
		knownText  = "Hello World"
		fontSize   = 32.0
	)
	all := fonts.All()
	var data []byte
	for _, f := range all {
		if f.Name == targetFont {
			data = f.Data
		}
	}
	r, err := render.NewXImageFromFonts(data, nil)
	if err != nil {
		b.Fatalf("build renderer: %v", err)
	}
	img, sx, err := r.Render(knownText, unpixel.Style{FontSize: fontSize})
	if err != nil {
		b.Fatalf("render: %v", err)
	}
	fp, err := fontrank.FingerprintFromGlyphs(img, knownText, sx)
	if err != nil {
		b.Fatalf("FingerprintFromGlyphs: %v", err)
	}
	named := namedFonts()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ranked := fontrank.RankByMetrics(fp, named)
		_ = ranked
	}
}
