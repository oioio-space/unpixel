// Package fontprior ranks the bundled fonts by how well each explains a mosaic
// redaction, blind — without any known plaintext — so the multi-font sweep can
// try the likeliest font first (reordering, result-preserving) or prune to the
// top-K (faster, opt-in).
//
// The default prior is a pure-Go pixelated-signature heuristic ([Histogram],
// wrapping internal/fontrank): it renders each font's exemplar, re-pixelates at
// the redaction's block size, and compares block-luminance histograms. A future
// ML classifier trained on the render->pixelate domain can replace the default
// via the //go:build ml seam (see Default) without changing callers.
//
// Use [RecoverWithPrior] for a one-call prior-ordered recovery over the bundled
// fonts:
//
//	res, err := fontprior.RecoverWithPrior(ctx, img,
//	    unpixel.WithBlockSize(6), unpixel.WithFontPriorTopK(3))
//	best := res[0] // best.Font, best.Result.BestGuess
package fontprior

import (
	"context"
	"image"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fontrank"
)

// Ranked is one prior result: a bundled font name and its match score, sorted
// best-first by the prior (lower Score is a better match).
type Ranked struct {
	// Name is the bundled font name, e.g. "Liberation Sans".
	Name string
	// Score is the prior's distance; lower is better. For the Histogram prior it
	// is the L1 block-luminance histogram distance in [0,1].
	Score float64
}

// Prior ranks candidate fonts by how well each explains a mosaic redaction,
// blind (no known plaintext). blockSize is the mosaic block side in pixels; pass
// 0 to auto-detect. Implementations must be safe for concurrent use and return a
// non-nil error only on context cancellation or an unrecoverable failure.
type Prior interface {
	// Rank returns fonts sorted best-first. It returns nil, nil when fnts is empty.
	Rank(ctx context.Context, img image.Image, blockSize int, fnts []fonts.Font) ([]Ranked, error)
}

// Histogram is the default pure-Go prior. It delegates to
// internal/fontrank.RankFontsAt (block-luminance histogram signature), which
// needs no known plaintext. The zero value is ready to use.
type Histogram struct{}

// Rank implements [Prior] using the block-luminance histogram ranker.
func (Histogram) Rank(ctx context.Context, img image.Image, blockSize int, fnts []fonts.Font) ([]Ranked, error) {
	if len(fnts) == 0 {
		return nil, nil
	}
	named := make([]fontrank.NamedFont, len(fnts))
	for i, f := range fnts {
		named[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}
	scores, err := fontrank.RankFontsAt(ctx, img, named, blockSize)
	if err != nil {
		return nil, err
	}
	ranked := make([]Ranked, len(scores))
	for i, s := range scores {
		ranked[i] = Ranked{Name: s.Name, Score: s.Score}
	}
	return ranked, nil
}
