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

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
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
//
// Limitation: the histogram separates broad font families reliably on text
// covering enough of the mosaic, but on short or low-ink redactions (very few
// mosaic blocks) separation is noisier and the true font may not appear in the
// top-3 ranked results.
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

// FontResult is one prior-ordered recovery: the recovery and the bundled font
// that produced it. The slice returned by [RecoverWithPrior] is best-first by
// whole-image distance.
type FontResult struct {
	// Result is the recovery produced with this font.
	Result unpixel.Result
	// Font is the bundled font name that produced Result.
	Font string
}

// RecoverWithPrior recovers img over the bundled fonts, using the default blind
// prior to order the sweep so the likeliest font is decoded first. It always
// reorders; [unpixel.WithFontPriorTopK] additionally prunes the sweep to the K
// best-ranked fonts. opts are forwarded unchanged to [unpixel.RecoverMultiFont]
// (e.g. WithBlockSize, WithCharset, WithWorkers). Results are best-first by
// whole-image distance.
//
// If the prior fails (degenerate image), RecoverWithPrior falls back to the full
// sweep in catalog order — never worse than an unordered sweep. It returns the
// same errors as [unpixel.RecoverMultiFont] (nil image, no fonts, all failed).
func RecoverWithPrior(ctx context.Context, img image.Image, opts ...unpixel.Option) ([]FontResult, error) {
	if img == nil {
		return nil, unpixel.ErrNilImage
	}

	// Resolve a throwaway config to read the prior knobs; opts pass through intact.
	var cfg unpixel.Config
	for _, o := range opts {
		o(&cfg)
	}

	ordered := fonts.All()
	if ranked, err := Default().Rank(ctx, img, cfg.BlockSize, ordered); err == nil && len(ranked) == len(ordered) {
		ordered = reorderByRank(ordered, ranked)
		if k := cfg.FontPriorTopK; k > 0 && k < len(ordered) {
			ordered = ordered[:k]
		}
	}
	// else: prior failed → keep catalog order (full unordered sweep).

	renderers := make([]unpixel.Renderer, 0, len(ordered))
	names := make([]string, 0, len(ordered))
	for _, f := range ordered {
		r, err := defaults.RendererFromFonts(f.Data, nil)
		if err != nil {
			continue // skip a corrupt font rather than aborting the whole sweep
		}
		renderers = append(renderers, r)
		names = append(names, f.Name)
	}

	results, err := unpixel.RecoverMultiFont(ctx, img, renderers, opts...)
	if err != nil {
		return nil, err
	}

	out := make([]FontResult, len(results))
	for i, fr := range results {
		name := ""
		if fr.Index >= 0 && fr.Index < len(names) {
			name = names[fr.Index]
		}
		out[i] = FontResult{Result: fr.Result, Font: name}
	}
	return out, nil
}

// reorderByRank returns fnts reordered to match the prior ranking (best-first).
// Fonts present in fnts but absent from ranked are appended in their original
// order, so the result is always a permutation of fnts.
func reorderByRank(fnts []fonts.Font, ranked []Ranked) []fonts.Font {
	byName := make(map[string]fonts.Font, len(fnts))
	for _, f := range fnts {
		byName[f.Name] = f
	}
	out := make([]fonts.Font, 0, len(fnts))
	seen := make(map[string]bool, len(fnts))
	for _, r := range ranked {
		if f, ok := byName[r.Name]; ok && !seen[r.Name] {
			out = append(out, f)
			seen[r.Name] = true
		}
	}
	for _, f := range fnts {
		if !seen[f.Name] {
			out = append(out, f)
		}
	}
	return out
}
