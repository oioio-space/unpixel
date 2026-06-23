// widthfilter.go — advance-width pre-filter for wordPool (roadmap item #2).
//
// # Motivation
//
// Dictionary candidates are initially selected by rune count (±1), which is
// an approximation: proportional fonts assign different advances to different
// glyphs, so "ill" and "www" both have 3 runes but very different pixel widths.
// The advance-width attack (arXiv 2206.02285) shows that glyph-position width
// alone recovers 81 % of PDF redactions.
//
// # Algorithm
//
// For each candidate string s in the per-band pool:
//  1. Look up the cached rendered pixel width of s (populated on first use by
//     calling Render and reading sentinelX — the same path the scorer uses).
//  2. Accept s when |renderedWidth(s) − bandWidth| ≤ tolerance, where tolerance
//     is one block size (opts.Block). One block of slack covers rounding in the
//     ink-scan crop and minor band-width estimation error.
//
// The filter runs BEFORE image scoring, so every pruned candidate avoids a full
// render → re-pixelate → metric call. The rendered widths are cached in a
// per-Decoder map so repeated calls for the same word are O(1).
//
// # Safety
//
// Set DisableWidthFilter: true in Options to skip the filter entirely and
// restore byte-identical behaviour to the pre-filter code path.
//
// The tolerance (one block) is conservative. If a word is on the boundary it
// stays in the pool; the image scorer then arbitrates. In practice the correct
// candidate renders to within a few pixels of the band width, well inside one
// block (8 px at the default block size), so it is never dropped.

package blinddecode

import (
	"github.com/oioio-space/unpixel"
)

// renderedWidth returns the pixel width of text as the renderer measures it:
// the sentinelX value returned by Render with the current font/size/spacing,
// which equals paddingLeft + textAdvance. Because blinddecode always passes
// PaddingLeft=0 in its Style, sentinelX equals the text advance in pixels.
//
// Results are cached in d.widthCache; the first call for each string pays the
// Render overhead; subsequent calls are O(1) map lookups.
func (d *Decoder) renderedWidth(s string) int {
	if w, ok := d.widthCache[s]; ok {
		return w
	}
	_, sx, err := d.opts.Renderer.Render(s, unpixel.Style{
		FontSize:      d.opts.FontSize,
		LetterSpacing: d.opts.LetterSpacing,
	})
	if err != nil || sx <= 0 {
		// On error, return the rune-count approximation so the candidate is
		// not spuriously dropped by the caller (conservative fallback).
		approx := int(float64(len([]rune(s))) * d.avgAdvance)
		d.widthCache[s] = approx
		return approx
	}
	d.widthCache[s] = sx
	return sx
}

// widthFits reports whether a candidate string s can fit the given bandWidthPx
// within ±tolPx pixels. One block of tolerance (opts.Block) is the recommended
// value; callers pass it explicitly so the function is testable in isolation.
func widthFits(candidateWidth, bandWidth, tolPx int) bool {
	diff := candidateWidth - bandWidth
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolPx
}
