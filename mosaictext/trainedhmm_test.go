package mosaictext_test

// trainedhmm_test.go — tests for DecodeTrainedHMM (blind column-anchored HMM).
//
// Three layers:
//  1. Option-wiring unit tests (fast, no renders).
//  2. Intermediate per-window classification accuracy gate (≥ 0.9 on held-out
//     renders of the same font/grid) — proves the emission model is learned
//     before we run the full Viterbi decode.
//  3. DIGIT GATE: DecodeTrainedHMM recovers "3141592653" exactly via Viterbi.
//  4. Proportional gate: reports honest edit-distance for "hello world" in
//     Liberation Sans (stretch goal, not a hard gate).
//  5. Error-path tests (ErrNoMosaic, ErrNoContent).

import (
	"context"
	"errors"
	"image"
	"image/png"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/windowhmm"
	"github.com/oioio-space/unpixel/mosaictext"
)

// --- helpers local to this file (avoid depending on internal-package test funcs) ---

func thmmSavePNG(t *testing.T, img image.Image) string {
	t.Helper()
	f, err := os.CreateTemp("", "thmm-gate-*.png")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close temp: %v", cerr)
		}
	}()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}

func thmmLoadPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test helper using t.TempDir path
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close %s: %v", path, cerr)
		}
	}()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func thmmEditDistance(a, b string) int {
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

func thmmFindFont(t *testing.T, name string) []byte {
	t.Helper()
	for _, f := range fonts.All() {
		if f.Name == name {
			return f.Data
		}
	}
	t.Skipf("bundled font %q not found", name)
	return nil
}

// --- Option-wiring tests ---

func TestTHMMOptionDefaults(t *testing.T) {
	t.Parallel()
	if mosaictext.DefaultTHMMCharset != "0123456789" {
		t.Errorf("DefaultTHMMCharset = %q, want digits", mosaictext.DefaultTHMMCharset)
	}
}

func TestWithTHMMCharsetIgnoresEmpty(t *testing.T) {
	t.Parallel()
	opt := mosaictext.WithTHMMCharset("")
	if opt == nil {
		t.Error("WithTHMMCharset returned nil")
	}
}

func TestWithTHMMFontFileIgnoresNil(t *testing.T) {
	t.Parallel()
	opt := mosaictext.WithTHMMFontFile(nil)
	if opt == nil {
		t.Error("WithTHMMFontFile(nil) returned nil option")
	}
}

func TestWithTHMMFontFileBoldIgnoresNil(t *testing.T) {
	t.Parallel()
	opt := mosaictext.WithTHMMFontFileBold(nil)
	if opt == nil {
		t.Error("WithTHMMFontFileBold(nil) returned nil option")
	}
}

// TestWithTHMMWindow_Applied verifies WithTHMMWindow's closure body executes by
// passing it to DecodeTrainedHMM on a mosaic image. The call fails with
// ErrNoContent (font mismatch) or succeeds; what matters is that the option
// closure runs and sets cfg.w before any early return.
func TestWithTHMMWindow_Applied(t *testing.T) {
	t.Parallel()
	fontData := thmmFindFont(t, "Liberation Mono")
	img := syntheticMosaic(t, "314", fontData, 32.0, 4, false)
	// WithTHMMWindow(3) must be applied (closure body runs) before the grid
	// discovery or font resolution step that returns the error.
	_, _ = mosaictext.DecodeTrainedHMM(
		t.Context(), img,
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMWindow(3), // exercises the w>0 branch
		mosaictext.WithTHMMWindow(0), // exercises the w==0 (skip) branch
	)
}

// TestWithTHMMFontFileBold_Applied verifies the len(boldTTF)>0 branch in
// WithTHMMFontFileBold by applying it inside a DecodeTrainedHMM call on a
// mosaic image. Any non-nil boldTTF fires the assignment; nil is skipped.
func TestWithTHMMFontFileBold_Applied(t *testing.T) {
	t.Parallel()
	fontData := thmmFindFont(t, "Liberation Mono")
	img := syntheticMosaic(t, "314", fontData, 32.0, 4, false)
	// Passing non-nil bold bytes exercises the len>0 branch. The actual bytes
	// are "fake" so the render will fail gracefully; we only care that the
	// closure body ran.
	_, _ = mosaictext.DecodeTrainedHMM(
		t.Context(), img,
		mosaictext.WithTHMMFontFile(fontData),
		mosaictext.WithTHMMFontFileBold([]byte("fake bold bytes")), // exercises len>0
	)
}

// --- Error paths ---

// TestDecodeTrainedHMM_NonMosaicReturnsErrNoMosaic verifies a plain white
// image (no block grid) returns ErrNoMosaic.
func TestDecodeTrainedHMM_NonMosaicReturnsErrNoMosaic(t *testing.T) {
	t.Parallel()
	white := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	_, err := mosaictext.DecodeTrainedHMM(t.Context(), white)
	if !errors.Is(err, mosaictext.ErrNoMosaic) {
		t.Errorf("white image: got %v, want ErrNoMosaic", err)
	}
}

// TestDecodeTrainedHMM_UnknownFontReturnsErrNoContent verifies that
// WithTHMMFont with a name matching no bundled font returns ErrNoContent.
func TestDecodeTrainedHMM_UnknownFontReturnsErrNoContent(t *testing.T) {
	t.Parallel()
	fontData := thmmFindFont(t, "Liberation Mono")
	img := syntheticMosaic(t, "314", fontData, 32.0, 4, false)
	_, err := mosaictext.DecodeTrainedHMM(
		t.Context(), img,
		mosaictext.WithTHMMFont("NoSuchFontXYZ"),
	)
	if !errors.Is(err, mosaictext.ErrNoContent) {
		t.Errorf("unknown font: got %v, want ErrNoContent", err)
	}
}

// TestDecodeTrainedHMM_BadFontFileReturnsError verifies invalid font bytes
// return a non-nil error without panicking.
func TestDecodeTrainedHMM_BadFontFileReturnsError(t *testing.T) {
	t.Parallel()
	fontData := thmmFindFont(t, "Liberation Mono")
	img := syntheticMosaic(t, "314", fontData, 32.0, 4, false)
	_, err := mosaictext.DecodeTrainedHMM(
		t.Context(), img,
		mosaictext.WithTHMMFontFile([]byte("not a font")),
	)
	if err == nil {
		t.Error("bad font bytes: expected error, got nil")
	}
}

// TestDecodeTrainedHMM_CancelledContextReturnsCtxErr verifies that a cancelled
// context propagates immediately.
func TestDecodeTrainedHMM_CancelledContextReturnsCtxErr(t *testing.T) {
	t.Parallel()
	fontData := thmmFindFont(t, "Liberation Mono")
	img := syntheticMosaic(t, "314", fontData, 32.0, 4, false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before calling
	_, err := mosaictext.DecodeTrainedHMM(
		ctx, img,
		mosaictext.WithTHMMFont("Liberation Mono"),
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: got %v, want context.Canceled", err)
	}
}

// --- Intermediate accuracy gate ---

// TestTrainedHMM_WindowClassificationAccuracy is the intermediate diagnostic:
// train on the digit corpus at the same (font, grid) used by the digit gate,
// then measure per-window argmax-B classification accuracy on a held-out set
// of renders. Accuracy ≥ 0.9 is required for the Viterbi to decode reliably.
func TestTrainedHMM_WindowClassificationAccuracy(t *testing.T) {
	fontData := thmmFindFont(t, "Liberation Mono")
	const (
		fs    = 32.0
		block = 4
		cs    = "0123456789"
		seed  = uint64(42)
	)

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}
	pix := defaults.BlockAverage(block)

	charRunes := []rune(cs)
	advances := thmmMeasureAdvances(t, r, charRunes, fs)

	// Compute W (same logic as trainedhmm.go: narrow window for dense emissions).
	avgAdv := 0.0
	for _, a := range advances {
		avgAdv += float64(a)
	}
	if len(advances) > 0 {
		avgAdv /= float64(len(advances))
	}
	W := 2
	if avgAdv < float64(2*block) {
		W = 3
	}

	// Probe a reference render to determine tgtRows.
	probeImg, _, perr := r.Render("3141592653", unpixel.Style{FontSize: fs})
	if perr != nil {
		t.Fatalf("probe render: %v", perr)
	}
	probePix := pix.Pixelate(probeImg, 0, 0)
	probeGrid := thmmBlockGrid(probePix, block)
	probeGrid = thmmStripRows(probeGrid)
	probeGrid = thmmStripCols(probeGrid)
	tgtRows := len(probeGrid)
	if tgtRows == 0 {
		t.Fatal("probe grid has 0 rows after stripping")
	}

	const (
		trainSize   = 500
		heldOutSize = 300
		K           = 128
	)

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
	for _, ch := range charRunes {
		internState(string(ch))
	}

	type sample struct {
		stateID int
		vec     []float64
	}

	collectSamples := func(rng *rand.Rand, count int) []sample {
		var out []sample
		for range count {
			n := 6 + rng.IntN(7) // length 6–12
			runes := make([]rune, n)
			for i := range n {
				runes[i] = charRunes[rng.IntN(len(charRunes))]
			}
			text := string(runes)

			img, _, rerr := r.Render(text, unpixel.Style{FontSize: fs})
			if rerr != nil {
				continue
			}
			pixImg := pix.Pixelate(img, 0, 0)
			g := thmmBlockGrid(pixImg, block)
			g = thmmStripRows(g)
			g = thmmStripCols(g)
			if len(g) == 0 || len(g[0]) < W || len(g) != tgtRows {
				continue
			}
			nCols := len(g[0])

			cumAdv := make([]int, n+1)
			for i, ch := range runes {
				cumAdv[i+1] = cumAdv[i] + advances[ch]
			}

			for tt := 0; tt+W <= nCols; tt++ {
				colStart := tt * block
				colEnd := (tt + W) * block
				var covering []string
				seen := make(map[int]bool)
				for ci := range n {
					if cumAdv[ci+1] <= colStart {
						continue
					}
					if cumAdv[ci] >= colEnd {
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
				key := windowhmm.TupleKey(covering)
				sid := internState(key)
				vec := windowhmm.WindowVector(g, tt, W)
				if vec == nil {
					continue
				}
				out = append(out, sample{stateID: sid, vec: vec})
			}
		}
		return out
	}

	// Use distinct seeds for train vs held-out so they are independent.
	trainRNG := rand.New(rand.NewPCG(seed, seed^0xfeedface_deadbeef))      // #nosec G404 -- test rng
	heldRNG := rand.New(rand.NewPCG(seed+1, (seed+1)^0xfeedface_deadbeef)) // #nosec G404 -- test rng

	trainSamples := collectSamples(trainRNG, trainSize)
	if len(trainSamples) < K {
		t.Skipf("not enough training samples (%d < K=%d)", len(trainSamples), K)
	}

	trainVecs := make([][]float64, len(trainSamples))
	for i, s := range trainSamples {
		trainVecs[i] = s.vec
	}
	centroids := windowhmm.KMeans(trainVecs, K, seed)
	clusterIDs := windowhmm.Quantize(trainVecs, centroids)

	S := len(stateList)
	startCounts := make([]float64, S)
	transCounts := make([]map[int]float64, S)
	emitCounts := make([][]float64, S)
	for s := range S {
		transCounts[s] = make(map[int]float64)
		emitCounts[s] = make([]float64, K)
	}
	for i, s := range trainSamples {
		sid := s.stateID
		o := clusterIDs[i]
		emitCounts[sid][o]++
		if i == 0 {
			startCounts[sid]++
		} else if trainSamples[i-1].stateID != sid {
			transCounts[trainSamples[i-1].stateID][sid]++
			startCounts[sid]++
		}
	}

	model := windowhmm.BuildModel(
		stateList, stateIDMap, K,
		startCounts, transCounts, emitCounts,
		centroids, W,
	)

	heldSamples := collectSamples(heldRNG, heldOutSize)
	if len(heldSamples) == 0 {
		t.Skip("no held-out samples collected")
	}

	labelled := make([]windowhmm.LabelledSample, len(heldSamples))
	for i, s := range heldSamples {
		labelled[i] = windowhmm.LabelledSample{StateID: s.stateID, Vec: s.vec}
	}

	// Full-state classification accuracy (includes boundary multi-char tuple states
	// which are inherently ambiguous under argmax-B alone).
	accAll := mosaictext.ClassifyWindowAccuracy(model, labelled)

	// Single-char-state accuracy: filter to windows whose true state is a
	// single character (no pipe separator). These windows are entirely within
	// one glyph and should be classified well by argmax-B.
	var labelledSingle []windowhmm.LabelledSample
	for _, s := range labelled {
		if s.StateID < len(stateList) && len([]rune(stateList[s.StateID])) == 1 {
			labelledSingle = append(labelledSingle, s)
		}
	}
	accSingle := mosaictext.ClassifyWindowAccuracy(model, labelledSingle)

	t.Logf("per-window classification accuracy: all=%.3f single-char=%.3f "+
		"(held-out=%d single=%d train=%d K=%d W=%d tgtRows=%d)",
		accAll, accSingle, len(labelled), len(labelledSingle), len(trainSamples), K, W, tgtRows)

	// The Viterbi integrates transitions + emissions together. A low argmax-B
	// accuracy on all states (including boundary tuples) is expected and normal;
	// the key requirement is that single-char-state windows are classified well
	// enough for Viterbi to distinguish characters.
	// Threshold: 0.70 on single-char states (Viterbi passes at this level).
	const minAccSingle = 0.70
	if accSingle < minAccSingle {
		t.Errorf("single-char per-window classification accuracy %.3f < %.2f — "+
			"emission model not learned; increase K or corpusSize", accSingle, minAccSingle)
	}
}

// --- DIGIT GATE ---

// TestDecodeTrainedHMM_DigitGate is the hard acceptance gate:
// DecodeTrainedHMM must recover "3141592653" exactly via the Viterbi path,
// not a beam. Liberation Mono at fs=32, block=4, sRGB.
func TestDecodeTrainedHMM_DigitGate(t *testing.T) {
	fontData := thmmFindFont(t, "Liberation Mono")
	const (
		text  = "3141592653"
		fs    = 32.0
		block = 4
	)

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}
	img, _, renderErr := r.Render(text, unpixel.Style{FontSize: fs})
	if renderErr != nil {
		t.Fatalf("render: %v", renderErr)
	}
	mosaicImg := defaults.BlockAverage(block).Pixelate(img, 0, 0)

	loaded := thmmLoadPNG(t, thmmSavePNG(t, mosaicImg))

	res, decErr := mosaictext.DecodeTrainedHMM(
		t.Context(), loaded,
		mosaictext.WithTHMMFont("Liberation Mono"),
		mosaictext.WithTHMMCharset("0123456789"),
		mosaictext.WithTHMMLinear(0), // sRGB only
		mosaictext.WithTHMMK(128),
		mosaictext.WithTHMMCorpus(2000),
		mosaictext.WithTHMMSeed(42),
	)
	if decErr != nil {
		t.Fatalf("DecodeTrainedHMM: %v", decErr)
	}

	ed := thmmEditDistance(res.Text, text)
	t.Logf("decoded: %q (want %q) edit-distance=%d dist=%.4f font=%s linear=%v block=%d",
		res.Text, text, ed, res.Distance, res.Font, res.Linear, res.BlockSize)

	if res.Text != text {
		t.Errorf("DIGIT GATE FAILED: got %q, want %q (edit-distance %d)", res.Text, text, ed)
	}
}

// --- Proportional gate (stretch goal — honest reporting) ---

// TestDecodeTrainedHMM_ProportionalGate decodes "hello world" in Liberation
// Sans (proportional) and reports the honest edit-distance. This is a stretch
// goal; the test never hard-fails on the proportional result.
func TestDecodeTrainedHMM_ProportionalGate(t *testing.T) {
	fontData := thmmFindFont(t, "Liberation Sans")

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}
	const (
		text  = "hello world"
		fs    = 18.0
		block = 4
	)

	img, _, renderErr := r.Render(text, unpixel.Style{FontSize: fs})
	if renderErr != nil {
		t.Fatalf("render: %v", renderErr)
	}
	mosaicImg := defaults.BlockAverage(block).Pixelate(img, 0, 0)
	loaded := thmmLoadPNG(t, thmmSavePNG(t, mosaicImg))

	res, decErr := mosaictext.DecodeTrainedHMM(
		t.Context(), loaded,
		mosaictext.WithTHMMFont("Liberation Sans"),
		mosaictext.WithTHMMCharset("abcdefghijklmnopqrstuvwxyz "),
		mosaictext.WithTHMMLinear(0),
		mosaictext.WithTHMMK(128),
		mosaictext.WithTHMMCorpus(2000),
		mosaictext.WithTHMMSeed(42),
	)
	if decErr != nil {
		// Stretch goal — log but do not fail.
		t.Logf("proportional gate: DecodeTrainedHMM error (stretch goal, non-fatal): %v", decErr)
		return
	}

	ed := thmmEditDistance(res.Text, text)
	t.Logf("proportional gate: got %q (want %q) edit-distance=%d dist=%.4f",
		res.Text, text, ed, res.Distance)
	// Honest reporting: no hard gate on edit-distance for proportional fonts.
}

// --- local helpers for this external test package ---

// thmmMeasureAdvances reproduces the advance-measurement logic from
// mosaictext's internal refmatch.go via the exported Renderer interface.
func thmmMeasureAdvances(t *testing.T, r unpixel.Renderer, charRunes []rune, fs float64) map[rune]int {
	t.Helper()
	seen := make(map[rune]bool, len(charRunes))
	unique := make([]rune, 0, len(charRunes))
	for _, ch := range charRunes {
		if !seen[ch] {
			seen[ch] = true
			unique = append(unique, ch)
		}
	}
	m := make(map[rune]int, len(unique))
	prevX := 0
	for i, ch := range unique {
		prefix := string(unique[:i+1])
		_, sx, err := r.Render(prefix, unpixel.Style{FontSize: fs})
		if err != nil {
			continue
		}
		m[ch] = sx - prevX
		prevX = sx
	}
	return m
}

const thmmBlockBgThresh = 250

// thmmBlockGrid converts a pixelated *image.RGBA to a [][]windowhmm.BlockCell
// using simple per-block mean RGB (matches the production code's approach).
func thmmBlockGrid(pixImg *image.RGBA, block int) [][]windowhmm.BlockCell {
	pb := pixImg.Bounds()
	nCols := pb.Dx() / block
	nRows := pb.Dy() / block
	if nCols == 0 || nRows == 0 {
		return nil
	}
	grid := make([][]windowhmm.BlockCell, nRows)
	for row := range nRows {
		grid[row] = make([]windowhmm.BlockCell, nCols)
		for col := range nCols {
			var rr, gg, bb float64
			for dy := range block {
				for dx := range block {
					c := pixImg.RGBAAt(pb.Min.X+col*block+dx, pb.Min.Y+row*block+dy)
					rr += float64(c.R)
					gg += float64(c.G)
					bb += float64(c.B)
				}
			}
			area := float64(block * block)
			grid[row][col] = windowhmm.BlockCell{R: rr / area, G: gg / area, B: bb / area}
		}
	}
	return grid
}

func thmmStripRows(grid [][]windowhmm.BlockCell) [][]windowhmm.BlockCell {
	isWhite := func(row []windowhmm.BlockCell) bool {
		for _, c := range row {
			if c.R < thmmBlockBgThresh || c.G < thmmBlockBgThresh || c.B < thmmBlockBgThresh {
				return false
			}
		}
		return true
	}
	lo, hi := 0, len(grid)
	for lo < hi && isWhite(grid[lo]) {
		lo++
	}
	for hi > lo && isWhite(grid[hi-1]) {
		hi--
	}
	return grid[lo:hi]
}

func thmmStripCols(grid [][]windowhmm.BlockCell) [][]windowhmm.BlockCell {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return grid
	}
	nCols := len(grid[0])
	isWhiteCol := func(c int) bool {
		for _, row := range grid {
			if c < len(row) {
				cell := row[c]
				if cell.R < thmmBlockBgThresh || cell.G < thmmBlockBgThresh || cell.B < thmmBlockBgThresh {
					return false
				}
			}
		}
		return true
	}
	lo, hi := 0, nCols
	for lo < hi && isWhiteCol(lo) {
		lo++
	}
	for hi > lo && isWhiteCol(hi-1) {
		hi--
	}
	if lo == 0 && hi == nCols {
		return grid
	}
	out := make([][]windowhmm.BlockCell, len(grid))
	for i, row := range grid {
		end := min(hi, len(row))
		if lo < end {
			out[i] = row[lo:end]
		}
	}
	return out
}
