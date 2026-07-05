package mosaictext

// refmatch.go — Depix-style reference-matching decoder for mosaic-pixelated text.
//
// DecodeReference recovers arbitrary content (passwords, code, random strings)
// from a mosaic-pixelated image by geometrically matching the pixelated target
// against a self-synthesised reference rendered from the same font.
//
// # Algorithm
//
// For each charset character X and each sub-block phase p in [0, block):
//  1. X is rendered alone with PaddingLeft=p and pixelated at originX=0.
//  2. Its block columns are extracted (Cols = ceil, Advance = floor) and stored
//     in the per-char-per-phase reference table perCharRef[X][p].
//
// Decoding sweeps all 8 initial phases. For each starting phase p0:
//  1. Walk the target block grid left-to-right, tracking the current pixel
//     offset within the line (pixOff). The sub-block phase of the current
//     position is pixOff mod block.
//  2. At each target column c, look up the per-char references for phase
//     (pixOff mod block) and pick the character with the lowest block
//     distance to tgtGrid[:][c:c+Cols].
//  3. Advance the cursor: pixOff += adv[X]; c += Advance.
//
// The starting phase that produces the lowest mean per-cell block distance wins.
//
// # Why per-char-per-phase (not a single strip)
//
// A single charset strip rendered with PaddingLeft=p gives the correct
// sub-block phase only for the FIRST character. For subsequent characters the
// phase is determined by the accumulated advances of the preceding TARGET TEXT
// characters, not the preceding CHARSET characters — and those are different
// sequences. Per-char-per-phase rendering produces the exact block signatures
// for every possible position in the decoded string, regardless of order.
//
// # Font contract
//
//   - WithRefFontFile(data) → use only that font (bundled sweep skipped).
//   - WithRefFont(name)     → use only the named bundled font.
//   - Neither supplied      → sweep all bundled fonts × {sRGB, linear},
//     keep the result with the lowest whole-image block-distance.

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"sync"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/refmatch"
	"github.com/oioio-space/unpixel/internal/render"
)

// DefaultRefCharset is the default candidate alphabet for DecodeReference.
// It covers all printable ASCII characters — wide enough for passwords,
// source code, and arbitrary secrets.
const DefaultRefCharset = unpixel.CharsetASCII

// LangEnglish and LangFrench re-export lang.Language constants for callers who
// want LM-guided ref-match without importing the internal lang package directly.
const (
	LangEnglish = lang.English
	LangFrench  = lang.French
)

// defaultRefBeamWidth is the number of beam hypotheses kept at each cell when
// the LM beam is enabled. Width 16 gives a good coverage of proportional-font
// ambiguity (many glyphs pixelate alike at block 8) without excessive runtime.
const defaultRefBeamWidth = 32

// defaultRefLambda is the LM weight λ for the ref-match beam. The beam prunes
// hypotheses by a combined score:
//
//	beamScore = distSum/cells − λ·lmSum/cells
//
// where distSum is the cumulative raw per-cell block distance (lower = better)
// and lmSum is the cumulative bigram log-prob (higher = better; negative).
// λ scales the LM contribution relative to geometry.
//
// Final winner-selection uses distSum/cells alone, so the reported perCell
// distance is a pure geometric measure comparable to the greedy path.
//
// Calibration: per-cell block distances ∈ [0.001, 0.15]; bigram log-probs
// ∈ [−8, −1] nats. With λ = 0.03, a 3-nat logP difference (≈ 20× probability
// ratio) shifts the beam score by 0.09 — comparable to the 0.01–0.05 distance
// gap between ambiguous lower-case proportional glyphs at block 8.
//
// The τ-based candidate gate (only candidates within [d_min, d_min+τ] enter the
// beam) uses τ = defaultRefTau, keeping unambiguous glyphs exact.
// defaultRefLambda is the LM weight λ used in the combined beam score and
// winner selection. The score is:
//
//	score = distSum/cells − λ·lmSum/cells
//
// λ must be small enough that a full-range LM difference (~7 nats) cannot
// compensate for a geometric distance difference that exceeds the τ tie band.
// With τ=0.05 and λ=0.005, the maximum LM compensation is 7×0.005=0.035 —
// less than τ, so geometry strictly dominates outside the tie band.
const defaultRefLambda = 0.02

// defaultRefTau is the geometric tie-band radius for beamMatchPhase. Only
// candidates whose per-cell distance is within τ of the per-hypothesis minimum
// d_min are expanded into the beam. This gates the LM influence: outside the
// band, only the geometric winner enters and the LM cannot change the choice.
//
// τ = 0.05 targets the ambiguous proportional lower-case letter cluster at
// block=8 (inter-candidate gaps typically 0.01–0.04) while excluding most
// unambiguous characters. Note that at block=8, digit gaps in proportional and
// monospace fonts can be as small as 0.001–0.05 depending on phase, so digit
// sequences may still have multiple τ-band candidates — the LM then acts as a
// tiebreaker, which for digit-only charsets has minimal effect since the
// English bigram model assigns similar probabilities to all digit transitions.
const defaultRefTau = 0.10

// refConfig holds DecodeReference option state.
type refConfig struct {
	charset      string
	fontName     string         // empty → sweep all bundled fonts
	fontData     []byte         // non-nil → use this TTF/OTF exclusively
	fontBoldData []byte         // non-nil → used as bold face alongside fontData
	linear       int            // -1 = auto/sweep, 0 = sRGB only, 1 = linear only
	lmLang       *lang.Language // non-nil → use LM beam with this language
	lmLambda     float64        // emission temperature (0 → use defaultRefLambda)
	blockSize    int            // 0 → auto-detect via InferBlockGrid; >0 → use this value directly
	visibleText  string         // non-empty → stored for future font-calibration use
	refXScale    float64        // 0 and 1.0 = no stretch; >1 stretches references horizontally
}

func defaultRefConfig() refConfig {
	return refConfig{
		charset: DefaultRefCharset,
		linear:  -1, // auto: sweep both
	}
}

// RefOption configures DecodeReference.
type RefOption func(*refConfig)

// WithRefCharset sets the candidate alphabet for DecodeReference. Defaults to
// DefaultRefCharset (all printable ASCII). An empty value is ignored.
func WithRefCharset(cs string) RefOption {
	return func(c *refConfig) {
		if cs != "" {
			c.charset = cs
		}
	}
}

// WithRefFont pins the decoder to a specific bundled font by name (e.g.
// "Liberation Sans"). When set, only that font is tried, skipping the full
// bundled sweep. If the name does not match any bundled font,
// DecodeReference returns ErrNoContent.
func WithRefFont(name string) RefOption {
	return func(c *refConfig) { c.fontName = name }
}

// WithRefFontFile supplies raw TrueType/OpenType bytes for the regular face.
// When set, DecodeReference renders all candidates with this font exclusively —
// the bundled sweep is skipped entirely. This is the primary mitigation when
// the redaction font is known exactly.
//
// Pair with WithRefFontFileBold if the redaction includes bold text; otherwise
// the regular face is reused for bold rendering.
func WithRefFontFile(regularTTF []byte) RefOption {
	return func(c *refConfig) {
		if len(regularTTF) > 0 {
			c.fontData = regularTTF
		}
	}
}

// WithRefFontFileBold supplies raw TrueType/OpenType bytes for the bold face
// used alongside WithRefFontFile. It has no effect unless WithRefFontFile is
// also set. If omitted, the regular font is reused for bold rendering.
func WithRefFontFileBold(boldTTF []byte) RefOption {
	return func(c *refConfig) {
		if len(boldTTF) > 0 {
			c.fontBoldData = boldTTF
		}
	}
}

// WithRefLinear controls whether linear-light (GIMP/GEGL) or sRGB block
// averaging is used. tri-state: -1 = auto/sweep both (default), 0 = sRGB
// only, 1 = linear only.
func WithRefLinear(mode int) RefOption {
	return func(c *refConfig) { c.linear = mode }
}

// WithRefLanguage enables the LM-guided beam over the per-cell geometric
// distances produced by the reference-match decoder. When set, decodeRefOne
// calls beamMatchPhase instead of greedyMatchPhase for each starting phase.
//
// The beam keeps the top defaultRefBeamWidth hypotheses at each character
// position, scoring by: dist − λ·TransitionLogProb(prev, candidate). Lower
// score is better (dist is a mean block distance, log-prob is negative).
//
// Without this option, DecodeReference uses the greedy path and the result
// is byte-identical to versions without this option — additive and opt-in.
//
// Use LangEnglish or LangFrench as the argument, or lang.ParseLanguage to
// convert a user-supplied string.
func WithRefLanguage(l lang.Language) RefOption {
	return func(c *refConfig) { c.lmLang = new(l) }
}

// WithRefBlockSize pins the mosaic block size rather than auto-detecting it
// via InferBlockGrid. Use this when the caller already knows the block size
// (e.g. from image metadata or a perspective pipeline that computes it from
// the quad geometry). Values ≤ 1 are ignored (auto-detection is used instead).
func WithRefBlockSize(size int) RefOption {
	return func(c *refConfig) {
		if size > 1 {
			c.blockSize = size
		}
	}
}

// WithRefEmissionTemperature sets λ, the scale applied to per-cell geometric
// distance before combining with bigram log-prob in the LM beam
// (score = dist − λ·logP). Larger λ weights the image signal more strongly;
// smaller λ lets the language model dominate. Default is defaultRefLambda
// (10.0). Has no effect unless WithRefLanguage is also set.
func WithRefEmissionTemperature(lambda float64) RefOption {
	return func(c *refConfig) {
		if lambda > 0 {
			c.lmLambda = lambda
		}
	}
}

// WithRefVisibleText provides cleartext known to appear in or adjacent to the
// redacted region. When set, DecodeReference stores it for font-calibration
// use: in a future pass it will rank bundled fonts by matching a rendering of
// visibleText against the corresponding visible crop, so the best-fit font is
// tried first (and exclusively) for the redacted part. For now the text is
// recorded in the result notes. An empty value is a no-op.
func WithRefVisibleText(visibleText string) RefOption {
	return func(c *refConfig) {
		if visibleText != "" {
			c.visibleText = visibleText
		}
	}
}

// WithRefXScale sets the horizontal stretch factor applied when synthesising
// per-character block references. It mirrors the unpixel.Style.XScale contract:
// the zero value and exactly 1.0 both mean no stretch (byte-identical output to
// an unset option). A value of 1.06 stretches each synthesised reference 6%
// wider, matching redactions produced by anisotropic scaling (e.g. a GIMP layer
// scaled where x and y zoom differ). Without the matching stretch the wrong
// characters win because the synthesised references are narrower than the target
// block columns.
func WithRefXScale(x float64) RefOption {
	return func(c *refConfig) { c.refXScale = x }
}

// refCandidate holds one (font, linear) decode result for the sweep winner.
type refCandidate struct {
	text      string
	fontName  string
	linear    bool
	block     int
	phaseX    int
	charCount int
	dist      float64 // whole-image block distance (lower = better)
	perCell   float64 // mean per-cell distance (comparable across fonts)
	lmPerCell float64 // mean per-cell LM log-prob (used for combined selection when LM enabled)
}

// DecodeReference recovers text from a mosaic-pixelated image by geometrically
// matching each pixelated block column against a self-synthesised reference
// rendered from the same font. It does not use a language model, so it
// recovers arbitrary content (passwords, random strings, code) exactly when
// the rendering font matches the redaction font.
//
// Font selection: if WithRefFontFile is supplied, only that font is used. If
// WithRefFont names a bundled font, only that font is used. Otherwise all
// bundled fonts × {sRGB, linear} are swept and the result with the lowest
// whole-image block distance is returned.
//
// Returns ErrNoMosaic if no block grid is detected, ErrNoContent if no
// non-background content is found, or a context error on cancellation.
func DecodeReference(ctx context.Context, img image.Image, opts ...RefOption) (Result, error) {
	rcfg := defaultRefConfig()
	for _, o := range opts {
		o(&rcfg)
	}

	rgba := toRGBA(img)
	var block int
	if rcfg.blockSize > 1 {
		block = rcfg.blockSize
	} else {
		grid, ok := unpixel.InferBlockGrid(img)
		if !ok || grid.Size < 2 {
			return Result{}, ErrNoMosaic
		}
		block = grid.Size
	}

	rect := contentBounds(rgba)
	if rect.Empty() {
		return Result{}, ErrNoContent
	}

	// Build target crop (identical to Decode/DecodeHMM with pad=24).
	const pad = 24
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+pad, rect.Dy()+pad))
	imutil.FillWhite(target)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), rgba, rect.Min, xdraw.Src)

	// Determine which (renderer, fontName) pairs to sweep.
	type fontEntry struct {
		r    unpixel.Renderer
		name string
	}
	var entries []fontEntry

	switch {
	case len(rcfg.fontData) > 0:
		r, err := render.NewXImageFromFonts(rcfg.fontData, rcfg.fontBoldData)
		if err != nil {
			return Result{}, fmt.Errorf("build renderer from supplied font: %w", err)
		}
		entries = []fontEntry{{r: r, name: "(user font)"}}

	case rcfg.fontName != "":
		all := fonts.All()
		var found fonts.Font
		for _, f := range all {
			if f.Name == rcfg.fontName {
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

	// Determine which linear modes to sweep.
	var linearModes []bool
	switch rcfg.linear {
	case 0:
		linearModes = []bool{false}
	case 1:
		linearModes = []bool{true}
	default:
		linearModes = []bool{false, true}
	}

	cfg := defaultConfig()
	frameBytes := target.Bounds().Dx() * target.Bounds().Dy() * 4
	totalTasks := len(entries) * len(linearModes)
	workers, _ := cfg.plan(frameBytes, totalTasks)

	var (
		mu   sync.Mutex
		best refCandidate
	)
	best.perCell = math.Inf(1)
	best.dist = math.Inf(1)

	sem := make(chan struct{}, max(1, workers))
	var wg sync.WaitGroup

	for _, fe := range entries {
		for _, lin := range linearModes {
			fe, lin := fe, lin
			wg.Go(func() {
				if ctx.Err() != nil {
					return
				}
				sem <- struct{}{}
				defer func() { <-sem }()

				cand := decodeRefOne(ctx, fe.r, fe.name, lin,
					target, block, rcfg.charset, rcfg.lmLang, rcfg.lmLambda, rcfg.refXScale)
				if cand.text == "" {
					return
				}
				mu.Lock()
				if cand.perCell < best.perCell {
					best = cand
				}
				mu.Unlock()
			})
		}
	}
	wg.Wait()

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if best.text == "" {
		return Result{}, ErrNoContent
	}
	return Result{
		Text:       best.text,
		Font:       best.fontName,
		Distance:   best.dist,
		Linear:     best.linear,
		BlockSize:  block,
		CharCount:  best.charCount,
		GridPhaseX: best.phaseX,
	}, nil
}

// perCharPhaseRef holds block references for one character at one sub-block phase.
// Two variants are stored per character: firstRef (used when the character is
// first on the line, col=0) and midRef (used for all subsequent positions). They
// differ in that the "mid" variant skips block 0, which in a mid-line context is
// contaminated by the preceding character's trailing pixels.
type perCharPhaseRef struct {
	// cols is the number of block columns used for comparison.
	cols int
	// advance is the number of block columns the cursor moves after a match.
	// advance ≤ cols; the difference represents blocks shared with adjacent chars.
	advance int
	// rows is the full pixelated block grid (all ceil block columns, no trimming).
	rows [][]refmatch.BlockSig
}

// refTable holds per-character per-phase references for a single font size.
// Indexed [phase][charIndex], each entry carries the full block grid and the
// skip/advance metadata so greedyMatchPhase can compare correctly.
type refTable = [][]*perCharPhaseRef

// buildPerCharRefs renders each charset character alone at every sub-block phase
// in [0, block) and returns a table indexed [phase][charIndex]. Renders that
// produce no ink blocks are omitted (nil entry). The returned refs always have
// skip=0; greedyMatchPhase adjusts skip dynamically based on column position.
func buildPerCharRefs(
	r unpixel.Renderer,
	pix unpixel.Pixelator,
	charRunes []rune,
	advances map[rune]int,
	fs float64,
	block int,
	inkRows int,
	xScale float64,
) refTable {
	table := make(refTable, block)
	for p := range block {
		table[p] = make([]*perCharPhaseRef, len(charRunes))
	}

	for i, ch := range charRunes {
		adv := advances[ch]
		if adv <= 0 {
			continue
		}
		for p := range block {
			// Render the character alone with PaddingLeft=p, PaddingTop=0.
			// Block 0 covers pixels [0,block); pixels [0,p) are white padding
			// and pixels [p, p+adv) contain the glyph's ink.
			img, sentinelX, err := r.Render(string(ch), unpixel.Style{
				FontSize:    fs,
				PaddingTop:  0,
				PaddingLeft: p,
				XScale:      xScale,
			})
			if err != nil {
				continue
			}
			// Build a canvas covering ceil(pixEnd/block) blocks. The renderer
			// paints a blue sentinel from sentinelX=p+adv onward; blank those
			// pixels with white so they do not bias the last partial block's average.
			pixEnd := p + adv
			colEndCeil := (pixEnd + block - 1) / block
			w := min(colEndCeil*block, img.Bounds().Dx())
			if w < 1 {
				continue
			}
			h := img.Bounds().Dy()
			canvas := image.NewRGBA(image.Rect(0, 0, w, h))
			imutil.FillWhite(canvas)
			xdraw.Draw(canvas, canvas.Bounds(), img, image.Point{}, xdraw.Src)
			for y := range h {
				for x := sentinelX; x < w; x++ {
					canvas.SetRGBA(x, y, whitePixel)
				}
			}

			pixelated := pix.Pixelate(canvas, 0, 0)
			pb := pixelated.Bounds()
			allBlocks := refmatch.ExtractBlocks(pixelated.Pix, pixelated.Stride,
				pb.Dx(), pb.Dy(), block)
			if len(allBlocks) == 0 {
				continue
			}

			// cols = ceil: include all block columns up to the last partial one.
			// advance = floor: cursor step excludes the last partial block so it
			// can be re-evaluated for the next character.
			// When p>0 (mid-line use), block 0 is contaminated by the preceding
			// character's tail; greedyMatchPhase skips it by comparing from
			// block 1 (refStart=1) instead.
			advance := max(1, pixEnd/block)
			nRefCols := len(allBlocks[0])
			numCols := min(colEndCeil, nRefCols)
			if numCols < 1 {
				continue
			}

			compareRows := min(inkRows, len(allBlocks))
			glyphRows := make([][]refmatch.BlockSig, compareRows)
			for ri := range compareRows {
				row := allBlocks[ri]
				end := min(numCols, len(row))
				if end >= 1 {
					glyphRows[ri] = row[:end]
				}
			}

			table[p][i] = &perCharPhaseRef{
				cols:    numCols,
				advance: advance,
				rows:    glyphRows,
			}
		}
	}
	return table
}

// decodeRefOne runs the reference-matching decoder for a single (renderer,
// linear) pair. It calibrates the font size from the target image height, then
// builds a per-character-per-phase reference table and sweeps all 8 starting
// phases. For each starting phase it runs a greedy (or LM-guided beam when
// lmLang is non-nil) left-to-right block match where the sub-block phase of
// each decoded position is tracked precisely as the accumulated pixel offset
// modulo the block size.
//
// The starting phase with the lowest mean per-cell block distance is returned.
func decodeRefOne(
	ctx context.Context,
	r unpixel.Renderer,
	fontName string,
	linear bool,
	target *image.RGBA,
	block int,
	charset string,
	lmLang *lang.Language,
	lmLambda float64,
	xScale float64,
) refCandidate {
	pix := pixelatorFor(block, linear)

	// Re-pixelate the target and strip white rows/cols to get the ink block grid.
	// The target is already a pixelated mosaic, so re-pixelating is idempotent.
	tgtPix := pix.Pixelate(target, 0, 0)
	tpb := tgtPix.Bounds()
	allTgtBlocks := refmatch.ExtractBlocks(tgtPix.Pix, tgtPix.Stride,
		tpb.Dx(), tpb.Dy(), block)
	if len(allTgtBlocks) == 0 {
		return refCandidate{}
	}
	tgtGrid := stripWhiteBlockRows(allTgtBlocks)
	tgtGrid = stripWhiteBlockCols(tgtGrid)
	if len(tgtGrid) == 0 || len(tgtGrid[0]) == 0 {
		return refCandidate{}
	}
	inkRows := len(tgtGrid)
	tgtCols := len(tgtGrid[0])

	charRunes := []rune(charset)

	// Find all candidate font sizes whose rendered ink occupies exactly inkRows
	// block rows. The mosaic rounds ink height to block boundaries, so multiple
	// font sizes may be consistent with the observed inkRows; we try all of them
	// and keep the (fs, p0) pair with the lowest mean per-cell block distance.
	fsCandidates := calibrateRefFS(r, inkRows, block)
	if len(fsCandidates) == 0 {
		return refCandidate{}
	}

	var bestCand refCandidate
	bestCand.perCell = math.Inf(1)

	for _, fs := range fsCandidates {
		if ctx.Err() != nil {
			break
		}

		// Measure per-glyph pixel advances via cumulative prefix renders.
		advances := measureAdvancesByCumulative(r, charRunes, fs, xScale)
		if len(advances) == 0 {
			continue
		}

		// Build the per-char-per-phase reference table.
		// table[p][i] is the block reference for charRunes[i] at sub-block phase p.
		table := buildPerCharRefs(r, pix, charRunes, advances, fs, block, inkRows, xScale)

		// Build LM model once if enabled (nil lmLang → greedy path).
		var lmModel *lang.Model
		if lmLang != nil {
			lmModel = lang.Default()
			if *lmLang == lang.French {
				// French corpus uses the same Default() bigram — it is an
				// English-biased model. For French we still use Default() since
				// the internal lang package does not export a French *Model
				// (it uses Infini for French). Callers that need true French
				// bigrams should pass lang.English until a French Model is added.
				lmModel = lang.Default()
			}
		}
		lambda := lmLambda
		if lambda == 0 {
			lambda = defaultRefLambda
		}

		// Sweep all 8 starting sub-block phases.
		for p0 := range block {
			if ctx.Err() != nil {
				break
			}
			var text string
			var perCell, lmPerCell float64
			if lmModel != nil {
				text, perCell, lmPerCell = beamMatchPhase(ctx, tgtGrid, tgtCols, charRunes, advances, table, block, p0, lmModel, lambda)
			} else {
				text, perCell = greedyMatchPhase(ctx, tgtGrid, tgtCols, charRunes, advances, table, block, p0)
			}
			if text == "" {
				continue
			}
			// When LM is enabled use the combined score for phase/fs winner
			// selection so the LM benefit is propagated through to the result;
			// pure-greedy path keeps the geometry-only comparison.
			var score float64
			if lmModel != nil {
				score = perCell - lambda*lmPerCell
			} else {
				score = perCell
			}
			if score < bestCand.perCell {
				bestCand = refCandidate{
					text:      text,
					fontName:  fontName,
					linear:    linear,
					block:     block,
					phaseX:    p0,
					charCount: len([]rune(text)),
					dist:      perCell,
					perCell:   score,
					lmPerCell: lmPerCell,
				}
			}
		}
	}
	return bestCand
}

// calibrateRefFS returns all integer font sizes in a reasonable range whose
// rendered ink height (measured from a representative probe string) falls within
// the block-aligned band that would produce exactly inkRows block rows after
// pixelation. Because small font sizes round non-linearly, we probe each integer
// size in [minFS, maxFS] and collect every size where inkBlockRows == inkRows.
//
// The probe string "Hhpq|" covers ascenders, x-height, and descenders so that
// the measured ink height is stable across font families.
func calibrateRefFS(r unpixel.Renderer, inkRows, block int) []float64 {
	// Bounds: a single text row is rarely below 8pt or above 200pt for common
	// redaction tools. Probe at each integer to find the matching sizes.
	const (
		minFS = 8
		maxFS = 120
	)
	var out []float64
	for fs := minFS; fs <= maxFS; fs++ {
		// Probe with ascenders and cap-height but no descenders so the measured
		// ink height matches text that lacks descenders (passwords, hex strings).
		// Using the same string as the existing calibrate() probe for consistency.
		img, sx, err := r.Render("Hhelo Wrd", unpixel.Style{FontSize: float64(fs)})
		if err != nil || sx <= 0 {
			continue
		}
		ib := inkBounds(img, sx)
		if ib.Empty() {
			continue
		}
		// inkBounds gives the tight pixel rectangle of the rendered ink.
		// How many block rows would that span after pixelation (block-aligned)?
		// A block row covers block pixels; any ink in that band makes the row non-white.
		// Count: ceil((ib.Max.Y) / block) - floor(ib.Min.Y / block)
		firstBlock := ib.Min.Y / block
		lastBlock := (ib.Max.Y - 1) / block
		rows := lastBlock - firstBlock + 1
		if rows == inkRows {
			out = append(out, float64(fs))
		}
	}
	return out
}

// greedyMatchPhase runs one greedy left-to-right pass over tgtGrid starting
// with sub-block pixel offset p0. It tracks the accumulated pixel offset so
// the sub-block phase (pixOff mod block) is correct for each character
// position, then looks up the per-char reference for that exact phase.
func greedyMatchPhase(
	ctx context.Context,
	tgtGrid [][]refmatch.BlockSig,
	tgtCols int,
	charRunes []rune,
	advances map[rune]int,
	table [][]*perCharPhaseRef,
	block, p0 int,
) (text string, perCell float64) {
	runes := make([]rune, 0, tgtCols)
	var distSum float64
	pixOff := p0 // accumulated pixel offset within the rendered line

	for col := 0; col < tgtCols; {
		if ctx.Err() != nil {
			break
		}

		phase := pixOff % block
		phaseRefs := table[phase]

		// At mid-line (col>0, phase>0) block 0 of each per-char ref contains
		// white PaddingLeft pixels that do not exist in the target — the
		// preceding character's tail occupies that region instead. To avoid
		// contamination we skip block 0 (skip=1) and compare blocks 1..cols-1
		// which are entirely within the current glyph. At the line start
		// (col==0) or when the glyph starts at a block boundary (phase==0)
		// block 0 is clean, so skip=0 and we compare blocks 0..advance-1
		// (floor, not ceil) to avoid contamination from the next character.
		firstChar := col == 0 || phase == 0

		bestRune := rune(0)
		bestDist := math.Inf(1)
		bestAdvance := 1

		for i, ch := range charRunes {
			ref := phaseRefs[i]
			if ref == nil {
				continue
			}
			var compareCols, refStart, tgtStart int
			if firstChar {
				// Use only the floor (advance) blocks — all owned by this glyph.
				compareCols = min(ref.advance, ref.cols)
				refStart = 0
				tgtStart = col
			} else {
				// Skip block 0 (contaminated by preceding char's tail). Prefer
				// advance-1 (floor minus leading block) to stay within the glyph's
				// owned pixels. Narrow glyphs where advance=1 give 0 blocks; fall
				// back to cols-1 (the partial trailing block is safe because sentinel
				// pixels were blanked in buildPerCharRefs).
				compareCols = ref.advance - 1
				if compareCols < 1 {
					compareCols = ref.cols - 1
				}
				refStart = 1
				tgtStart = col + 1
			}
			if compareCols < 1 {
				continue
			}
			if tgtStart+compareCols > tgtCols {
				continue
			}
			d := glyphDistPhaseSkip(tgtGrid, tgtStart, ref, refStart, compareCols)
			if d < bestDist {
				bestDist = d
				bestRune = ch
				bestAdvance = ref.advance
			}
		}
		if bestRune == 0 {
			break
		}
		runes = append(runes, bestRune)
		distSum += bestDist
		pixOff += advances[bestRune]
		col += max(1, bestAdvance)
	}

	if len(runes) == 0 {
		return "", math.Inf(1)
	}
	return string(runes), distSum / float64(len(runes))
}

// beamMatchPhase runs a left-to-right beam search over tgtGrid starting with
// sub-block pixel offset p0. It mirrors greedyMatchPhase but keeps the top
// defaultRefBeamWidth hypotheses at each character position instead of
// committing to the single nearest glyph.
//
// Scoring per candidate extension at each hypothesis's current cell:
//
//	cellCost = (d − d_min) + λ·(logP_max − logP(prev→cand))
//
// Both terms are local-relative: d_min is the geometric minimum across all
// valid candidates for that hypothesis, and logP_max is the highest bigram
// log-prob available from the hypothesis's last rune. Neither term can
// accumulate a cross-step advantage from absolute-value differences — the
// LM only penalises transitions that are less probable than the best
// available one, and the geometric term only penalises candidates that are
// further than the nearest match.
//
// Each hypothesis tracks its own column cursor (proportional advances differ
// per glyph). Hypotheses that exhaust tgtCols are "complete"; the one with
// the lowest per-cell raw-dist average wins (perCell matches greedyMatchPhase).
//
// Algorithm per step:
//  1. For each live hypothesis, collect all valid candidates and find d_min.
//  2. Admit only candidates within [d_min, d_min+τ] (the geometric tie band).
//     The geometric winner is always admitted; unambiguous glyphs (gap > τ)
//     have only one admissible candidate, so the LM cannot influence them.
//  3. Prune the expanded set to defaultRefBeamWidth by:
//     beamScore = distSum/cells − λ·lmSum/cells  (lower = better).
//  4. Winner is the complete hypothesis with the combined score above.
//  5. Returns (text, perCell, lmPerCell): perCell = distSum/cells (geometry),
//     lmPerCell = lmSum/cells (for combined phase selection in decodeRefOne).
func beamMatchPhase(
	ctx context.Context,
	tgtGrid [][]refmatch.BlockSig,
	tgtCols int,
	charRunes []rune,
	advances map[rune]int,
	table [][]*perCharPhaseRef,
	block, p0 int,
	model *lang.Model,
	lambda float64, // LM weight in beam score (0 → geometry-only, same as greedy)
) (text string, perCell, lmPerCell float64) {
	// beamHyp is one live hypothesis in the beam.
	type beamHyp struct {
		runes   []rune
		col     int     // current block-column cursor in tgtGrid
		pixOff  int     // accumulated pixel offset (for phase tracking)
		prev    rune    // last chosen rune (LM context)
		distSum float64 // Σ per-cell geometric distance (lower = better)
		lmSum   float64 // Σ bigram log-prob (higher = better; used for pruning only)
		cells   int     // number of cells chosen so far
	}

	// cellCandidate holds a single extension option for one hypothesis.
	type cellCandidate struct {
		charIdx int
		ch      rune
		d       float64 // raw per-cell geometric distance
		logP    float64 // TransitionLogProb(prev→ch)
	}

	beam := []beamHyp{{
		col:    0,
		pixOff: p0,
		prev:   ' ', // sentence-start context
	}}

	var complete []beamHyp

	for len(beam) > 0 {
		if ctx.Err() != nil {
			break
		}

		next := make([]beamHyp, 0, len(beam)*4)

		for _, h := range beam {
			phase := h.pixOff % block
			phaseRefs := table[phase]
			firstChar := h.col == 0 || phase == 0

			// Collect valid candidates; track geometric minimum.
			var cands []cellCandidate
			dMin := math.Inf(1)

			for i, ch := range charRunes {
				ref := phaseRefs[i]
				if ref == nil {
					continue
				}
				var compareCols, refStart, tgtStart int
				if firstChar {
					compareCols = min(ref.advance, ref.cols)
					refStart = 0
					tgtStart = h.col
				} else {
					compareCols = ref.advance - 1
					if compareCols < 1 {
						compareCols = ref.cols - 1
					}
					refStart = 1
					tgtStart = h.col + 1
				}
				if compareCols < 1 || tgtStart+compareCols > tgtCols {
					continue
				}
				d := glyphDistPhaseSkip(tgtGrid, tgtStart, ref, refStart, compareCols)
				if math.IsInf(d, 1) {
					continue
				}
				cands = append(cands, cellCandidate{
					charIdx: i,
					ch:      ch,
					d:       d,
					logP:    model.TransitionLogProb(h.prev, ch),
				})
				if d < dMin {
					dMin = d
				}
			}
			if len(cands) == 0 {
				continue
			}

			// Admit only candidates in the geometric tie band [d_min, d_min+τ].
			// This is the hard gate: unambiguous glyphs have only one admissible
			// candidate (the geometric winner), so the LM cannot change the choice.
			for _, cc := range cands {
				if cc.d > dMin+defaultRefTau {
					continue
				}
				ref := phaseRefs[cc.charIdx]
				newCol := h.col + max(1, ref.advance)
				newPixOff := h.pixOff + advances[cc.ch]
				newRunes := make([]rune, len(h.runes)+1)
				copy(newRunes, h.runes)
				newRunes[len(h.runes)] = cc.ch

				hyp := beamHyp{
					runes:   newRunes,
					col:     newCol,
					pixOff:  newPixOff,
					prev:    cc.ch,
					distSum: h.distSum + cc.d,
					lmSum:   h.lmSum + cc.logP,
					cells:   h.cells + 1,
				}
				if newCol >= tgtCols {
					complete = append(complete, hyp)
				} else {
					next = append(next, hyp)
				}
			}
		}

		// Prune to beam width by combined score: distSum/cells − λ·lmSum/cells.
		// lmSum ≤ 0, so −λ·lmSum ≥ 0; higher-probability paths get lower score.
		// λ is small enough that the LM term (max ≈ 7λ ≈ 0.035) cannot overcome
		// a geometric difference outside the τ tie band — geometry always dominates.
		if len(next) > defaultRefBeamWidth {
			slices.SortFunc(next, func(a, b beamHyp) int {
				ac := float64(max(1, a.cells))
				bc := float64(max(1, b.cells))
				as := a.distSum/ac - lambda*(a.lmSum/ac)
				bs := b.distSum/bc - lambda*(b.lmSum/bc)
				if as < bs {
					return -1
				}
				if as > bs {
					return 1
				}
				return 0
			})
			next = next[:defaultRefBeamWidth]
		}
		beam = next
	}

	if len(complete) == 0 {
		return "", math.Inf(1), math.Inf(1)
	}

	// Pick the winner using a combined score: distSum/cells − λ·lmSum/cells.
	// lmSum ≤ 0, so −λ·lmSum ≥ 0; higher log-prob reduces the score.
	// Returns the winner's perCell (geometry) and lmPerCell so decodeRefOne
	// can select the best phase using the same combined criterion.
	slices.SortFunc(complete, func(a, b beamHyp) int {
		if a.cells == 0 {
			return 1
		}
		if b.cells == 0 {
			return -1
		}
		ac, bc := float64(a.cells), float64(b.cells)
		as := a.distSum/ac - lambda*(a.lmSum/ac)
		bs := b.distSum/bc - lambda*(b.lmSum/bc)
		if as < bs {
			return -1
		}
		if as > bs {
			return 1
		}
		return 0
	})
	best := complete[0]
	if best.cells == 0 {
		return "", math.Inf(1), math.Inf(1)
	}
	c := float64(best.cells)
	return string(best.runes), best.distSum / c, best.lmSum / c
}

// glyphDistPhaseSkip computes the mean block-signature distance between a
// perCharPhaseRef and the target grid, comparing ref blocks [refSkip, refSkip+compareCols)
// against target columns starting at targetCol. Returns +Inf when the window
// extends beyond the target width or compareCols < 1.
func glyphDistPhaseSkip(target [][]refmatch.BlockSig, targetCol int, ref *perCharPhaseRef, refSkip, compareCols int) float64 {
	if len(target) == 0 || len(ref.rows) == 0 {
		return math.Inf(1)
	}
	if compareCols < 1 {
		return math.Inf(1)
	}
	bCols := len(target[0])
	if targetCol+compareCols > bCols {
		return math.Inf(1)
	}
	var sum float64
	n := 0
	compareRows := min(len(target), len(ref.rows))
	for r := range compareRows {
		refRow := ref.rows[r]
		tgtRow := target[r]
		for c := range compareCols {
			rc := refSkip + c
			if rc >= len(refRow) {
				break
			}
			sum += refRow[rc].Dist(tgtRow[targetCol+c])
			n++
		}
	}
	if n == 0 {
		return math.Inf(1)
	}
	return sum / float64(n)
}

// measureAdvancesByCumulative measures per-glyph pixel advances by rendering
// cumulative prefix strings and differencing the sentinel x positions. This
// gives the exact advance the renderer uses, accounting for hinting and
// rounding that the font.MeasureString API may not reproduce.
//
// Duplicate runes in charRunes are deduplicated before measurement so that
// the cumulative prefix technique stays consistent: each rune appears exactly
// once in the prefix sequence, giving a correct advance for every entry.
func measureAdvancesByCumulative(r unpixel.Renderer, charRunes []rune, fs float64, xScale float64) map[rune]int {
	// Dedup while preserving first-occurrence order.
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
		// Render the prefix up to and including unique[i].
		prefix := string(unique[:i+1])
		_, sx, err := r.Render(prefix, unpixel.Style{
			FontSize:    fs,
			PaddingTop:  0,
			PaddingLeft: 0,
			XScale:      xScale,
		})
		if err != nil || sx <= 0 {
			return nil // fatal: can't measure
		}
		m[ch] = sx - prevX
		prevX = sx
	}
	return m
}

// blockBgThresh is the per-channel threshold above which a block is
// considered background for the purpose of row/column stripping.
// It matches contentLumThreshold (244) so that block-level stripping
// and pixel-level contentBounds agree on which regions are background.
const blockBgThresh = float64(contentLumThreshold) // 244

// whitePixel is a fully-opaque white RGBA pixel used to blank sentinel regions.
var whitePixel = color.RGBA{R: 255, G: 255, B: 255, A: 255}

// isWhiteBlockSig reports whether a single block signature is background.
func isWhiteBlockSig(s refmatch.BlockSig) bool {
	return s.R >= blockBgThresh && s.G >= blockBgThresh && s.B >= blockBgThresh
}

// isWhiteRow2 reports whether every block in the row is background.
func isWhiteRow2(row []refmatch.BlockSig) bool {
	for _, s := range row {
		if !isWhiteBlockSig(s) {
			return false
		}
	}
	return true
}

// isWhiteCol reports whether column c of every row is background.
func isWhiteCol(blocks [][]refmatch.BlockSig, c int) bool {
	for _, row := range blocks {
		if c < len(row) && !isWhiteBlockSig(row[c]) {
			return false
		}
	}
	return true
}

// stripWhiteBlockRows removes all-white block rows from the top and bottom of
// a block grid.
func stripWhiteBlockRows(blocks [][]refmatch.BlockSig) [][]refmatch.BlockSig {
	start, end := 0, len(blocks)
	for start < end && isWhiteRow2(blocks[start]) {
		start++
	}
	for end > start && isWhiteRow2(blocks[end-1]) {
		end--
	}
	return blocks[start:end]
}

// stripWhiteBlockCols removes all-white block columns from the left and right
// of a block grid.
func stripWhiteBlockCols(blocks [][]refmatch.BlockSig) [][]refmatch.BlockSig {
	if len(blocks) == 0 || len(blocks[0]) == 0 {
		return blocks
	}
	nCols := len(blocks[0])
	start, end := 0, nCols
	for start < end && isWhiteCol(blocks, start) {
		start++
	}
	for end > start && isWhiteCol(blocks, end-1) {
		end--
	}
	if start == 0 && end == nCols {
		return blocks
	}
	result := make([][]refmatch.BlockSig, len(blocks))
	for i, row := range blocks {
		if end <= len(row) {
			result[i] = row[start:end]
		} else if start < len(row) {
			result[i] = row[start:]
		}
	}
	return result
}
