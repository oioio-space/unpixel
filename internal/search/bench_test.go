package search_test

import (
	"context"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

// BenchmarkGuidedSearch exercises the multi-character DFS on a real rendering
// pipeline, so it covers the prevGuess marginal-region path (render + pixelate +
// diff + metric) that DiscoverOffsets (prevGuess == "") does not.
func BenchmarkGuidedSearch(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	spec := fixture.Spec{Text: "ab", Charset: "ab ", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8}
	redacted, err := fixture.Redact(spec)
	if err != nil {
		b.Fatalf("redact: %v", err)
	}
	cfg := unpixel.Config{
		Charset:        spec.Charset,
		MaxLength:      3,
		BlockSize:      spec.BlockSize,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		Style:          spec.Style(),
		Renderer:       r,
		Pixelator:      pixelate.NewBlockAverage(spec.BlockSize),
		Metric:         metric.NewPixelmatch(0.02),
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	b.ReportAllocs()
	for b.Loop() {
		// Fresh scorer each iteration so the render cache doesn't hide the work.
		scorer := search.NewPipelineScorer(redacted, cfg)
		var evals []unpixel.Eval
		search.GuidedDFS(context.Background(), scorer, cfg, offset, func(e unpixel.Eval) {
			evals = append(evals, e)
		})
		sinkEvals = evals
	}
}

// BenchmarkGuidedSearch_cached exercises GuidedDFS with a CachingScorer shared
// across iterations, matching the production wiring in GuidedStrategy.Search.
// Unlike BenchmarkGuidedSearch (which builds a fresh PipelineScorer each
// iteration to measure cold-path cost), this benchmark measures the warm-cache
// steady-state: stageImage hits are served from the LRU and only the metric
// step runs. benchstat against the uncached variant proves the cache win.
func BenchmarkGuidedSearch_cached(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	spec := fixture.Spec{Text: "ab", Charset: "ab ", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8}
	redacted, err := fixture.Redact(spec)
	if err != nil {
		b.Fatalf("redact: %v", err)
	}
	cfg := unpixel.Config{
		Charset:        spec.Charset,
		MaxLength:      3,
		BlockSize:      spec.BlockSize,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		Style:          spec.Style(),
		Renderer:       r,
		Pixelator:      pixelate.NewBlockAverage(spec.BlockSize),
		Metric:         metric.NewPixelmatch(0.02),
		CacheSize:      unpixel.DefaultCacheSize,
	}
	offset := unpixel.Offset{X: 0, Y: 0}
	// Shared scorer across iterations — this is what GuidedStrategy.Search does.
	scorer := search.NewCachingScorer(search.NewPipelineScorer(redacted, cfg), cfg.CacheSize)

	b.ReportAllocs()
	for b.Loop() {
		var evals []unpixel.Eval
		search.GuidedDFS(context.Background(), scorer, cfg, offset, func(e unpixel.Eval) {
			evals = append(evals, e)
		})
		sinkEvals = evals
	}
}

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

// uniformLM is a language model that assigns equal weight to every candidate
// string. Used by BenchmarkGuidedDFS_wideCharset to activate the TopK pruning
// path without biasing which characters are selected.
func uniformLM(string) float64 { return 1 }

// BenchmarkGuidedSearch_wideCharset measures GuidedDFS on a real rendering
// pipeline with a wide charset (CharsetASCII, 95 runes) so the intra-node
// parallel path (parChildThreshold=16) fires. Sub-benchmarks vary Workers:
//
//   - workers_1:   sequential offset fan-out → intraNodeWorkers = GOMAXPROCS,
//     so child eval is fully parallel inside the single offset goroutine.
//   - workers_max: GOMAXPROCS offset goroutines → intraNodeWorkers = 1,
//     so child eval falls back to sequential (offset-level parallelism saturates).
//
// benchstat against bench-baseline.txt proves whether intra-node parallelism
// helps on a real pipeline with a wide charset.
func BenchmarkGuidedSearch_wideCharset(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	spec := fixture.Spec{Text: "ab", Charset: unpixel.CharsetASCII, FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8}
	redacted, err := fixture.Redact(spec)
	if err != nil {
		b.Fatalf("redact: %v", err)
	}

	baseCfg := unpixel.Config{
		Charset:        unpixel.CharsetASCII,
		MaxLength:      2, // bounded: 95² = 9025 nodes without pruning
		BlockSize:      spec.BlockSize,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		Style:          spec.Style(),
		Renderer:       r,
		Pixelator:      pixelate.NewBlockAverage(spec.BlockSize),
		Metric:         metric.NewPixelmatch(0.02),
	}
	offset := unpixel.Offset{X: 0, Y: 0}

	for _, w := range []struct {
		name    string
		workers int
	}{
		{"workers_1", 1},
		{"workers_max", 0},
	} {
		b.Run(w.name, func(b *testing.B) {
			cfg := baseCfg
			cfg.Workers = w.workers
			scorer := search.NewPipelineScorer(redacted, cfg)
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

// BenchmarkGuidedDFS_wideCharset measures the auto-K heuristic (P3.11): with
// CharsetASCII (~95 chars) and a LanguageModel set, effectiveTopK fires and
// reduces each evalChildren call from 95 to autoTopKValue evaluations. The
// sub-benchmarks compare:
//
//   - no_lm: wide charset, no language model → whole-charset path (baseline).
//   - with_lm: wide charset + language model → auto-K path (should be faster).
//
// benchstat against bench-baseline.txt proves the speedup on the with_lm case
// and neutrality on the no_lm case.
func BenchmarkGuidedDFS_wideCharset(b *testing.B) {
	// A scripted scorer where every charset character passes (score=0.05 < 0.25
	// threshold). This maximises work per depth level and keeps results
	// deterministic independent of which characters the LM selects.
	scores := make(map[rune]float64, len(unpixel.CharsetASCII))
	for _, ch := range unpixel.CharsetASCII {
		scores[ch] = 0.05
	}
	scorer := &scriptedScorer{charScore: scores}
	offset := unpixel.Offset{X: 0, Y: 0}

	cases := []struct {
		name string
		cfg  unpixel.Config
	}{
		{
			name: "no_lm",
			cfg: unpixel.Config{
				Charset:        unpixel.CharsetASCII,
				MaxLength:      2, // bounded: 95² = 9025 nodes without pruning
				Threshold:      0.25,
				SpaceThreshold: 0.5,
				// No LanguageModel → whole-charset path, unchanged from before.
			},
		},
		{
			name: "with_lm",
			cfg: unpixel.Config{
				Charset:        unpixel.CharsetASCII,
				MaxLength:      2,
				Threshold:      0.25,
				SpaceThreshold: 0.5,
				// uniformLM activates auto-K: charset(95) ≥ autoTopKThreshold(40)
				// → effectiveTopK = autoTopKValue(24); 24² = 576 nodes (~94% fewer).
				LanguageModel: uniformLM,
			},
		},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var evals []unpixel.Eval
				search.GuidedDFS(context.Background(), scorer, tc.cfg, offset, func(e unpixel.Eval) {
					evals = append(evals, e)
				})
				sinkEvals = evals
			}
		})
	}
}
