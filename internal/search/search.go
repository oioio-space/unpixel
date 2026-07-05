// Package search implements offset discovery and the GuidedDFS search strategy
// for the unpixel pipeline.
package search

import (
	"cmp"
	"context"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/oioio-space/unpixel"
)

// parChildThreshold is the minimum effective charset size (in runes) that makes
// intra-node parallel child evaluation worth the goroutine overhead. Charsets
// smaller than this are scored sequentially regardless of worker count: the
// per-goroutine setup cost of forEachIndex exceeds the scorer work for tiny
// alphabets (e.g. DefaultCharset=27 with TopK pruning active, or small test
// charsets), while wide charsets (e.g. CharsetASCII=95, or a custom 30+ char
// set) amortise the overhead across enough children.
//
// 16 is chosen because:
//   - DefaultCharset (27 chars) × half-pass → ~13 surviving children: below the
//     threshold, so the common case stays sequential and allocation-free.
//   - CharsetASCII (95 chars) with no TopK → always above threshold.
//   - A 16-char boundary keeps the gate check O(1) and the constant small enough
//     that genuinely wide charsets always benefit.
const parChildThreshold = 16

// intraNodeWorkers computes how many goroutines the intra-node (per-DFS-level)
// parallel child evaluation may use without oversubscribing the system when the
// offset-level fan-out (searchOffsets / DiscoverOffsets) is also active.
//
// The heuristic: divide GOMAXPROCS evenly across the concurrent offset
// goroutines. If cfg.Workers == 1 (sequential offset fan-out) or the quotient
// rounds to zero, return 1 so the caller falls back to sequential evalChildren.
// The result is floored at 1 and capped at the number of available processors,
// so it is always safe to pass directly to forEachIndex.
//
// Example (20-core machine, cfg.Workers==0 → resolveWorkers returns 20):
//
//	intraNodeWorkers = max(1, 20/20) = 1  → sequential (offsets already use all cores)
//
// Example (20-core machine, cfg.Workers==4):
//
//	intraNodeWorkers = max(1, 20/4)  = 5  → up to 5 child goroutines per offset
func intraNodeWorkers(cfg unpixel.Config) int {
	procs := runtime.GOMAXPROCS(0)
	outer := resolveWorkers(cfg)
	return max(1, procs/outer)
}

// resolveWorkers returns the concurrency level for offset fan-out: the configured
// value when positive, otherwise runtime.GOMAXPROCS. Engine.Run already fills
// Workers via applyDefaults, but DiscoverOffsets and the strategies are also
// callable directly (tests, benchmarks, custom drivers) with an unset Workers,
// so the fallback is resolved here too rather than assumed upstream.
func resolveWorkers(cfg unpixel.Config) int {
	if cfg.Workers > 0 {
		return cfg.Workers
	}
	return runtime.GOMAXPROCS(0)
}

// forEachIndex runs fn(i) for i in [0, n) using up to workers goroutines, or
// sequentially when workers <= 1. It returns after every invocation completes.
// fn must be safe for concurrent use and must only touch storage unique to its
// index so no further synchronisation is needed.
func forEachIndex(ctx context.Context, n, workers int, fn func(i int)) {
	if workers <= 1 || n <= 1 {
		for i := range n {
			if ctx.Err() != nil {
				return
			}
			fn(i)
		}
		return
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range n {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			fn(i)
		})
	}
	wg.Wait()
}

// EvalResult carries the outcome of scoring one candidate string.
type EvalResult struct {
	// Score is the marginal-region diff score in [0, 1]. Lower is better.
	Score float64
	// TooBig indicates the rendered candidate is wider than the redacted image.
	TooBig bool
}

// Scorer evaluates a candidate string at a given grid offset.
// prevGuess is the parent guess whose rendered image is compared to find the
// marginal change region (empty string on the first call at each depth).
type Scorer interface {
	Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) EvalResult
}

// boundedScorer is an optional extension of Scorer. When a Scorer implements
// it, evalChildren passes the per-character threshold as the ceiling so the
// metric can abort the pixel scan early for candidates that will be rejected.
// The score contract is identical to Scorer.Eval for accepted candidates
// (score < maxDiffRatio); for rejected ones the value is >= maxDiffRatio.
type boundedScorer interface {
	EvalBounded(ctx context.Context, guess, prevGuess string, offset unpixel.Offset, maxDiffRatio float64) EvalResult
}

// TotalScorer is an optional Scorer capability: it scores the WHOLE rendered
// candidate against the WHOLE redacted image (no marginal cropping). The search
// uses it only to rank the final answer — a correct prefix or a coincidental
// glyph match drives the marginal Eval score to ~0, so marginal score cannot
// tell "go" or a fluke from the complete "go run", but the total score can.
// A Scorer that does not implement it falls back to marginal-score ranking.
type TotalScorer interface {
	TotalScore(ctx context.Context, guess string, offset unpixel.Offset) float64
}

// node is an internal DFS stack entry.
type node struct {
	guess  string
	result EvalResult
}

// ensureThresholdFor returns cfg with ThresholdFor set to a sensible default
// if the caller did not supply one, without mutating the original.
func ensureThresholdFor(cfg unpixel.Config) unpixel.Config {
	if cfg.ThresholdFor == nil {
		t, st := cfg.Threshold, cfg.SpaceThreshold
		cfg.ThresholdFor = func(ch rune) float64 {
			if ch == ' ' {
				return st
			}
			return t
		}
	}
	return cfg
}

// GuidedDFS runs a depth-first search over candidate strings, calling emit for
// every candidate that passes the threshold gate.
//
// faithful: preload.ts guessRecursive — render parent; if tooBig prune;
// append each charset char; keep score < threshold (< spaceThreshold for ' ');
// sort ascending; recurse.
//
// When the effective charset width is at least parChildThreshold runes and the
// intra-node worker budget (intraNodeWorkers) is > 1, children at every DFS
// level are evaluated concurrently via evalChildrenParCapped. The worker budget
// is divided evenly across the outer offset-level fan-out so the total goroutine
// count stays bounded at ≈ GOMAXPROCS.
func GuidedDFS(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	emit func(unpixel.Eval),
) {
	cfg = ensureThresholdFor(cfg)
	iw := intraNodeWorkers(cfg)
	// Seed: evaluate every single character.
	seeds := chooseEvalChildren(ctx, scorer, cfg, offset, "", iw)
	for _, s := range seeds {
		emit(unpixel.Eval{
			Guess:  s.guess,
			Score:  s.result.Score,
			TooBig: s.result.TooBig,
		})
	}
	for _, s := range seeds {
		if ctx.Err() != nil {
			return
		}
		guessRecursive(ctx, scorer, cfg, offset, s, emit, iw)
	}
}

// guessRecursive extends the given node depth-first.
// The node's result already contains tooBig, so no extra parent eval is needed.
// iw is the intra-node worker count computed once by GuidedDFS and threaded
// through to avoid repeated calls to intraNodeWorkers / runtime.GOMAXPROCS.
func guessRecursive(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parent node,
	emit func(unpixel.Eval),
	iw int,
) {
	if ctx.Err() != nil {
		return
	}
	// faithful: preload.ts — if tooBig prune immediately.
	if parent.result.TooBig {
		return
	}
	if len(parent.guess) >= cfg.MaxLength {
		return
	}

	children := chooseEvalChildren(ctx, scorer, cfg, offset, parent.guess, iw)
	for _, child := range children {
		emit(unpixel.Eval{
			Guess:  child.guess,
			Score:  child.result.Score,
			TooBig: child.result.TooBig,
		})
	}
	for _, child := range children {
		guessRecursive(ctx, scorer, cfg, offset, child, emit, iw)
	}
}

// chooseEvalChildren dispatches to evalChildrenParCapped when the effective
// charset is wide enough (>= parChildThreshold) and iw > 1, otherwise falls
// back to the sequential evalChildren. The threshold prevents goroutine-setup
// overhead from dominating on small charsets (e.g. DefaultCharset with TopK
// active yields ~13 survivors — below the threshold, so the fast path is kept).
//
// When iw == 1 (the common case: GOMAXPROCS offset goroutines saturate cores)
// the function is a zero-overhead call to evalChildren with no extra work.
// When iw > 1, the charset size check is fused with the topKChars call so that
// chars are computed only once and passed directly to evalChildrenParCapped,
// avoiding the double topKChars allocation that a naïve pre-check would cause.
func chooseEvalChildren(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parentGuess string,
	iw int,
) []node {
	if iw <= 1 {
		return evalChildren(ctx, scorer, cfg, offset, parentGuess)
	}
	// Compute the effective character set once. topKChars returns nil when no
	// pruning is needed, in which case we fall back to the full charset slice.
	chars := topKChars(cfg, parentGuess)
	if chars == nil {
		chars = []rune(cfg.Charset)
	}
	if len(chars) < parChildThreshold {
		// Too few children to justify goroutine overhead — stay sequential.
		// Re-use chars directly rather than calling evalChildren (which would
		// call topKChars a second time).
		return evalChildrenFromChars(ctx, scorer, cfg, offset, parentGuess, chars)
	}
	return evalChildrenParCappedFromChars(ctx, scorer, cfg, offset, parentGuess, chars, iw)
}

// evalChild dispatches a single candidate evaluation to EvalBounded when the
// scorer supports it, falling back to Eval otherwise. It is the shared
// per-child dispatch used by both the sequential and parallel eval loops;
// the compiler inlines it so there is no per-call overhead.
func evalChild(ctx context.Context, scorer Scorer, bs boundedScorer, bounded bool, next, parent string, offset unpixel.Offset, thr float64) EvalResult {
	if bounded {
		return bs.EvalBounded(ctx, next, parent, offset, thr)
	}
	return scorer.Eval(ctx, next, parent, offset)
}

// evalChildrenFromChars is the sequential evalChildren inner loop operating on
// a pre-computed chars slice. It avoids a redundant topKChars call when the
// caller (chooseEvalChildren) has already resolved the character set.
//
// When scorer implements boundedScorer, each child is evaluated via
// EvalBounded(threshold) so the metric can abort the pixel scan early for
// candidates that will be rejected. Accepted candidates always receive the
// exact same score as Eval.
func evalChildrenFromChars(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parentGuess string,
	chars []rune,
) []node {
	bs, isBounded := scorer.(boundedScorer)
	// Preallocate to the full charset width; slices.Clip trims unused capacity
	// before returning so callers never hold a permanently oversized backing array.
	children := make([]node, 0, len(chars))
	for _, ch := range chars {
		if ctx.Err() != nil {
			return children
		}
		next := parentGuess + string(ch)
		thr := cfg.ThresholdFor(ch)
		res := evalChild(ctx, scorer, bs, isBounded, next, parentGuess, offset, thr)
		if res.Score < thr {
			children = append(children, node{guess: next, result: res})
		}
	}
	slices.SortFunc(children, func(a, b node) int {
		return cmp.Compare(a.result.Score, b.result.Score)
	})
	return slices.Clip(children)
}

// evalChildrenParCappedFromChars is the parallel evalChildrenParCapped inner
// loop operating on a pre-computed chars slice. It avoids a redundant topKChars
// call when the caller has already resolved the character set.
//
// When scorer implements boundedScorer, each child is evaluated via
// EvalBounded(threshold) so the metric can abort early for rejected candidates.
//
// results is a flat []node (not []*node) so no per-child heap allocation is
// needed. survived[i] marks slots that passed the threshold; the compact scan
// that follows is then a simple index walk without nil-pointer checks.
func evalChildrenParCappedFromChars(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parentGuess string,
	chars []rune,
	workers int,
) []node {
	bs, isBounded := scorer.(boundedScorer)
	n := len(chars)
	results := make([]node, n)
	survived := make([]bool, n)
	forEachIndex(ctx, n, workers, func(i int) {
		if ctx.Err() != nil {
			return
		}
		ch := chars[i]
		next := parentGuess + string(ch)
		thr := cfg.ThresholdFor(ch)
		res := evalChild(ctx, scorer, bs, isBounded, next, parentGuess, offset, thr)
		if res.Score < thr {
			results[i] = node{guess: next, result: res}
			survived[i] = true
		}
	})
	// Compact survivors into a dense slice without a second allocation.
	children := make([]node, 0, n)
	for i, ok := range survived {
		if ok {
			children = append(children, results[i])
		}
	}
	slices.SortStableFunc(children, func(a, b node) int {
		return cmp.Compare(a.result.Score, b.result.Score)
	})
	return slices.Clip(children)
}

// evalChildren scores each character in cfg.Charset appended to parentGuess,
// keeps those below the threshold, and returns them sorted ascending by score.
// parentGuess is passed as prevGuess to the scorer for marginal-region diffing.
func evalChildren(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parentGuess string,
) []node {
	pruned := topKChars(cfg, parentGuess)

	// Preallocate to the effective charset width so the hot append loop never
	// copies. slices.Clip trims unused capacity before returning.
	capHint := len(pruned)
	if capHint == 0 {
		capHint = len(cfg.Charset) // byte len ≥ rune count, safe over-estimate
	}
	children := make([]node, 0, capHint)

	eval := func(ch rune) {
		next := parentGuess + string(ch)
		res := scorer.Eval(ctx, next, parentGuess, offset)
		if res.Score < cfg.ThresholdFor(ch) {
			children = append(children, node{guess: next, result: res})
		}
	}
	if pruned != nil {
		for _, ch := range pruned {
			if ctx.Err() != nil {
				return children
			}
			eval(ch)
		}
	} else {
		for _, ch := range cfg.Charset { // default: whole charset, no allocation
			if ctx.Err() != nil {
				return children
			}
			eval(ch)
		}
	}
	slices.SortFunc(children, func(a, b node) int {
		return cmp.Compare(a.result.Score, b.result.Score)
	})
	return slices.Clip(children)
}

// autoTopKThreshold is the minimum charset size (in runes) that triggers
// automatic Top-K pruning when a LanguageModel is set but CharsetTopK is 0.
// Charsets up to this size (e.g. DefaultCharset = 27, CharsetAlnum = 63)
// remain on the allocation-free whole-charset path; wider charsets (e.g.
// CharsetASCII = 95) benefit from pruning without requiring the caller to
// set CharsetTopK explicitly.
//
// 40 is chosen because it sits above the faithful default (27 chars) and
// the typical "target + distractors" charsets used in tests (≤ ~30), so
// auto-pruning never fires for normal small charsets and their behavior is
// byte-identical to today.
const autoTopKThreshold = 40

// autoTopKValue is the effective K used by auto-pruning. Language-model
// distributions are sharply peaked: for English text, the top 24 characters
// capture >97 % of the probability mass at any position, so K=24 loses
// almost no recall while cutting ~75 % of renders on CharsetASCII (95 → 24).
// It is capped by the actual charset length, so it is always safe.
const autoTopKValue = 24

// effectiveTopK derives the Top-K to use from cfg without mutating it.
//
// Priority (highest first):
//  1. cfg.CharsetTopK > 0 — caller wins, always; returned as-is.
//  2. cfg.LanguageModel != nil AND len(charset) >= autoTopKThreshold —
//     auto-K of min(len(charset), autoTopKValue) reduces wide-charset cost.
//  3. 0 — no pruning; evalChildren stays on the allocation-free path.
//
// The small-charset / no-model case always returns 0, so existing behavior
// for DefaultCharset (27 chars) is byte-identical to before this change.
func effectiveTopK(cfg unpixel.Config) int {
	if cfg.CharsetTopK > 0 {
		return cfg.CharsetTopK
	}
	if cfg.LanguageModel == nil {
		return 0
	}
	n := len([]rune(cfg.Charset))
	if n < autoTopKThreshold {
		return 0
	}
	return min(n, autoTopKValue)
}

// topKChars returns the effectiveTopK(cfg) most-likely next characters after
// parentGuess, ranked by the language model, or nil to disable pruning (when
// the effective K is 0, there is no model, or K covers the whole charset).
// nil keeps callers on the allocation-free whole-charset path.
func topKChars(cfg unpixel.Config, parentGuess string) []rune {
	k := effectiveTopK(cfg)
	if k <= 0 || cfg.LanguageModel == nil {
		return nil
	}
	runes := []rune(cfg.Charset)
	if k >= len(runes) {
		return nil
	}
	scored := make([]runeScore, len(runes))
	for i, r := range runes {
		scored[i] = runeScore{r: r, s: cfg.LanguageModel(parentGuess + string(r))}
	}
	slices.SortStableFunc(scored, func(a, b runeScore) int {
		return cmp.Compare(b.s, a.s) // most plausible first
	})
	out := make([]rune, k)
	for i := range out {
		out[i] = scored[i].r
	}
	return out
}

// runeScore pairs a candidate character with its language-model plausibility.
type runeScore struct {
	r rune
	s float64
}

// bestSeenTracker records the single lowest-scored (guess, score) pair seen
// across all Eval calls, regardless of whether the score passed the threshold.
// It is safe for concurrent use: the score is stored as IEEE-754 bits in an
// atomic.Uint64 and updated with a CAS loop; the guess is protected by mu and
// written only when a new minimum score is confirmed.
//
// On the hot path (above-threshold fast path) this adds one atomic.Load plus,
// on improvement, one CAS retry loop and one mu.Lock — negligible beside the
// render→pixelate→metric work each Eval already performs.
type bestSeenTracker struct {
	scoreBits atomic.Uint64 // math.Float64bits of best score; init to +Inf bits
	mu        sync.Mutex
	guess     string
}

// newBestSeenTracker returns a tracker initialised to the worst possible score.
func newBestSeenTracker() *bestSeenTracker {
	t := &bestSeenTracker{}
	t.scoreBits.Store(math.Float64bits(math.Inf(1)))
	return t
}

// update records (guess, score) if it improves on the best seen so far.
// A strictly lower score always wins. Tied scores use the lexicographically
// smaller guess as a tie-breaker, ensuring the result is deterministic
// regardless of goroutine scheduling. Concurrent calls are safe; the common
// (non-improving strict) path is lock-free.
func (t *bestSeenTracker) update(guess string, score float64) {
	newBits := math.Float64bits(score)
	for {
		oldBits := t.scoreBits.Load()
		oldScore := math.Float64frombits(oldBits)
		if oldScore < score {
			return // existing best is strictly better; no update
		}
		if oldScore == score {
			// Equal score: serialise on mu to apply the lexicographic tie-break
			// without a second CAS loop. This path is rare (exact float equality).
			t.mu.Lock()
			if math.Float64frombits(t.scoreBits.Load()) == score && guess >= t.guess {
				t.mu.Unlock()
				return // existing guess is ≤ new one; keep it
			}
			t.scoreBits.Store(newBits)
			t.guess = guess
			t.mu.Unlock()
			return
		}
		// New score is strictly lower: claim it with a CAS.
		if t.scoreBits.CompareAndSwap(oldBits, newBits) {
			t.mu.Lock()
			t.guess = guess
			t.mu.Unlock()
			return
		}
		// Another goroutine raced; re-read and retry.
	}
}

// best returns the best (guess, score) seen so far. Returns ("", +Inf) if no
// Eval has been called.
func (t *bestSeenTracker) best() (string, float64) {
	t.mu.Lock()
	score := math.Float64frombits(t.scoreBits.Load())
	guess := t.guess
	t.mu.Unlock()
	return guess, score
}

// trackingScorer wraps a Scorer and forwards every Eval result to a
// bestSeenTracker so the search can surface the closest candidate even when
// nothing passes the acceptance threshold.
type trackingScorer struct {
	Scorer
	seen *bestSeenTracker
}

func (ts trackingScorer) Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) EvalResult {
	res := ts.Scorer.Eval(ctx, guess, prevGuess, offset)
	ts.seen.update(guess, res.Score)
	return res
}

// inkFilteredCharset returns the subset of charset that excludes whitespace
// runes (unicode.IsSpace). A whitespace glyph renders as an all-white image
// and therefore scores ≈0 at every grid origin, destroying phase
// discrimination on margined images: including it drives the per-origin
// bestScore to 0 everywhere so the true phase cannot be identified.
//
// Filtering by codepoint is free — no render pre-pass. If generalising to
// also exclude font-missing glyphs is ever needed, a render-based ink
// check would catch those too, but at the cost of one render per charset
// entry; that is deferred until a concrete need arises.
//
// When every rune in charset is whitespace (degenerate), the full charset is
// returned so DiscoverOffsets degrades gracefully.
func inkFilteredCharset(charset string) string {
	inked := make([]rune, 0, len(charset)) // byte length is a safe upper bound on rune count
	for _, ch := range charset {
		if !unicode.IsSpace(ch) {
			inked = append(inked, ch)
		}
	}
	if len(inked) == 0 {
		return charset
	}
	return string(inked)
}

// DiscoverOffsets probes all blockSize² grid origins and returns those whose
// best single-character score is below cfg.Threshold, sorted ascending.
// emit is called with an EventOffsetProbed event after each origin is scored;
// pass nil to suppress progress events.
//
// Whitespace runes are excluded from the per-origin probe: a whitespace glyph
// renders blank, scores ≈0 at every origin, and destroys phase discrimination.
// Only non-whitespace glyphs carry phase information.
//
// faithful: preload.ts offset discovery loop — 8×8=64 origins, keep best < threshold.
func DiscoverOffsets(ctx context.Context, scorer Scorer, cfg unpixel.Config, emit func(unpixel.Progress)) []unpixel.Offset {
	cfg = ensureThresholdFor(cfg)
	bs := cfg.BlockSize
	total := bs * bs
	if total == 0 {
		return nil
	}

	// Exclude whitespace runes before the probe loop. A whitespace glyph renders
	// as an all-white image and scores ≈0 at every origin, so including one
	// drives bestScore to 0 everywhere and the true grid phase is indistinguishable.
	probeCharset := inkFilteredCharset(cfg.Charset)

	// One slot per grid origin, indexed i = y*bs + x, so concurrent probes write
	// disjoint storage and the survivor scan stays deterministic.
	scored := make([]unpixel.Offset, total)
	survived := make([]bool, total)
	var done atomic.Int64

	forEachIndex(ctx, total, resolveWorkers(cfg), func(i int) {
		if ctx.Err() != nil {
			return
		}
		x, y := i%bs, i/bs
		offset := unpixel.Offset{X: x, Y: y}
		bestScore := 1.0
		evaluated := 0
		for _, ch := range probeCharset {
			if ctx.Err() != nil {
				return
			}
			res := scorer.Eval(ctx, string(ch), "", offset)
			evaluated++
			if res.Score < bestScore {
				bestScore = res.Score
			}
		}
		probed := unpixel.Offset{X: x, Y: y, Score: bestScore}
		scored[i] = probed
		survived[i] = bestScore < cfg.Threshold
		if emit != nil {
			emit(unpixel.Progress{
				Kind:         unpixel.EventOffsetProbed,
				Offset:       probed,
				OffsetsDone:  int(done.Add(1)),
				OffsetsTotal: total,
				Evaluated:    evaluated,
			})
		}
	})

	var offsets []unpixel.Offset
	for i := range total {
		if survived[i] {
			offsets = append(offsets, scored[i])
		}
	}
	slices.SortFunc(offsets, func(a, b unpixel.Offset) int {
		return cmp.Compare(a.Score, b.Score)
	})
	return offsets
}
