//go:build !ml

package fontprior

// Default returns the prior used by [RecoverWithPrior] and recommended for
// callers. Without the "ml" build tag it is the pure-Go [Histogram] heuristic.
// Building with -tags ml swaps in the trained ML classifier (see ml.go).
func Default() Prior { return Histogram{} }
