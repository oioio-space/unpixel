//go:build ml

package fontprior

import (
	"context"
	"image"

	"github.com/oioio-space/unpixel/fonts"
)

// Default returns the trained ML prior when built with -tags ml: a pure-Go softmax
// font-ID classifier (see mlmodel.go) trained at first use on synthetic
// render→pixelate samples of the bundled fonts. No CGO, no external framework, no
// embedded weights — the renderer is the labeller.
func Default() Prior { return mlPrior{} }

// mlPrior is a font classifier trained on the render→pixelate→font-label synthetic
// domain (the renderer labels the data). Inference is a pure-Go softmax forward
// pass over a block-luminance-histogram feature. It ranks the bundled fonts by
// P(font | mosaic) — a discriminative alternative to the L1-histogram [Histogram]
// prior, dropping in behind the //go:build ml seam without touching callers.
type mlPrior struct{}

// Rank ranks fnts best-first by the trained model's class probabilities. It returns
// nil, nil for an empty font list or a nil image (nothing to classify).
func (mlPrior) Rank(ctx context.Context, img image.Image, blockSize int, fnts []fonts.Font) ([]Ranked, error) {
	if len(fnts) == 0 || img == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rankWithModel(img, blockSize, fnts), nil
}
