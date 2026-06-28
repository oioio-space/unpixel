package pixelate_test

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/refmatch"
)

// extractViaPixelate runs Pixelate then ExtractBlocksDirect — the reference
// two-pass path — and returns the resulting grid.
func extractViaPixelate(p *pixelate.BlockAverage, src *image.RGBA, originX, originY, block int) [][]refmatch.BlockSig {
	dst := p.Pixelate(src, originX, originY)
	pb := dst.Bounds()
	return refmatch.ExtractBlocksDirect(dst.Pix, dst.Stride, pb.Dx(), pb.Dy(), block)
}

// sigEqual reports whether two BlockSig slices (rows) are identical.
func sigEqual(a, b []refmatch.BlockSig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// gridEqual reports whether two block grids are identical.
func gridEqual(a, b [][]refmatch.BlockSig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sigEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// makeCheckerboard builds a w×h RGBA image with a checkerboard pattern whose
// squares are bs×bs pixels — useful for testing block-average boundaries.
func makeCheckerboard(w, h, bs int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if (x/bs+y/bs)%2 == 0 {
				img.SetRGBA(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{R: 30, G: 150, B: 220, A: 255})
			}
		}
	}
	return img
}

// TestPixelateToGrid_ByteIdentity asserts that PixelateToGrid produces
// bit-identical output to ExtractBlocksDirect(Pixelate(src, originX, originY))
// across several image sizes, origins, and pixel modes (sRGB and linear-light).
func TestPixelateToGrid_ByteIdentity(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		src     *image.RGBA
		originX int
		originY int
		block   int
		linear  bool
	}

	cases := []testCase{
		// --- sRGB mode ---
		{
			name:  "sRGB/exact-multiple/zero-origin",
			src:   makeGradient(16, 16),
			block: 8,
		},
		{
			name:  "sRGB/padded-width/zero-origin",
			src:   makeGradient(10, 10), // width 10, padded to 16
			block: 8,
		},
		{
			name:    "sRGB/exact-multiple/nonzero-origin",
			src:     makeGradient(24, 24),
			originX: 3,
			originY: 3,
			block:   8,
		},
		{
			name:    "sRGB/padded-width/nonzero-origin",
			src:     makeGradient(14, 20), // width 14, padded to 16
			originX: 5,
			originY: 2,
			block:   8,
		},
		{
			name:  "sRGB/checkerboard/block4",
			src:   makeCheckerboard(32, 32, 4),
			block: 4,
		},
		{
			name:    "sRGB/checkerboard/block4/nonzero-origin",
			src:     makeCheckerboard(30, 28, 4), // padded to 32×28
			originX: 2,
			originY: 1,
			block:   4,
		},
		{
			name:  "sRGB/realistic-redaction-size",
			src:   makePixelateSrc(), // 264×40
			block: 8,
		},
		{
			name:    "sRGB/realistic-redaction-size/nonzero-origin",
			src:     makePixelateSrc(), // 264×40
			originX: 3,
			originY: 3,
			block:   8,
		},
		// --- linear-light mode ---
		{
			name:   "linear/exact-multiple/zero-origin",
			src:    makeGradient(16, 16),
			block:  8,
			linear: true,
		},
		{
			name:   "linear/padded-width/zero-origin",
			src:    makeGradient(10, 10),
			block:  8,
			linear: true,
		},
		{
			name:    "linear/exact-multiple/nonzero-origin",
			src:     makeGradient(24, 24),
			originX: 3,
			originY: 3,
			block:   8,
			linear:  true,
		},
		{
			name:   "linear/checkerboard/block4",
			src:    makeCheckerboard(32, 32, 4),
			block:  4,
			linear: true,
		},
		{
			name:   "linear/realistic-redaction-size",
			src:    makePixelateSrc(),
			block:  8,
			linear: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var p *pixelate.BlockAverage
			if tc.linear {
				p = pixelate.NewLinearBlockAverage(tc.block)
			} else {
				p = pixelate.NewBlockAverage(tc.block)
			}

			want := extractViaPixelate(p, cloneRGBA(tc.src), tc.originX, tc.originY, tc.block)
			got := p.PixelateToGrid(cloneRGBA(tc.src), tc.originX, tc.originY)

			if !gridEqual(got, want) {
				// Find first mismatch for a useful error message.
				for r := range max(len(got), len(want)) {
					if r >= len(got) {
						t.Errorf("row %d: got missing, want %v", r, want[r])
						break
					}
					if r >= len(want) {
						t.Errorf("row %d: got %v, want missing", r, got[r])
						break
					}
					for c := range max(len(got[r]), len(want[r])) {
						var g, w refmatch.BlockSig
						if c < len(got[r]) {
							g = got[r][c]
						}
						if c < len(want[r]) {
							w = want[r][c]
						}
						if g != w {
							t.Errorf("block [%d][%d]: got {R:%.0f G:%.0f B:%.0f}, want {R:%.0f G:%.0f B:%.0f}",
								r, c, g.R, g.G, g.B, w.R, w.G, w.B)
						}
					}
				}
				t.Fatalf("PixelateToGrid not byte-identical to Pixelate+ExtractBlocksDirect (first mismatch above); grid dimensions: got %d×%d, want %d×%d",
					len(got), func() int {
						if len(got) > 0 {
							return len(got[0])
						}
						return 0
					}(),
					len(want), func() int {
						if len(want) > 0 {
							return len(want[0])
						}
						return 0
					}())
			}
		})
	}
}

// sinkGrid defeats dead-code elimination for PixelateToGrid benchmarks.
var sinkGrid [][]refmatch.BlockSig

// BenchmarkPixelateToGrid benchmarks the fused pixelate-to-grid path on a
// 264×40 RGBA image at a non-zero origin. Compare against
// BenchmarkBlockAverage_Pixelate + ExtractBlocksDirect to measure the saving
// from eliminating the full-size alloc and block fill.
func BenchmarkPixelateToGrid(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewBlockAverage(8)
	b.ReportAllocs()
	for b.Loop() {
		sinkGrid = p.PixelateToGrid(src, 3, 3)
	}
}

// BenchmarkLinearPixelateToGrid benchmarks the fused path in linear-light mode.
func BenchmarkLinearPixelateToGrid(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewLinearBlockAverage(8)
	b.ReportAllocs()
	for b.Loop() {
		sinkGrid = p.PixelateToGrid(src, 3, 3)
	}
}

// BenchmarkPixelateAndExtract benchmarks the two-pass reference path:
// Pixelate followed by ExtractBlocksDirect. Used as the baseline for
// comparison against BenchmarkPixelateToGrid.
func BenchmarkPixelateAndExtract(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewBlockAverage(8)
	b.ReportAllocs()
	for b.Loop() {
		dst := p.Pixelate(src, 3, 3)
		pb := dst.Bounds()
		sinkGrid = refmatch.ExtractBlocksDirect(dst.Pix, dst.Stride, pb.Dx(), pb.Dy(), 8)
	}
}

// BenchmarkLinearPixelateAndExtract benchmarks the two-pass path in
// linear-light mode.
func BenchmarkLinearPixelateAndExtract(b *testing.B) {
	src := makePixelateSrc()
	p := pixelate.NewLinearBlockAverage(8)
	b.ReportAllocs()
	for b.Loop() {
		dst := p.Pixelate(src, 3, 3)
		pb := dst.Bounds()
		sinkGrid = refmatch.ExtractBlocksDirect(dst.Pix, dst.Stride, pb.Dx(), pb.Dy(), 8)
	}
}

// TestPixelateToGridDimensions verifies that PixelateToGrid returns the same
// grid dimensions as ExtractBlocksDirect(Pixelate(...)) for a range of sizes.
func TestPixelateToGridDimensions(t *testing.T) {
	t.Parallel()
	p := pixelate.NewBlockAverage(8)
	for _, wh := range [][2]int{{16, 16}, {10, 10}, {264, 40}, {261, 40}, {8, 8}, {7, 7}} {
		w, h := wh[0], wh[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			src := makeGradient(w, h)
			want := extractViaPixelate(p, cloneRGBA(src), 0, 0, 8)
			got := p.PixelateToGrid(cloneRGBA(src), 0, 0)
			if len(got) != len(want) {
				t.Errorf("rows: got %d, want %d", len(got), len(want))
			}
			if len(got) > 0 && len(want) > 0 && len(got[0]) != len(want[0]) {
				t.Errorf("cols: got %d, want %d", len(got[0]), len(want[0]))
			}
		})
	}
}
