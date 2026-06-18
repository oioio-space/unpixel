package render_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/render"
)

// sinkImg and sinkInt defeat dead-code elimination for render benchmark results.
var (
	sinkImg *image.RGBA
	sinkInt int
)

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
