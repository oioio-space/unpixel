package multiframe_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/multiframe"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// sink defeats dead-code elimination on benchmark results.
var sink *image.RGBA

// sinkFrames defeats dead-code elimination on DiscoverPhases results.
var sinkFrames []multiframe.Frame

// BenchmarkFuse measures the cost of fusing phase-diverse mosaics via IBP
// (3 iterations, the default). Run with -count=10 for benchstat output.
//
// Sub-benchmarks cover two representative image sizes and block sizes so
// throughput (MB/s) can be compared across configurations.
func BenchmarkFuse(b *testing.B) {
	cases := []struct {
		name   string
		w, h   int
		block  int
		phases [][2]int
	}{
		{
			name:   "64x64_block8_F4",
			w:      64,
			h:      64,
			block:  8,
			phases: [][2]int{{0, 0}, {2, 0}, {0, 3}, {3, 3}},
		},
		{
			name:   "256x64_block10_F4",
			w:      256,
			h:      64,
			block:  10,
			phases: [][2]int{{0, 0}, {3, 0}, {0, 5}, {3, 5}},
		},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			src := syntheticSource(tc.w, tc.h)
			pix := pixelate.NewBlockAverage(tc.block)

			frames := make([]multiframe.Frame, len(tc.phases))
			for i, ph := range tc.phases {
				mosaic := pix.Pixelate(src, ph[0], ph[1])
				frames[i] = multiframe.Frame{Img: mosaic, OffsetX: ph[0], OffsetY: ph[1]}
			}

			// Bytes = output pixels × 4 channels (the work done per Fuse call).
			b.SetBytes(int64(tc.w * tc.h * 4))
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				fused, err := multiframe.Fuse(frames, tc.block)
				if err != nil {
					b.Fatal(err)
				}
				sink = fused
			}
		})
	}
}

// BenchmarkDiscoverPhases measures the cost of automatic phase detection over a
// realistic small frame set (4 frames, 256×64, block 8). Run with -count=10
// for benchstat output.
func BenchmarkDiscoverPhases(b *testing.B) {
	const (
		W     = 256
		H     = 64
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	phases := [][2]int{{0, 0}, {3, 0}, {0, 4}, {5, 2}}
	frames := make([]multiframe.Frame, len(phases))
	for i, ph := range phases {
		frames[i] = multiframe.Frame{Img: pix.Pixelate(src, ph[0], ph[1])}
	}

	// Bytes = pixels per frame × frames × 4 channels.
	b.SetBytes(int64(W * H * 4 * len(frames)))
	b.ReportAllocs()

	for b.Loop() {
		sinkFrames = multiframe.DiscoverPhases(frames, block)
	}
}
