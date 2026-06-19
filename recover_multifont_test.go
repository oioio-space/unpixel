package unpixel_test

import (
	"errors"
	"image"
	"os"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/render"
)

// TestRecoverMultiFont_ranksCorrectFontFirst builds a redaction with the regular
// font, then sweeps a wrong (bold) and the right (regular) renderer and checks
// the right one ranks first by whole-image fidelity.
func TestRecoverMultiFont_ranksCorrectFontFirst(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)
	redacted := makeSyntheticRedacted(t, c, "go", style, blockSize)

	good, err := render.NewXImage() // same font the redaction was made with
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	boldData, err := os.ReadFile("internal/render/fonts/LiberationSans-Bold.ttf")
	if err != nil {
		t.Fatalf("read bold font: %v", err)
	}
	bad, err := render.NewXImageFromFonts(boldData, nil) // wrong weight
	if err != nil {
		t.Fatalf("NewXImageFromFonts: %v", err)
	}

	// Order [bad, good] so a correct ranking must reorder, not just preserve input.
	ranked, err := unpixel.RecoverMultiFont(
		t.Context(), redacted,
		[]unpixel.Renderer{bad, good},
		unpixel.WithCharset("go abc"),
		unpixel.WithMaxLength(3),
		unpixel.WithBlockSize(blockSize),
		unpixel.WithStyle(style),
	)
	if err != nil {
		t.Fatalf("RecoverMultiFont: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("got %d results, want 2", len(ranked))
	}
	if ranked[0].Index != 1 {
		t.Errorf("winner Index = %d, want 1 (the regular font)", ranked[0].Index)
	}
	if ranked[0].Result.BestGuess != "go" {
		t.Errorf("winner BestGuess = %q, want %q", ranked[0].Result.BestGuess, "go")
	}
	if ranked[0].Result.BestTotal > ranked[1].Result.BestTotal {
		t.Errorf("winner BestTotal %.4f should be <= runner-up %.4f",
			ranked[0].Result.BestTotal, ranked[1].Result.BestTotal)
	}
}

// TestRecoverMultiFont_errors covers the guard conditions.
func TestRecoverMultiFont_errors(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("NewXImage: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))

	if _, err := unpixel.RecoverMultiFont(t.Context(), nil, []unpixel.Renderer{r}); !errors.Is(err, unpixel.ErrNilImage) {
		t.Errorf("nil image: got %v, want ErrNilImage", err)
	}
	if _, err := unpixel.RecoverMultiFont(t.Context(), img, nil); err == nil {
		t.Error("empty renderers: expected error, got nil")
	}
}
