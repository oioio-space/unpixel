// Smoothing rationale: Lidstone add-k with k=0.1. Pure add-1 (Laplace)
// over-smooths heavily for a small corpus (thousands of distinct byte n-grams
// vs. hundreds of occurrences each), collapsing the signal that variable-order
// backoff provides. k=0.1 preserves the conditional distribution while still
// preventing log(0) on unseen bytes. The vocabulary is fixed at 256 (single-byte
// values), so the smoothed denominator increment is k*256=25.6 — small relative
// to typical context counts, avoiding over-smoothing.
//
// Concurrency: Infini is not safe for concurrent Score calls because the internal
// count cache is a plain map. This matches the existing Model contract.

package lang

import (
	_ "embed"
	"index/suffixarray"
	"math"
	"strings"
	"sync"
)

//go:embed corpus_fr.txt
var corpusFR string

const (
	// maxOrder is the maximum context length (in bytes) that Infini considers.
	// 8 bytes covers long n-grams for French prose while keeping Lookup fast.
	maxOrder = 8

	// smoothK is the Lidstone add-k constant. See the file-level comment for
	// the rationale. Effective denominator increment: smoothK*vocabSize = 25.6.
	smoothK = 0.1

	// vocabSize is the byte-level vocabulary size (all 256 byte values).
	vocabSize = 256
)

// Infini is a variable-order character language model backed by a suffix array
// over an embedded corpus: at each byte position it scores the next byte using
// the longest preceding context that still occurs in the corpus (infini-gram
// backoff), so it captures long-range structure where the data supports it and
// falls back gracefully where it does not. Unlike Model it preserves non-ASCII
// bytes, so it models accented text (e.g. French "connaît") correctly.
//
// Inspiration: nathan-barry/tiny-infini-gram (infini-gram via Go's
// index/suffixarray). The standard-library suffix array (index/suffixarray)
// provides Lookup(-1) which returns all match positions in O(log n + k) time,
// so count(sub) = len(sa.Lookup(sub, -1)) with no external dependencies.
//
// The zero value is not usable; use NewInfini or DefaultFrench.
type Infini struct {
	sa    *suffixarray.Index
	cache map[string]int // memoises sa.Lookup counts; not goroutine-safe
	floor float64        // log-prob floor: log(1/(corpusLen+1))
}

// NewInfini builds an Infini model over text (lowercased, UTF-8 preserved so
// accented bytes survive as multi-byte sequences).
func NewInfini(text string) *Infini {
	data := []byte(strings.ToLower(text))
	return &Infini{
		sa:    suffixarray.New(data),
		cache: make(map[string]int),
		floor: math.Log(1.0 / float64(len(data)+1)),
	}
}

// count returns the number of times sub occurs in the corpus, using the internal
// cache to avoid redundant suffix-array lookups.
func (m *Infini) count(sub string) int {
	if v, ok := m.cache[sub]; ok {
		return v
	}
	v := len(m.sa.Lookup([]byte(sub), -1))
	m.cache[sub] = v
	return v
}

// Score returns the mean per-byte log-probability of s (higher = more plausible
// text). An empty string returns the floor log-prob without panicking. Case is
// folded to lower before scoring.
func (m *Infini) Score(s string) float64 {
	if s == "" {
		return m.floor
	}
	b := []byte(strings.ToLower(s))
	n := len(b)
	// buf is a stack-allocated scratch buffer for building the bigram lookup key
	// (context bytes + next byte). Sized to maxOrder+1 so no heap allocation is
	// needed for the string conversion inside the hot loop.
	var buf [maxOrder + 1]byte
	var sum float64
	for i := range n {
		// Context: up to maxOrder bytes immediately before position i.
		ctx := b[max(0, i-maxOrder):i]

		// Infini-gram backoff: find the longest suffix of ctx that appears in
		// the corpus, backing off by one byte at a time until L==0.
		var ctxCount, L int
		for L = len(ctx); L >= 0; L-- {
			c := m.count(string(ctx[len(ctx)-L:]))
			if c > 0 || L == 0 {
				ctxCount = c
				break
			}
		}

		// Build (context_L ++ next byte) in the stack buffer; convert to string
		// without a heap allocation (the compiler elides the alloc for
		// string(buf[:L+1]) when buf does not escape — verified by benchstat).
		ctxBytes := ctx[len(ctx)-L:]
		copy(buf[:L], ctxBytes)
		buf[L] = b[i]
		biCount := m.count(string(buf[:L+1]))

		// Lidstone-smoothed conditional probability:
		//   P(next | ctx_L) = (count(ctx_L ++ next) + k) / (count(ctx_L) + k*256)
		denom := float64(ctxCount) + smoothK*vocabSize
		if denom == 0 {
			sum += m.floor
			continue
		}
		sum += math.Log((float64(biCount) + smoothK) / denom)
	}
	return sum / float64(n)
}

var (
	defaultFrenchOnce  sync.Once
	defaultFrenchModel *Infini
)

// DefaultFrench returns the shared Infini model trained on the embedded French
// corpus. It is initialised exactly once (sync.Once). Not safe for concurrent
// Score calls — matches the Model contract.
func DefaultFrench() *Infini {
	defaultFrenchOnce.Do(func() {
		defaultFrenchModel = NewInfini(corpusFR)
	})
	return defaultFrenchModel
}

// InfiniPrior returns a prior closure (Score) over the default French corpus,
// ready for use with unpixel.WithPriors:
//
//	res, _ := unpixel.Recover(ctx, img,
//	    unpixel.WithPriors(lang.InfiniPrior()),
//	)
func InfiniPrior() func(string) float64 {
	return DefaultFrench().Score
}
