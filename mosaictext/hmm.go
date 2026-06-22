package mosaictext

// hmm.go — LM-guided beam-search decoder for long monospace mosaic text.
//
// DecodeHMM breaks the charset^len barrier by replacing per-character greedy
// search with a left-to-right LM-guided beam search that jointly decodes all N
// cells using a bigram language model as the transition prior and whole-string
// MSE (one cell changed at a time, with prior choices already committed) as the
// emission signal.
//
// Why beam search rather than exact Viterbi: the emission cost d.dist(text)
// measures whole-image MSE, so changing cell i shifts block averages that
// straddle the cell boundary, making the cost depend on the full evolving
// hypothesis — not just cell i in isolation. The emission is therefore NOT
// cell-local, and true Viterbi (which requires cell-independent emissions) is
// not applicable without a significant per-cell MSE approximation that
// introduces errors. The left-to-right beam propagates the already-committed
// prefix when evaluating each new cell, preserving block-boundary accuracy.
//
// Algorithm (per position i):
//
//	For each beam hypothesis (text_prefix, score):
//	  For each charset character s:
//	    new_text = prefix_so_far + s + remaining_seed_chars
//	    score += LM_logP(prev_char, s) − λ·MSE(new_text)
//	  Keep the top beamWidth hypotheses by score.
//
// The seed string (a two-pass greedy warm-start) fills positions not yet
// committed, so each MSE evaluation reflects realistic neighbouring characters.
// beamWidth=1 reduces to LM-guided greedy; width 8 is the calibrated default.
//
// Font sweep: DecodeHMM sweeps all bundled monospace fonts and both
// linear/sRGB pixelation modes unless WithFont is supplied. When WithFont
// pins a specific bundled font name, only that font is tried — faster and
// reliable when the caller knows the font.
//
// Pipeline:
//  1. Calibrate font size and advance from the image height (identical to Decode).
//  2. Find grid phase at nRef via a half-block sweep.
//  3. For each N in [nMin, nMax]: warm-start with greedyN(2 passes), then run
//     lmBeam with score = Σ LM_logP(prev,cur) − λ·MSE(current_text).
//  4. Pick the winner by lowest per-cell MSE (wholeMSE/decodedLen) with a
//     terminal-punctuation tiebreak. Per-cell MSE is comparable across N.
//
// This is not an unpixel.Strategy and does not touch Engine/Config/Strategy paths.

import (
	"cmp"
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
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/render"
)

// DefaultHMMCharset is the default candidate alphabet for DecodeHMM: ASCII
// letters, space, and the common punctuation that survives mosaic pixelation
// as a distinct shape. Identical to the private defaultCharset used by Decode.
const DefaultHMMCharset = defaultCharset

// defaultEmissionTemperature scales emission costs (MSE) relative to log-prob
// transitions. λ = 0.04 ≈ 1/25 makes a 25-unit MSE gap weigh as 1 nat — tuned
// so that the correct character's MSE advantage dominates over language noise.
const defaultEmissionTemperature = 0.04

// hmmConfig holds DecodeHMM option state.
type hmmConfig struct {
	language            lang.Language
	charset             string
	emissionTemperature float64
	fontName            string  // empty → sweep all mono fonts
	fontData            []byte  // non-nil → use this TTF/OTF as the sole font
	fontBoldData        []byte  // non-nil → used as bold face alongside fontData
	fontSize            float64 // 0 → calibrate from image
	charCount           int     // 0 → calibrate from image
}

func defaultHMMConfig() hmmConfig {
	return hmmConfig{
		language:            lang.English,
		charset:             defaultCharset,
		emissionTemperature: defaultEmissionTemperature,
	}
}

// HMMOption configures DecodeHMM.
type HMMOption func(*hmmConfig)

// WithLanguage selects the bigram language model used for transition
// probabilities. Defaults to lang.English.
func WithLanguage(l lang.Language) HMMOption {
	return func(c *hmmConfig) { c.language = l }
}

// WithCharset sets the candidate alphabet for DecodeHMM. Defaults to
// DefaultHMMCharset (ASCII letters, space, and common punctuation).
func WithCharset(cs string) HMMOption {
	return func(c *hmmConfig) {
		if cs != "" {
			c.charset = cs
		}
	}
}

// WithEmissionTemperature sets the scale λ applied to emission costs before
// combining with log-prob transitions (beam score = Σtrans − λ·Σemit).
// Larger λ weights the image more strongly; smaller weights the language model
// more. Default is 0.04.
func WithEmissionTemperature(lambda float64) HMMOption {
	return func(c *hmmConfig) {
		if lambda > 0 {
			c.emissionTemperature = lambda
		}
	}
}

// WithFont pins the decoder to a specific bundled monospace font by name (e.g.
// "Liberation Mono"). When set, only that font is tried, skipping the full
// font sweep. Use when the font is known to save time and avoid font-selection
// ambiguity. The name must match a bundled font with Style=="mono"; if it does
// not match any bundled mono font DecodeHMM returns ErrNoContent.
func WithFont(name string) HMMOption {
	return func(c *hmmConfig) { c.fontName = name }
}

// WithFontFile supplies raw TrueType/OpenType bytes for the regular (upright)
// face. When set, DecodeHMM renders all candidates with this font exclusively —
// the bundled monospace sweep is skipped entirely. This is the primary real-world
// mitigation when the redaction font is known (e.g. Notepad uses Consolas,
// Sublime Text uses its own custom monospace). The font should be monospace;
// proportional fonts will produce poor calibration.
//
// Pair with WithFontFileBold if the redaction includes bold text; otherwise the
// regular face is reused for bold rendering.
func WithFontFile(regularTTF []byte) HMMOption {
	return func(c *hmmConfig) {
		if len(regularTTF) > 0 {
			c.fontData = regularTTF
		}
	}
}

// WithFontFileBold supplies raw TrueType/OpenType bytes for the bold face used
// alongside WithFontFile. It has no effect unless WithFontFile is also set.
// If omitted, DecodeHMM reuses the regular font for bold rendering.
func WithFontFileBold(boldTTF []byte) HMMOption {
	return func(c *hmmConfig) {
		if len(boldTTF) > 0 {
			c.fontBoldData = boldTTF
		}
	}
}

// WithFontSize bypasses calibration and uses the given point size directly.
// Useful in tests and when the rendering font size is known exactly. If 0 or
// negative, calibration runs normally.
func WithFontSize(pt float64) HMMOption {
	return func(c *hmmConfig) {
		if pt > 0 {
			c.fontSize = pt
		}
	}
}

// WithCharCount bypasses the N sweep and decodes exactly n characters. Useful
// in tests and when the string length is known. If 0 or negative, the sweep
// runs normally over [nMin, nMax].
func WithCharCount(n int) HMMOption {
	return func(c *hmmConfig) {
		if n > 0 {
			c.charCount = n
		}
	}
}

// DecodeHMM recovers monospace text from a mosaic redaction using an LM-guided
// beam search decoder. Unlike Decode, it runs in O(N·|charset|·beamWidth) time
// per position and is not limited by charset^len; it is suited to longer strings
// (N ≳ 8).
//
// The beam search propagates the top beamWidth hypotheses left-to-right,
// scoring each by Σ LM_logP(prev, cur) − λ·MSE(current_text). Committing each
// cell's choice before evaluating the next preserves the block-boundary
// accuracy of the whole-image MSE signal (a seed-fixed cell-local approximation
// would break here because block averages straddle character boundaries).
//
// It reuses Decode's calibration, render cache, and MSE infrastructure. It is
// not an unpixel.Strategy and does not touch the Engine/Config/Strategy paths.
//
// Returns ErrNoMosaic if no block grid is detected, ErrNoContent if no
// non-background content is found, or a context error on cancellation.
func DecodeHMM(ctx context.Context, img image.Image, opts ...HMMOption) (Result, error) {
	hcfg := defaultHMMConfig()
	for _, o := range opts {
		o(&hcfg)
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

	// Build target (identical to Decode's targetHi with pad=24).
	const pad = 24
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+pad, rect.Dy()+pad))
	imutil.FillWhite(target)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), rgba, rect.Min, xdraw.Src)
	tW, tH := rect.Dx(), rect.Dy()

	// Build the renderer+metadata lists used by the sweep loop below.
	// When WithFontFile is set the caller has supplied the exact font, so we
	// build a single-entry synthetic list and skip the bundled-mono sweep
	// entirely — this is the key real-world mitigation when the redaction
	// font is known (e.g. Notepad/Consolas, Sublime's custom monospace).
	// When no user font is given, fall back to the full bundled-mono sweep.
	var (
		rs  []unpixel.Renderer
		all []fonts.Font
	)
	if len(hcfg.fontData) > 0 {
		r, buildErr := render.NewXImageFromFonts(hcfg.fontData, hcfg.fontBoldData)
		if buildErr != nil {
			return Result{}, fmt.Errorf("build renderer from supplied font: %w", buildErr)
		}
		rs = []unpixel.Renderer{r}
		all = []fonts.Font{{Name: "(user font)", Style: "mono"}}
	} else {
		var err error
		rs, err = fonts.Renderers()
		if err != nil {
			return Result{}, err
		}
		all = fonts.All()
	}

	// Build the LM model once.
	model := lang.Default()
	charset := []rune(hcfg.charset)
	lambda := hcfg.emissionTemperature

	// lmBeamOne decodes one (N, phase) hypothesis using LM-guided left-to-right
	// beam search. Unlike a seed-fixed cell-local emission, this updates the
	// working string after each position so that subsequent MSE evaluations
	// reflect the already-chosen characters — essential because mosaic block
	// averages straddle character boundaries and the MSE is not cell-independent.
	//
	// Algorithm (per position i):
	//
	//	For each beam hypothesis (text_prefix, score):
	//	  For each charset character s:
	//	    new_text = prefix_so_far + s + remaining_seed_chars
	//	    cost = λ · MSE(new_text)
	//	    score += LM_logP(prev_char, s) − cost
	//	  Keep the top beamWidth hypotheses by score.
	//
	// Returns decoded text (trailing spaces stripped) and whole-image MSE.
	// beamWidth=1 reduces to LM-guided greedy (fast); larger values trade
	// speed for accuracy on simultaneous multi-cell corrections.
	const beamWidth = 8

	lmBeamOne := func(d *decoder, n, pox int, stretch float64) (text string, wholeMSE float64) {
		d.cache = newRenderCache(d.cacheCap)
		defer func() { d.cache = nil }()

		// Warm-start seed to fill positions not yet chosen.
		seed := d.greedyN(stretch, n, pox, charset, 2)
		seedRunes := []rune(seed)

		// beam holds the current set of hypotheses: chosen prefix + combined score.
		// score = Σ LM_logP(prev,cur) − λ·MSE(current_text); higher is better.
		type hyp struct {
			runes []rune
			prev  rune // last chosen character (for LM transition)
			score float64
		}
		beam := []hyp{{
			runes: make([]rune, n),
			prev:  ' ', // sentence-start context
			score: 0,
		}}
		copy(beam[0].runes, seedRunes)

		for i := range n {
			next := make([]hyp, 0, len(beam)*len(charset))
			for _, h := range beam {
				for _, ch := range charset {
					newRunes := make([]rune, n)
					copy(newRunes, h.runes)
					newRunes[i] = ch
					mse := d.dist(string(newRunes), d.fs, stretch, pox)
					lmScore := model.TransitionLogProb(h.prev, ch)
					next = append(next, hyp{
						runes: newRunes,
						prev:  ch,
						score: h.score + lmScore - lambda*mse,
					})
				}
			}
			// Keep top beamWidth by score (descending — higher score = better).
			if len(next) > beamWidth {
				slices.SortFunc(next, func(a, b hyp) int {
					return cmp.Compare(b.score, a.score) // descending
				})
				next = next[:beamWidth]
			}
			beam = next
		}

		// Best hypothesis has highest score.
		best := beam[0]
		for _, h := range beam[1:] {
			if h.score > best.score {
				best = h
			}
		}
		decoded := strings.TrimRight(string(best.runes), " ")
		wholeMSE = d.dist(decoded, d.fs, stretch, pox)
		return decoded, wholeMSE
	}

	// candidate holds one (font, linear, N) decode result.
	type candidate struct {
		text     string
		fontName string
		linear   bool
		n, pox   int
		mse      float64 // whole-image MSE (for Result.Distance)
		score    float64 // per-cell MSE (comparable across N, for winner selection)
	}

	cfg := defaultConfig()
	frameBytes := target.Bounds().Dx() * target.Bounds().Dy() * 4
	workers, cacheCap := cfg.plan(frameBytes, len(rs)*2)

	var (
		mu   sync.Mutex
		best candidate
	)
	best.score = math.Inf(1)
	best.mse = math.Inf(1)

	sem := make(chan struct{}, max(1, workers))
	var wg sync.WaitGroup

	for fi := range rs {
		fi2 := all[fi]
		if fi2.Style != "mono" {
			continue
		}
		// WithFont filter: skip fonts that don't match the pinned name.
		if hcfg.fontName != "" && fi2.Name != hcfg.fontName {
			continue
		}
		for li := range 2 {
			fi, li := fi, li
			wg.Go(func() {
				if ctx.Err() != nil {
					return
				}
				sem <- struct{}{}
				defer func() { <-sem }()

				linear := li == 1
				d := &decoder{
					r:        rs[fi],
					target:   target,
					tW:       tW,
					tH:       tH,
					block:    block,
					pixelate: pixelatorFor(block, linear),
					cacheCap: cacheCap,
				}
				nRef, nMin, nMax, ok := d.calibrate()
				if !ok {
					return
				}
				// WithFontSize overrides the calibrated fs (and re-derives adv).
				if hcfg.fontSize > 0 {
					d.fs = hcfg.fontSize
					w2, s2, _ := d.r.Render("HH", unpixel.Style{FontSize: d.fs})
					w1, s1, _ := d.r.Render("H", unpixel.Style{FontSize: d.fs})
					if adv := float64(inkBounds(w2, s2).Dx() - inkBounds(w1, s1).Dx()); adv > 1 {
						d.adv = adv
						nRef = max(1, int(math.Round(float64(d.tW)/d.adv)))
						nMin = max(1, int(float64(d.tW)/(d.adv*1.5)))
						nMax = int(float64(d.tW)/(d.adv*0.85)) + 1
					}
				}
				// WithCharCount pins the N sweep to exactly that count.
				if hcfg.charCount > 0 {
					nMin, nMax = hcfg.charCount, hcfg.charCount
					nRef = hcfg.charCount
				}

				// Phase: use nRef to find the grid phase offset.
				pox, _ := d.phase(d.stretchForN(nRef), nRef)

				// Beam decode for each N in [nMin, nMax].
				for n := nMin; n <= nMax; n++ {
					if ctx.Err() != nil {
						return
					}
					stretch := d.stretchForN(n)
					text, wholeMSE := lmBeamOne(d, n, pox, stretch)
					if text == "" {
						continue
					}
					// Per-cell MSE: divide by the actual decoded rune count
					// (after TrimRight) so the score is comparable across N
					// hypotheses that produce different trimmed lengths.
					actualLen := float64(len([]rune(text)))
					perCell := wholeMSE / actualLen
					score := perCell
					if terminalPunct(text) {
						score -= terminalBonus / actualLen
					}

					mu.Lock()
					if score < best.score {
						best = candidate{
							text:     text,
							fontName: all[fi].Name,
							linear:   linear,
							n:        n,
							pox:      pox,
							mse:      wholeMSE,
							score:    score,
						}
					}
					mu.Unlock()
				}
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
		Distance:   best.mse,
		Linear:     best.linear,
		BlockSize:  block,
		CharCount:  best.n,
		GridPhaseX: best.pox,
	}, nil
}
