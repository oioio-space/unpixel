package mosaictext

// did.go — Document Image Decoding (DID) / trellis decoder for proportional
// mosaic-pixelated text.
//
// DecodeDID implements the Kopec–Chou (1994) DID method adapted for block-mosaic
// redactions:
//
//  1. Auto-detect the block grid (InferBlockGrid) and content bounds.
//  2. For each (font, linear-averaging?) pair, calibrate font size.
//  3. Measure per-glyph pixel advances via cumulative prefix renders (exact
//     hinted widths, not theoretical font metrics).
//  4. Run the column trellis DP (internal/did.trellisDP):
//     - A node is a pixel-column position c ∈ [0, W].
//     - An edge places glyph g at column c → c+advance(g).
//     - Edge cost = emissionCost(g, c) + λ·(−logP_LM(g|prev)).
//     - emissionCost: render g alone → pixelate at the same block grid and phase →
//       compare the sub-column slice against the target band by MSE.
//     - Best path = Viterbi (min-cost) from col 0 to col W.
//  5. ICP loop: run DP over the cached emission table; after convergence emit
//     the recovered string.
//
// The emissionCache memoises each (glyph, startCol) pair so each pixel-level
// render+pixelate+MSE is paid at most once across all DP iterations.
//
// This decoder does NOT require known character boundaries (proportional-font-
// capable) and does NOT assume a fixed monospace pitch. It is the architectural
// fix for the single-band, no-boundary-anchor problem that DecodeHMM (which
// commits to a fixed character count N and a fixed cell width) cannot handle.
//
// References:
//   - Kopec, G. E., & Chou, P. A. (1994). Document Image Decoding Using Markov
//     Source Models. IEEE Transactions on Pattern Analysis and Machine
//     Intelligence, 16(6), 602-617.
//   - Hill, A. C., et al. (2016). Reverse Engineering of Redacted Information in
//     Mosaiced Document Images. PETS 2016.

import (
	"context"
	"fmt"
	"image"
	"math"
	"slices"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/did"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/render"
)

// DefaultDIDCharset is the default candidate alphabet for DecodeDID: lowercase
// letters, space, and common punctuation — the same width as DefaultHMMCharset.
// A space glyph is essential so word gaps are modelled in the trellis.
const DefaultDIDCharset = "abcdefghijklmnopqrstuvwxyz .,!?'-:;"

// defaultDIDLambda is the default LM weight λ. The edge cost in the trellis is:
//
//	emissionCost(g, c) + λ · (−logP_LM(g | prev))
//
// A bigram log-prob range of ~7 nats × 0.04 ≈ 0.28. At block 8 the per-column
// MSE gap between correct and wrong glyphs is typically ≥ 0.5, so the LM nudges
// near-ties without overriding clear image signal.
const defaultDIDLambda = 0.04

// DIDResult is a decoded result from DecodeDID.
type DIDResult struct {
	// Text is the most plausible recovered string.
	Text string
	// Font is the bundled font name whose calibration produced this result.
	Font string
	// Distance is the mean-per-column MSE of the winning path (lower is better).
	Distance float64
	// Linear reports whether linear-light block averaging matched (GEGL/GIMP).
	Linear bool
	// BlockSize is the detected (or supplied) mosaic block size in pixels.
	BlockSize int
	// GridPhaseX is the best horizontal grid phase discovered by the decoder.
	GridPhaseX int
	// EmissionEvals is the total number of distinct (glyph, startCol) emission
	// computations paid — the tractability metric for ICP reporting.
	EmissionEvals int
}

// DIDOption configures DecodeDID.
type DIDOption func(*didConfig)

type didConfig struct {
	charset         string
	fontName        string  // empty → sweep all bundled fonts
	fontData        []byte  // non-nil → use this TTF/OTF exclusively
	fontBold        []byte  // non-nil → bold face alongside fontData
	linear          int     // -1 = auto/sweep, 0 = sRGB only, 1 = linear only
	lambda          float64 // LM weight (0 → defaultDIDLambda)
	language        lang.Language
	fontSize        float64 // 0 → auto-calibrate from image
	blockSize       int     // 0 → auto from InferBlockGrid
	contextEmission bool    // true → render left neighbor alongside glyph
}

func defaultDIDConfig() didConfig {
	return didConfig{
		charset:  DefaultDIDCharset,
		linear:   -1,
		language: lang.English,
	}
}

// WithDIDCharset sets the candidate alphabet for DecodeDID. Defaults to
// DefaultDIDCharset. An empty value is ignored.
func WithDIDCharset(cs string) DIDOption {
	return func(c *didConfig) {
		if cs != "" {
			c.charset = cs
		}
	}
}

// WithDIDFont pins DecodeDID to a specific bundled font by name (e.g.
// "Liberation Mono"). When set, only that font is tried. If the name does not
// match any bundled font, DecodeDID returns ErrNoContent.
func WithDIDFont(name string) DIDOption {
	return func(c *didConfig) { c.fontName = name }
}

// WithDIDFontFile supplies raw TrueType/OpenType bytes to use exclusively.
// The bundled sweep is skipped.
func WithDIDFontFile(regularTTF []byte) DIDOption {
	return func(c *didConfig) {
		if len(regularTTF) > 0 {
			c.fontData = regularTTF
		}
	}
}

// WithDIDFontFileBold supplies bold font bytes alongside WithDIDFontFile.
func WithDIDFontFileBold(boldTTF []byte) DIDOption {
	return func(c *didConfig) {
		if len(boldTTF) > 0 {
			c.fontBold = boldTTF
		}
	}
}

// WithDIDLinear controls block-averaging colour space. -1 = auto/sweep (default),
// 0 = sRGB only, 1 = linear-light only.
func WithDIDLinear(mode int) DIDOption {
	return func(c *didConfig) { c.linear = mode }
}

// WithDIDLambda sets the LM weight λ in the trellis edge cost. Default is
// defaultDIDLambda (0.04). Larger λ weights the LM more strongly.
func WithDIDLambda(lambda float64) DIDOption {
	return func(c *didConfig) {
		if lambda >= 0 {
			c.lambda = lambda
		}
	}
}

// WithDIDLanguage sets the language for the bigram LM. Defaults to English.
func WithDIDLanguage(l lang.Language) DIDOption {
	return func(c *didConfig) { c.language = l }
}

// WithDIDFontSize pins the font size (pt). 0 (default) auto-calibrates from the
// image's ink height.
func WithDIDFontSize(size float64) DIDOption {
	return func(c *didConfig) {
		if size > 0 {
			c.fontSize = size
		}
	}
}

// WithDIDBlockSize pins the mosaic block size (px). 0 (default) uses
// InferBlockGrid.
func WithDIDBlockSize(size int) DIDOption {
	return func(c *didConfig) {
		if size > 0 {
			c.blockSize = size
		}
	}
}

// WithDIDContext enables context-aware emission (opt-in, default false).
// When true, each glyph is rendered alongside its left neighbour so that
// boundary blocks — which in the real mosaic average pixels from two adjacent
// glyphs — are pixelated the same way as in the target. This reduces the
// emission bias at glyph boundaries and can improve recovery on
// boundary-heavy or JPEG-compressed mosaics. The isolated-glyph path (default)
// is unchanged and all clean-monospace fixtures remain valid with either mode.
func WithDIDContext(enable bool) DIDOption {
	return func(c *didConfig) { c.contextEmission = enable }
}

// DecodeDID recovers text from a mosaic-pixelated image using Document Image
// Decoding (Kopec-Chou 1994). It models the line as a left-to-right path
// through a column trellis where each edge places one glyph and its cost is
// the per-glyph emission MSE plus an LM transition cost. The Viterbi shortest
// path gives the recovered string without needing to know character boundaries
// in advance — making it effective for proportional fonts.
//
// DecodeDID sweeps all bundled fonts and both colour spaces (linear/sRGB)
// unless WithDIDFont/WithDIDFontFile or WithDIDLinear restrict the sweep.
// It returns ErrNoMosaic when no block grid is detected, or ErrNoContent when
// the decoder cannot score any candidate.
func DecodeDID(ctx context.Context, img image.Image, opts ...DIDOption) (DIDResult, error) {
	cfg := defaultDIDConfig()
	for _, o := range opts {
		o(&cfg)
	}

	rgba := toRGBA(img)

	// Discover block grid.
	block := cfg.blockSize
	if block <= 0 {
		grid, ok := unpixel.InferBlockGrid(img)
		if !ok || grid.Size < 2 {
			return DIDResult{}, ErrNoMosaic
		}
		block = grid.Size
	}

	// Use the full image as the target band: do NOT apply a content crop.
	//
	// Rationale: glyph canvases are rendered with PaddingLeft=0, PaddingTop=0
	// so their advance cells start at x=0 and their baseline at y=0. The input
	// image must start at the same origin for block averages to align. A pixel-
	// level contentBounds crop shifts the origin by the bearing of the first glyph
	// (typically 1–7 px) which misaligns blocks and inflates emission costs for
	// the correct glyph. Real mosaics are typically delivered as tight crops
	// (the redacted region only), so no extra crop is needed. The ErrNoMosaic /
	// ErrNoContent path (white image) is still guarded by InferBlockGrid and the
	// empty-path check in decodeOneDID.
	target := rgba

	lm := lang.Default()

	lambda := cfg.lambda
	if lambda == 0 {
		lambda = defaultDIDLambda
	}

	// Build the set of (renderer, fontName, linear) combos to try.
	type combo struct {
		r        unpixel.Renderer
		fontName string
		linear   bool
	}
	var combos []combo

	switch {
	case len(cfg.fontData) > 0:
		// User-supplied font file.
		r, err := render.NewXImageFromFonts(cfg.fontData, cfg.fontBold)
		if err != nil {
			return DIDResult{}, fmt.Errorf("did: parse supplied font: %w", err)
		}
		linModes := linearModes(cfg.linear)
		for _, lin := range linModes {
			combos = append(combos, combo{r: r, fontName: "supplied", linear: lin})
		}

	case cfg.fontName != "":
		// Named bundled font.
		all := fonts.All()
		var matched *fonts.Font
		for i := range all {
			if all[i].Name == cfg.fontName {
				matched = &all[i]
				break
			}
		}
		if matched == nil {
			return DIDResult{}, fmt.Errorf("did: unknown bundled font %q: %w", cfg.fontName, ErrNoContent)
		}
		r, err := render.NewXImageFromFonts(matched.Data, nil)
		if err != nil {
			return DIDResult{}, fmt.Errorf("did: parse font %q: %w", cfg.fontName, err)
		}
		linModes := linearModes(cfg.linear)
		for _, lin := range linModes {
			combos = append(combos, combo{r: r, fontName: matched.Name, linear: lin})
		}

	default:
		// Sweep all bundled fonts.
		all := fonts.All()
		linModes := linearModes(cfg.linear)
		for _, f := range all {
			r, err := render.NewXImageFromFonts(f.Data, nil)
			if err != nil {
				continue
			}
			for _, lin := range linModes {
				combos = append(combos, combo{r: r, fontName: f.Name, linear: lin})
			}
		}
	}

	if len(combos) == 0 {
		return DIDResult{}, ErrNoContent
	}

	// Run each combo concurrently and collect candidates.
	type candidate struct {
		text          string
		fontName      string
		linear        bool
		dist          float64
		phaseX        int
		emissionEvals int
	}
	results := make([]candidate, len(combos))
	var wg sync.WaitGroup
	for i, c := range combos {
		wg.Go(func() {
			if ctx.Err() != nil {
				return
			}
			text, dist, phaseX, evals := decodeOneDID(ctx, c.r, target, block, cfg.charset, cfg.fontSize, lm, lambda, c.linear, cfg.contextEmission)
			results[i] = candidate{
				text:          text,
				fontName:      c.fontName,
				linear:        c.linear,
				dist:          dist,
				phaseX:        phaseX,
				emissionEvals: evals,
			}
		})
	}
	wg.Wait()

	if ctx.Err() != nil {
		return DIDResult{}, ctx.Err()
	}

	// Pick the winner by minimum distance.
	best := candidate{dist: math.Inf(1)}
	for _, r := range results {
		if r.text != "" && r.dist < best.dist {
			best = r
		}
	}
	if best.text == "" {
		return DIDResult{}, ErrNoContent
	}

	return DIDResult{
		Text:          best.text,
		Font:          best.fontName,
		Distance:      best.dist,
		Linear:        best.linear,
		BlockSize:     block,
		GridPhaseX:    best.phaseX,
		EmissionEvals: best.emissionEvals,
	}, nil
}

// linearModes returns the list of linear booleans implied by mode:
// -1 → both, 0 → sRGB only, 1 → linear only.
func linearModes(mode int) []bool {
	switch mode {
	case 1:
		return []bool{true}
	case 0:
		return []bool{false}
	default:
		return []bool{false, true}
	}
}

// decodeOneDID runs the DID trellis decoder for a single (renderer, linear)
// pair. It calibrates the font size, measures per-glyph advances, sweeps all
// horizontal grid phases [0, block), and returns the best string, its mean MSE,
// the winning phase, and the total emission evaluation count.
//
// When contextEmission is true, TrellisDPContextual is used so the emission
// function receives the left-neighbour rune and renders it alongside the
// current glyph, faithfully reproducing boundary-block averaging.
func decodeOneDID(
	ctx context.Context,
	r unpixel.Renderer,
	target *image.RGBA,
	block int,
	charset string,
	fontSizeHint float64,
	lm *lang.Model,
	lambda float64,
	linear bool,
	contextEmission bool,
) (text string, dist float64, phaseX, emissionEvals int) {
	tH := target.Bounds().Dy()

	// Calibrate font size from ink height if not pinned.
	fs := fontSizeHint
	if fs <= 0 {
		probe, psx, err := r.Render("Hhelo Wrd", unpixel.Style{FontSize: 100})
		if err != nil || psx <= 0 {
			return "", math.Inf(1), 0, 0
		}
		k := float64(inkBounds(probe, psx).Dy()) / 100.0
		if k <= 0 {
			return "", math.Inf(1), 0, 0
		}
		fs = float64(tH) / k
	}
	if fs < 4 {
		return "", math.Inf(1), 0, 0
	}

	// Measure per-glyph pixel advances via cumulative prefix renders.
	charRunes := []rune(charset)
	advances := measureAdvancesByCumulative(r, charRunes, fs)
	if len(advances) == 0 {
		return "", math.Inf(1), 0, 0
	}

	// Build glyph vocabulary: one glyphSpec per unique rune with its pixel advance.
	// Glyphs with zero advance are skipped (they cannot fill any column).
	glyphs := make([]did.GlyphSpec, 0, len(advances))
	for _, ch := range charset {
		adv, ok := advances[ch]
		if !ok || adv <= 0 {
			continue
		}
		glyphs = append(glyphs, did.GlyphSpec{R: ch, Advance: adv})
	}
	// Deduplicate by rune (charset may have duplicates after rune iteration).
	slices.SortFunc(glyphs, func(a, b did.GlyphSpec) int {
		return int(a.R) - int(b.R)
	})
	glyphs = slices.CompactFunc(glyphs, func(a, b did.GlyphSpec) bool { return a.R == b.R })

	if len(glyphs) == 0 {
		return "", math.Inf(1), 0, 0
	}

	pix := pixelatorFor(block, linear)
	W := target.Bounds().Dx()

	// Pre-render each glyph image at the calibrated size with PaddingTop=0 and
	// PaddingLeft=0. We keep the FULL render height (no Y-ink-crop) and clip X
	// to [0, sentinelX). This preserves both the horizontal left bearing and the
	// vertical baseline position so block averages align with the target band.
	//
	// Why no Y-ink-crop: the target band starts at the topmost ink row across all
	// glyphs (e.g. the ascender of 'b' in "abc"). Shorter glyphs ('a', 'c') have
	// whitespace above their ink in the target. A PaddingTop=0 render replicates
	// this: 'a' rendered alone with PaddingTop=0 also has its ink starting at the
	// same Y offset relative to the top of the image as 'a' does inside "abc".
	// Y-ink-cropping would remove that offset and misalign block rows.
	glyphImgs := make([]*image.RGBA, len(glyphs))
	for i, g := range glyphs {
		img, sx, err := r.Render(string(g.R), unpixel.Style{FontSize: fs, PaddingTop: 0, PaddingLeft: 0})
		if err != nil || sx <= 0 {
			glyphImgs[i] = image.NewRGBA(image.Rect(0, 0, g.Advance, tH))
			imutil.FillWhite(glyphImgs[i])
			continue
		}
		clipW := min(sx, img.Bounds().Dx())
		clipH := min(tH, img.Bounds().Dy())
		if clipW <= 0 || clipH <= 0 {
			glyphImgs[i] = image.NewRGBA(image.Rect(0, 0, g.Advance, tH))
			imutil.FillWhite(glyphImgs[i])
			continue
		}
		tile := image.NewRGBA(image.Rect(0, 0, clipW, clipH))
		imutil.FillWhite(tile)
		xdraw.Draw(tile, tile.Bounds(), img, img.Bounds().Min, xdraw.Src)
		glyphImgs[i] = tile
	}

	// Index rune → glyph slot once, so the context-aware emission resolves the
	// left neighbour in O(1) instead of scanning the whole charset per cache miss.
	runeIdx := make(map[rune]int, len(glyphs))
	for i, g := range glyphs {
		runeIdx[g.R] = i
	}

	// Sweep grid phases. For each phase, run the trellis DP with memoised emissions.
	bestText := ""
	bestDist := math.Inf(1)
	bestPhase := 0
	totalEvals := 0

	for phaseX := range block {
		if ctx.Err() != nil {
			break
		}

		var (
			path  []rune
			cost  float64
			evals int
		)

		if contextEmission {
			cache := did.NewContextualEmissionCache()
			emitFn := func(gi, col int, leftRune rune) float64 {
				if v, ok := cache.Get(leftRune, gi, col); ok {
					return v
				}
				// Resolve the left-neighbour glyph image. When leftRune is the
				// sentence-start sentinel (' ') or not in the vocabulary, leftImg
				// is nil (treated as blank in columnEmissionContextDID).
				var leftImg *image.RGBA
				var leftAdv int
				if j, ok := runeIdx[leftRune]; ok {
					leftImg = glyphImgs[j]
					leftAdv = glyphs[j].Advance
				}
				c := columnEmissionContextDID(target, leftImg, glyphImgs[gi], leftAdv, glyphs[gi].Advance, col, block, phaseX, tH, pix)
				cache.Put(leftRune, gi, col, c)
				return c
			}
			path, cost = did.TrellisDPContextual(W, glyphs, emitFn, lm, lambda)
			evals = cache.Len()
		} else {
			cache := did.NewEmissionCache()
			emitFn := func(gi, col int) float64 {
				if v, ok := cache.Get(gi, col); ok {
					return v
				}
				c := columnEmissionDID(target, glyphImgs[gi], glyphs[gi].Advance, col, block, phaseX, tH, pix)
				cache.Put(gi, col, c)
				return c
			}
			path, cost = did.TrellisDP(W, glyphs, emitFn, lm, lambda)
			evals = cache.Len()
		}

		totalEvals += evals

		if len(path) == 0 || math.IsInf(cost, 1) {
			continue
		}

		// Mean cost per column-pixel for comparability across widths.
		meanDist := cost / float64(W)
		if meanDist < bestDist {
			bestDist = meanDist
			bestText = strings.TrimRight(string(path), " ")
			bestPhase = phaseX
		}
	}

	return bestText, bestDist, bestPhase, totalEvals
}

// columnEmissionDID computes the emission cost for glyph image glyphImg (pixel
// advance glyphAdv) placed at startCol in the target band. It places the glyph
// on a white canvas the same height as the target, pixelates the canvas at the
// block grid phase phaseX, then computes MSE between the pixelated candidate
// column slice and the corresponding columns of target.
func columnEmissionDID(
	target, glyphImg *image.RGBA,
	glyphAdv, startCol, block, phaseX, bandH int,
	pixelateFn unpixel.Pixelator,
) float64 {
	W := target.Bounds().Dx()
	if startCol >= W || glyphAdv <= 0 {
		return math.Inf(1)
	}
	endCol := min(startCol+glyphAdv, W)

	// Build a canvas: phaseX pixels of white padding, then the glyph image,
	// aligned so that column startCol in the canvas corresponds to column
	// startCol of the target.
	canvasW := W + phaseX
	canvas := image.NewRGBA(image.Rect(0, 0, canvasW, bandH))
	imutil.FillWhite(canvas)
	if glyphImg != nil {
		gW := glyphImg.Bounds().Dx()
		gH := glyphImg.Bounds().Dy()
		// Place glyph at (phaseX+startCol, 0): the full tile (bearing preserved,
		// no ink-crop) sits flush against the left edge of the advance cell.
		// Vertical clip to canvas height; horizontal clip to canvas width.
		dstRect := image.Rect(phaseX+startCol, 0, phaseX+startCol+gW, min(gH, bandH))
		dstRect = dstRect.Intersect(canvas.Bounds())
		if !dstRect.Empty() {
			xdraw.Draw(canvas, dstRect, glyphImg, glyphImg.Bounds().Min, xdraw.Src)
		}
	}

	// Pixelate at originX=0, originY=0 (grid phase is baked into the phaseX padding).
	pixelated := pixelateFn.Pixelate(canvas, 0, 0)

	// Compare only block columns that are FULLY inside the glyph's advance cell
	// [startCol, endCol). Blocks at the boundary mix ink from adjacent glyphs in
	// the target but not in the isolated render — comparing them unfairly penalises
	// the correct glyph. Use ceiling for blockStart and floor for blockEnd so only
	// complete interior blocks are included.
	blockStart := ((startCol + block - 1) / block) * block // ceil
	blockEnd := (endCol / block) * block                   // floor
	if blockStart >= blockEnd {
		// Glyph narrower than one block: no fully-interior block exists. Compare
		// the advance-cell columns [startCol, endCol) of the target directly against
		// the corresponding canvas columns. Each "pixel" in the pixelated target has
		// a uniform block-average value, so this sub-column comparison is a
		// fair partial match: the glyph contributes ink in those columns, and the
		// target's block value there reflects that contribution (plus adjacent-glyph
		// bleed, which the DP handles globally via coverage).
		cmpW := endCol - startCol
		if cmpW <= 0 {
			return math.Inf(1)
		}
		targetSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
		xdraw.Draw(targetSub, targetSub.Bounds(), target, image.Pt(startCol, 0), xdraw.Src)
		candSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
		xdraw.Draw(candSub, candSub.Bounds(), pixelated, image.Pt(phaseX+startCol, 0), xdraw.Src)
		return mseRGB(targetSub, candSub)
	}
	blockEnd = min(blockEnd, W)

	cmpW := blockEnd - blockStart
	if cmpW <= 0 {
		return math.Inf(1)
	}

	targetSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
	xdraw.Draw(targetSub, targetSub.Bounds(), target, image.Pt(blockStart, 0), xdraw.Src)

	// The pixelated canvas is offset by phaseX horizontally.
	candSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
	xdraw.Draw(candSub, candSub.Bounds(), pixelated, image.Pt(phaseX+blockStart, 0), xdraw.Src)

	return mseRGB(targetSub, candSub)
}

// columnEmissionContextDID is the context-aware emission function for the DID
// trellis. Unlike [columnEmissionDID] — which renders the current glyph in
// isolation — this version also places the left-neighbor glyph (leftGlyphImg,
// leftAdv pixels wide ending at startCol) on the canvas before pixelating.
// Blocks that straddle the boundary between the left and current glyph therefore
// average ink from both glyphs, exactly as they do in the real mosaic target.
//
// The comparison window and the scoring logic are otherwise identical to
// [columnEmissionDID]: fully-interior block columns of the current glyph's
// advance cell are used; degenerate inputs return +Inf.
func columnEmissionContextDID(
	target, leftGlyphImg, glyphImg *image.RGBA,
	leftAdv, glyphAdv, startCol, block, phaseX, bandH int,
	pixelateFn unpixel.Pixelator,
) float64 {
	W := target.Bounds().Dx()
	if startCol >= W || glyphAdv <= 0 {
		return math.Inf(1)
	}
	endCol := min(startCol+glyphAdv, W)

	// Build a canvas wide enough for phaseX padding + the full target width.
	canvasW := W + phaseX
	canvas := image.NewRGBA(image.Rect(0, 0, canvasW, bandH))
	imutil.FillWhite(canvas)

	// Place the left-neighbor glyph ending at startCol so its right edge
	// aligns with the left edge of the current glyph's advance cell.
	if leftGlyphImg != nil && leftAdv > 0 {
		lStart := startCol - leftAdv
		if lStart >= 0 {
			gW := leftGlyphImg.Bounds().Dx()
			gH := leftGlyphImg.Bounds().Dy()
			dstRect := image.Rect(phaseX+lStart, 0, phaseX+lStart+gW, min(gH, bandH))
			dstRect = dstRect.Intersect(canvas.Bounds())
			if !dstRect.Empty() {
				xdraw.Draw(canvas, dstRect, leftGlyphImg, leftGlyphImg.Bounds().Min, xdraw.Src)
			}
		}
	}

	// Place the current glyph starting at startCol.
	if glyphImg != nil {
		gW := glyphImg.Bounds().Dx()
		gH := glyphImg.Bounds().Dy()
		dstRect := image.Rect(phaseX+startCol, 0, phaseX+startCol+gW, min(gH, bandH))
		dstRect = dstRect.Intersect(canvas.Bounds())
		if !dstRect.Empty() {
			xdraw.Draw(canvas, dstRect, glyphImg, glyphImg.Bounds().Min, xdraw.Src)
		}
	}

	// Pixelate at originX=0, originY=0 (grid phase baked into phaseX padding).
	pixelated := pixelateFn.Pixelate(canvas, 0, 0)

	// Score the comparison region. With a left neighbour rendered on the canvas
	// the left boundary block is now faithfully averaged, so we include it.
	//
	// cmpStart: the block boundary at or before startCol (the "left boundary
	//           block") when a real left neighbour was placed; otherwise the
	//           first fully-interior block (ceiling, same as isolated).
	// cmpEnd:   the last fully-interior block boundary (floor of endCol/block),
	//           excluding the right boundary whose neighbour is still unknown.
	var cmpStart int
	if leftGlyphImg != nil && leftAdv > 0 && startCol-leftAdv >= 0 {
		// Left boundary block: floor(startCol / block) * block.
		cmpStart = (startCol / block) * block
	} else {
		// No left context: first fully-interior block (ceiling).
		cmpStart = ((startCol + block - 1) / block) * block
	}
	cmpEnd := (endCol / block) * block // floor — excludes unknown right boundary

	if cmpStart >= cmpEnd {
		// No scorable blocks. Fall back to raw advance-cell pixel comparison.
		cmpW := endCol - startCol
		if cmpW <= 0 {
			return math.Inf(1)
		}
		targetSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
		xdraw.Draw(targetSub, targetSub.Bounds(), target, image.Pt(startCol, 0), xdraw.Src)
		candSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
		xdraw.Draw(candSub, candSub.Bounds(), pixelated, image.Pt(phaseX+startCol, 0), xdraw.Src)
		return mseRGB(targetSub, candSub)
	}

	cmpEnd = min(cmpEnd, W)
	cmpW := cmpEnd - cmpStart
	if cmpW <= 0 {
		return math.Inf(1)
	}

	targetSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
	xdraw.Draw(targetSub, targetSub.Bounds(), target, image.Pt(cmpStart, 0), xdraw.Src)

	candSub := image.NewRGBA(image.Rect(0, 0, cmpW, bandH))
	xdraw.Draw(candSub, candSub.Bounds(), pixelated, image.Pt(phaseX+cmpStart, 0), xdraw.Src)

	return mseRGB(targetSub, candSub)
}
