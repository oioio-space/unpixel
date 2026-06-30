//go:build !ml

package rerank

// Default returns the reranker used by [Rerank]. Without the "ml" build tag it is
// the pure-Go [Linguistic] reranker. Building with -tags ml swaps in the trained
// CTC model (see ml.go).
func Default() Reranker { return Linguistic{} }
