// Command narrow-charsets recovers structured secrets by constraining the search to
// the smallest alphabet that can spell them: digits for a PIN, alphanumerics for a
// token, a handful of symbols for an expression. A tighter charset means a smaller
// search space — faster, and less prone to a look-alike wrong answer. It mirrors
// `unpixel --charset-preset digits <image>` and `--charset "<set>"`.
package main

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults"
)

func main() {
	// Each image is a different kind of structured secret; the charset is tuned to it.
	// unpixel.CharsetDigits / CharsetHex are ready-made; for anything else pass the
	// exact characters (include a space if the text may contain one).
	samples := []struct {
		file, charset, note string
	}{
		{"pin_digits.png", unpixel.CharsetDigits, "a 4-digit PIN (digits only)"},
		{"token_alnum.png", "Go2 abc019", "a short alphanumeric token"},
		{"expr_symbols.png", "x=1 +-_a0", "an expression with symbols"},
	}

	ctx := context.Background()
	for _, s := range samples {
		img := loadPNG(filepath.Join("images", s.file))
		res, err := unpixel.Recover(ctx, img,
			unpixel.WithCharset(s.charset),
			unpixel.WithBlockSize(8),
		)
		if err != nil {
			fmt.Printf("%-18s error: %v\n", s.file, err)
			continue
		}
		fmt.Printf("%-18s -> %-6q  (%s)\n", s.file, res.BestGuess, s.note)
	}
}

func loadPNG(path string) image.Image {
	f, err := os.Open(path) // #nosec G304 -- example reads its own bundled images under ./images
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	return img
}
