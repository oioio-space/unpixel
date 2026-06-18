package search_test

import (
	"context"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

// sinkEvals defeats dead-code elimination for GuidedDFS benchmark results.
var sinkEvals []unpixel.Eval

// sinkOffsets defeats dead-code elimination for DiscoverOffsets benchmark results.
var sinkOffsets []unpixel.Offset

// BenchmarkDiscoverOffsets measures grid-origin discovery (64 origins × charset
// renders) with a real rendering pipeline. The workers_1 sub-benchmark forces
// sequential execution (the pre-fan-out baseline); workers_max uses GOMAXPROCS.
// Comparing the two with benchstat proves the fan-out speedup with no change to
// the sequential path.
func BenchmarkDiscoverOffsets(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	pix := pixelate.NewBlockAverage(8)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	img, _, err := r.Render("secret", style)
	if err != nil {
		b.Fatalf("render: %v", err)
	}
	redacted := pix.Pixelate(img, 0, 0)

	cfg := unpixel.Config{
		Charset:        "abcdefgh", // bounded charset keeps the bench duration sane
		BlockSize:      8,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		Style:          style,
		Renderer:       r,
		Pixelator:      pix,
		Metric:         metric.NewPixelmatch(0.02),
	}
	scorer := search.NewPipelineScorer(redacted, cfg)

	for _, w := range []struct {
		name    string
		workers int
	}{
		{"workers_1", 1},
		{"workers_max", 0},
	} {
		b.Run(w.name, func(b *testing.B) {
			c := cfg
			c.Workers = w.workers
			b.ReportAllocs()
			for b.Loop() {
				sinkOffsets = search.DiscoverOffsets(context.Background(), scorer, c, nil)
			}
		})
	}
}

// scriptedScorer returns fixed scores for any single-character guess that
// appears in its map; all others return 1.0 (pruned). Multi-character guesses
// inherit the last character's score, so the DFS can recurse predictably
// without real rendering.
type scriptedScorer struct {
	charScore map[rune]float64
}

func (s *scriptedScorer) Eval(_ context.Context, guess, _ string, _ unpixel.Offset) search.EvalResult {
	if guess == "" {
		return search.EvalResult{Score: 1.0}
	}
	lastRune := rune(guess[len(guess)-1])
	if score, ok := s.charScore[lastRune]; ok {
		return search.EvalResult{Score: score}
	}
	return search.EvalResult{Score: 1.0}
}

// BenchmarkGuidedDFS benchmarks the search bookkeeping cost (evalChildren,
// sorting, recursive dispatch) using a mock scorer that returns scripted scores
// — no real rendering is involved. Sub-benchmarks vary the charset width and
// hence the branching factor.
func BenchmarkGuidedDFS(b *testing.B) {
	cases := []struct {
		name    string
		charset string
		// fraction of charset chars that pass the threshold
		passFrac float64
	}{
		// Narrow charset: 10-char alphabet, all pass — high branching factor but
		// pruned quickly by MaxLength=3.
		{"charset10_allpass", "abcdefghij", 0.1},
		// Full default charset, half pass.
		{"charset27_halfpass", "abcdefghijklmnopqrstuvwxyz ", 0.2},
	}

	offset := unpixel.Offset{X: 3, Y: 3}

	for _, tc := range cases {
		// Build a scorer that returns passFrac * threshold for every rune so
		// that all characters pass (score < threshold).
		scores := make(map[rune]float64, len(tc.charset))
		for _, ch := range tc.charset {
			scores[ch] = tc.passFrac
		}
		scorer := &scriptedScorer{charScore: scores}

		cfg := unpixel.Config{
			Charset:        tc.charset,
			MaxLength:      3, // keep bench duration bounded
			Threshold:      0.25,
			SpaceThreshold: 0.5,
		}

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var evals []unpixel.Eval
				search.GuidedDFS(context.Background(), scorer, cfg, offset, func(e unpixel.Eval) {
					evals = append(evals, e)
				})
				sinkEvals = evals
			}
		})
	}
}
