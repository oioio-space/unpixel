// Package blinddecode_test — TDD tests for P6.4 (whole-line re-ranking) and
// P6.5 (font-family sweep).
package blinddecode_test

import (
	"image"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/blinddecode"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// syntheticLineBand renders phrase using r at fontSize, pixelates the whole
// rendered line with LinearBlockAverage(block) at offsetX, crops to the ink
// bounding box, and returns the pixelated band.
//
// This models a real whole-line mosaic redaction: all words share the same
// pixelation pass, so their block phases are set by their column position
// within the line (not by each word's own left edge). DecodeLineWhole is
// designed to score hypotheses in this same whole-line context.
func syntheticLineBand(t *testing.T, r unpixel.Renderer, phrase string, block int, fontSize float64, offsetX int) *image.RGBA {
	t.Helper()
	img, sx, err := r.Render(phrase, unpixel.Style{FontSize: fontSize})
	if err != nil {
		t.Fatalf("render %q: %v", phrase, err)
	}
	bb := inkBoundsT(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	p := pixelate.NewLinearBlockAverage(block)
	return p.Pixelate(ink, offsetX, 0)
}

// wholeLineDecoder builds a Decoder suitable for whole-line recovery tests.
// TopK=50 is set explicitly so the caller bypasses the adaptive combination
// cap inside DecodeLineWhole — this guarantees that even low-prior-rank words
// (e.g. English "cat" at rank 41/87) appear in the per-band candidate pool.
// The total combination count stays tractable by keeping test phrases to ≤3
// words (50^3 = 125 K renders, ~125 ms at 1 µs/render).
func wholeLineDecoder(t *testing.T, l lang.Language) *blinddecode.Decoder {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	return blinddecode.New(blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewLinearBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(l),
		Prior:     lang.PriorFor(l),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      0.005,
		TopK:      50, // explicit: bypasses adaptive cap, includes rank-41 "cat"
		BeamWidth: 8,
	})
}

// wholeLineRenderer returns a renderer matching the decoder's font.
func wholeLineRenderer(t *testing.T) unpixel.Renderer {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	return r
}

// TestDecodeLineWhole_French verifies that DecodeLineWhole recovers a French
// three-word phrase as the top-1 candidate from a whole-line pixelated band.
//
// Phrase chosen: "le chat est" — all three words are in the French dictionary.
//   - "le":   prior rank 0/27  (very common)
//   - "chat": prior rank 15/129 (included at TopK=50)
//   - "est":  prior rank 0/61  (very common)
//
// This is the key regression for P6.4: per-word isolated scoring mis-phases
// the block grid for words after the first. The whole-line scorer fixes this
// by rendering the joined hypothesis and pixelating it in one shot.
//
// offsetX=0: several non-zero offsets collapse inter-word gaps in
// LinearBlockAverage at block=8, giving wrong word counts. The phase fix
// lives in the whole-line scoring step, not in the offset value.
func TestDecodeLineWhole_French(t *testing.T) {
	if testing.Short() {
		t.Skip("50^3 combinations; skipping in short mode")
	}
	const (
		phrase  = "le chat est"
		offsetX = 0
	)
	d := wholeLineDecoder(t, lang.French)
	r := wholeLineRenderer(t)
	band := syntheticLineBand(t, r, phrase, testBlock, testFontSize, offsetX)

	candidates := d.DecodeLineWhole(band)
	if len(candidates) == 0 {
		t.Fatal("DecodeLineWhole returned no candidates")
	}
	top := candidates[0]
	t.Logf("top-1: %q dist=%.6f prior=%.4f cost=%.6f", top.Text, top.Dist, top.Prior, top.Cost)
	if top.Text != phrase {
		n := min(5, len(candidates))
		for i, c := range candidates[:n] {
			t.Logf("  [%d] %q dist=%.6f cost=%.6f", i, c.Text, c.Dist, c.Cost)
		}
		t.Errorf("top-1 = %q, want %q", top.Text, phrase)
	}
}

// TestDecodeLineWhole_English verifies that DecodeLineWhole recovers an English
// three-word phrase as the top-1 candidate from a whole-line pixelated band.
//
// Phrase: "the cat is".
//   - "the": prior rank 0/87
//   - "cat": prior rank 41/87 — requires TopK≥42 to appear in the pool
//   - "is":  prior rank 7/26
func TestDecodeLineWhole_English(t *testing.T) {
	if testing.Short() {
		t.Skip("50^3 combinations; skipping in short mode")
	}
	const (
		phrase  = "the cat is"
		offsetX = 0
	)
	d := wholeLineDecoder(t, lang.English)
	r := wholeLineRenderer(t)
	band := syntheticLineBand(t, r, phrase, testBlock, testFontSize, offsetX)

	candidates := d.DecodeLineWhole(band)
	if len(candidates) == 0 {
		t.Fatal("DecodeLineWhole returned no candidates")
	}
	top := candidates[0]
	t.Logf("top-1: %q dist=%.6f prior=%.4f cost=%.6f", top.Text, top.Dist, top.Prior, top.Cost)
	if top.Text != phrase {
		n := min(5, len(candidates))
		for i, c := range candidates[:n] {
			t.Logf("  [%d] %q dist=%.6f cost=%.6f", i, c.Text, c.Dist, c.Cost)
		}
		t.Errorf("top-1 = %q, want %q", top.Text, phrase)
	}
}

// TestDecodeLineWhole_NearMissGuard asserts that the correct phrase scores
// strictly lower Dist than a one-word substituted variant rendered the same way.
//
// "the car is" substitutes "car" for "cat". Both are 3-letter words in the
// English dictionary; the forward-model SSIM must prefer the correct phrase.
func TestDecodeLineWhole_NearMissGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("50^3 combinations; skipping in short mode")
	}
	const (
		phrase   = "the cat is"
		nearMiss = "the car is" // "car" substituted for "cat"
		offsetX  = 0
	)
	d := wholeLineDecoder(t, lang.English)
	r := wholeLineRenderer(t)
	band := syntheticLineBand(t, r, phrase, testBlock, testFontSize, offsetX)
	candidates := d.DecodeLineWhole(band)

	distCorrect, distNearMiss := -1.0, -1.0
	for _, c := range candidates {
		switch c.Text {
		case phrase:
			distCorrect = c.Dist
		case nearMiss:
			distNearMiss = c.Dist
		}
	}
	if distCorrect < 0 {
		t.Fatalf("correct phrase %q not found in candidates", phrase)
	}
	if distNearMiss < 0 {
		t.Logf("near-miss %q not in candidates (pruned) — guard trivially satisfied", nearMiss)
		return
	}
	t.Logf("correct dist=%.6f  near-miss dist=%.6f", distCorrect, distNearMiss)
	if distCorrect >= distNearMiss {
		t.Errorf("near-miss guard failed: correct dist %.6f >= near-miss dist %.6f",
			distCorrect, distNearMiss)
	}
}

// TestRecover_FontSweep verifies that Recover with BundledRenderers("sans")
// recovers the correct text when the font is chosen by the sweep rather than
// pre-specified.
//
// The band is rendered with the default Liberation Sans renderer (same as the
// other wholeline tests). Recover sweeps all bundled sans-style fonts and must
// select the correct text; the winning font will be Liberation Sans (dist≈0),
// while Carlito (metrically close but not identical) will score slightly higher.
//
// Speed: TopK=10 gives ≤26 words per pool position (3 positions × 2 fonts =
// ~26^3 × 2 ≈ 35 K renders). This keeps the test well under 30 s and avoids
// the 10-minute limit imposed by mise run lint. The phrase uses only top-10
// within-tier words so TopK=10 covers them all.
func TestRecover_FontSweep(t *testing.T) {
	const (
		// "the" rank=0, "hat" rank=3, "for" rank=6 — all within per-tier top-10
		// so TopK=10 is sufficient. "the hat for" segments into 3 words at
		// block=8 / fontSize=32 (verified: gaps at cols 48→56 and 96→104).
		phrase  = "the hat for"
		offsetX = 0
		topK    = 10 // 10 per tier → ~30 merged → 30^3 × 2 fonts ≈ 54 K renders
	)

	r := wholeLineRenderer(t)
	band := syntheticLineBand(t, r, phrase, testBlock, testFontSize, offsetX)

	opts := blinddecode.Options{
		Pixelator: pixelate.NewLinearBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(lang.English),
		Prior:     lang.PriorFor(lang.English),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      0.005,
		TopK:      topK,
		BeamWidth: 8,
		OffsetX:   offsetX,
	}

	renderers, err := blinddecode.BundledRenderers("sans")
	if err != nil {
		t.Fatalf("BundledRenderers: %v", err)
	}

	result := blinddecode.Recover(band, opts, renderers)
	t.Logf("Recover: text=%q font=%q dist=%.6f", result.Text, result.Font, result.Dist)

	if result.Text != phrase {
		t.Errorf("Recover text = %q, want %q", result.Text, phrase)
	}
	if result.Dist >= 0.01 {
		t.Errorf("Recover dist = %.6f, want < 0.01", result.Dist)
	}
}

// sinkLineCandidates prevents dead-code elimination of benchmark results.
var sinkLineCandidates []blinddecode.LineCandidate

// BenchmarkDecodeLineWhole measures per-line decode throughput.
// Decoder setup and band construction run outside b.Loop() so only the
// scoring inner loop is timed. TopK=0 (default 30, adaptive cap active)
// reflects the production default; the test-specific TopK=50 is intentionally
// not used here so the benchmark measures the real default code path.
func BenchmarkDecodeLineWhole(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("NewXImage: %v", err)
	}
	const phrase = "the cat is"
	opts := blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewLinearBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(lang.English),
		Prior:     lang.PriorFor(lang.English),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      0.005,
		TopK:      0, // default pool size + adaptive cap
		BeamWidth: 8,
	}
	d := blinddecode.New(opts)

	img, sx, _ := r.Render(phrase, unpixel.Style{FontSize: testFontSize})
	bb := inkBoundsT(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	lineBand := pixelate.NewLinearBlockAverage(testBlock).Pixelate(ink, 0, 0)

	b.ReportAllocs()
	for b.Loop() {
		sinkLineCandidates = d.DecodeLineWhole(lineBand)
	}
}
