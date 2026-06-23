package mosaictext

// trainedhmm.go — blind column-anchored trained-HMM decoder (Hill-2016 §2.2–2.3).
//
// DecodeTrainedHMM is a DISTINCT decoder from DecodeWindowHMM (window-hmm beam).
// Unlike the beam, it trains a genuine HMM on a corpus of rendered strings and
// then runs Viterbi over the target's grid columns WITHOUT knowing character
// boundaries. It is column-anchored: the sliding window advances one block
// column at a time over the target's grid, producing an observation sequence
// that is decoded in a single Viterbi pass.
//
// Algorithm (reference: Hill et al., PETS 2016, §2.2–2.3, Fig 4 and 11):
//
//  1. Discover the block grid (InferBlockGrid) and content bounds from the target.
//  2. Build a font renderer, calibrate the font size, measure per-glyph advances.
//  3. Generate a bounded corpus of known strings (digits: ~2000 random strings
//     of length 6–12). For each string, render at calibrated size, pixelate at
//     the exact discovered block+phase, extract a block grid, then for each
//     W-column window position record (stateID, vector) pairs where stateID
//     encodes the ordered tuple of distinct characters covering that window.
//  4. K-means cluster all window vectors → K centroids. Quantise each training
//     vector → cluster ID. Accumulate emission, transition, and start counts.
//  5. BuildModel (Laplace-smoothed log-space π, A, B).
//  6. Decode: slide the same W-window over the target's grid → obs sequence →
//     Model.Viterbi → state-tuple path → Concatenate → recovered text.
//  7. Sweep fonts × {sRGB, linear}, pick the winner by whole-image MSE (mseRGB).

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"unicode"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/windowhmm"
)

// DefaultTHMMCharset is the default candidate alphabet for DecodeTrainedHMM:
// digits only, suited to PINs, credit card numbers, and numeric codes.
const DefaultTHMMCharset = "0123456789"

// trainedHMMConfig holds DecodeTrainedHMM option state.
type trainedHMMConfig struct {
	charset     string
	fontName    string         // empty → sweep all bundled fonts
	fontData    []byte         // non-nil → use this TTF/OTF exclusively
	fontBold    []byte         // non-nil → bold face alongside fontData
	linear      int            // -1 = auto/sweep, 0 = sRGB only, 1 = linear only
	k           int            // KMeans clusters; 0 → auto (128)
	w           int            // window width in block columns; 0 → auto
	corpus      int            // training corpus size; 0 → auto (2000)
	seed        uint64         // PRNG seed for corpus generation and KMeans
	language    *lang.Language // non-nil → draw training strings from word-list corpus
	jpegQuality int            // > 0 → JPEG-roundtrip rendered training images before pixelation
	lmBeta      float64        // > 0 → fuse LM into Viterbi with this weight (requires language)
}

func defaultTrainedHMMConfig() trainedHMMConfig {
	return trainedHMMConfig{
		charset: DefaultTHMMCharset,
		linear:  -1,
		seed:    42,
	}
}

// THMMOption configures DecodeTrainedHMM.
type THMMOption func(*trainedHMMConfig)

// WithTHMMCharset sets the candidate alphabet. Defaults to DefaultTHMMCharset
// (digits 0–9). An empty value is ignored.
func WithTHMMCharset(cs string) THMMOption {
	return func(c *trainedHMMConfig) {
		if cs != "" {
			c.charset = cs
		}
	}
}

// WithTHMMFont pins the decoder to a specific bundled font by name (e.g.
// "Liberation Mono"). When set, only that font is tried. If the name does not
// match any bundled font, DecodeTrainedHMM returns ErrNoContent.
func WithTHMMFont(name string) THMMOption {
	return func(c *trainedHMMConfig) { c.fontName = name }
}

// WithTHMMFontFile supplies raw TrueType/OpenType bytes for the regular face.
// When set, DecodeTrainedHMM renders all candidates with this font exclusively.
func WithTHMMFontFile(regularTTF []byte) THMMOption {
	return func(c *trainedHMMConfig) {
		if len(regularTTF) > 0 {
			c.fontData = regularTTF
		}
	}
}

// WithTHMMFontFileBold supplies raw TrueType/OpenType bytes for the bold face
// alongside WithTHMMFontFile. Has no effect unless WithTHMMFontFile is set.
func WithTHMMFontFileBold(boldTTF []byte) THMMOption {
	return func(c *trainedHMMConfig) {
		if len(boldTTF) > 0 {
			c.fontBold = boldTTF
		}
	}
}

// WithTHMMLinear controls linear-light vs sRGB block averaging.
// -1 = auto/sweep both (default), 0 = sRGB only, 1 = linear only.
func WithTHMMLinear(mode int) THMMOption {
	return func(c *trainedHMMConfig) { c.linear = mode }
}

// WithTHMMK sets the number of KMeans clusters K. 0 → auto (128).
// Try K ∈ {64,128,256,512} when per-window classification accuracy is low.
func WithTHMMK(k int) THMMOption {
	return func(c *trainedHMMConfig) {
		if k > 0 {
			c.k = k
		}
	}
}

// WithTHMMWindow pins the sliding-window width W (block columns). 0 → auto.
func WithTHMMWindow(w int) THMMOption {
	return func(c *trainedHMMConfig) {
		if w > 0 {
			c.w = w
		}
	}
}

// WithTHMMCorpus sets the training corpus size (number of random strings).
// 0 → auto (2000). Larger corpora improve emission-model accuracy at the cost
// of training time.
func WithTHMMCorpus(n int) THMMOption {
	return func(c *trainedHMMConfig) {
		if n > 0 {
			c.corpus = n
		}
	}
}

// WithTHMMSeed sets the PRNG seed for corpus generation and KMeans.
// Identical seeds produce identical training results.
func WithTHMMSeed(seed uint64) THMMOption {
	return func(c *trainedHMMConfig) { c.seed = seed }
}

// WithTHMMLanguage enables language-structured corpus generation (B4.1).
// When set, training strings are drawn by sampling random words from the
// embedded word list for l and joining them with spaces, so the learned
// HMM transition matrix reflects real letter n-gram statistics for that
// language rather than the flat distribution of uniform-random sampling.
// Only words whose runes are all present in the active charset are kept;
// out-of-charset words are skipped during sampling.
//
// When this option is NOT set the default uniform-random sampling is used
// and the output is byte-identical to previous versions (given the same seed).
func WithTHMMLanguage(l lang.Language) THMMOption {
	return func(c *trainedHMMConfig) { c.language = new(l) }
}

// WithTHMMJPEG enables JPEG-augmented emission training (B4.2). When quality
// is > 0, each rendered training image is JPEG-roundtripped (encoded at the
// given quality with [image/jpeg], then decoded back) before pixelation, so
// the KMeans emission clusters absorb JPEG compression artefacts.  This
// improves decoding accuracy when the target image was captured as a JPEG.
//
// quality must be in [1, 100]; values outside that range are clamped.
// quality 0 (or unset) disables JPEG augmentation — the default is off and
// the output is byte-identical to previous versions.
//
// Mix strategy: every training sample is JPEG-roundtripped when this option
// is active, keeping the augmentation deterministic given the seed.  A 50/50
// mix would require twice the corpus to achieve the same emission coverage, so
// full augmentation is preferred for noisy/JPEG targets.
func WithTHMMJPEG(quality int) THMMOption {
	return func(c *trainedHMMConfig) {
		if quality > 0 {
			c.jpegQuality = min(100, quality)
		}
	}
}

// WithTHMMLMWeight enables language-model-fused Viterbi decoding (roadmap item #3).
//
// When beta > 0 and [WithTHMMLanguage] is also set, the per-transition score
// in Viterbi becomes:
//
//	logA[prev][s] + beta * lmScore(prevContext, addedChars)
//
// where addedChars is the non-overlapping prefix of the prev state tuple that
// the window commits as it advances to s (the maximal-overlap merge rule, same
// as [windowhmm.Concatenate]), and prevContext is the text committed on the
// best path reaching the previous state. The LM scorer is
// [lang.Model.TransitionLogProb] applied char-by-char over addedChars, using
// the English or French bigram model selected by [WithTHMMLanguage].
//
// Accounting note: the LM scores only the characters actually committed by each
// transition, not the full state tuple, so characters in the window overlap are
// never double-counted across adjacent transitions.
//
// beta=0 (the default) restores byte-identical behaviour to the plain Viterbi;
// nil or absent [WithTHMMLanguage] also silently disables the LM term.
// Typical useful range: beta ∈ [0.5, 5.0]; start with beta=2.0.
func WithTHMMLMWeight(beta float64) THMMOption {
	return func(c *trainedHMMConfig) { c.lmBeta = beta }
}

// DecodeTrainedHMM recovers text from a mosaic-pixelated image using a
// blind, column-anchored trained HMM (Hill-2016, §2.2–2.3).
//
// Unlike DecodeWindowHMM (a per-character beam search that re-renders for
// every candidate), this decoder trains a genuine HMM on a corpus of rendered
// strings: it clusters column-window vectors into K emission symbols, learns
// state-transition and emission probabilities, and then runs a single Viterbi
// pass over the target's block-column observations WITHOUT knowing character
// boundaries. The Viterbi state path is converted to text by the
// maximal-overlap merge rule (windowhmm.Concatenate).
//
// Pipeline:
//  1. Detect block grid and content bounds from the target.
//  2. For each (font, linear) combination: calibrate font size, measure
//     per-glyph advances, compute W (window width in block columns).
//  3. Generate a corpus of random strings from the charset. For each string,
//     render → pixelate → extract block grid → slide W-window → accumulate
//     (stateID, vector) pairs.
//  4. KMeans over all vectors → centroids. Quantise → emission counts.
//     Accumulate transition and start counts from consecutive window states.
//  5. BuildModel (Laplace-smoothed log π, A, B) → Viterbi over target obs.
//  6. Sweep (font, linear), keep winner by lowest whole-image mseRGB.
//
// Returns ErrNoMosaic if no block grid is detected, ErrNoContent if no
// non-background content is found, or a context error on cancellation.
func DecodeTrainedHMM(ctx context.Context, img image.Image, opts ...THMMOption) (Result, error) {
	tcfg := defaultTrainedHMMConfig()
	for _, o := range opts {
		o(&tcfg)
	}

	rgba := toRGBA(img)
	grid, ok := unpixel.InferBlockGrid(img)
	if !ok || grid.Size < 2 {
		return Result{}, ErrNoMosaic
	}
	block := grid.Size

	rect := contentBounds(rgba)
	if rect.Empty() {
		return Result{}, ErrNoContent
	}

	// obsImg: tight content crop (no padding) used to build the observation grid.
	obsImg := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	imutil.FillWhite(obsImg)
	xdraw.Draw(obsImg, obsImg.Bounds(), rgba, rect.Min, xdraw.Src)

	// target: padded version for whole-image MSE scoring.
	const pad = 24
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+pad, rect.Dy()+pad))
	imutil.FillWhite(target)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), rgba, rect.Min, xdraw.Src)

	// Resolve font entries.
	type fontEntry struct {
		r    unpixel.Renderer
		name string
	}
	var entries []fontEntry
	switch {
	case len(tcfg.fontData) > 0:
		r, err := render.NewXImageFromFonts(tcfg.fontData, tcfg.fontBold)
		if err != nil {
			return Result{}, fmt.Errorf("build renderer from supplied font: %w", err)
		}
		entries = []fontEntry{{r: r, name: "(user font)"}}
	case tcfg.fontName != "":
		all := fonts.All()
		var found fonts.Font
		for _, f := range all {
			if f.Name == tcfg.fontName {
				found = f
				break
			}
		}
		if found.Name == "" {
			return Result{}, ErrNoContent
		}
		r, err := defaults.RendererFromFonts(found.Data, nil)
		if err != nil {
			return Result{}, fmt.Errorf("build renderer for %s: %w", found.Name, err)
		}
		entries = []fontEntry{{r: r, name: found.Name}}
	default:
		all := fonts.All()
		entries = make([]fontEntry, 0, len(all))
		for _, f := range all {
			r, err := defaults.RendererFromFonts(f.Data, nil)
			if err != nil {
				return Result{}, fmt.Errorf("build renderer for %s: %w", f.Name, err)
			}
			entries = append(entries, fontEntry{r: r, name: f.Name})
		}
	}

	var linearModes []bool
	switch tcfg.linear {
	case 0:
		linearModes = []bool{false}
	case 1:
		linearModes = []bool{true}
	default:
		linearModes = []bool{false, true}
	}

	charRunes := []rune(tcfg.charset)
	if len(charRunes) == 0 {
		return Result{}, ErrNoContent
	}

	numClusters := tcfg.k
	if numClusters <= 0 {
		numClusters = 128
	}
	corpusSize := tcfg.corpus
	if corpusSize <= 0 {
		corpusSize = 2000
	}

	type candidate struct {
		text   string
		font   string
		linear bool
		block  int
		phaseX int
		dist   float64
	}
	bestDist := -1.0
	var best candidate

	for _, fe := range entries {
		for _, lin := range linearModes {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}

			pix := pixelatorFor(block, lin)

			// Build the observation block grid from the content crop.
			tgtPix := pix.Pixelate(obsImg, 0, 0)
			tgtGrid := whPixToBlockGrid(tgtPix, block)
			tgtGrid = whStripBlockRows(tgtGrid)
			tgtGrid = whStripBlockCols(tgtGrid)
			if len(tgtGrid) == 0 || len(tgtGrid[0]) == 0 {
				continue
			}
			tgtCols := len(tgtGrid[0])
			tgtRows := len(tgtGrid)

			// Calibrate font size.
			inkRows := tgtRows // tgtGrid is already stripped
			fsCands := calibrateRefFSByPix(fe.r, pix, inkRows, block)
			if len(fsCands) == 0 {
				continue
			}
			fs := fsCands[0] // use first matching font size

			// Measure per-glyph advances.
			advances := measureAdvancesByCumulative(fe.r, charRunes, fs)
			if len(advances) == 0 {
				continue
			}

			// Compute W (window width in block columns).
			//
			// The trained HMM benefits from a NARROW window: W=2 keeps the
			// per-window state space small (single-character tuples dominate)
			// and gives dense emission counts per state, making KMeans centroids
			// discriminating. A wider W explodes the tuple state space and dilutes
			// emission counts. W=2 works even when advance ≫ 2×block because the
			// window captures a partial cross-section of glyph ink — the model
			// learns "two columns of ink from a '3'" → cluster C, not the whole '3'.
			//
			// Promote to W=3 only when avgAdv < 2×block, i.e., when a glyph spans
			// fewer than 2 block columns — so a W=2 window would almost always
			// straddle two characters and produce ambiguous state tuples.
			windowW := tcfg.w
			if windowW <= 0 {
				avgAdv := 0.0
				for _, a := range advances {
					avgAdv += float64(a)
				}
				if len(advances) > 0 {
					avgAdv /= float64(len(advances))
				}
				if avgAdv < float64(2*block) {
					windowW = 3
				} else {
					windowW = 2
				}
			}
			if tgtCols < windowW {
				continue
			}

			// Build char-string representations (single rune per entry).
			charStrs := make([]string, len(charRunes))
			for i, ch := range charRunes {
				charStrs[i] = string(ch)
			}

			// Train the HMM on a corpus of rendered strings.
			model, err := trainHMM(
				ctx, fe.r, pix, charRunes, charStrs, advances,
				fs, block, windowW, numClusters, tgtRows, corpusSize,
				tcfg.seed, tcfg.language, tcfg.jpegQuality,
			)
			if err != nil {
				if ctx.Err() != nil {
					return Result{}, ctx.Err()
				}
				continue
			}
			if model == nil {
				continue
			}

			// Decode: slide windowW-column window over target → obs sequence → Viterbi → text.
			nWindows := tgtCols - windowW + 1
			if nWindows <= 0 {
				continue
			}
			obs := make([]int, nWindows)
			for t := range nWindows {
				vec := windowhmm.WindowVector(tgtGrid, t, windowW)
				if vec == nil {
					obs[t] = 0
					continue
				}
				obs[t] = windowhmm.NearestCentroid(vec, model.Centroids)
			}

			// Build the LM scorer when language + positive beta are set AND
			// the charset contains at least one Unicode letter. For digit-only or
			// symbol-only charsets the English/French bigram model carries no
			// meaningful signal and would distort the acoustic-only optimum.
			var lmScore func(prevContext, addedChars string) float64
			if tcfg.language != nil && tcfg.lmBeta > 0 && charsetHasLetters(charRunes) {
				lm := lang.ModelFor(*tcfg.language)
				lmScore = func(prevContext, addedChars string) float64 {
					// Score each committed rune against the bigram model.
					// prevContext is the text committed so far on the best path;
					// the LM context rune is its last character (space if empty).
					prev := ' '
					for _, r := range prevContext {
						prev = r // last rune
					}
					var sum float64
					for _, r := range addedChars {
						sum += lm.TransitionLogProb(prev, r)
						prev = r
					}
					return sum
				}
			}

			path := model.ViterbiLM(obs, tcfg.lmBeta, lmScore)
			if len(path) == 0 {
				continue
			}

			decoded := windowhmm.Concatenate(model.States, path)
			if decoded == "" {
				continue
			}

			// Score by whole-image MSE against the padded target.
			dist := whScoreDecoded(fe.r, decoded, fs, block, lin, target)
			if bestDist < 0 || dist < bestDist {
				bestDist = dist
				best = candidate{
					text:   decoded,
					font:   fe.name,
					linear: lin,
					block:  block,
					phaseX: grid.PhaseX,
					dist:   dist,
				}
			}
		}
	}

	if best.text == "" {
		return Result{}, ErrNoContent
	}
	return Result{
		Text:       best.text,
		Font:       best.font,
		Distance:   best.dist,
		Linear:     best.linear,
		BlockSize:  best.block,
		CharCount:  len([]rune(best.text)),
		GridPhaseX: best.phaseX,
	}, nil
}

// trainHMM generates a corpus of rendered strings, extracts window vectors,
// clusters them with KMeans, and builds a log-space HMM model.
//
// Parameters:
//   - charRunes, charStrs: the candidate alphabet (runes and their string forms).
//   - advances: per-rune pixel advances (from measureAdvancesByCumulative).
//   - fs: calibrated font size in points.
//   - block: block size in pixels.
//   - W: window width in block columns.
//   - K: number of KMeans clusters.
//   - tgtRows: number of ink block rows in the target (controls vector dimension).
//   - corpusSize: number of random strings to generate.
//   - seed: PRNG seed for corpus generation and KMeans.
//   - language: non-nil enables language-structured corpus sampling (B4.1).
//   - jpegQuality: > 0 enables JPEG-roundtrip augmentation (B4.2).
func trainHMM(
	ctx context.Context,
	r unpixel.Renderer,
	pix unpixel.Pixelator,
	charRunes []rune,
	charStrs []string,
	advances map[rune]int,
	fs float64,
	block, windowW, numClusters, tgtRows, corpusSize int,
	seed uint64,
	language *lang.Language,
	jpegQuality int,
) (*windowhmm.Model, error) {
	rng := rand.New(rand.NewPCG(seed, seed^0xfeedface_deadbeef)) // #nosec G404 -- deterministic seed, not security

	// Build the language-corpus sampler if requested (B4.1).
	// wordPool is the pre-filtered subset of the dictionary that consists only
	// of words whose runes are all in the active charset.  Sampling joins random
	// words with spaces until the target length is reached, giving the transition
	// matrix real letter-pair statistics.  When language is nil (default) the
	// wordPool is empty and uniform-random sampling is used unchanged.
	// spaceInCharset is captured by sampleText and the wordPool filter below;
	// computed once here so neither path repeats the scan over charRunes.
	spaceInCharset := false
	for _, r := range charRunes {
		if r == ' ' {
			spaceInCharset = true
			break
		}
	}

	var wordPool []string
	if language != nil {
		dict := lang.DictionaryFor(*language)
		charSet := make(map[rune]bool, len(charRunes))
		for _, r := range charRunes {
			charSet[r] = true
		}
		// Space must be in the charset for word-join to make sense; if it is
		// not, fall back to concatenation without separators.
		for w := range dict.All() {
			ok := true
			for _, r := range w {
				if !charSet[r] {
					ok = false
					break
				}
			}
			if ok {
				// Ensure joining words with space is valid when space is in charset.
				if !spaceInCharset {
					// Drop words with embedded spaces (none expected in word lists).
					hasSpace := false
					for _, r := range w {
						if r == ' ' {
							hasSpace = true
							break
						}
					}
					if hasSpace {
						continue
					}
				}
				wordPool = append(wordPool, w)
			}
		}
	}

	// sampleText returns a random training string of length n.
	// With wordPool: join random words (separated by space when space is in charset)
	// until at least n runes are accumulated, then truncate.
	// Without wordPool: uniform-random from charRunes (default unchanged path).
	sampleText := func(n int) string {
		if len(wordPool) == 0 {
			// Default: uniform-random from charset.
			runes := make([]rune, n)
			for i := range n {
				runes[i] = charRunes[rng.IntN(len(charRunes))]
			}
			return string(runes)
		}
		// Language-structured: pick random words and join.
		sep := ""
		if spaceInCharset {
			sep = " "
		}
		var buf []rune
		for len(buf) < n {
			w := wordPool[rng.IntN(len(wordPool))]
			if len(buf) > 0 && sep != "" {
				buf = append(buf, ' ')
			}
			buf = append(buf, []rune(w)...)
		}
		return string(buf[:n])
	}

	// jpegRoundtrip applies JPEG compression+decompression to img when
	// jpegQuality > 0 (B4.2), returning an *image.RGBA ready for Pixelate.
	// Returns img unchanged (already *image.RGBA) when disabled.
	jpegRoundtrip := func(img *image.RGBA) *image.RGBA {
		if jpegQuality <= 0 {
			return img
		}
		var buf bytes.Buffer
		if encErr := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); encErr != nil {
			return img // best-effort: skip augmentation on encode failure
		}
		decoded, decErr := jpeg.Decode(bytes.NewReader(buf.Bytes()))
		if decErr != nil {
			return img
		}
		return toRGBA(decoded)
	}

	// Intern state tuples.
	stateIDMap := make(map[string]int)
	var stateList []string
	internState := func(key string) int {
		if id, ok := stateIDMap[key]; ok {
			return id
		}
		id := len(stateList)
		stateIDMap[key] = id
		stateList = append(stateList, key)
		return id
	}

	// We need to pre-intern at least single-char states for all chars so the
	// model has states even if training misses some.
	for _, cs := range charStrs {
		internState(cs)
	}

	type windowSample struct {
		stateID int
		vec     []float64
	}
	var allSamples []windowSample

	// Corpus generation: generate corpusSize random strings of length 6–12.
	const (
		minLen = 6
		maxLen = 12
	)

	for range corpusSize {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Sample a training string: language-structured (B4.1) when wordPool is
		// non-empty, otherwise uniform-random from the charset (default path).
		n := minLen + rng.IntN(maxLen-minLen+1)
		text := sampleText(n)
		runes := []rune(text)
		n = len(runes) // sampleText may produce a different length for language path

		// Render → (optional JPEG roundtrip, B4.2) → pixelate → extract block grid.
		rawImg, _, err := r.Render(text, unpixel.Style{FontSize: fs})
		if err != nil {
			continue
		}
		pixImg := pix.Pixelate(jpegRoundtrip(toRGBA(rawImg)), 0, 0)
		grid := whPixToBlockGrid(pixImg, block)
		grid = whStripBlockRows(grid)
		grid = whStripBlockCols(grid)
		if len(grid) == 0 || len(grid[0]) < windowW {
			continue
		}
		nCols := len(grid[0])
		nRows := len(grid)

		// Build a cumulative pixel offset → char index map.
		// cumAdv[i] is the start pixel column of the i-th rendered character.
		cumAdv := make([]int, n+1)
		for i, ch := range runes {
			cumAdv[i+1] = cumAdv[i] + advances[ch]
		}
		totalPx := cumAdv[n]
		if totalPx <= 0 {
			continue
		}

		// Slide windowW-column window over the rendered grid columns.
		for t := 0; t+windowW <= nCols; t++ {
			// Determine which characters cover block columns [t, t+windowW).
			// Block column c covers pixel range [c*block, (c+1)*block).
			colStart := t * block
			colEnd := (t + windowW) * block

			// Find distinct chars whose pixel span overlaps [colStart, colEnd).
			var covering []string
			seen := make(map[int]bool, windowW)
			for ci := range n {
				charStart := cumAdv[ci]
				charEnd := cumAdv[ci+1]
				if charEnd <= colStart {
					continue
				}
				if charStart >= colEnd {
					break
				}
				if !seen[ci] {
					seen[ci] = true
					covering = append(covering, string(runes[ci]))
				}
			}
			if len(covering) == 0 {
				continue
			}

			// Skip when the rendered row count doesn't match the target, to
			// avoid dimension mismatches in KMeans and at decode time.
			if nRows != tgtRows {
				continue
			}

			vec := windowhmm.WindowVector(grid, t, windowW)
			if vec == nil {
				continue
			}

			key := windowhmm.TupleKey(covering)
			stateID := internState(key)
			allSamples = append(allSamples, windowSample{stateID: stateID, vec: vec})
		}
	}

	if len(allSamples) == 0 || len(stateList) == 0 {
		return nil, nil
	}
	effectiveClusters := numClusters
	if len(allSamples) < effectiveClusters {
		// Not enough samples to cluster. Fall back to half the sample count, but
		// never request more clusters than samples (KMeans panics when K > n).
		effectiveClusters = max(1, len(allSamples)/2)
	}

	// Extract just the vectors for KMeans.
	vecs := make([][]float64, len(allSamples))
	for i, s := range allSamples {
		vecs[i] = s.vec
	}

	// KMeans clustering.
	centroids := windowhmm.KMeans(vecs, effectiveClusters, seed)

	// Quantise all training vectors to cluster IDs.
	clusterIDs := windowhmm.Quantize(vecs, centroids)

	S := len(stateList)
	startCounts := make([]float64, S)
	transCounts := make([]map[int]float64, S)
	emitCounts := make([][]float64, S)
	for s := range S {
		transCounts[s] = make(map[int]float64)
		emitCounts[s] = make([]float64, effectiveClusters)
	}

	// Second pass: accumulate counts from the corpus using the quantised IDs.
	// We replay the corpus in order, tracking transitions within each string's
	// window sequence.
	//
	// Since allSamples is a flat slice of all windows across all strings, we
	// need to track string boundaries. We do a second, cheap replay of the
	// corpus using the same seed to get per-string sample counts.
	//
	// Strategy: during the first pass above we collected allSamples in order.
	// Each contiguous run within a string has a "first" sample (start of string)
	// and consecutive samples (transitions). We reconstruct this by replaying
	// the corpus string lengths.
	// Second pass: replay the corpus with rng2 (same seed as rng) to reconstruct
	// per-string boundaries and accumulate start/transition counts.  Every choice
	// that affects the rendered pixel grid — string content, JPEG roundtrip —
	// must be reproduced identically so sampleIdx advances in lock-step with the
	// allSamples slice built in the first pass.
	rng2 := rand.New(rand.NewPCG(seed, seed^0xfeedface_deadbeef)) // #nosec G404 -- deterministic seed, not security

	sampleIdx := 0
	for range corpusSize {
		n := minLen + rng2.IntN(maxLen-minLen+1)
		text := sampleText(n) // must mirror first-pass sampleText(n) call
		runes := []rune(text)
		n = len(runes)

		rawImg, _, err := r.Render(text, unpixel.Style{FontSize: fs})
		if err != nil {
			continue
		}
		pixImg := pix.Pixelate(jpegRoundtrip(toRGBA(rawImg)), 0, 0)
		g := whPixToBlockGrid(pixImg, block)
		g = whStripBlockRows(g)
		g = whStripBlockCols(g)
		if len(g) == 0 || len(g[0]) < windowW {
			continue
		}
		nCols := len(g[0])
		nRows := len(g)
		if nRows != tgtRows {
			continue
		}

		cumAdv := make([]int, n+1)
		for i, ch := range runes {
			cumAdv[i+1] = cumAdv[i] + advances[ch]
		}

		prevSampleIdx := -1
		for t := 0; t+windowW <= nCols; t++ {
			colStart := t * block
			colEnd := (t + windowW) * block

			var covering []string
			seen := make(map[int]bool, windowW)
			for ci := range n {
				charStart := cumAdv[ci]
				charEnd := cumAdv[ci+1]
				if charEnd <= colStart {
					continue
				}
				if charStart >= colEnd {
					break
				}
				if !seen[ci] {
					seen[ci] = true
					covering = append(covering, string(runes[ci]))
				}
			}
			if len(covering) == 0 {
				continue
			}

			if sampleIdx >= len(allSamples) {
				break
			}
			s := allSamples[sampleIdx]
			o := clusterIDs[sampleIdx]
			sampleIdx++

			emitCounts[s.stateID][o]++
			if prevSampleIdx < 0 {
				startCounts[s.stateID]++
			} else {
				prevStateID := allSamples[prevSampleIdx].stateID
				transCounts[prevStateID][s.stateID]++
			}
			prevSampleIdx = sampleIdx - 1
		}
	}

	model := windowhmm.BuildModel(
		stateList, stateIDMap, effectiveClusters,
		startCounts, transCounts, emitCounts,
		centroids, windowW,
	)
	return model, nil
}

// charsetHasLetters reports whether runes contains at least one Unicode letter.
// Used to guard LM activation: the English/French bigram model is only
// meaningful for alphabetic charsets; digit-only or symbol-only charsets
// must not receive a spurious LM bias.
func charsetHasLetters(runes []rune) bool {
	for _, r := range runes {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// ClassifyWindowAccuracy returns the fraction of held-out window samples
// (from a separate corpus) whose argmax_state B[s][obs]·prior correctly
// predicts the training state. This is the intermediate diagnostic described
// in the algorithm spec: if this is low (< 0.7), the Viterbi cannot work well.
//
// It is exported so tests can assert per-window classification accuracy ≥ 0.9
// before running the full decode gate.
func ClassifyWindowAccuracy(
	model *windowhmm.Model,
	heldOutSamples []windowhmm.LabelledSample,
) float64 {
	if len(heldOutSamples) == 0 {
		return 0
	}
	correct := 0
	for _, s := range heldOutSamples {
		obs := windowhmm.NearestCentroid(s.Vec, model.Centroids)
		// argmax_state B[state][obs] · prior (uniform prior → just argmax B).
		bestState, bestLogP := 0, math.Inf(-1)
		for si := range len(model.States) {
			if obs < len(model.LogB[si]) {
				if lp := model.LogB[si][obs]; lp > bestLogP {
					bestLogP, bestState = lp, si
				}
			}
		}
		if bestState == s.StateID {
			correct++
		}
	}
	return float64(correct) / float64(len(heldOutSamples))
}
