// Command block-sizes recovers text hidden at coarse mosaic block sizes (20–32 px).
// A larger block averages a bigger area, so it destroys more detail — but as long as
// a few information-rich blocks still span each glyph, recovery succeeds. It mirrors
// `unpixel -b <n> <image>`.
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
	// Same short word at growing block sizes — each is a variant this feature
	// handles. These fixtures were rendered large (80 pt) so a 2-letter word still
	// spans a few coarse blocks.
	samples := []struct {
		file, charset string
		block         int
		fontSize      float64
	}{
		{"go_block20.png", "go abcdef", 20, 80},
		{"go_block24.png", "go abcdef", 24, 96},
		{"go_block32.png", "go abcdef", 32, 128},
		{"cat_block20.png", "cat eoabd", 20, 80},
	}

	ctx := context.Background()
	for _, s := range samples {
		img := loadPNG(filepath.Join("images", s.file))
		res, err := unpixel.Recover(ctx, img,
			unpixel.WithCharset(s.charset),
			unpixel.WithBlockSize(s.block),
			// These fixtures render the word large enough to span a few coarse blocks;
			// pin the size so the forward model matches (the CLI auto-calibrates this).
			unpixel.WithStyle(unpixel.Style{FontSize: s.fontSize, PaddingTop: 8, PaddingLeft: 8}),
		)
		if err != nil {
			fmt.Printf("%-16s error: %v\n", s.file, err)
			continue
		}
		fmt.Printf("%-16s (block %2d) -> %-6q (distance %.4f)\n", s.file, s.block, res.BestGuess, res.BestTotal)
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
