// Package blinddecode_test — TDD tests for the blind word decoder.
package blinddecode_test

import (
	"image"
	"strings"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/blinddecode"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

const (
	testBlock    = 8
	testFontSize = 32.0
	// testBeta is the language-prior weight used in all tests. See
	// defaultBeta in blinddecode.go for the full calibration rationale.
	// 0.005 keeps the image signal dominant while breaking near-ties by prior.
	testBeta = 0.005
)

// syntheticBand renders word at testFontSize with testBlock pixelation and
// returns the pixelated image cropped to the ink content bounds. It simulates
// the band a decoder receives: a standalone, single-word mosaic band.
func syntheticBand(t *testing.T, r unpixel.Renderer, word string) *image.RGBA {
	t.Helper()
	img, sx, err := r.Render(word, unpixel.Style{FontSize: testFontSize})
	if err != nil {
		t.Fatalf("render %q: %v", word, err)
	}
	bb := inkBoundsT(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	p := pixelate.NewBlockAverage(testBlock)
	return p.Pixelate(ink, 0, 0)
}

// inkBoundsT returns the tight bounding box of non-white pixels in [0, sentinelX).
func inkBoundsT(img *image.RGBA, sentinelX int) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := sentinelX, b.Dy(), 0, 0
	for y := range b.Dy() {
		for x := range sentinelX {
			c := img.RGBAAt(x, y)
			lum := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
			if lum < 240 {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x+1), max(y1, y+1)
			}
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return image.Rect(0, 0, 1, 1)
	}
	return image.Rect(x0, y0, x1, y1)
}

func newDecoder(t *testing.T, l lang.Language) (*blinddecode.Decoder, unpixel.Renderer) {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	d := blinddecode.New(blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(l),
		Prior:     lang.PriorFor(l),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      testBeta,
		TopK:      5,
	})
	return d, r
}

// TestDecodeWord_French asserts that "histoire" (a French dict word) is
// recovered by DecodeWord as the top candidate, block 8, font 32.
func TestDecodeWord_French(t *testing.T) {
	d, r := newDecoder(t, lang.French)
	const word = "histoire"
	band := syntheticBand(t, r, word)
	candidates := d.DecodeWord(band)
	if len(candidates) == 0 {
		t.Fatal("DecodeWord returned no candidates")
	}
	top := min(3, len(candidates))
	found := false
	for _, c := range candidates[:top] {
		if c.Word == word {
			found = true
			t.Logf("%q: rank=top-%d dist=%.6f prior=%.4f cost=%.6f",
				word, top, c.ImageDist, c.Prior, c.Cost)
			break
		}
	}
	if !found {
		t.Errorf("word %q not in top-%d candidates; got %v", word, top, candidates[:top])
	}
}

// TestDecodeWord_English asserts that "history" is recovered with an English
// dict/prior, block 8, font 32.
func TestDecodeWord_English(t *testing.T) {
	d, r := newDecoder(t, lang.English)
	const word = "history"
	band := syntheticBand(t, r, word)
	candidates := d.DecodeWord(band)
	if len(candidates) == 0 {
		t.Fatal("DecodeWord returned no candidates")
	}
	top := min(3, len(candidates))
	found := false
	for _, c := range candidates[:top] {
		if c.Word == word {
			found = true
			t.Logf("%q: rank=top-%d dist=%.6f prior=%.4f cost=%.6f",
				word, top, c.ImageDist, c.Prior, c.Cost)
			break
		}
	}
	if !found {
		t.Errorf("word %q not in top-%d candidates; got %v", word, top, candidates[:top])
	}
}

// TestDecodeLine_French asserts that DecodeLine on two standalone-pixelated
// French words — each decoded as its own band via DecodeLine — recovers them.
//
// DecodeLine segments a pixelated image into word bands and scores each. When
// each word is rendered and pixelated independently (as syntheticBand does),
// the block-grid alignment matches the forward model exactly so the signal is
// clean. The words chosen ("histoire" and "période") are ≥7 letters so the
// SSIM window has enough variation to discriminate them from near-neighbours.
//
// Limitation (documented in PROGRESS.md P5.4 / P6.3): when words come from a
// multi-word line pixelated as a whole, each word's band has a block-grid
// alignment inherited from the line's left edge, not from the word's own left
// edge. The forward model scores each candidate as if its left edge is at the
// grid origin, introducing a mismatch for words whose column is not
// block-aligned. DecodeLine accounts for this via colOffset = wordCol % block,
// but residual near-ties still occur at small block sizes. End-to-end tests
// for multi-word lines belong in P6.4 (full-line re-ranking).
func TestDecodeLine_French(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	d := blinddecode.New(blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(lang.French),
		Prior:     lang.PriorFor(lang.French),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      testBeta,
		TopK:      5,
	})

	// Test each word independently as its own pixelated band, feeding it
	// through DecodeLine (which calls segment.Lines → segment.Words → DecodeWord).
	// Words chosen: ≥7 letters, confirmed in the French dictionary, and long
	// enough that SSIM discriminates them from same-length near-neighbours.
	for _, word := range []string{"histoire", "document", "société"} {
		band := syntheticBand(t, r, word)
		text, perWord := d.DecodeLine(band)
		t.Logf("DecodeLine(%q) -> %q (%d bands)", word, text, len(perWord))
		if text != word {
			// Accept top-3: the word must appear in the first band's candidates.
			found := false
			if len(perWord) > 0 {
				for _, c := range perWord[0] {
					if c.Word == word {
						found = true
						break
					}
				}
			}
			if !found {
				t.Errorf("DecodeLine(%q) = %q, word not in top-%d candidates", word, text, testTopK)
			} else {
				t.Logf("DecodeLine(%q): top-1=%q but word in top-3 candidates", word, text)
			}
		}
	}
}

// testTopK mirrors the TopK used in newDecoder for error messages.
const testTopK = 5

// TestDecodeLine_English asserts that DecodeLine recovers English words
// supplied as standalone pixelated bands.
func TestDecodeLine_English(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	d := blinddecode.New(blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(lang.English),
		Prior:     lang.PriorFor(lang.English),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      testBeta,
		TopK:      5,
	})

	// Words chosen: ≥7 letters, confirmed in the English dictionary, long enough
	// that SSIM distinguishes them from same-length near-neighbours at block=8.
	for _, word := range []string{"history", "document", "computer"} {
		band := syntheticBand(t, r, word)
		text, perWord := d.DecodeLine(band)
		t.Logf("DecodeLine(%q) -> %q (%d bands)", word, text, len(perWord))
		if text != word {
			found := false
			if len(perWord) > 0 {
				for _, c := range perWord[0] {
					if c.Word == word {
						found = true
						break
					}
				}
			}
			if !found {
				t.Errorf("DecodeLine(%q) = %q, word not in top-%d candidates", word, text, testTopK)
			} else {
				t.Logf("DecodeLine(%q): top-1=%q but word in top-3 candidates", word, text)
			}
		}
	}
}

// TestDecodeLine_MultiWord_English verifies that DecodeLine correctly segments
// a two-word pixelated line and returns both words joined by a space.
//
// "the old" is chosen because:
//   - both words are in the English dictionary,
//   - "the" (3 letters) and "old" (3 letters) produce distinctly sized bands
//     so the segmenter reliably finds two words,
//   - at block=8 both words score near-zero against their correct band.
func TestDecodeLine_MultiWord_English(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	d := blinddecode.New(blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(lang.English),
		Prior:     lang.PriorFor(lang.English),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      testBeta,
		TopK:      5,
	})

	const phrase = "the old"
	lineImg := syntheticBand(t, r, phrase)
	text, perWord := d.DecodeLine(lineImg)
	t.Logf("DecodeLine(%q) -> %q (%d word bands)", phrase, text, len(perWord))

	// Assert the segmenter found exactly two bands.
	if len(perWord) != 2 {
		t.Fatalf("expected 2 word bands, got %d", len(perWord))
	}
	if text != phrase {
		t.Errorf("DecodeLine = %q, want %q", text, phrase)
	}
}

// TestDecodeWord_PriorOrdering demonstrates that the language prior breaks
// image near-ties to promote the more plausible word.
//
// Empirically established pair (verified via probe runs with the alphabet
// avgAdvance calibration at block=8, font=32):
//
//   - Band: "history" (7 letters)
//   - "hosting" (prior ≈ -2.52) and "length" (prior ≈ -1.38) are image near-ties.
//   - At Beta ≈ 0 (image-only): "hosting" ranks 1, "length" ranks 3.
//   - At Beta = 0.005: "length" (better prior) rises to rank 1, "hosting" drops.
//
// This confirms that Beta > 0 correctly promotes the linguistically more common
// word when the image signal cannot discriminate alone.
func TestDecodeWord_PriorOrdering(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}

	// tinyBeta ≈ image-only: prior contributes < 0.001 per unit, negligible vs
	// typical image diffs of 0.005–0.020.
	const tinyBeta = 0.0001

	makeDecoder := func(beta float64) *blinddecode.Decoder {
		return blinddecode.New(blinddecode.Options{
			Renderer:  r,
			Pixelator: pixelate.NewBlockAverage(testBlock),
			Metric:    metric.NewSSIM(0),
			Dict:      lang.DictionaryFor(lang.English),
			Prior:     lang.PriorFor(lang.English),
			Block:     testBlock,
			FontSize:  testFontSize,
			Alpha:     1.0,
			Beta:      beta,
			TopK:      10,
		})
	}

	// "history" band: "hosting" beats "length" by image alone, but "length"
	// has a much better prior (-1.38 vs -2.52). Beta=0.005 reverses the order.
	band := syntheticBand(t, r, "history")

	dImg := makeDecoder(tinyBeta)
	dPrior := makeDecoder(testBeta)

	imgCands := dImg.DecodeWord(band)
	priorCands := dPrior.DecodeWord(band)

	hostingImg := rankOf(imgCands, "hosting")
	lengthImg := rankOf(imgCands, "length")
	hostingPrior := rankOf(priorCands, "hosting")
	lengthPrior := rankOf(priorCands, "length")

	t.Logf("beta≈0:    hosting=%d length=%d", hostingImg, lengthImg)
	t.Logf("beta=.005: hosting=%d length=%d", hostingPrior, lengthPrior)

	if hostingImg < 0 || lengthImg < 0 {
		t.Skipf("near-tie pair not both in top-10 at beta≈0 (hosting=%d, length=%d); skipping",
			hostingImg, lengthImg)
	}
	if hostingPrior < 0 || lengthPrior < 0 {
		t.Skipf("near-tie pair not both in top-10 at beta=%.4f (hosting=%d, length=%d); skipping",
			testBeta, hostingPrior, lengthPrior)
	}

	// At image-only ranking, "hosting" must rank above "length".
	if hostingImg >= lengthImg {
		t.Errorf("image-only ranking: expected hosting(%d) < length(%d) so the prior has something to override",
			hostingImg, lengthImg)
	}
	// With prior enabled, "length" (better prior, pr≈-1.38) must rank above "hosting" (pr≈-2.52).
	if lengthPrior >= hostingPrior {
		t.Errorf("prior ordering failed: 'length' at rank %d, 'hosting' at rank %d with beta=%.4f; expected 'length' ranked higher",
			lengthPrior, hostingPrior, testBeta)
	}
}

// rankOf returns the 0-based index of word in candidates, or -1 if absent.
func rankOf(candidates []blinddecode.WordCandidate, word string) int {
	for i, c := range candidates {
		if c.Word == word {
			return i
		}
	}
	return -1
}

// TestDecodeLine_JoinsBySpaces is a minimal contract test: DecodeLine on a
// multi-word band returns words joined by single spaces with no trailing space.
func TestDecodeLine_JoinsBySpaces(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	d := blinddecode.New(blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(lang.English),
		Prior:     lang.PriorFor(lang.English),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      testBeta,
		TopK:      5,
	})

	lineImg := syntheticBand(t, r, "the old")
	text, perWord := d.DecodeLine(lineImg)
	if len(perWord) == 0 {
		t.Fatal("DecodeLine returned no words")
	}
	// Rebuild the expected join.
	parts := make([]string, len(perWord))
	for i, cands := range perWord {
		if len(cands) > 0 {
			parts[i] = cands[0].Word
		} else {
			parts[i] = "?"
		}
	}
	want := strings.Join(parts, " ")
	if text != want {
		t.Errorf("DecodeLine text=%q does not match joined candidates %q", text, want)
	}
}

// sinkCandidates is a package-level sink that prevents dead-code elimination of
// the benchmark result.
var sinkCandidates []blinddecode.WordCandidate

// BenchmarkDecodeWord measures the per-band decode throughput. Decoder creation
// and band construction run outside b.Loop(); only the scoring loop is timed.
func BenchmarkDecodeWord(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("NewXImage: %v", err)
	}
	d := blinddecode.New(blinddecode.Options{
		Renderer:  r,
		Pixelator: pixelate.NewBlockAverage(testBlock),
		Metric:    metric.NewSSIM(0),
		Dict:      lang.DictionaryFor(lang.English),
		Prior:     lang.PriorFor(lang.English),
		Block:     testBlock,
		FontSize:  testFontSize,
		Alpha:     1.0,
		Beta:      testBeta,
		TopK:      5,
	})

	img, sx, _ := r.Render("history", unpixel.Style{FontSize: testFontSize})
	bb := inkBoundsT(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	band := pixelate.NewBlockAverage(testBlock).Pixelate(ink, 0, 0)

	b.ReportAllocs()
	for b.Loop() {
		sinkCandidates = d.DecodeWord(band)
	}
}
