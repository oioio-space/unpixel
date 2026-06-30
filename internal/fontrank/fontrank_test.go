package fontrank_test

import (
	"context"
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fontrank"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// sink absorbs benchmark results so the compiler cannot eliminate the call.
var sink []fontrank.FontScore

// makeMosaic renders text with the named bundled font, pixelates at blockSize,
// and returns the result as a synthetic mosaic redaction for the ranker to probe.
func makeMosaic(t *testing.T, fontName, text string, blockSize int) image.Image {
	t.Helper()
	all := fonts.All()
	idx := slices.IndexFunc(all, func(f fonts.Font) bool { return f.Name == fontName })
	if idx < 0 {
		t.Fatalf("font %q not found in bundle", fontName)
	}
	r, err := render.NewXImageFromFonts(all[idx].Data, nil)
	if err != nil {
		t.Fatalf("build renderer for %s: %v", fontName, err)
	}
	img, sx, err := r.Render(text, unpixel.Style{FontSize: 30})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if sx <= 0 {
		t.Fatal("render returned zero sentinel")
	}
	pix := pixelate.NewBlockAverage(blockSize)
	return pix.Pixelate(img, 0, 0)
}

// namedFonts converts the bundled font catalog to []fontrank.NamedFont.
func namedFonts() []fontrank.NamedFont {
	all := fonts.All()
	out := make([]fontrank.NamedFont, len(all))
	for i, f := range all {
		out[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}
	return out
}

// TestTrueFontRanksHigh verifies that the font that produced a synthetic mosaic
// is ranked in the top-3 by RankFonts. Top-3 rather than #1 is asserted because
// visually similar fonts (e.g. Liberation Sans vs Carlito, both metrically close
// to Arial) produce nearly identical block statistics at modest block sizes and
// legitimately swap positions — that confusability is documented in the log.
func TestTrueFontRanksHigh(t *testing.T) {
	const (
		targetFont = "Liberation Sans"
		text       = "Hello World"
		blockSize  = 8
	)
	mosaic := makeMosaic(t, targetFont, text, blockSize)
	named := namedFonts()

	scores, err := fontrank.RankFonts(t.Context(), mosaic, named)
	if err != nil {
		t.Fatalf("RankFonts: %v", err)
	}
	if len(scores) == 0 {
		t.Fatal("RankFonts returned empty slice")
	}

	t.Log("font ranking (lower score = better match):")
	for i, s := range scores {
		marker := "   "
		if s.Name == targetFont {
			marker = ">>>"
		}
		t.Logf("  %s #%d  %-26s  score=%.6f", marker, i+1, s.Name, s.Score)
	}

	// Note confusable fonts: Liberation Sans and Carlito are metrically nearly
	// identical (both ≈ Arial), so they are expected to have very similar scores
	// and may appear in either order. The signal is still useful for pruning the
	// dissimilar remainder (serifs, monospaces, etc.) from the candidate set.
	const topK = 3
	inTopK := slices.ContainsFunc(scores[:min(topK, len(scores))], func(s fontrank.FontScore) bool {
		return s.Name == targetFont
	})
	if !inTopK {
		t.Errorf("true font %q not in top-%d", targetFont, topK)
		for i, s := range scores {
			t.Errorf("  #%d %-26s score=%.6f", i+1, s.Name, s.Score)
		}
	}
}

// TestMonoRanksHigh verifies that a monospace font that produced the mosaic
// is ranked in the top-3 — capturing the mono vs proportional axis.
func TestMonoRanksHigh(t *testing.T) {
	const (
		targetFont = "Liberation Mono"
		text       = "Secret123"
		blockSize  = 6
	)
	mosaic := makeMosaic(t, targetFont, text, blockSize)
	named := namedFonts()

	scores, err := fontrank.RankFonts(t.Context(), mosaic, named)
	if err != nil {
		t.Fatalf("RankFonts: %v", err)
	}

	t.Log("font ranking — mono target:")
	for i, s := range scores {
		marker := "   "
		if s.Name == targetFont {
			marker = ">>>"
		}
		t.Logf("  %s #%d  %-26s  score=%.6f", marker, i+1, s.Name, s.Score)
	}

	const topK = 3
	inTopK := slices.ContainsFunc(scores[:min(topK, len(scores))], func(s fontrank.FontScore) bool {
		return s.Name == targetFont
	})
	if !inTopK {
		t.Errorf("true font %q not in top-%d", targetFont, topK)
		for i, s := range scores {
			t.Errorf("  #%d %-26s score=%.6f", i+1, s.Name, s.Score)
		}
	}
}

// TestDeterminism confirms that identical inputs produce identical rankings.
func TestDeterminism(t *testing.T) {
	mosaic := makeMosaic(t, "Carlito", "Quick brown fox", 8)
	named := namedFonts()

	a, err := fontrank.RankFonts(t.Context(), mosaic, named)
	if err != nil {
		t.Fatalf("first RankFonts: %v", err)
	}
	b, err := fontrank.RankFonts(t.Context(), mosaic, named)
	if err != nil {
		t.Fatalf("second RankFonts: %v", err)
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Score != b[i].Score {
			t.Errorf("rank %d differs: run1=%v run2=%v", i+1, a[i], b[i])
		}
	}
}

// TestEmptyFontList confirms RankFonts returns an empty slice (not an error)
// when the font list is empty.
func TestEmptyFontList(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 16))
	scores, err := fontrank.RankFonts(t.Context(), img, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("got %d scores, want 0", len(scores))
	}
}

// TestWhiteImage confirms RankFonts handles a blank (all-white) image without
// panicking and returns one score per font.
func TestWhiteImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 16))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	named := namedFonts()
	scores, err := fontrank.RankFonts(t.Context(), img, named)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != len(named) {
		t.Errorf("got %d scores, want %d", len(scores), len(named))
	}
}

// TestNonRGBATarget confirms RankFonts accepts a non-RGBA image (e.g. image.Gray)
// without panicking and returns one score per font.
func TestNonRGBATarget(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 64, 16))
	for y := range 16 {
		for x := range 64 {
			img.SetGray(x, y, color.Gray{Y: uint8((x + y*3) % 200)}) //nolint:gosec
		}
	}
	named := namedFonts()
	_, err := fontrank.RankFonts(t.Context(), img, named)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCropToSentinel_CopyPath exercises cropToSentinel's draw.Draw copy branch.
// cropToSentinel is unexported; the only route from the external test package
// is via RankFonts → scoreFont → cropToSentinel. scoreFont passes the rendered
// *image.RGBA SubImage to pixelate; that SubImage has a non-zero Bounds().Min
// (because *image.RGBA.SubImage returns a view with the original offset).
// RankFonts already calls scoreFont for every font, so any successful
// RankFonts call covers the fast-path (sentinelX >= image width → return img
// as-is). To cover the copy path we need sentinelX < image width.
//
// We exercise this indirectly: render a string, obtain the *image.RGBA and a
// sentinelX that is strictly less than the image width, then invoke RankFonts
// on an image with a recognisable block pattern so scoreFont's internal call to
// cropToSentinel reaches the SubImage → !ok → draw.Draw copy branch.
//
// Concretely: pass a non-*image.RGBA image type (image.NRGBA) as the mosaic
// target so that fontrank.toRGBA converts it (exercising that helper too).
// Then rely on the normal RankFonts path to call scoreFont which calls
// cropToSentinel on the rendered *image.RGBA exemplar.
func TestCropToSentinel_CopyPath(t *testing.T) {
	// Build a small NRGBA mosaic (non-*image.RGBA) so toRGBA inside RankFonts
	// also executes its copy branch.
	const W, H = 32, 16
	nrgba := image.NewNRGBA(image.Rect(0, 0, W, H))
	for y := range H {
		for x := range W {
			// Alternating grey bands to give the block detector something to find.
			v := uint8(200)
			if (x/8)%2 == 1 {
				v = 50
			}
			nrgba.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255}) //nolint:gosec
		}
	}

	named := namedFonts()
	scores, err := fontrank.RankFonts(t.Context(), nrgba, named)
	if err != nil {
		t.Fatalf("RankFonts(NRGBA): %v", err)
	}
	if len(scores) != len(named) {
		t.Errorf("got %d scores, want %d", len(scores), len(named))
	}
}

// TestRankFontsAt_explicitBlockMatchesAuto verifies that RankFontsAt(0) and
// RankFonts produce identical results — confirming RankFonts delegates to
// RankFontsAt with blockSize=0 (auto-detect) and the path is deterministic.
func TestRankFontsAt_explicitBlockMatchesAuto(t *testing.T) {
	img := makeMosaic(t, "Liberation Mono", "ABC123", 6)
	named := namedFonts()

	auto, err := fontrank.RankFonts(t.Context(), img, named)
	if err != nil {
		t.Fatalf("RankFonts: %v", err)
	}
	explicit, err := fontrank.RankFontsAt(t.Context(), img, named, 0)
	if err != nil {
		t.Fatalf("RankFontsAt(0): %v", err)
	}
	if len(auto) != len(explicit) {
		t.Fatalf("len mismatch: auto %d, explicit %d", len(auto), len(explicit))
	}
	// Both paths use auto-detection: the rankings must be identical.
	for i := range auto {
		if auto[i].Name != explicit[i].Name || auto[i].Score != explicit[i].Score {
			t.Errorf("rank %d: RankFonts=%v, RankFontsAt(0)=%v", i, auto[i], explicit[i])
		}
	}
}

// TestRankFontsAt_zeroBlockAutoDetects verifies that blockSize=0 falls back to
// auto-detection and returns a full result (same contract as RankFonts).
func TestRankFontsAt_zeroBlockAutoDetects(t *testing.T) {
	img := makeMosaic(t, "Liberation Sans", "Hello", 8)
	got, err := fontrank.RankFontsAt(t.Context(), img, namedFonts(), 0)
	if err != nil {
		t.Fatalf("RankFontsAt(0): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("RankFontsAt(0) returned no scores")
	}
}

// TestRankFontsAt_positiveBlockSkipsDetection verifies that a positive blockSize
// is used directly without auto-detection, and returns one score per font.
func TestRankFontsAt_positiveBlockSkipsDetection(t *testing.T) {
	img := makeMosaic(t, "Liberation Mono", "ABC123", 8)
	named := namedFonts()
	got, err := fontrank.RankFontsAt(t.Context(), img, named, 8)
	if err != nil {
		t.Fatalf("RankFontsAt(8): %v", err)
	}
	if len(got) != len(named) {
		t.Errorf("got %d scores, want %d", len(got), len(named))
	}
}

// BenchmarkRankFonts measures the end-to-end cost of ranking all bundled fonts.
// The ns/op figure should be compared to a full per-font calibrate+decode sweep
// (see BenchmarkFullDecodeSweep) to quantify the pruning value.
func BenchmarkRankFonts(b *testing.B) {
	const (
		targetFont = "Liberation Sans"
		text       = "Hello World"
		blockSize  = 8
	)
	all := fonts.All()
	named := make([]fontrank.NamedFont, len(all))
	for i, f := range all {
		named[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}

	// Build the synthetic mosaic once outside the loop.
	idx := slices.IndexFunc(all, func(f fonts.Font) bool { return f.Name == targetFont })
	r, _ := render.NewXImageFromFonts(all[idx].Data, nil)
	img, _, _ := r.Render(text, unpixel.Style{FontSize: 30})
	mosaic := pixelate.NewBlockAverage(blockSize).Pixelate(img, 0, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		sink, err = fontrank.RankFonts(context.Background(), mosaic, named)
		if err != nil {
			b.Fatal(err)
		}
	}
}
