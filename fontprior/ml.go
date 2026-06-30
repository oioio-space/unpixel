//go:build ml

package fontprior

import (
	"context"
	"errors"
	"image"

	"github.com/oioio-space/unpixel/fonts"
)

// ErrMLNotBuilt is returned by the ML prior until a trained model is wired in.
// It exists so callers built with -tags ml fail loudly rather than silently
// degrading. Build without the tag for the pure-Go [Histogram] prior.
var ErrMLNotBuilt = errors.New("fontprior: ML prior not built — train and embed a model, or build without -tags ml")

// Default returns the ML prior when built with -tags ml. Until a model is
// trained and embedded, its Rank returns [ErrMLNotBuilt]; [RecoverWithPrior]
// then falls back to the catalog-order sweep.
func Default() Prior { return mlPrior{} }

// mlPrior is the seam for a CNN font classifier trained on the
// render→pixelate→font-label synthetic domain (the renderer is the labeller).
// Training and weights live outside this repo; inference would be a
// hand-written pure-Go forward pass (no CGO). This commit ships only the
// build-tag seam so a model can drop in without touching callers.
type mlPrior struct{}

// Rank reports [ErrMLNotBuilt] until a trained model is embedded.
func (mlPrior) Rank(_ context.Context, _ image.Image, _ int, _ []fonts.Font) ([]Ranked, error) {
	return nil, ErrMLNotBuilt
}
