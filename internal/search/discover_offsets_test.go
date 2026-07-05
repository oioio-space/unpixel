package search_test

import (
	"context"
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// inkedGlyphRenderer is a mock Renderer used by TestDiscoverOffsets_excludesBlankGlyphs.
// It returns an all-white image for chars in the blank set, and an image with a
// single black pixel for all others.
type inkedGlyphRenderer struct {
	blank map[rune]bool
}

func (r *inkedGlyphRenderer) Render(text string, _ unpixel.Style) (*image.RGBA, int, error) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	// Fill white (background).
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	const sentinelX = 10
	runes := []rune(text)
	if len(runes) == 1 && !r.blank[runes[0]] {
		// Place a single black pixel to mark this glyph as inked.
		off := img.PixOffset(5, 5)
		img.Pix[off] = 0   // R
		img.Pix[off+1] = 0 // G
		img.Pix[off+2] = 0 // B
	}
	return img, sentinelX, nil
}

// offsetAwareScorer returns scripted scores keyed on (guess, offset); unknowns use def.
type offsetAwareScorer struct {
	scores map[string]map[unpixel.Offset]search.EvalResult
	def    search.EvalResult
}

func (s *offsetAwareScorer) Eval(_ context.Context, guess, _ string, offset unpixel.Offset) search.EvalResult {
	if byOffset, ok := s.scores[guess]; ok {
		if r, ok2 := byOffset[offset]; ok2 {
			return r
		}
	}
	return s.def
}

// TestDiscoverOffsets_excludesBlankGlyphs is the TDD guard for the blank-glyph
// phase-contamination bug.
//
// Setup: charset "A " (inked 'A' + blank space).
//   - Space scores 0.0 at EVERY offset (blank matches anything → no phase info).
//   - 'A' scores 0.1 only at offset (0,0); 0.9 everywhere else.
//
// Before the fix: space contaminates bestScore → all four 2×2 origins tie at
// 0.0, and the true origin (0,0) is not distinguishable.
//
// After the fix: space is excluded; only 'A' is used → (0,0) is the sole
// survivor (0.1 < threshold=0.25) and ranks first.
func TestDiscoverOffsets_excludesBlankGlyphs(t *testing.T) {
	origin := unpixel.Offset{X: 0, Y: 0}
	scorer := &offsetAwareScorer{
		scores: map[string]map[unpixel.Offset]search.EvalResult{
			"A": {
				origin: {Score: 0.1}, // true phase: low score only here
			},
			" ": {
				// Space scores 0.0 everywhere — no phase discrimination.
				{X: 0, Y: 0}: {Score: 0.0},
				{X: 1, Y: 0}: {Score: 0.0},
				{X: 0, Y: 1}: {Score: 0.0},
				{X: 1, Y: 1}: {Score: 0.0},
			},
		},
		def: search.EvalResult{Score: 0.9}, // 'A' at non-origin offsets
	}
	renderer := &inkedGlyphRenderer{
		blank: map[rune]bool{' ': true}, // space renders blank; 'A' has ink
	}
	cfg := unpixel.Config{
		Charset:   "A ",
		BlockSize: 2,    // 4 origins: (0,0),(1,0),(0,1),(1,1)
		Threshold: 0.25, // 0.1 passes; 0.9 fails
		Workers:   1,    // sequential for determinism
		Renderer:  renderer,
	}

	offsets := search.DiscoverOffsets(t.Context(), scorer, cfg, nil)

	if len(offsets) == 0 {
		t.Fatal("DiscoverOffsets returned no offsets; want exactly (0,0)")
	}
	top := offsets[0]
	if top.X != 0 || top.Y != 0 {
		t.Errorf("top offset = (%d,%d), want (0,0); offsets=%v", top.X, top.Y, offsets)
	}
	// With the fix, only the true phase should survive — not all four.
	if len(offsets) > 1 {
		t.Errorf("got %d surviving offsets, want 1 (only the true phase); offsets=%v", len(offsets), offsets)
	}
}
