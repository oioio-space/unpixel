// Package search (cache.go) provides CachingScorer, an LRU-backed wrapper
// around PipelineScorer that memoises the render→pixelate→crop steps (1–7)
// to avoid redundant work when the same prefix is evaluated at the same offset.
package search

import (
	"container/list"
	"context"
	"fmt"
	"hash/fnv"
	"image"
	"sync"

	"github.com/oioio-space/unpixel"
)

// cacheKey identifies a unique stageImage input. styleKey is an FNV-64 hash
// of the Style fields so that a config change cannot return a stale render.
type cacheKey struct {
	guess     string
	ox, oy    int
	blockSize int
	styleKey  uint64
}

// cacheEntry is the value stored in the LRU cache for one cacheKey.
// The img field must not be mutated after the entry is stored; see the
// stageResult.img doc comment for the immutability invariant.
type cacheEntry struct {
	element *list.Element // back-pointer into the LRU list
	sr      stageResult
}

// CachingScorer wraps a PipelineScorer and memoises stageImage (steps 1–7)
// results in an LRU cache keyed on (guess, offset, blockSize, styleKey).
//
// Thread safety: all public methods are safe for concurrent use.
// The render is performed outside the lock so that a cache miss does not block
// other goroutines; a benign duplicate render may occur if two goroutines race
// on the same key, but the result is always correct.
//
// Cache entries are treated as immutable: imutil.Crop always returns a fresh
// *image.RGBA, so sharing the cached img across Eval calls is safe.
type CachingScorer struct {
	inner      *PipelineScorer
	lru        *list.List // front = most recently used
	entries    map[cacheKey]*cacheEntry
	maxEntries int
	styleKey   uint64

	hits   int
	misses int

	mu sync.Mutex
}

// NewCachingScorer returns a CachingScorer that wraps inner with an LRU cache
// of up to maxEntries stageImage results. When maxEntries <= 0 the cache is
// disabled: every Eval call goes directly to the inner scorer.
func NewCachingScorer(inner *PipelineScorer, maxEntries int) *CachingScorer {
	sk := styleHash(inner.cfg.Style)
	return &CachingScorer{
		inner:      inner,
		maxEntries: maxEntries,
		styleKey:   sk,
		lru:        list.New(),
		entries:    make(map[cacheKey]*cacheEntry),
	}
}

// styleHash returns an FNV-64a hash of all Style fields that influence
// the rendered image. A change to any field produces a different key,
// preventing stale renders from being returned.
func styleHash(st unpixel.Style) uint64 {
	h := fnv.New64a()
	// NUL separators keep distinct field combinations from colliding on field
	// boundaries. Computed once per scorer (Style is fixed for a search).
	_, _ = fmt.Fprintf(h, "%v\x00%v\x00%v\x00%v\x00%v",
		st.FontSize, st.Bold, st.PaddingTop, st.PaddingLeft, st.LetterSpacing)
	return h.Sum64()
}

// TotalScore delegates to the inner PipelineScorer so CachingScorer satisfies
// TotalScorer. The whole-image score is only computed for the small final-ranking
// pool, so it is not worth caching separately from the stageImage LRU.
func (c *CachingScorer) TotalScore(ctx context.Context, guess string, offset unpixel.Offset) float64 {
	return c.inner.TotalScore(ctx, guess, offset)
}

// Eval scores guess at offset, using the LRU cache for the stageImage result.
// It implements the Scorer interface and is safe for concurrent use.
func (c *CachingScorer) Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) EvalResult {
	if ctx.Err() != nil {
		return EvalResult{Score: 1}
	}
	sr, err := c.cachedStage(ctx, guess, offset)
	if err != nil {
		return EvalResult{Score: 1}
	}
	return c.inner.evalFromStage(ctx, sr, prevGuess, offset, 0)
}

// EvalBounded implements boundedScorer. It uses the LRU cache for stageImage
// and passes maxDiffRatio to the inner evalFromStage so the metric can abort
// early for rejected candidates. Accepted candidates receive the exact same
// score as Eval.
func (c *CachingScorer) EvalBounded(ctx context.Context, guess, prevGuess string, offset unpixel.Offset, maxDiffRatio float64) EvalResult {
	if ctx.Err() != nil {
		return EvalResult{Score: 1}
	}
	sr, err := c.cachedStage(ctx, guess, offset)
	if err != nil {
		return EvalResult{Score: 1}
	}
	return c.inner.evalFromStage(ctx, sr, prevGuess, offset, maxDiffRatio)
}

// cachedStage returns the stageResult for guess, fetching from the cache or
// computing (and storing) it on a miss.
func (c *CachingScorer) cachedStage(ctx context.Context, guess string, offset unpixel.Offset) (stageResult, error) {
	// Bypass entirely when caching is disabled.
	if c.maxEntries <= 0 {
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		return c.inner.stageImage(ctx, guess, offset)
	}

	k := cacheKey{
		guess:     guess,
		ox:        offset.X,
		oy:        offset.Y,
		blockSize: c.inner.cfg.BlockSize,
		styleKey:  c.styleKey,
	}

	// Check cache under lock.
	c.mu.Lock()
	if ent, ok := c.entries[k]; ok {
		c.lru.MoveToFront(ent.element)
		c.hits++
		sr := ent.sr
		c.mu.Unlock()
		return sr, nil
	}
	c.misses++
	c.mu.Unlock()

	// Cache miss: render outside the lock so other goroutines are not blocked.
	sr, err := c.inner.stageImage(ctx, guess, offset)
	if err != nil {
		return stageResult{}, err
	}

	// Store under lock; a concurrent goroutine may have stored the same key
	// already (benign duplicate render). In that case, update the LRU order.
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.entries[k]; ok {
		c.lru.MoveToFront(ent.element)
		return ent.sr, nil
	}
	elem := c.lru.PushFront(k)
	c.entries[k] = &cacheEntry{sr: sr, element: elem}
	if c.lru.Len() > c.maxEntries {
		c.evictOldest()
	}
	return sr, nil
}

// evictOldest removes the least-recently-used entry. Must be called with mu held.
func (c *CachingScorer) evictOldest() {
	back := c.lru.Back()
	if back == nil {
		return
	}
	k := back.Value.(cacheKey)
	delete(c.entries, k)
	c.lru.Remove(back)
}

// Stats returns the cumulative hit and miss counts since the scorer was created.
// It is intended for testing and diagnostics.
func (c *CachingScorer) Stats() (hits, misses int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

// RedactedImage delegates to the inner scorer.
func (c *CachingScorer) RedactedImage() *image.RGBA { return c.inner.RedactedImage() }
