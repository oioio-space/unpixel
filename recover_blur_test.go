package unpixel_test

import (
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

// TestRecover_blurRoundTrip proves the generate-and-test paradigm extends from
// mosaic to Gaussian blur: redact a known plaintext with a blur operator, then
// recover it through the same engine with Pixelator=GaussianBlur and BlockSize=1.
func TestRecover_blurRoundTrip(t *testing.T) {
	const (
		blockSize = 1 // blur has no grid; bs=1 makes the grid/padding steps no-ops
		sigma     = 3.0
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	c := components{
		renderer:  r,
		pixelator: pixelate.NewGaussianBlur(sigma),
		metric:    metric.NewPixelmatch(0.02),
		strategy:  search.NewGuidedStrategy(),
	}
	redacted := makeSyntheticRedacted(t, c, "go", style, blockSize)

	res, err := unpixel.Recover(
		t.Context(), redacted,
		unpixel.WithCharset("go abc"),
		unpixel.WithMaxLength(3),
		unpixel.WithBlockSize(blockSize),
		unpixel.WithPixelator(pixelate.NewGaussianBlur(sigma)),
		unpixel.WithStyle(style),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	guesses := []string{res.BestGuess}
	for _, e := range res.Candidates {
		guesses = append(guesses, e.Guess)
	}
	if !slices.Contains(guesses, "go") {
		t.Errorf("blur recovery missed %q; best=%q (%d candidates)", "go", res.BestGuess, len(res.Candidates))
	}
}
