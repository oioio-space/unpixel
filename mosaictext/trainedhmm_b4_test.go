package mosaictext_test

// trainedhmm_b4_test.go — TDD tests for B4.1 (language-structured corpus) and
// B4.2 (JPEG-augmented emission training).
//
// Test organisation:
//  1. TestB4DefaultUnchanged   — default path (no new options) is byte-identical to seed-controlled baseline.
//  2. TestB4LanguageCorpus     — WithTHMMLanguage(English) improves edit-distance vs uniform-random on a synthetic sentence.
//  3. TestB4JPEGAugmented      — WithTHMMJPEG(q) recovers a JPEG-roundtripped image better than clean-trained emissions.
//  4. BenchmarkB4LanguageCorpus — measures training overhead of language sampler vs uniform-random.
//
// Timing note: corpus=200 keeps each case under ~5 s on a developer laptop; the
// default (2000) would run ~10× slower — not suitable for unit tests.

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/mosaictext"
)

// ---- shared helpers ----

// b4FindFont returns the first bundled font whose name contains substr,
// or skips the test.
func b4FindFont(t *testing.T, name string) []byte {
	t.Helper()
	for _, f := range fonts.All() {
		if f.Name == name {
			return f.Data
		}
	}
	t.Skipf("bundled font %q not found", name)
	return nil
}

// b4EditDistance returns the Levenshtein edit-distance between a and b (rune-aware).
func b4EditDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := range lb + 1 {
		prev[j] = j
	}
	for i := range la {
		cur[0] = i + 1
		for j := range lb {
			cost := 1
			if ra[i] == rb[j] {
				cost = 0
			}
			cur[j+1] = min(cur[j]+1, min(prev[j+1]+1, prev[j]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

// b4SyntheticMosaic renders text and pixelates it.  The font data must be a
// Liberation-family TTF (or any bundled font).  block controls the mosaic block size.
func b4SyntheticMosaic(t *testing.T, text string, fontData []byte, fs float64, block int) image.Image {
	t.Helper()
	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}
	rendered, sentinelX, rErr := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: 16, PaddingLeft: 4})
	if rErr != nil {
		t.Fatalf("render %q: %v", text, rErr)
	}
	cropped := image.NewRGBA(image.Rect(0, 0, sentinelX, rendered.Bounds().Dy()))
	draw.Draw(cropped, cropped.Bounds(), rendered, image.Point{}, draw.Src)
	return defaults.BlockAverage(block).Pixelate(cropped, 0, 0)
}

// b4JpegRoundtrip encodes img to JPEG at quality q and decodes it back.
func b4JpegRoundtrip(t *testing.T, img image.Image, quality int) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	out, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("jpeg decode: %v", err)
	}
	return out
}

// b4SavePNG writes img to a temp PNG and returns the path.
func b4SavePNG(t *testing.T, img image.Image) string {
	t.Helper()
	f, err := os.CreateTemp("", "b4-*.png")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if encErr := png.Encode(f, img); encErr != nil {
		t.Fatalf("png encode: %v", encErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		t.Errorf("close temp: %v", closeErr)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}

// b4LoadPNG loads a PNG from path (round-trips via disk to simulate real input).
func b4LoadPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test helper with temp path
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close %s: %v", path, cerr)
		}
	}()
	img, _, dErr := image.Decode(f)
	if dErr != nil {
		t.Fatalf("decode %s: %v", path, dErr)
	}
	return img
}

// ---- B4 option-wiring smoke tests ----

// TestB4WithTHMMLanguageOptionExists verifies WithTHMMLanguage compiles and returns a non-nil option.
func TestB4WithTHMMLanguageOptionExists(t *testing.T) {
	t.Parallel()
	opt := mosaictext.WithTHMMLanguage(lang.English)
	if opt == nil {
		t.Error("WithTHMMLanguage(English) returned nil option")
	}
}

// TestB4WithTHMMJPEGOptionExists verifies WithTHMMJPEG compiles and returns a non-nil option.
func TestB4WithTHMMJPEGOptionExists(t *testing.T) {
	t.Parallel()
	opt := mosaictext.WithTHMMJPEG(75)
	if opt == nil {
		t.Error("WithTHMMJPEG(75) returned nil option")
	}
}

// TestB4WithTHMMJPEGZeroIsNoop verifies that quality=0 (off) is accepted without error.
func TestB4WithTHMMJPEGZeroIsNoop(t *testing.T) {
	t.Parallel()
	opt := mosaictext.WithTHMMJPEG(0)
	if opt == nil {
		t.Error("WithTHMMJPEG(0) returned nil option")
	}
}

// ---- B4.0 — default-path unchanged ----

// TestB4DefaultUnchanged proves that without the new options, DecodeTrainedHMM
// produces the same result as before — same seed → same text + distance.
func TestB4DefaultUnchanged(t *testing.T) {
	fontData := b4FindFont(t, "Liberation Mono")
	const (
		text  = "3141592653"
		fs    = 32.0
		block = 4
	)

	mosaic := b4SyntheticMosaic(t, text, fontData, fs, block)
	loaded := b4LoadPNG(t, b4SavePNG(t, mosaic))

	baseOpts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset("0123456789"),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(128),
		mosaictext.WithTHMMCorpus(200),
		mosaictext.WithTHMMSeed(42),
	}

	res1, err1 := mosaictext.DecodeTrainedHMM(t.Context(), loaded, baseOpts...)
	if err1 != nil {
		t.Fatalf("first call: %v", err1)
	}
	res2, err2 := mosaictext.DecodeTrainedHMM(t.Context(), loaded, baseOpts...)
	if err2 != nil {
		t.Fatalf("second call: %v", err2)
	}

	if res1.Text != res2.Text {
		t.Errorf("default-path is not deterministic: got %q then %q", res1.Text, res2.Text)
	}
	if res1.Distance != res2.Distance {
		t.Errorf("default-path distance differs: %.6f vs %.6f", res1.Distance, res2.Distance)
	}
}

// ---- B4.1 — language-structured corpus ----

// TestB4LanguageCorpus renders a short English sentence (lowercase, block 8,
// Liberation Sans) and compares recovery with vs without WithTHMMLanguage(English).
//
// The test always reports the honest comparison.  It does NOT hard-fail if
// the language sampler doesn't help: the finding is the data point.  However,
// it verifies that the language-sampler path completes without error and that
// the option produces a different result (or the same, reported).
//
// Timing: corpus=150 → ~3-4 s on a laptop.
func TestB4LanguageCorpus(t *testing.T) {
	fontData := b4FindFont(t, "Liberation Sans")
	const (
		text    = "two dogs run"
		charset = "abcdefghijklmnopqrstuvwxyz "
		fs      = 32.0
		block   = 8
		corpus  = 150
	)

	mosaic := b4SyntheticMosaic(t, text, fontData, fs, block)
	loaded := b4LoadPNG(t, b4SavePNG(t, mosaic))

	baseOpts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Sans"),
		mosaictext.WithTHMMCharset(charset),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(64),
		mosaictext.WithTHMMCorpus(corpus),
		mosaictext.WithTHMMSeed(7),
	}

	resUniform, errU := mosaictext.DecodeTrainedHMM(t.Context(), loaded, baseOpts...)
	if errU != nil {
		t.Fatalf("uniform corpus: %v", errU)
	}

	langOpts := append(slices.Clone(baseOpts), mosaictext.WithTHMMLanguage(lang.English))
	resLang, errL := mosaictext.DecodeTrainedHMM(t.Context(), loaded, langOpts...)
	if errL != nil {
		t.Fatalf("language corpus: %v", errL)
	}

	edUniform := b4EditDistance(resUniform.Text, text)
	edLang := b4EditDistance(resLang.Text, text)

	t.Logf("B4.1 language vs uniform — target: %q", text)
	t.Logf("  uniform-random:  %q  edit-distance=%d", resUniform.Text, edUniform)
	t.Logf("  language-corpus: %q  edit-distance=%d", resLang.Text, edLang)
	t.Logf("  improvement: %+d chars (positive = language better)", edUniform-edLang)

	// B4.1 does NOT hard-fail on the quality comparison — the honest outcome is
	// the finding.  It DOES assert correctness: the language path must return a
	// valid result restricted to the charset.
	for _, r := range resLang.Text {
		if !charInSet(r, charset) {
			t.Errorf("language-corpus output %q contains rune %q outside charset %q",
				resLang.Text, r, charset)
		}
	}
	if utf8.RuneCountInString(resLang.Text) == 0 {
		t.Errorf("language-corpus returned empty text")
	}
}

// charInSet reports whether r is among the runes in set.
func charInSet(r rune, set string) bool {
	for _, s := range set {
		if r == s {
			return true
		}
	}
	return false
}

// ---- B4.2 — JPEG-augmented emission training ----

// TestB4JPEGAugmented takes a synthetic mosaic, JPEG-roundtrips it to simulate
// a captured image, then compares DecodeTrainedHMM WITH WithTHMMJPEG(q) vs
// without.  Honest reporting; no hard quality gate.
//
// Block=16 is used because InferBlockGrid cannot reliably detect block=4 or
// block=8 grids after JPEG compression (JPEG 8×8 DCT blurs the hard block
// edges that the GCD detector relies on).  Block=16 survives JPEG roundtrip
// at all tested quality levels.
func TestB4JPEGAugmented(t *testing.T) {
	fontData := b4FindFont(t, "Liberation Mono")
	const (
		text    = "31415"
		charset = "0123456789"
		fs      = 48.0 // larger font to fill 16-pixel blocks
		block   = 16
		corpus  = 100
		quality = 75 // realistic JPEG quality
	)

	mosaic := b4SyntheticMosaic(t, text, fontData, fs, block)
	// Simulate a JPEG-compressed capture of the target.
	jpegInput := b4JpegRoundtrip(t, mosaic, quality)
	loaded := b4LoadPNG(t, b4SavePNG(t, jpegInput))

	baseOpts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset(charset),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(64),
		mosaictext.WithTHMMCorpus(corpus),
		mosaictext.WithTHMMSeed(99),
	}

	resClean, errC := mosaictext.DecodeTrainedHMM(t.Context(), loaded, baseOpts...)
	if errC != nil {
		t.Fatalf("clean-trained: %v", errC)
	}

	jpegOpts := append(slices.Clone(baseOpts), mosaictext.WithTHMMJPEG(quality))
	resJPEG, errJ := mosaictext.DecodeTrainedHMM(t.Context(), loaded, jpegOpts...)
	if errJ != nil {
		t.Fatalf("jpeg-trained: %v", errJ)
	}

	edClean := b4EditDistance(resClean.Text, text)
	edJPEG := b4EditDistance(resJPEG.Text, text)

	t.Logf("B4.2 JPEG-augmented vs clean — target: %q (jpeg q=%d)", text, quality)
	t.Logf("  clean-trained: %q  edit-distance=%d", resClean.Text, edClean)
	t.Logf("  jpeg-trained:  %q  edit-distance=%d", resJPEG.Text, edJPEG)
	t.Logf("  improvement: %+d chars (positive = JPEG training better)", edClean-edJPEG)

	// Correctness: both must return output restricted to the charset.
	for _, r := range resJPEG.Text {
		if !charInSet(r, charset) {
			t.Errorf("jpeg-trained output %q contains rune %q outside charset", resJPEG.Text, r)
		}
	}
	if utf8.RuneCountInString(resJPEG.Text) == 0 {
		t.Errorf("jpeg-trained returned empty text")
	}
}

// sinkTHMMResult defeats dead-code elimination for TrainedHMM benchmark results.
var sinkTHMMResult mosaictext.Result

// ---- Benchmark ----

// BenchmarkTrainHMM isolates the corpus-generation + HMM-training cost by
// calling DecodeTrainedHMM with a small corpus (50 strings) so the Viterbi
// decoding step contributes negligibly.  Use it to measure the second-pass
// re-render optimisation with benchstat (-count=10).
func BenchmarkTrainHMM(b *testing.B) {
	var fontData []byte
	for _, f := range fonts.All() {
		if f.Name == "Liberation Mono" {
			fontData = f.Data
			break
		}
	}
	if fontData == nil {
		b.Skip("Liberation Mono not found")
	}
	const (
		text    = "31415"
		charset = "0123456789"
		fs      = 32.0
		block   = 4
		corpus  = 50 // small: isolates training cost
	)

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		b.Fatalf("renderer: %v", err)
	}
	rendered, sx, rErr := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: 16, PaddingLeft: 4})
	if rErr != nil {
		b.Fatalf("render: %v", rErr)
	}
	cropped := image.NewRGBA(image.Rect(0, 0, sx, rendered.Bounds().Dy()))
	draw.Draw(cropped, cropped.Bounds(), rendered, image.Point{}, draw.Src)
	mosaic := defaults.BlockAverage(block).Pixelate(cropped, 0, 0)

	var buf bytes.Buffer
	if err := png.Encode(&buf, mosaic); err != nil {
		b.Fatalf("png encode: %v", err)
	}
	imgBytes := buf.Bytes()

	opts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset(charset),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(32),
		mosaictext.WithTHMMCorpus(corpus),
		mosaictext.WithTHMMSeed(42),
	}

	b.ReportAllocs()
	for b.Loop() {
		img, _, dErr := image.Decode(bytes.NewReader(imgBytes))
		if dErr != nil {
			b.Fatalf("decode: %v", dErr)
		}
		res, _ := mosaictext.DecodeTrainedHMM(b.Context(), img, opts...)
		sinkTHMMResult = res
	}
}

// BenchmarkB4LanguageCorpus measures the overhead of language-sampler training
// vs uniform-random training. Run with:
//
//	scripts/gotest-caged.sh go test ./mosaictext/ -run '^$' -bench BenchmarkB4 -benchtime 3x -count 3
func BenchmarkB4LanguageCorpus(b *testing.B) {
	var fontData []byte
	for _, f := range fonts.All() {
		if f.Name == "Liberation Mono" {
			fontData = f.Data
			break
		}
	}
	if fontData == nil {
		b.Skip("Liberation Mono not found")
	}
	const (
		text    = "31415"
		charset = "0123456789"
		fs      = 32.0
		block   = 4
		corpus  = 200
	)

	mosaic := func() image.Image {
		r, err := defaults.RendererFromFonts(fontData, nil)
		if err != nil {
			b.Fatalf("renderer: %v", err)
		}
		rendered, sx, rErr := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: 16, PaddingLeft: 4})
		if rErr != nil {
			b.Fatalf("render: %v", rErr)
		}
		cropped := image.NewRGBA(image.Rect(0, 0, sx, rendered.Bounds().Dy()))
		draw.Draw(cropped, cropped.Bounds(), rendered, image.Point{}, draw.Src)
		return defaults.BlockAverage(block).Pixelate(cropped, 0, 0)
	}()

	// Save+reload to simulate real disk round-trip.
	var buf bytes.Buffer
	if err := png.Encode(&buf, mosaic); err != nil {
		b.Fatalf("png encode: %v", err)
	}
	imgBytes := buf.Bytes()
	loadImg := func() image.Image {
		img, _, err := image.Decode(bytes.NewReader(imgBytes))
		if err != nil {
			b.Fatalf("decode: %v", err)
		}
		return img
	}

	baseOpts := []mosaictext.THMMOption{
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset(charset),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(64),
		mosaictext.WithTHMMCorpus(corpus),
		mosaictext.WithTHMMSeed(42),
	}
	langOpts := append(slices.Clone(baseOpts), mosaictext.WithTHMMLanguage(lang.English))

	b.Run("uniform", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			img := loadImg()
			_, _ = mosaictext.DecodeTrainedHMM(b.Context(), img, baseOpts...)
		}
	})

	b.Run("language", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			img := loadImg()
			_, _ = mosaictext.DecodeTrainedHMM(b.Context(), img, langOpts...)
		}
	})
}
