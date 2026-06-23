// Package blinddecode_test — TDD tests for the advance-width pre-filter (roadmap #2).
//
// The width filter prunes candidates whose predicted rendered width cannot
// match the band width within ±tolerance BEFORE the expensive image scoring
// step. This shrinks each word pool and reduces Cartesian-product combinations.
//
// Tests follow the project convention: got before want.
package blinddecode_test

import (
	"image"
	"slices"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/blinddecode"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// widthDecoder builds a Decoder with the width filter enabled (default).
func widthDecoder(t *testing.T, l lang.Language) *blinddecode.Decoder {
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
		TopK:      0, // budget-adaptive
		BeamWidth: 8,
		// DisableWidthFilter defaults to false → filter enabled
	})
}

// widthDecoderDisabled builds a Decoder with the width filter disabled,
// producing byte-identical output to the pre-filter behaviour.
func widthDecoderDisabled(t *testing.T, l lang.Language) *blinddecode.Decoder {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	return blinddecode.New(blinddecode.Options{
		Renderer:           r,
		Pixelator:          pixelate.NewLinearBlockAverage(testBlock),
		Metric:             metric.NewSSIM(0),
		Dict:               lang.DictionaryFor(l),
		Prior:              lang.PriorFor(l),
		Block:              testBlock,
		FontSize:           testFontSize,
		Alpha:              1.0,
		Beta:               0.005,
		TopK:               0,
		BeamWidth:          8,
		DisableWidthFilter: true,
	})
}

// TestWidthFilter_TrueWordSurvives asserts that the width filter keeps the
// true word in the pool for representative English and French words.
//
// Words are chosen so they are within the top-300 prior rank in their rune-length
// tier (verified empirically), ensuring they appear in the unfiltered pool:
//   - "the"      EN 3-rune rank 0,   renderedWidth=45, blockBand=48
//   - "cat"      EN 3-rune rank 139, renderedWidth=43, blockBand=48
//   - "chat"     FR 4-rune rank 51,  renderedWidth=61, blockBand=64
//   - "le"       FR 2-rune rank 0,   renderedWidth=25, blockBand=24
//   - "histoire" FR 8-rune rank 102, renderedWidth=104, blockBand=104
//
// The filter must never drop any of these (tolerance = one block = 8 px; all
// renderedWidth values are within 8 px of their respective band widths).
func TestWidthFilter_TrueWordSurvives(t *testing.T) {
	t.Parallel()

	cases := []struct {
		l    lang.Language
		word string
		k    int // pool cap; must be > prior rank of word in its rune-length tier
	}{
		{lang.English, "the", 10},
		{lang.English, "cat", 300},
		{lang.French, "chat", 100},
		{lang.French, "le", 10},
		{lang.French, "histoire", 200},
	}

	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			t.Parallel()
			r, err := render.NewXImage()
			if err != nil {
				t.Fatalf("NewXImage: %v", err)
			}
			band := syntheticBand(t, r, tc.word)
			bandW := band.Bounds().Dx()

			d := widthDecoder(t, tc.l)
			pool := d.WordPool(bandW, tc.k)

			got := slices.Contains(pool, tc.word)
			want := true
			if got != want {
				// Also report the unfiltered pool for diagnosis.
				dOff := widthDecoderDisabled(t, tc.l)
				poolOff := dOff.WordPool(bandW, tc.k)
				t.Logf("unfiltered pool size=%d, contains=%v", len(poolOff), slices.Contains(poolOff, tc.word))
				t.Logf("filtered   pool size=%d", len(pool))
				t.Errorf("WordPool(bandW=%d, k=%d): word %q in filtered pool = %v, want %v",
					bandW, tc.k, tc.word, got, want)
			}
		})
	}
}

// TestWidthFilter_PoolShrinkage asserts that the filtered pool is meaningfully
// smaller than the unfiltered pool for a representative band.
//
// "and" (EN 3-rune rank 1, renderedWidth=54) mixed with many other 3-rune words
// of various widths provides the richest width spread. At k=300 the tier has
// many words well outside the ±8 px window of the band, so at least 10 % are
// pruned.
func TestWidthFilter_PoolShrinkage(t *testing.T) {
	if testing.Short() {
		t.Skip("requires large pool for ratio measurement; skipping in -short")
	}

	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}

	// Use "the" band (bandW=48): the 3-rune tier contains words from "ill" (very
	// narrow) to "www" (very wide), so many fall outside ±8 px of 48.
	band := syntheticBand(t, r, "the")
	bandW := band.Bounds().Dx()

	const bigK = 300

	dOn := widthDecoder(t, lang.English)
	dOff := widthDecoderDisabled(t, lang.English)

	filteredPool := dOn.WordPool(bandW, bigK)
	unfilteredPool := dOff.WordPool(bandW, bigK)

	got := len(filteredPool)
	want := len(unfilteredPool)
	t.Logf("pool: filtered=%d unfiltered=%d ratio=%.2f",
		got, want, float64(got)/float64(want))

	if want == 0 {
		t.Fatal("unfiltered pool is empty — test setup error")
	}
	if got >= want {
		t.Errorf("filtered pool size %d >= unfiltered %d: filter prunes nothing", got, want)
	}
	// At least 10 % of candidates must be pruned.
	const maxRatio = 0.90
	if ratio := float64(got) / float64(want); ratio > maxRatio {
		t.Errorf("shrinkage ratio %.2f > %.2f: filter prunes too few candidates", ratio, maxRatio)
	}
}

// TestWidthFilter_DisabledIsIdentical proves that two Decoders both with
// DisableWidthFilter=true produce byte-identical pools (determinism guard).
func TestWidthFilter_DisabledIsIdentical(t *testing.T) {
	t.Parallel()

	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	band := syntheticBand(t, r, "the")
	bandW := band.Bounds().Dx()

	const k = 50

	d1 := widthDecoderDisabled(t, lang.English)
	d2 := widthDecoderDisabled(t, lang.English)

	pool1 := d1.WordPool(bandW, k)
	pool2 := d2.WordPool(bandW, k)

	if got, want := len(pool1), len(pool2); got != want {
		t.Fatalf("pool sizes differ: got %d, want %d", got, want)
	}
	for i := range pool1 {
		if got, want := pool1[i], pool2[i]; got != want {
			t.Errorf("pool[%d]: got %q, want %q", i, got, want)
		}
	}
}

// TestWidthFilter_EndToEnd_English verifies that blind recovery of "the cat"
// still succeeds with the width filter enabled (end-to-end non-regression).
func TestWidthFilter_EndToEnd_English(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}

	const (
		phrase  = "the cat"
		offsetX = 0
	)
	d := widthDecoder(t, lang.English)
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	band := syntheticLineBand(t, r, phrase, testBlock, testFontSize, offsetX)

	candidates := d.DecodeLineWhole(band)
	if len(candidates) == 0 {
		t.Fatal("DecodeLineWhole returned no candidates")
	}
	got := candidates[0].Text
	want := phrase
	t.Logf("top-1: %q dist=%.6f", got, candidates[0].Dist)
	if got != want {
		n := min(5, len(candidates))
		for i, c := range candidates[:n] {
			t.Logf("  [%d] %q dist=%.6f", i, c.Text, c.Dist)
		}
		t.Errorf("top-1: got %q, want %q", got, want)
	}
}

// TestWidthFilter_EndToEnd_French verifies that blind recovery of "le chat"
// still succeeds with the width filter enabled (end-to-end non-regression).
func TestWidthFilter_EndToEnd_French(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}

	const (
		phrase  = "le chat"
		offsetX = 0
	)
	d := widthDecoder(t, lang.French)
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	band := syntheticLineBand(t, r, phrase, testBlock, testFontSize, offsetX)

	candidates := d.DecodeLineWhole(band)
	if len(candidates) == 0 {
		t.Fatal("DecodeLineWhole returned no candidates")
	}
	got := candidates[0].Text
	want := phrase
	t.Logf("top-1: %q dist=%.6f", got, candidates[0].Dist)
	if got != want {
		n := min(5, len(candidates))
		for i, c := range candidates[:n] {
			t.Logf("  [%d] %q dist=%.6f", i, c.Text, c.Dist)
		}
		t.Errorf("top-1: got %q, want %q", got, want)
	}
}

// BenchmarkWordPool_WidthFilter measures WordPool throughput with the width
// filter enabled versus disabled. The pool-size reduction directly translates
// to fewer image scorings in DecodeLineWhole.
//
// Compare with -count=10 via benchstat:
//
//	mise run bench:baseline && <change> && mise run bench:compare
func BenchmarkWordPool_WidthFilter(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("NewXImage: %v", err)
	}
	img, sx, _ := r.Render("the", unpixel.Style{FontSize: testFontSize})
	bb := inkBoundsT(img, sx)
	bandW := bb.Dx()

	const k = 235 // k for a 2-word line (effectivePoolK(2,0) ≈ 235)

	for _, tc := range []struct {
		name     string
		disabled bool
	}{
		{"filter=on", false},
		{"filter=off", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			opts := blinddecode.Options{
				Renderer:           r,
				Pixelator:          pixelate.NewLinearBlockAverage(testBlock),
				Metric:             metric.NewSSIM(0),
				Dict:               lang.DictionaryFor(lang.English),
				Prior:              lang.PriorFor(lang.English),
				Block:              testBlock,
				FontSize:           testFontSize,
				Alpha:              1.0,
				Beta:               0.005,
				TopK:               0,
				BeamWidth:          8,
				DisableWidthFilter: tc.disabled,
			}
			d := blinddecode.New(opts)
			var sink []string
			b.ReportAllocs()
			for b.Loop() {
				sink = d.WordPool(bandW, k)
			}
			_ = sink
		})
	}
}

// BenchmarkDecodeLineWhole_WidthFilter measures DecodeLineWhole throughput with
// and without the width pre-filter on the "the cat" 2-word phrase (k≈235).
// The filter reduces the Cartesian-product size and should yield a measurable
// ns/op improvement on subsequent b.Loop iterations (first call warms widthCache).
func BenchmarkDecodeLineWhole_WidthFilter(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("NewXImage: %v", err)
	}

	img, sx, _ := r.Render("the cat", unpixel.Style{FontSize: testFontSize})
	bb := inkBoundsT(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	band := pixelate.NewLinearBlockAverage(testBlock).Pixelate(ink, 0, 0)

	for _, tc := range []struct {
		name     string
		disabled bool
	}{
		{"filter=on", false},
		{"filter=off", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			opts := blinddecode.Options{
				Renderer:           r,
				Pixelator:          pixelate.NewLinearBlockAverage(testBlock),
				Metric:             metric.NewSSIM(0),
				Dict:               lang.DictionaryFor(lang.English),
				Prior:              lang.PriorFor(lang.English),
				Block:              testBlock,
				FontSize:           testFontSize,
				Alpha:              1.0,
				Beta:               0.005,
				TopK:               0,
				BeamWidth:          8,
				DisableWidthFilter: tc.disabled,
			}
			d := blinddecode.New(opts)
			b.ReportAllocs()
			for b.Loop() {
				sinkLineCandidates = d.DecodeLineWhole(band)
			}
		})
	}
}
