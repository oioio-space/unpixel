//go:build ml

package rerank

import (
	"context"
	"errors"
	"image"

	"github.com/oioio-space/unpixel"
)

// ErrCTCNotBuilt is returned by the CTC reranker until a trained model is wired
// in. It exists so callers built with -tags ml fail loudly rather than silently
// degrading. Build without the tag for the pure-Go [Linguistic] reranker.
var ErrCTCNotBuilt = errors.New("rerank: CTC reranker not built — train and embed a model, or build without -tags ml")

// Default returns the CTC reranker when built with -tags ml. Until a model is
// trained and embedded, its Rerank returns [ErrCTCNotBuilt].
func Default() Reranker { return ctcReranker{} }

// ctcReranker is the seam for a CRNN-CTC model trained on the
// render→pixelate→text synthetic domain (the renderer is the labeller). Unlike
// the language-only [Linguistic] blend, a CTC head scores P(text | image)
// discriminatively and can recognise fonts outside the bundled set that the
// forward model cannot render. Inference would be a hand-written pure-Go forward
// pass (conv+BiLSTM+CTC, no CGO). It is intentionally unimplemented: this commit
// ships only the build-tag seam so the model can drop in without touching callers.
type ctcReranker struct{}

// Rerank reports [ErrCTCNotBuilt] until a trained model is embedded.
func (ctcReranker) Rerank(_ context.Context, _ image.Image, _ []unpixel.Verdict, _ func(string) float64, _ float64) ([]Ranked, error) {
	return nil, ErrCTCNotBuilt
}
