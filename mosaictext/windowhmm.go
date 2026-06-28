package mosaictext

// windowhmm.go — sliding-window beam-search decoder for mosaic-pixelated text.
//
// DecodeWindowHMM recovers text via a character-level beam search with
// per-character in-context window scoring. For each candidate character at
// each position, the string is rendered in full, pixelated, and a window of
// block columns centred on that character is extracted and compared by MSE
// against the corresponding window in the target. The top-K candidates survive
// each step.
//
// Unlike DecodeHMM (monospace-only beam search), this decoder handles
// proportional fonts: each character's window width is set to
// ⌈advance/block⌉ so that wider glyphs get wider windows, which
// produces a discriminating per-character MSE even when characters span
// varying numbers of block columns.
//
// References from this package:
//   - measureAdvancesByCumulative (refmatch.go ~681): exact per-glyph advances.
//   - calibrateRefFS (refmatch.go ~512): font-size candidates from ink-row count.
//   - contentBounds / mseRGB (recover.go): target crop and scoring.
//   - pixelatorFor (decode.go): linear/sRGB pixelator selection.
//
// References from internal packages:
//   - internal/refmatch.ExtractBlocks / BlockSig: block-grid extraction.
//   - internal/windowhmm.BlockCell / WindowVector: block-grid types + extraction.

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"math"
	"slices"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/refmatch"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/windowhmm"
)

// DefaultWHMMCharset is the default candidate alphabet for DecodeWindowHMM:
// the digit characters plus a space, suited to numeric redactions (PINs,
// credit-card numbers, etc.).
const DefaultWHMMCharset = "0123456789 "

// whmmConfig holds DecodeWindowHMM option state.
type whmmConfig struct {
	charset   string
	fontName  string // empty → sweep all bundled fonts
	fontData  []byte // non-nil → use this TTF/OTF exclusively
	fontBold  []byte // non-nil → bold face alongside fontData
	linear    int    // -1 = auto/sweep, 0 = sRGB only, 1 = linear only
	windowW   int    // 0 → auto-select
	beamWidth int    // top-K beam width; 0 → default (5), capped at 50
	seed      int64  // PRNG seed (currently unused; reserved for future corpus sampling)
}

func defaultWHMMConfig() whmmConfig {
	return whmmConfig{
		charset: DefaultWHMMCharset,
		linear:  -1,
	}
}

// WHMMOption configures DecodeWindowHMM.
type WHMMOption func(*whmmConfig)

// WithWHMMCharset sets the candidate alphabet. Defaults to DefaultWHMMCharset.
// An empty value is ignored.
func WithWHMMCharset(cs string) WHMMOption {
	return func(c *whmmConfig) {
		if cs != "" {
			c.charset = cs
		}
	}
}

// WithWHMMFont pins the decoder to a specific bundled font by name (e.g.
// "Liberation Mono"). When set, only that font is tried. If the name does not
// match any bundled font, DecodeWindowHMM returns ErrNoContent.
func WithWHMMFont(name string) WHMMOption {
	return func(c *whmmConfig) { c.fontName = name }
}

// WithWHMMFontFile supplies raw TrueType/OpenType bytes for the regular face.
// When set, DecodeWindowHMM renders all candidates with this font exclusively.
// Pair with WithWHMMFontFileBold when the redaction uses bold text.
func WithWHMMFontFile(regularTTF []byte) WHMMOption {
	return func(c *whmmConfig) {
		if len(regularTTF) > 0 {
			c.fontData = regularTTF
		}
	}
}

// WithWHMMFontFileBold supplies raw TrueType/OpenType bytes for the bold face
// used alongside WithWHMMFontFile. It has no effect unless WithWHMMFontFile is
// also set.
func WithWHMMFontFileBold(boldTTF []byte) WHMMOption {
	return func(c *whmmConfig) {
		if len(boldTTF) > 0 {
			c.fontBold = boldTTF
		}
	}
}

// WithWHMMLinear controls whether linear-light (GIMP/GEGL) or sRGB block
// averaging is used. Tri-state: -1 = auto/sweep both (default), 0 = sRGB
// only, 1 = linear only.
func WithWHMMLinear(mode int) WHMMOption {
	return func(c *whmmConfig) { c.linear = mode }
}

// WithWHMMWindow pins the sliding-window width W (in block columns). When W ≤
// 0, the width is selected automatically as the smallest W such that W·block ≥
// widest glyph advance, clamped to [2, 3].
func WithWHMMWindow(w int) WHMMOption {
	return func(c *whmmConfig) {
		if w > 0 {
			c.windowW = w
		}
	}
}

// WithWHMMBeamWidth sets the number of live candidates (beam width) kept at
// each decoding step. Default is 5; capped at 50 for performance. Larger
// values improve accuracy at the cost of O(beamWidth·|charset|) work per
// character.
func WithWHMMBeamWidth(n int) WHMMOption {
	return func(c *whmmConfig) {
		if n > 0 {
			c.beamWidth = n
		}
	}
}

// WithWHMMSeed sets the PRNG seed. Currently reserved for future use; the
// beam-search decoder is deterministic and does not use random sampling.
// Fixing the seed makes the decoder future-proof across calls.
func WithWHMMSeed(seed int64) WHMMOption {
	return func(c *whmmConfig) { c.seed = seed }
}

// DecodeWindowHMM recovers text from a mosaic-pixelated image using a
// sliding-window beam search with per-character in-context window scoring.
// It handles proportional fonts (no per-glyph segmentation) by setting each
// character's window width to ⌈advance/block⌉ rather than a fixed constant.
//
// Pipeline per (font, linear) combination:
//  1. Detect the block grid and content bounds.
//  2. Calibrate the font size from the content height.
//  3. Measure per-glyph pixel advances (measureAdvancesByCumulative).
//  4. Select window width W: smallest W s.t. W·block ≥ widest advance, clamped
//     to [2, 3] (or pinned via WithWHMMWindow).
//  5. Beam search: at each character position t, extend each live candidate
//     by every alphabet character, render the full N-character string, extract
//     a ⌈advance/block⌉-wide window at the character's block column, and score
//     by MSE against the target. Top beamWidth candidates survive.
//  6. Try N, N-1, N-2 character counts; select best by per-character beam score.
//  7. Score by whole-image MSE; keep the (font, linear) winner.
//
// Font selection mirrors DecodeReference: WithWHMMFontFile → that font only;
// WithWHMMFont → that bundled font only; neither → sweep all bundled fonts.
//
// This decoder is NOT wired into the default Decode path and does not
// implement unpixel.Strategy. Call it directly or via --decoder window-hmm.
//
// Returns ErrNoMosaic if no block grid is detected, ErrNoContent if no
// non-background content is found, or a context error on cancellation.
func DecodeWindowHMM(ctx context.Context, img image.Image, opts ...WHMMOption) (Result, error) {
	wcfg := defaultWHMMConfig()
	for _, o := range opts {
		o(&wcfg)
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

	// obsImg: the content crop without padding, used to build the observation
	// block grid. It must have the same row count as training renders (which are
	// also unpadded), so that WindowVector dimensions match.
	obsImg := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	imutil.FillWhite(obsImg)
	xdraw.Draw(obsImg, obsImg.Bounds(), rgba, rect.Min, xdraw.Src)

	// inkRows: pixelate the content crop → strip → count, matching exactly what
	// the probe renderer does (render → pixelate → strip → count).
	inkRowsFromImg := func(pix unpixel.Pixelator) int {
		pixImg := pix.Pixelate(obsImg, 0, 0)
		g := whPixToBlockGrid(pixImg, block)
		g = whStripBlockRows(g)
		return len(g)
	}

	// target: padded version of the content crop, used only for MSE scoring so
	// that the rendered candidate has room to render into.
	const pad = 24
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+pad, rect.Dy()+pad))
	imutil.FillWhite(target)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), rgba, rect.Min, xdraw.Src)

	// Resolve font entries to sweep. Mirrors DecodeReference font contract
	// (refmatch.go ~185–239): user font file → single entry; named bundled font
	// → single entry; neither → sweep all bundled fonts.
	type fontEntry struct {
		r    unpixel.Renderer
		name string
	}
	var entries []fontEntry
	switch {
	case len(wcfg.fontData) > 0:
		r, err := render.NewXImageFromFonts(wcfg.fontData, wcfg.fontBold)
		if err != nil {
			return Result{}, fmt.Errorf("build renderer from supplied font: %w", err)
		}
		entries = []fontEntry{{r: r, name: "(user font)"}}
	case wcfg.fontName != "":
		all := fonts.All()
		var found fonts.Font
		for _, f := range all {
			if f.Name == wcfg.fontName {
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

	// Resolve linear modes.
	var linearModes []bool
	switch wcfg.linear {
	case 0:
		linearModes = []bool{false}
	case 1:
		linearModes = []bool{true}
	default:
		linearModes = []bool{false, true}
	}

	charRunes := []rune(wcfg.charset)

	type candidate struct {
		text    string
		font    string
		linear  bool
		block   int
		phaseX  int
		charCnt int
		dist    float64
	}
	bestDist := -1.0 // negative sentinel → no winner yet
	var best candidate

	for _, fe := range entries {
		for _, lin := range linearModes {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}

			pix := pixelatorFor(block, lin)

			// Build the observation block grid from obsImg (unpadded content crop),
			// not from target — padding shifts block boundaries and adds extra rows,
			// causing a dimension mismatch with training grids (also unpadded).
			tgtPix := pix.Pixelate(obsImg, 0, 0)
			tgtGrid := whPixToBlockGrid(tgtPix, block)
			tgtGrid = whStripBlockRows(tgtGrid)
			tgtGrid = whStripBlockCols(tgtGrid)
			if len(tgtGrid) == 0 {
				continue
			}
			tgtCols := len(tgtGrid[0])

			// inkRows: count from obsImg via pixelate+strip, matching the probe.
			inkRows := inkRowsFromImg(pix)

			// Calibrate font size: sweep [8,120] probing with the same
			// pixelate+strip method so both sides count non-white block rows
			// identically.
			fsCands := calibrateRefFSByPix(fe.r, pix, inkRows, block)
			if len(fsCands) == 0 {
				continue
			}

			for _, fs := range fsCands {
				if ctx.Err() != nil {
					break
				}

				// Measure per-glyph pixel advances (reused from refmatch.go ~681).
				advances := measureAdvancesByCumulative(fe.r, charRunes, fs)
				if len(advances) == 0 {
					continue
				}

				// Select window width W: smallest W s.t. W·block ≥ widest advance,
				// clamped to [2, 3].
				W := wcfg.windowW
				if W <= 0 {
					maxAdv := 0
					for _, a := range advances {
						if a > maxAdv {
							maxAdv = a
						}
					}
					W = 2
					for W*block < maxAdv && W < 3 {
						W++
					}
					W = min(max(W, 2), 3)
				}
				if tgtCols < W {
					continue
				}

				// Compute average advance in pixels.
				avgAdv := 0.0
				for _, a := range advances {
					avgAdv += float64(a)
				}
				if len(advances) > 0 {
					avgAdv /= float64(len(advances))
				}
				if avgAdv <= 0 {
					avgAdv = float64(block)
				}

				// Estimate N (number of characters). The rendered pixel width
				// includes font side-bearings (~1 extra character worth), so we
				// try both floor and floor-1 and pick the better-scoring result.
				tgtPixWidth := tgtCols * block
				Nest := max(1, int(float64(tgtPixWidth)/avgAdv))

				beamWidth := wcfg.beamWidth
				if beamWidth <= 0 {
					beamWidth = 5
				}
				beamWidth = min(beamWidth, 50) // cap for performance

				// Try N, N-1, N-2: font side-bearings and right-side-bearing can
				// add up to ~2 extra characters worth of pixel columns. The beam's
				// per-character score selects the best N without length bias.
				var decoded string
				bestBeamScore := math.Inf(-1)
				for _, N := range []int{Nest, Nest - 1, Nest - 2} {
					if N <= 0 {
						continue
					}
					res, err := whBeamSearchDecode(
						ctx, fe.r, pix, charRunes, tgtGrid, advances,
						fs, block, N, beamWidth,
					)
					if err != nil {
						if ctx.Err() != nil {
							return Result{}, ctx.Err()
						}
						continue
					}
					if res.text == "" {
						continue
					}
					if res.beamScore > bestBeamScore {
						bestBeamScore = res.beamScore
						decoded = res.text
					}
				}
				if decoded == "" {
					continue
				}

				// Score by whole-image MSE against the padded target (lower = better).
				dist := whScoreDecoded(fe.r, decoded, fs, block, lin, target)
				if bestDist < 0 || dist < bestDist {
					bestDist = dist
					best = candidate{
						text:    decoded,
						font:    fe.name,
						linear:  lin,
						block:   block,
						phaseX:  grid.PhaseX,
						charCnt: len([]rune(decoded)),
						dist:    dist,
					}
				}
			} // end for _, fs := range fsCands
		} // end for _, lin := range linearModes
	} // end for _, fe := range entries

	if best.text == "" {
		return Result{}, ErrNoContent
	}
	return Result{
		Text:       best.text,
		Font:       best.font,
		Distance:   best.dist,
		Linear:     best.linear,
		BlockSize:  best.block,
		CharCount:  best.charCnt,
		GridPhaseX: best.phaseX,
	}, nil
}

// whBeamSearchDecode recovers text from a target block grid using character-level
// beam search with per-character in-context window scoring.
//
// At each step t the beam holds partial strings of length t. Each candidate is
// extended by every alphabet character, producing a string of length t+1. The
// new string is rendered in full and a window of width ⌈advance/block⌉ is
// extracted at the block column where character t starts. MSE against the
// corresponding target observation determines the step score.
//
// Per-character windows are discriminating because they capture the full glyph
// ink rather than a fixed-width W-tuple that may span only a fraction of one
// character. TestPerCharScore confirms that the correct character achieves near-
// zero MSE while wrong characters yield MSE > 0.01 at all tested block sizes.
//
// N is the estimated number of characters in the target.
type whBeamSearchDecodeResult struct {
	text      string
	beamScore float64 // per-character beam score; higher = better
}

func whBeamSearchDecode(
	ctx context.Context,
	r unpixel.Renderer,
	pix unpixel.Pixelator,
	charRunes []rune,
	tgtGrid [][]windowhmm.BlockCell,
	advances map[rune]int,
	fs float64,
	block, n, beamWidth int,
) (whBeamSearchDecodeResult, error) {
	if n <= 0 || len(charRunes) == 0 || len(tgtGrid) == 0 {
		return whBeamSearchDecodeResult{}, nil
	}
	tgtCols := len(tgtGrid[0])

	// padChar is used to pad beam renders to n characters so every candidate
	// is rendered on the same canvas size as the target (which was rendered as
	// the full n-character string). Without padding, shorter prefixes produce
	// narrower canvases and the font renderer shifts glyph anti-aliasing at the
	// right edge, causing MSE artifacts between the beam render and the target.
	// 'e' is a good neutral pad: common, mid-advance, symmetric glyph.
	padChar := 'e'
	// Fall back to the first charset character if 'e' is not in charRunes.
	if _, ok := advances['e']; !ok && len(charRunes) > 0 {
		padChar = charRunes[0]
	}

	// beamItem holds a partial candidate string and its accumulated score.
	// score = Σ(-MSE) over all placed characters; higher is better.
	type beamItem struct {
		text  string
		score float64
	}

	// beamItemAdv extends beamItem with the cumulative pixel advance so that
	// proportional-font characters use their actual advance for column lookup.
	type beamItemAdv struct {
		beamItem
		prefixAdv int // cumulative pixel advance of item.text
	}

	beamAdv := []beamItemAdv{{beamItem: beamItem{text: "", score: 0}, prefixAdv: 0}}

	// Precompute the n-char padding string so every beam render is padded to
	// length n (same canvas size as the target, which is also n chars long).
	// This keeps glyph anti-aliasing identical between beam renders and target.
	padSuffix := make([]rune, n)
	for i := range padSuffix {
		padSuffix[i] = padChar
	}

	for step := range n {
		if ctx.Err() != nil {
			return whBeamSearchDecodeResult{}, ctx.Err()
		}

		candidates := make([]beamItemAdv, 0, len(beamAdv)*len(charRunes))

		// remainPad: pad characters after the character being placed at step.
		// prefix (step chars) + ch (1 char) + remainPad (n-step-1 chars) = n total.
		remainPad := string(padSuffix[:max(0, n-step-1)])

		for _, item := range beamAdv {
			for _, ch := range charRunes {
				chAdv := advances[ch]
				newAdv := item.prefixAdv // cumulative advance before ch

				// Render the full N-char string: prefix + ch + remainPad.
				// This matches the canvas size of the target render (N chars),
				// eliminating font anti-aliasing artifacts from canvas-size mismatch.
				renderStr := item.text + string(ch) + remainPad

				grid, err := whRenderToBlockGrid2D(r, pix, renderStr, fs, block)
				if err != nil || len(grid) == 0 {
					continue
				}
				grid = whStripBlockRows(grid)
				grid = whStripBlockCols(grid)
				if len(grid) == 0 || len(grid[0]) == 0 {
					continue
				}

				// Per-character window width: ceil(advance/block), minimum 1.
				chW := max(1, int(math.Ceil(float64(chAdv)/float64(block))))
				startCol := newAdv / block

				// effectiveW: chW clamped to available columns in both grids so
				// vectors are always the same dimension.
				effectiveW := min(
					chW,
					tgtCols-max(0, min(startCol, tgtCols-1)),
					len(grid[0])-max(0, min(startCol, len(grid[0])-1)),
				)
				if effectiveW <= 0 {
					effectiveW = 1
				}

				col := max(0, min(startCol, tgtCols-effectiveW))
				tgtVec := windowhmm.WindowVector(tgtGrid, col, effectiveW)

				rCol := max(0, min(startCol, len(grid[0])-effectiveW))
				vec := windowhmm.WindowVector(grid, rCol, effectiveW)

				stepScore := -1.0
				if vec != nil && tgtVec != nil && len(vec) == len(tgtVec) {
					mse := 0.0
					for i, v := range vec {
						d := v - tgtVec[i]
						mse += d * d
					}
					stepScore = -(mse / float64(len(vec)))
				}
				candidates = append(candidates, beamItemAdv{
					beamItem:  beamItem{item.text + string(ch), item.score + stepScore},
					prefixAdv: newAdv + chAdv,
				})
			}
		}

		if len(candidates) == 0 {
			break
		}

		slices.SortFunc(candidates, func(a, b beamItemAdv) int {
			return cmp.Compare(b.score, a.score) // descending
		})
		beamAdv = candidates[:min(beamWidth, len(candidates))]
	}

	if len(beamAdv) == 0 {
		return whBeamSearchDecodeResult{}, nil
	}
	// Trim to exactly n characters.
	result := beamAdv[0].text
	if rs := []rune(result); len(rs) > n {
		result = string(rs[:n])
	}
	// Per-character beam score: total / n (higher is better).
	perChar := 0.0
	if n > 0 {
		perChar = beamAdv[0].score / float64(n)
	}
	return whBeamSearchDecodeResult{text: result, beamScore: perChar}, nil
}

// whRenderToBlockGrid2D renders text at fs, pixelates at block, and returns
// the extracted block grid as a 2-D [R][C] slice of windowhmm.BlockCell.
func whRenderToBlockGrid2D(
	r unpixel.Renderer,
	pix unpixel.Pixelator,
	text string,
	fs float64,
	block int,
) ([][]windowhmm.BlockCell, error) {
	img, _, err := r.Render(text, unpixel.Style{FontSize: fs})
	if err != nil {
		return nil, err
	}
	// Pixelate returns *image.RGBA directly per the unpixel.Pixelator contract.
	pixImg := pix.Pixelate(img, 0, 0)
	pb := pixImg.Bounds()
	// ExtractBlocksDirect reads one pixel per block instead of averaging all
	// block² pixels. This is byte-identical to ExtractBlocks on a pixelated
	// image (every block is uniform) and is ~14× faster on the hot path.
	raw := refmatch.ExtractBlocksDirect(pixImg.Pix, pixImg.Stride, pb.Dx(), pb.Dy(), block)
	if len(raw) == 0 {
		return nil, nil
	}
	grid := make([][]windowhmm.BlockCell, len(raw))
	for ri, row := range raw {
		grid[ri] = make([]windowhmm.BlockCell, len(row))
		for ci, sig := range row {
			grid[ri][ci] = windowhmm.BlockCell{R: sig.R, G: sig.G, B: sig.B}
		}
	}
	return grid, nil
}

// whPixToBlockGrid converts a pixelated *image.RGBA to a 2-D
// [][]windowhmm.BlockCell by extracting block signatures.
// The input must already be pixelated (every block uniform); ExtractBlocksDirect
// reads one pixel per block, byte-identical to ExtractBlocks on uniform blocks.
func whPixToBlockGrid(pix *image.RGBA, block int) [][]windowhmm.BlockCell {
	pb := pix.Bounds()
	raw := refmatch.ExtractBlocksDirect(pix.Pix, pix.Stride, pb.Dx(), pb.Dy(), block)
	if len(raw) == 0 {
		return nil
	}
	grid := make([][]windowhmm.BlockCell, len(raw))
	for ri, row := range raw {
		grid[ri] = make([]windowhmm.BlockCell, len(row))
		for ci, sig := range row {
			grid[ri][ci] = windowhmm.BlockCell{R: sig.R, G: sig.G, B: sig.B}
		}
	}
	return grid
}

// calibrateRefFSByPix returns all integer font sizes in [8,120] whose probe
// render, when pixelated at block and stripped of all-white rows, produces
// exactly inkRows non-white block rows. This is the WHMM-specific variant of
// calibrateRefFS: it uses pixelation+stripping instead of inkBounds so that
// the measurement matches how inkRows is computed from the target grid
// (whPixToBlockGrid → whStripBlockRows → len).
func calibrateRefFSByPix(r unpixel.Renderer, pix unpixel.Pixelator, inkRows, block int) []float64 {
	const (
		minFS = 8
		maxFS = 120
	)
	var out []float64
	for fs := range maxFS - minFS + 1 {
		img, _, err := r.Render("Hhelo Wrd", unpixel.Style{FontSize: float64(fs + minFS)})
		if err != nil {
			continue
		}
		pixImg := pix.Pixelate(img, 0, 0)
		grid := whPixToBlockGrid(pixImg, block)
		grid = whStripBlockRows(grid)
		if len(grid) == inkRows {
			out = append(out, float64(fs+minFS))
		}
	}
	return out
}

// whStripBlockRows removes all-white block rows from the top and bottom of a
// 2-D windowhmm.BlockCell grid, matching the same convention as
// stripWhiteBlockRows in refmatch.go.
func whStripBlockRows(grid [][]windowhmm.BlockCell) [][]windowhmm.BlockCell {
	isWhiteRow := func(row []windowhmm.BlockCell) bool {
		for _, cell := range row {
			if cell.R < blockBgThresh || cell.G < blockBgThresh || cell.B < blockBgThresh {
				return false
			}
		}
		return true
	}
	start, end := 0, len(grid)
	for start < end && isWhiteRow(grid[start]) {
		start++
	}
	for end > start && isWhiteRow(grid[end-1]) {
		end--
	}
	return grid[start:end]
}

// whStripBlockCols removes all-white block columns from the left and right of
// a 2-D windowhmm.BlockCell grid (mirrors stripWhiteBlockCols in refmatch.go).
func whStripBlockCols(grid [][]windowhmm.BlockCell) [][]windowhmm.BlockCell {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return grid
	}
	nCols := len(grid[0])
	isWhiteCol := func(c int) bool {
		for _, row := range grid {
			if c < len(row) {
				cell := row[c]
				if cell.R < blockBgThresh || cell.G < blockBgThresh || cell.B < blockBgThresh {
					return false
				}
			}
		}
		return true
	}
	start, end := 0, nCols
	for start < end && isWhiteCol(start) {
		start++
	}
	for end > start && isWhiteCol(end-1) {
		end--
	}
	if start == 0 && end == nCols {
		return grid
	}
	result := make([][]windowhmm.BlockCell, len(grid))
	for i, row := range grid {
		if end <= len(row) {
			result[i] = row[start:end]
		} else if start < len(row) {
			result[i] = row[start:]
		}
	}
	return result
}

// whScoreDecoded renders decoded at fs, pixelates, and returns the whole-image
// MSE against target (lower is better; 0 means exact reproduction).
func whScoreDecoded(
	r unpixel.Renderer,
	decoded string,
	fs float64,
	block int,
	linear bool,
	target *image.RGBA,
) float64 {
	img, _, err := r.Render(decoded, unpixel.Style{FontSize: fs})
	if err != nil {
		return 1e9
	}
	pix := pixelatorFor(block, linear)
	// Pixelate returns *image.RGBA directly per the unpixel.Pixelator contract.
	return mseRGB(pix.Pixelate(img, 0, 0), target)
}
