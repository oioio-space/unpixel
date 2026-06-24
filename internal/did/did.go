// Package did implements Document Image Decoding (DID) for mosaic-pixelated text.
//
// The method models a redacted text line as a left-to-right path through a
// column trellis (Kopec & Chou, 1994 — "Document Image Decoding Using Markov
// Source Models"):
//
//   - Nodes are horizontal pixel-column positions c ∈ [0, W].
//   - An edge places glyph g starting at column c and ending at
//     c + advanceWidth(g). Its cost is:
//     emissionCost(g, c) + λ · (−logP_LM(g | previous glyph))
//   - Glyph boundaries are discovered by the DP, not assumed.
//   - A space glyph is included in the charset so word gaps are modelled.
//
// Tractability — Iterated Complete Path (ICP):
//
// Full O(W·|charset|) emission evaluation is deferred: the DP is run first with
// a cached subset of emissions (all admissible placements, computed once and
// stored in a flat map). A second pass evaluates exact emissions for the best
// path found. Because each (glyph, startCol) pair is computed at most once
// (cached), the total work is O(W·|charset|) render+pixelate+compare ops,
// amortised across ICP iterations. In practice the path stabilises in one or
// two iterations for well-typed text.
//
// Coverage relaxation:
//
// Mosaic target widths are block-aligned (multiples of the block size) and are
// not in general exact multiples of any glyph advance. TrellisDP therefore
// finds the best path ending at any column in the range [W-tolerance, W], where
// tolerance defaults to the largest glyph advance. This lets the DP cover the
// target band without requiring an unreachable exact endpoint.
package did

import (
	"math"

	"github.com/oioio-space/unpixel/internal/lang"
)

// GlyphSpec describes one glyph in the trellis vocabulary.
type GlyphSpec struct {
	// R is the Unicode rune this glyph represents.
	R rune
	// Advance is the pixel width the cursor steps after placing this glyph.
	Advance int
}

// emissionKey is the cache key for a (glyph-index, start-column) pair.
type emissionKey struct {
	gi  int
	col int
}

// EmissionFunc computes the emission cost for glyph gi placed at start column
// col. It must be idempotent and safe to call from a single goroutine.
type EmissionFunc func(gi int, col int) float64

// TrellisDP runs Viterbi / shortest-path DP over the column trellis.
//
// W is the target width in pixels; glyphs is the vocabulary; emitFn returns the
// emission cost for (glyph-index, start-col); lm is the bigram language model;
// lambda weights the LM penalty (0 = image-only).
//
// The returned path is the sequence of runes in the best path that ends at any
// column c in [W-tolerance, W], where tolerance = max glyph advance (so paths
// that fall just short of the right edge are accepted). This relaxation is
// necessary because block-aligned target widths are rarely exact multiples of
// glyph advances. If no such path exists, cost is +Inf and path is nil.
func TrellisDP(w int, glyphs []GlyphSpec, emitFn EmissionFunc, lm *lang.Model, lambda float64) (path []rune, cost float64) {
	if w <= 0 || len(glyphs) == 0 {
		return nil, math.Inf(1)
	}

	// Compute max advance for the coverage-relaxation tolerance.
	maxAdv := 0
	for _, g := range glyphs {
		if g.Advance > maxAdv {
			maxAdv = g.Advance
		}
	}

	// Extend the DP table by maxAdv so that a final glyph whose advance slightly
	// overshoots W can still be placed. This handles the common case where the
	// target width is not an exact multiple of any glyph advance sequence: the
	// last glyph may land 1..maxAdv pixels past W, which is still a valid decode.
	dpSize := w + maxAdv + 1

	// dp[c] = best cost to reach column c.
	dp := make([]float64, dpSize)
	for i := range dp {
		dp[i] = math.Inf(1)
	}
	dp[0] = 0

	// back[c] = (glyph index, start column) of the edge that achieved dp[c].
	type backEntry struct{ gi, from int }
	back := make([]backEntry, dpSize)

	// prevRune[c] = the last rune placed on the best path arriving at c.
	// Initialised to ' ' (sentence-start context).
	prevRune := make([]rune, dpSize)
	for i := range prevRune {
		prevRune[i] = ' '
	}

	// Sweep columns 0..w (not w+maxAdv): we only START a glyph within the target
	// width. Its end may land in [w, w+maxAdv] due to the extended table.
	for c := range w {
		if math.IsInf(dp[c], 1) {
			continue
		}
		for gi, g := range glyphs {
			end := c + g.Advance
			if end >= dpSize {
				continue
			}
			emit := emitFn(gi, c)
			if math.IsInf(emit, 1) {
				continue
			}
			lmCost := 0.0
			if lambda != 0 && lm != nil {
				lmCost = -lambda * lm.TransitionLogProb(prevRune[c], g.R)
			}
			total := dp[c] + emit + lmCost
			if total < dp[end] {
				dp[end] = total
				back[end] = backEntry{gi: gi, from: c}
				prevRune[end] = g.R
			}
		}
	}

	// Find the best endpoint in [max(1, w-maxAdv), w+maxAdv].
	// Apply a small coverage penalty proportional to |c - w| so that paths
	// that closely cover the target width are preferred over short paths that
	// terminate early with low emission cost. The penalty scale (0.1 per pixel
	// of deviation) is intentionally small relative to typical per-block MSE
	// (≥ 100) so it only breaks ties between paths of similar emission quality.
	// We exclude col 0 (the start state) since it carries no placed glyphs.
	bestEnd := -1
	bestCost := math.Inf(1)
	lo := max(1, w-maxAdv)
	hi := min(w+maxAdv, dpSize-1)
	// 0.1 per pixel of deviation: small enough not to override clear emission
	// signal (typical per-block MSE gaps are ≥ 50), but large enough to prefer
	// a full-coverage path over a shorter path with equal emission quality.
	const coveragePenaltyPerPx = 0.1
	for c := lo; c <= hi; c++ {
		if math.IsInf(dp[c], 1) {
			continue
		}
		adjusted := dp[c] + coveragePenaltyPerPx*math.Abs(float64(c-w))
		if adjusted < bestCost {
			bestCost = adjusted
			bestEnd = c
		}
	}

	if bestEnd < 0 || math.IsInf(dp[bestEnd], 1) {
		return nil, math.Inf(1)
	}

	// Backtrack from bestEnd to 0.
	var runes []rune
	for col := bestEnd; col > 0; {
		be := back[col]
		runes = append(runes, glyphs[be.gi].R)
		col = be.from
	}
	// Reverse in-place.
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return runes, dp[bestEnd]
}

// GlyphAdvancePixels converts a floating-point glyph advance width (in pixels)
// to a positive integer advance, clamping to at least 1.
func GlyphAdvancePixels(advPx, stretch float64) int {
	return max(1, int(math.Round(advPx*stretch)))
}

// EmissionCache is a flat map from (glyph-index, start-col) to emission cost.
// Each entry is computed at most once (first access triggers the render).
// It is not safe for concurrent use; each decoder goroutine should own its own.
type EmissionCache struct {
	data map[emissionKey]float64
}

// NewEmissionCache returns an empty EmissionCache.
func NewEmissionCache() *EmissionCache {
	return &EmissionCache{data: make(map[emissionKey]float64)}
}

// Get returns the cached emission cost and whether it was found.
func (c *EmissionCache) Get(gi, col int) (float64, bool) {
	v, ok := c.data[emissionKey{gi, col}]
	return v, ok
}

// Put stores an emission cost for (gi, col).
func (c *EmissionCache) Put(gi, col int, cost float64) {
	c.data[emissionKey{gi, col}] = cost
}

// Len returns the number of cached entries, for benchmark and ICP reporting.
func (c *EmissionCache) Len() int { return len(c.data) }
