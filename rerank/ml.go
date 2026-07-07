//go:build ml

package rerank

// Default returns the trained emission reranker when built with -tags ml: a pure-Go
// glyph-emission model (see emission.go) that scores each candidate against the
// image via a learned P(char | tile), a discriminative tie-break the language-only
// [Linguistic] reranker cannot provide. Build without -tags ml for [Linguistic].
func Default() Reranker { return ctcReranker{} }

// ctcReranker rescores candidates with a trained glyph-emission model: it segments
// the redaction into per-glyph tiles and blends the mean per-glyph emission
// log-likelihood P(char | tile) with the physical distance and the language prior.
// The model trains itself at first use on synthetic render→pixelate glyph tiles of
// the bundled fonts and runs a pure-Go softmax forward pass — no CGO, no framework,
// no embedded weights. See [ctcReranker.Rerank] in emission.go.
type ctcReranker struct{}
