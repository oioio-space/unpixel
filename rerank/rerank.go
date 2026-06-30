// Package rerank re-orders decode candidates that have already been scored
// physically (by unpixel.Verify), by blending each candidate's image distance
// with a language score. It generalises the narrow language tie-break buried in
// the search into a first-class, tunable, inspectable post-search stage.
//
// The default reranker ([Linguistic]) is pure Go and reuses the existing
// language models (internal/lang). A future discriminative CTC model trained on
// the render→pixelate domain can replace the default via the //go:build ml seam
// (see Default) without changing callers.
//
// One-call use over the bundled forward model:
//
//	ranked, err := rerank.Rerank(ctx, img, candidates,
//	    unpixel.WithBlockSize(6), unpixel.WithRerankWeight(0.08))
//	best := ranked[0].Text
package rerank

import (
	"cmp"
	"context"
	"image"
	"slices"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/lang"
)

// Ranked is one re-ranked candidate.
type Ranked struct {
	// Text is the candidate string.
	Text string
	// Distance is the physical Verify distance in [0,1] (lower is better).
	Distance float64
	// LMScore is the language score (higher is more plausible); 0 when no LM.
	LMScore float64
	// Blended is the fused ordering key (lower is better): the candidates are
	// returned sorted ascending by Blended.
	Blended float64
}

// Reranker re-orders physically-scored candidates. img is provided for a future
// discriminative (CTC) implementation; the pure-Go default ignores it. lm scores
// linguistic plausibility (higher is better); weight is the blend strength in
// distance-units per language-unit. Implementations must be safe for concurrent
// use and return a non-nil error only on an unrecoverable failure.
type Reranker interface {
	Rerank(ctx context.Context, img image.Image, verdicts []unpixel.Verdict, lm func(string) float64, weight float64) ([]Ranked, error)
}

// Linguistic is the pure-Go default reranker. It blends each candidate's physical
// distance with its language score, relative to the most plausible candidate:
// blended = distance − weight·(lmScore − bestLM). With weight ≤ 0 or lm == nil
// the blend is the physical distance, so the result is physical-distance order.
// The zero value is ready to use.
type Linguistic struct{}

// Rerank implements [Reranker]. It ignores img (the language blend needs only the
// candidate strings and their physical distances).
func (Linguistic) Rerank(_ context.Context, _ image.Image, verdicts []unpixel.Verdict, lm func(string) float64, weight float64) ([]Ranked, error) {
	if len(verdicts) == 0 {
		return nil, nil
	}

	lmScores := make([]float64, len(verdicts))
	bestLM := 0.0
	useLM := lm != nil && weight > 0
	if useLM {
		for i, v := range verdicts {
			s := lm(v.Text)
			lmScores[i] = s
			if i == 0 || s > bestLM {
				bestLM = s
			}
		}
	}

	ranked := make([]Ranked, len(verdicts))
	for i, v := range verdicts {
		blended := v.Distance
		if useLM {
			blended = v.Distance - weight*(lmScores[i]-bestLM)
		}
		ranked[i] = Ranked{Text: v.Text, Distance: v.Distance, LMScore: lmScores[i], Blended: blended}
	}

	slices.SortStableFunc(ranked, func(a, b Ranked) int {
		if c := cmp.Compare(a.Blended, b.Blended); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Distance, b.Distance); c != 0 {
			return c
		}
		return cmp.Compare(a.Text, b.Text)
	})
	return ranked, nil
}

// Rerank verifies candidates against img with the faithful forward model
// (unpixel.Verify) and re-orders them with [Default] by blending physical
// distance with a language score. The language model is cfg.LanguageModel when
// set (via WithLanguageModel/WithPriors), else the bundled English prior; the
// blend weight is cfg.RerankWeight (set via WithRerankWeight; 0 = physical
// order). opts are forwarded to Verify (e.g. WithBlockSize, WithCharset).
// Results are sorted best-first. It returns the errors of unpixel.Verify.
func Rerank(ctx context.Context, img image.Image, candidates []string, opts ...unpixel.Option) ([]Ranked, error) {
	verdicts, err := unpixel.Verify(ctx, img, candidates, opts...)
	if err != nil {
		return nil, err
	}
	var cfg unpixel.Config
	for _, o := range opts {
		o(&cfg)
	}
	lm := cfg.LanguageModel
	if lm == nil {
		lm = lang.PriorFor(lang.English)
	}
	return Default().Rerank(ctx, img, verdicts, lm, cfg.RerankWeight)
}
