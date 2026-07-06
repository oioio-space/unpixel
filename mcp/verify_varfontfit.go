package mcpserver

// verify_varfontfit.go — VerifyVarFontFit: the variable-font calibration decode path.
//
// Some real redactions use a VARIABLE font whose exact weight (and size) is not
// known precisely — calibrate-from-visible gives an approximation, but a redaction
// pixelated at a coarse block is unforgiving: a weight off by ~30 units shifts the
// block averages enough that a confusable-glyph decoy out-scores the truth (measured:
// ctx_crossimg_wght700 "Secret7" loses to "Sccret7" at the nominal weight). This
// fits the weight axis (and font size) PER CANDIDATE by generate-and-test — each
// candidate is scored at its own best (wght, size) — so the truth reaches its true
// physical minimum. On ctx_crossimg_wght700 the truth then hits distance 0.0000,
// and the residual homoglyph tie ("Secret7" vs "Sccret7") is broken by the English
// language prior (rerank), which favours the real word. That full chain — calibrate
// → whole-string verify → semantic tie-break — decodes the redaction.

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"image"
	"slices"
	"strings"
	"unicode"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/varfont"
	"github.com/oioio-space/unpixel/rerank"
)

// VarFontFitHints configures the variable-font calibration decode. FontData is the
// variable font (e.g. the bundled Nunito VF); Axis is the fitted axis tag ("wght").
// The weight is swept over [WghtMin, WghtMax] step WghtStep and the size over
// [SizeMin, SizeMax] step SizeStep; each candidate takes its minimum distance over
// that grid. Crop (analyze's redaction band) and BlockSize/Linear match VerifyHints.
// RerankWeight (> 0) blends the English language prior to break residual homoglyph ties.
type VarFontFitHints struct {
	Crop      image.Rectangle
	BlockSize int
	Linear    bool
	FontData  []byte
	Axis      string
	WghtMin   int
	WghtMax   int
	WghtStep  int
	SizeMin   float64
	SizeMax   float64
	SizeStep  float64

	RerankWeight float64
}

// VerifyVarFontFit scores each candidate at its best (weight, size) fit against the
// redaction and returns a ranked VerifyReport. Pick is the lowest-distance confident
// match; with RerankWeight > 0 the ranking blends the language prior to separate
// physically-tied homoglyphs (the real word wins). It is the decode path for
// variable-font redactions whose exact weight is only approximately known.
func VerifyVarFontFit(ctx context.Context, img image.Image, candidates []string, h VarFontFitHints) (VerifyReport, error) {
	if len(candidates) == 0 {
		return VerifyReport{}, fmt.Errorf("candidates must not be empty")
	}
	if h.BlockSize < 2 {
		return VerifyReport{}, fmt.Errorf("block_size must be >= 2")
	}
	axis := cmp.Or(h.Axis, "wght")

	// Per-candidate minimum distance over the (weight, size) grid.
	best := make(map[string]float64, len(candidates))
	for _, c := range candidates {
		best[c] = 1.0
	}
	wStep := max(1, h.WghtStep)
	sStep := h.SizeStep
	if sStep <= 0 {
		sStep = 1
	}
	for w := h.WghtMin; w <= h.WghtMax; w += wStep {
		vr, verr := varfont.NewVarRenderer(bytes.NewReader(h.FontData),
			[]varfont.Axis{{Tag: axis, Value: float32(w)}})
		if verr != nil {
			return VerifyReport{}, fmt.Errorf("build var renderer @ %s=%d: %w", axis, w, verr)
		}
		for size := h.SizeMin; size <= h.SizeMax; size += sStep {
			opts := []unpixel.Option{
				unpixel.WithRenderer(vr),
				unpixel.WithBlockSize(h.BlockSize),
				unpixel.WithStyle(unpixel.Style{FontSize: size, PaddingTop: 8, PaddingLeft: 8}),
			}
			if h.Linear {
				opts = append(opts, unpixel.WithPixelator(defaults.LinearBlockAverage(h.BlockSize)))
			}
			if !h.Crop.Empty() {
				opts = append(opts, unpixel.WithCrop(h.Crop))
			}
			verdicts, verr := unpixel.Verify(ctx, img, candidates, opts...)
			if verr != nil {
				return VerifyReport{}, fmt.Errorf("verify @ %s=%d size=%.1f: %w", axis, w, size, verr)
			}
			for _, v := range verdicts {
				if v.Distance < best[v.Text] {
					best[v.Text] = v.Distance
				}
			}
			if ctx.Err() != nil {
				return VerifyReport{}, ctx.Err()
			}
		}
	}

	verdicts := make([]unpixel.Verdict, len(candidates))
	for i, c := range candidates {
		verdicts[i] = unpixel.Verdict{Text: c, Distance: best[c], Match: best[c] < unpixel.VerifyMatchThreshold}
	}

	ranked := make([]RankedCandidate, len(verdicts))
	if h.RerankWeight > 0 {
		// Break physical ties in favour of candidates whose alphabetic part is a real
		// word: at a coarse block, confusable homoglyphs (Secret7 vs Secnet7) are
		// physically identical, so only the semantic prior separates them. dictWordBonus
		// (word membership) is decisive where a char-n-gram prior is too weak (it rates
		// "Secnet7" ≈ "Secret7"). rerank blends it as distance − weight·(bonus − maxBonus),
		// so with a small weight it only reorders candidates whose distances already tie.
		rr, rerr := rerank.Default().Rerank(ctx, img, verdicts, dictWordBonus, h.RerankWeight)
		if rerr != nil {
			return VerifyReport{}, fmt.Errorf("rerank: %w", rerr)
		}
		for i, r := range rr {
			ranked[i] = RankedCandidate{Text: r.Text, Distance: r.Distance, Match: r.Distance < unpixel.VerifyMatchThreshold}
		}
	} else {
		for i, v := range verdicts {
			ranked[i] = RankedCandidate{Text: v.Text, Distance: v.Distance, Match: v.Match}
		}
		slices.SortFunc(ranked, func(a, b RankedCandidate) int { return cmp.Compare(a.Distance, b.Distance) })
	}

	report := VerifyReport{Ranked: ranked}
	if len(ranked) > 0 {
		report.Best = ranked[0].Text
	}
	if len(ranked) >= 2 {
		report.Margin = ranked[1].Distance - ranked[0].Distance
	}
	report.Pick = lowestDistanceMatch(verdicts)
	return report, nil
}

// dictWordBonus returns 1 when any maximal alphabetic run of s (length ≥ 3) is an
// English dictionary word, else 0. It is the tie-breaker that separates a real-word
// secret from its confusable-glyph decoys ("Secret7" → "Secret" is a word; "Secnet7"
// → "Secnet" is not), where a character-n-gram prior cannot.
func dictWordBonus(s string) float64 {
	d := lang.Dictionary()
	var run []rune
	isWord := func() bool { return len(run) >= 3 && d.Contains(strings.ToLower(string(run))) }
	for _, r := range s {
		if unicode.IsLetter(r) {
			run = append(run, r)
			continue
		}
		if isWord() {
			return 1
		}
		run = run[:0]
	}
	if isWord() {
		return 1
	}
	return 0
}
