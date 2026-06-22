package refmatch_test

import (
	"context"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/refmatch"
)

// makeUniformBlock returns a BlockSig with all channels set to the given values.
func makeUniformBlock(r, g, b uint8) refmatch.BlockSig {
	return refmatch.BlockSig{R: float64(r), G: float64(g), B: float64(b)}
}

// makeRGBA fills a new image.RGBA with the given colour.
func makeRGBA(w, h int, r, g, b uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := color.RGBA{R: r, G: g, B: b, A: 255}
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// TestBlockSigDist verifies that identical blocks have zero distance and
// differing blocks have positive distance, and that Dist is symmetric.
func TestBlockSigDist(t *testing.T) {
	a := makeUniformBlock(200, 150, 100)
	b := makeUniformBlock(200, 150, 100)
	if d := a.Dist(b); d != 0 {
		t.Errorf("Dist(same, same) = %v, want 0", d)
	}

	c := makeUniformBlock(0, 0, 0)
	if d := a.Dist(c); d <= 0 {
		t.Errorf("Dist(light, dark) = %v, want > 0", d)
	}

	// Symmetry.
	if da, dc := a.Dist(c), c.Dist(a); da != dc {
		t.Errorf("Dist not symmetric: %v != %v", da, dc)
	}

	// White→black distance should be 1.0 (full diagonal).
	white := makeUniformBlock(255, 255, 255)
	black := makeUniformBlock(0, 0, 0)
	if d := white.Dist(black); math.Abs(d-1.0) > 1e-9 {
		t.Errorf("Dist(white, black) = %v, want 1.0", d)
	}
}

// TestExtractBlocks verifies that ExtractBlocks returns the right grid size
// and that uniform blocks produce the exact expected signature.
func TestExtractBlocks(t *testing.T) {
	// 4×4 image with block size 2 → 2×2 = 4 blocks; each block is uniform.
	img := makeRGBA(4, 4, 100, 150, 200)
	grid := refmatch.ExtractBlocks(img.Pix, img.Stride, 4, 4, 2)

	if len(grid) != 2 {
		t.Fatalf("ExtractBlocks(4×4, block=2): got %d rows, want 2", len(grid))
	}
	for i, row := range grid {
		if len(row) != 2 {
			t.Errorf("row %d: got %d blocks, want 2", i, len(row))
		}
		for j, sig := range row {
			if math.Abs(sig.R-100) > 0.5 || math.Abs(sig.G-150) > 0.5 || math.Abs(sig.B-200) > 0.5 {
				t.Errorf("block[%d][%d] = %+v, want {R:100 G:150 B:200}", i, j, sig)
			}
		}
	}
}

// TestExtractBlocks_Empty verifies that an image smaller than the block size
// produces nil output without panicking.
func TestExtractBlocks_Empty(t *testing.T) {
	img := makeRGBA(1, 1, 255, 255, 255)
	grid := refmatch.ExtractBlocks(img.Pix, img.Stride, 1, 1, 8)
	if grid != nil {
		t.Errorf("ExtractBlocks(1×1, block=8): got %v, want nil", grid)
	}
}

// TestExtractBlocks_Mixed verifies that a 4-pixel-wide image with two
// different-coloured halves produces two distinct block signatures.
func TestExtractBlocks_Mixed(t *testing.T) {
	// 4×2 image: left 2 columns red, right 2 columns blue, block size 2.
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			img.SetRGBA(x+2, y, color.RGBA{B: 255, A: 255})
		}
	}
	grid := refmatch.ExtractBlocks(img.Pix, img.Stride, 4, 2, 2)
	if len(grid) != 1 || len(grid[0]) != 2 {
		t.Fatalf("ExtractBlocks(4×2, block=2): grid shape %dx%d, want 1×2",
			len(grid), func() int {
				if len(grid) > 0 {
					return len(grid[0])
				}
				return 0
			}())
	}
	left, right := grid[0][0], grid[0][1]
	if left.R < 200 || left.B > 10 {
		t.Errorf("left block = %+v, want red-dominant", left)
	}
	if right.B < 200 || right.R > 10 {
		t.Errorf("right block = %+v, want blue-dominant", right)
	}
}

// TestBlockRowDist verifies that identical rows score 0 and differing rows > 0,
// and that mismatched-length rows return +Inf.
func TestBlockRowDist(t *testing.T) {
	rowA := []refmatch.BlockSig{
		makeUniformBlock(100, 100, 100),
		makeUniformBlock(200, 200, 200),
	}
	rowB := []refmatch.BlockSig{
		makeUniformBlock(100, 100, 100),
		makeUniformBlock(200, 200, 200),
	}
	if d := refmatch.BlockRowDist(rowA, rowB); d != 0 {
		t.Errorf("BlockRowDist(same) = %v, want 0", d)
	}

	rowC := []refmatch.BlockSig{
		makeUniformBlock(0, 0, 0),
		makeUniformBlock(0, 0, 0),
	}
	if d := refmatch.BlockRowDist(rowA, rowC); d <= 0 {
		t.Errorf("BlockRowDist(different) = %v, want > 0", d)
	}

	// Mismatched lengths → +Inf.
	rowShort := []refmatch.BlockSig{makeUniformBlock(100, 100, 100)}
	if d := refmatch.BlockRowDist(rowA, rowShort); !math.IsInf(d, 1) {
		t.Errorf("BlockRowDist mismatched lengths = %v, want +Inf", d)
	}

	// Empty rows → 0.
	if d := refmatch.BlockRowDist(nil, nil); d != 0 {
		t.Errorf("BlockRowDist(nil, nil) = %v, want 0", d)
	}
}

// TestMatch_Identity verifies that a reference built from the same block
// signatures as the target recovers the input text exactly with zero distance.
func TestMatch_Identity(t *testing.T) {
	refA := &refmatch.GlyphRef{
		Rune:   'A',
		Blocks: [][]refmatch.BlockSig{{makeUniformBlock(200, 100, 50)}},
		Cols:   1,
	}
	refB := &refmatch.GlyphRef{
		Rune:   'B',
		Blocks: [][]refmatch.BlockSig{{makeUniformBlock(50, 100, 200)}},
		Cols:   1,
	}

	// Target: [A-column][B-column] — one block row, two block columns.
	target := [][]refmatch.BlockSig{
		{makeUniformBlock(200, 100, 50), makeUniformBlock(50, 100, 200)},
	}

	text, dists := refmatch.Match(t.Context(), target, []*refmatch.GlyphRef{refA, refB}, 4)
	if text != "AB" {
		t.Errorf("Match(identity) = %q, want %q", text, "AB")
	}
	if len(dists) != 2 {
		t.Fatalf("Match: got %d cell distances, want 2", len(dists))
	}
	if dists[0] != 0 || dists[1] != 0 {
		t.Errorf("Match: cell distances = %v, want [0 0]", dists)
	}
}

// TestMatch_BestFit verifies that Match picks the closer reference when there
// is an unambiguous winner.
func TestMatch_BestFit(t *testing.T) {
	refA := &refmatch.GlyphRef{
		Rune:   'A',
		Blocks: [][]refmatch.BlockSig{{makeUniformBlock(240, 240, 240)}},
		Cols:   1,
	}
	refB := &refmatch.GlyphRef{
		Rune:   'B',
		Blocks: [][]refmatch.BlockSig{{makeUniformBlock(10, 10, 10)}},
		Cols:   1,
	}

	// Target is nearly black → should match 'B'.
	target := [][]refmatch.BlockSig{{makeUniformBlock(15, 15, 15)}}
	text, _ := refmatch.Match(t.Context(), target, []*refmatch.GlyphRef{refA, refB}, 4)
	if text != "B" {
		t.Errorf("Match(dark target) = %q, want %q", text, "B")
	}
}

// TestMatch_MultiColGlyph verifies that a 2-column glyph advances by 2 columns
// and consumes the correct target blocks.
func TestMatch_MultiColGlyph(t *testing.T) {
	// Wide glyph 'W' covers 2 columns; narrow 'x' covers 1.
	refW := &refmatch.GlyphRef{
		Rune: 'W',
		Blocks: [][]refmatch.BlockSig{
			{makeUniformBlock(200, 100, 50), makeUniformBlock(210, 110, 60)},
		},
		Cols: 2,
	}
	refX := &refmatch.GlyphRef{
		Rune:   'x',
		Blocks: [][]refmatch.BlockSig{{makeUniformBlock(50, 200, 100)}},
		Cols:   1,
	}

	// Target: [W-col-0][W-col-1][x-col-0] — three columns.
	target := [][]refmatch.BlockSig{{
		makeUniformBlock(200, 100, 50),
		makeUniformBlock(210, 110, 60),
		makeUniformBlock(50, 200, 100),
	}}

	text, dists := refmatch.Match(t.Context(), target, []*refmatch.GlyphRef{refW, refX}, 4)
	if text != "Wx" {
		t.Errorf("Match(multi-col) = %q, want %q", text, "Wx")
	}
	if len(dists) != 2 {
		t.Fatalf("Match: got %d cell distances, want 2", len(dists))
	}
	if dists[0] != 0 || dists[1] != 0 {
		t.Errorf("Match: cell distances = %v, want [0 0]", dists)
	}
}

// TestMatch_Empty verifies that empty targets or empty reference tables return
// empty results without panicking.
func TestMatch_Empty(t *testing.T) {
	text, dists := refmatch.Match(t.Context(), nil, nil, 4)
	if text != "" || len(dists) != 0 {
		t.Errorf("Match(nil, nil) = %q %v, want empty", text, dists)
	}

	// Non-nil target, nil refs.
	target := [][]refmatch.BlockSig{{makeUniformBlock(100, 100, 100)}}
	text, dists = refmatch.Match(t.Context(), target, nil, 4)
	if text != "" || len(dists) != 0 {
		t.Errorf("Match(target, nil refs) = %q %v, want empty", text, dists)
	}
}

// TestMatch_CancelledContext verifies that a cancelled context causes Match to
// return early without panicking. The partial result (possibly empty) is valid.
func TestMatch_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // pre-cancel

	ref := &refmatch.GlyphRef{
		Rune:   'X',
		Blocks: [][]refmatch.BlockSig{{makeUniformBlock(100, 100, 100)}},
		Cols:   1,
	}
	target := [][]refmatch.BlockSig{{makeUniformBlock(100, 100, 100)}}

	// Must not panic; result may be empty due to pre-cancellation.
	text, _ := refmatch.Match(ctx, target, []*refmatch.GlyphRef{ref}, 4)
	_ = text // partial or empty — both are acceptable
}

var (
	sinkText  string
	sinkDists []float64
)

// BenchmarkMatch measures the reference-matching hot loop on a moderately-sized
// target (20 block-columns, 3 block rows, 26-glyph reference table). This is
// the per-image inner loop for DecodeReference.
func BenchmarkMatch(b *testing.B) {
	const (
		blockSize = 4
		cols      = 20
		rows      = 3
	)

	// Build a 26-glyph reference table (a–z), each 1 column wide.
	refs := make([]*refmatch.GlyphRef, 26)
	for i := range 26 {
		blocks := make([][]refmatch.BlockSig, rows)
		for r := range rows {
			blocks[r] = []refmatch.BlockSig{
				makeUniformBlock(uint8(i*9+r*3), uint8(i*5+r*7), uint8(i*3+r*11)),
			}
		}
		refs[i] = &refmatch.GlyphRef{
			Rune:   rune('a' + i),
			Blocks: blocks,
			Cols:   1,
		}
	}

	// Build a target with cols block-columns.
	target := make([][]refmatch.BlockSig, rows)
	for r := range rows {
		target[r] = make([]refmatch.BlockSig, cols)
		for c := range cols {
			target[r][c] = makeUniformBlock(uint8(c*11), uint8(c*7), uint8(c*3))
		}
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		text, dists := refmatch.Match(ctx, target, refs, blockSize)
		sinkText = text
		sinkDists = dists
	}
}
