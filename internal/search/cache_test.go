package search_test

import (
	"sync"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// buildCachingFixture returns a CachingScorer and matching Config backed by the
// real PipelineScorer so that cache hits are compared against genuine renders.
func buildCachingFixture(t *testing.T, maxEntries int) (*search.CachingScorer, unpixel.Config) {
	t.Helper()
	inner, cfg, _, _ := buildScorerFixture(t)
	return search.NewCachingScorer(inner, maxEntries), cfg
}

// TestCachingScorer_hitMatchesMiss verifies that a second Eval with the same key
// returns an EvalResult identical to the first (cache miss) result.
func TestCachingScorer_hitMatchesMiss(t *testing.T) {
	cs, _ := buildCachingFixture(t, 64)
	offset := unpixel.Offset{X: 0, Y: 0}

	first := cs.Eval(t.Context(), "a", "", offset)
	second := cs.Eval(t.Context(), "a", "", offset)

	if first != second {
		t.Errorf("cache hit differs from miss: first=%+v second=%+v", first, second)
	}

	_, misses := cs.Stats()
	if misses != 1 {
		t.Errorf("got %d misses, want 1 (first call only)", misses)
	}
	hits, _ := cs.Stats()
	if hits != 1 {
		t.Errorf("got %d hits, want 1 (second call)", hits)
	}
}

// TestCachingScorer_lruEvicts verifies that the LRU evicts the oldest (least
// recently used) entry when the cache reaches its capacity.
//
// Sequence with maxEntries=2:
//  1. Eval "a" → miss (cache: [a])
//  2. Eval "b" → miss (cache: [a, b])
//  3. Eval "c" → miss, evicts "a" (cache: [b, c])
//  4. Eval "b" → hit (still in cache)
//  5. Eval "a" → miss (was evicted)
func TestCachingScorer_lruEvicts(t *testing.T) {
	cs, _ := buildCachingFixture(t, 2)
	offset := unpixel.Offset{X: 0, Y: 0}

	cs.Eval(t.Context(), "a", "", offset) // miss 1
	cs.Eval(t.Context(), "b", "", offset) // miss 2
	cs.Eval(t.Context(), "c", "", offset) // miss 3, evicts "a"

	hits, misses := cs.Stats()
	if misses != 3 {
		t.Errorf("after 3 distinct evals: got %d misses, want 3", misses)
	}
	if hits != 0 {
		t.Errorf("after 3 distinct evals: got %d hits, want 0", hits)
	}

	cs.Eval(t.Context(), "b", "", offset) // hit — "b" still in cache
	hits, _ = cs.Stats()
	if hits != 1 {
		t.Errorf("re-eval 'b': got %d hits, want 1", hits)
	}

	cs.Eval(t.Context(), "a", "", offset) // miss — "a" was evicted
	_, misses = cs.Stats()
	if misses != 4 {
		t.Errorf("re-eval evicted 'a': got %d misses, want 4", misses)
	}
}

// TestCachingScorer_race verifies that concurrent Eval calls from multiple
// goroutines produce no data races. Run with go test -race.
func TestCachingScorer_race(t *testing.T) {
	cs, _ := buildCachingFixture(t, 8)
	offset := unpixel.Offset{X: 0, Y: 0}

	const goroutines = 8
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for _, g := range []string{"a", "b", "a", "c", "b"} {
				cs.Eval(t.Context(), g, "", offset)
			}
		})
	}
	wg.Wait()
}

// TestCachingScorer_disabled verifies that NewCachingScorer with maxEntries=0
// still returns valid results (cache simply never stores anything).
func TestCachingScorer_disabled(t *testing.T) {
	inner, cfg, _, _ := buildScorerFixture(t)
	cs := search.NewCachingScorer(inner, 0)
	offset := unpixel.Offset{X: 0, Y: 0}
	_ = cfg

	got := cs.Eval(t.Context(), "a", "", offset)
	if got.Score < 0 || got.Score > 1 {
		t.Errorf("CachingScorer(maxEntries=0).Eval: Score=%v out of [0,1]", got.Score)
	}

	// With maxEntries=0 every call is a miss (nothing is stored).
	cs.Eval(t.Context(), "a", "", offset)
	_, misses := cs.Stats()
	if misses != 2 {
		t.Errorf("maxEntries=0: got %d misses, want 2 (no caching)", misses)
	}
}
