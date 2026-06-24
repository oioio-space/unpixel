package render_test

import (
	"image"
	"sync/atomic"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/render"
)

// sinkImg and sinkInt defeat dead-code elimination for render benchmark results.
var (
	sinkImg *image.RGBA
	sinkInt int
)

// BenchmarkXImage_Render_Parallel exercises Render under concurrent load — the
// scenario where GOMAXPROCS goroutines all call Render simultaneously (offset
// fan-out). Run with -cpu=1,4,8,20 to see how throughput scales; the mutex-bound
// baseline should be flat/negative beyond -cpu=1, while the pool implementation
// should scale linearly with P.
func BenchmarkXImage_Render_Parallel(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("NewXImage: %v", err)
	}

	cases := []struct {
		name     string
		text     string
		fontSize float64
	}{
		{"default_32pt", "the quick brown", 32},
		{"small_24pt", "the quick brown", 24},
	}

	for _, tc := range cases {
		style := unpixel.Style{
			FontSize:    tc.fontSize,
			PaddingTop:  8,
			PaddingLeft: 8,
		}
		// Warm the face pool for this size before timing starts.
		if _, _, err := r.Render(tc.text, style); err != nil {
			b.Fatalf("warm-up Render(%s): %v", tc.name, err)
		}

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			// sinkImgAtomic + sinkIntAtomic: goroutine-safe sinks to defeat
			// dead-code elimination without introducing a data race.
			var sinkImgAtomic atomic.Pointer[image.RGBA]
			var sinkIntAtomic atomic.Int64
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					img, sentinelX, err := r.Render(tc.text, style)
					if err != nil {
						b.Error(err)
						return
					}
					sinkImgAtomic.Store(img)
					sinkIntAtomic.Store(int64(sentinelX))
				}
			})
			// Flush sinks so the compiler cannot elide the stores.
			sinkImg = sinkImgAtomic.Load()
			sinkInt = int(sinkIntAtomic.Load())
		})
	}
}

// BenchmarkXImage_Render benchmarks the per-call cost of Render after the
// renderer and its font faces are fully warmed up. The renderer is constructed
// once outside the loop so the bench measures only the hot Render path
// (text measurement, image allocation, draw, sentinel fill).
//
// Sub-benchmarks at the default size (32 pt, cache hit) and a second size
// (24 pt, also a cache hit after the first iteration) show the caching benefit.
func BenchmarkXImage_Render(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("NewXImage: %v", err)
	}

	cases := []struct {
		name     string
		text     string
		fontSize float64
	}{
		{"default_32pt", "the quick brown", 32},
		{"small_24pt", "the quick brown", 24},
	}

	for _, tc := range cases {
		style := unpixel.Style{
			FontSize:    tc.fontSize,
			PaddingTop:  8,
			PaddingLeft: 8,
		}
		// Warm the face cache for this size before timing starts.
		if _, _, err := r.Render(tc.text, style); err != nil {
			b.Fatalf("warm-up Render(%s): %v", tc.name, err)
		}

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				img, sentinelX, err := r.Render(tc.text, style)
				if err != nil {
					b.Fatal(err)
				}
				sinkImg = img
				sinkInt = sentinelX
			}
		})
	}
}
