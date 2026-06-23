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
	"context"
	"fmt"
	"image"
	"math"
	"math/rand/v2"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/windowhmm"
)

// DefaultTHMMCharset is the default candidate alphabet for DecodeTrainedHMM:
// digits only, suited to PINs, credit card numbers, and numeric codes.
const DefaultTHMMCharset = "0123456789"

// trainedHMMConfig holds DecodeTrainedHMM option state.
type trainedHMMConfig struct {
	charset  string
	fontName string // empty → sweep all bundled fonts
	fontData []byte // non-nil → use this TTF/OTF exclusively
	fontBold []byte // non-nil → bold face alongside fontData
	linear   int    // -1 = auto/sweep, 0 = sRGB only, 1 = linear only
	k        int    // KMeans clusters; 0 → auto (128)
	w        int    // window width in block columns; 0 → auto
	corpus   int    // training corpus size; 0 → auto (2000)
	seed     uint64 // PRNG seed for corpus generation and KMeans
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
				fs, block, windowW, numClusters, tgtRows, corpusSize, tcfg.seed,
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

			path := model.Viterbi(obs)
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
) (*windowhmm.Model, error) {
	rng := rand.New(rand.NewPCG(seed, seed^0xfeedface_deadbeef)) // #nosec G404 -- deterministic seed, not security

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

		// Random string from charset.
		n := minLen + rng.IntN(maxLen-minLen+1)
		runes := make([]rune, n)
		for i := range n {
			runes[i] = charRunes[rng.IntN(len(charRunes))]
		}
		text := string(runes)

		// Render → pixelate → extract block grid.
		img, _, err := r.Render(text, unpixel.Style{FontSize: fs})
		if err != nil {
			continue
		}
		pixImg := pix.Pixelate(img, 0, 0)
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
	rng2 := rand.New(rand.NewPCG(seed, seed^0xfeedface_deadbeef)) // #nosec G404 -- deterministic seed, not security

	sampleIdx := 0
	for range corpusSize {
		n := minLen + rng2.IntN(maxLen-minLen+1)
		// Re-generate the string length so we can compute the per-string
		// sample count (we only need the rune count, not the runes themselves,
		// since the sample count is determined by the grid column count).
		// But we stored samples sequentially, so we advance through allSamples
		// tracking whether each sample is the first in its string.
		//
		// Actually we stored per-string contiguous windows. To know where one
		// string ends we stored samples without a sentinel. Instead, count
		// samples per string during a replay.
		//
		// Simplest approach: replay the corpus completely with the same rng,
		// track samples per string, and label first vs. subsequent.
		runes := make([]rune, n)
		for i := range n {
			runes[i] = charRunes[rng2.IntN(len(charRunes))]
		}
		text := string(runes)

		img, _, err := r.Render(text, unpixel.Style{FontSize: fs})
		if err != nil {
			continue
		}
		pixImg := pix.Pixelate(img, 0, 0)
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
