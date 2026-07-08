// Command basic-recovery recovers the text behind each mosaic image in ./images,
// the same task as `unpixel <image>` on the command line. It is the "hello world"
// of UnPixel: render every candidate string, re-pixelate it the same way, and keep
// the one whose blocks match the redaction best.
package main

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wires the default renderer/pixelator/metric/strategy
)

func main() {
	// Each image is a variant this feature handles: a short secret redacted at
	// block 8 in the default font. The charset is the alphabet to search — the
	// narrowest set that could spell the secret keeps the search small and fast.
	samples := []struct {
		file, charset string
	}{
		{"admin.png", "admin xyz0"},
		{"hello.png", "helo abcd"},
		{"Go2.png", "Go2 abc019"},
		{"cat.png", "cat eoabd"},
	}

	ctx := context.Background()
	for _, s := range samples {
		img := loadPNG(filepath.Join("images", s.file))
		res, err := unpixel.Recover(ctx, img,
			unpixel.WithCharset(s.charset),
			unpixel.WithBlockSize(8),
		)
		if err != nil {
			fmt.Printf("%-10s error: %v\n", s.file, err)
			continue
		}
		fmt.Printf("%-10s -> %-8q (distance %.4f)\n", s.file, res.BestGuess, res.BestTotal)
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
