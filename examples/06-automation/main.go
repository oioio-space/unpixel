// Command automation shows how to consume UnPixel results programmatically: the best
// guess, a confidence in [0,1], the ranked alternatives, and a reliability gate that
// refuses a likely-wrong answer. On the CLI the same information is available as
// `unpixel --format json` and the `--strict` / `--min-confidence` exit-code gates.
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

// minConfidence is the reliability bar: below it we treat the recovery as untrusted
// (the CLI's --strict exits 2 in this case so CI can tell it from a clean decode).
const minConfidence = 0.5

func main() {
	samples := []struct {
		file, charset string
	}{
		{"admin.png", "admin xyz0"},
		{"hello.png", "helo abcd"},
	}

	ctx := context.Background()
	exit := 0
	for _, s := range samples {
		img := loadPNG(filepath.Join("images", s.file))
		res, err := unpixel.Recover(ctx, img,
			unpixel.WithCharset(s.charset),
			unpixel.WithBlockSize(8),
		)
		if err != nil {
			fmt.Printf("%s error: %v\n", s.file, err)
			exit = 1
			continue
		}
		fmt.Printf("%s: best=%q confidence=%.2f distance=%.4f\n",
			s.file, res.BestGuess, res.Confidence, res.BestTotal)
		for i, e := range res.TopN {
			if i >= 3 {
				break
			}
			fmt.Printf("    #%d %-8q score=%.4f\n", i+1, e.Guess, e.Score)
		}
		if res.Confidence < minConfidence {
			fmt.Printf("    ⚠ below the %.2f confidence bar — not trusting this guess\n", minConfidence)
			exit = 2 // mirrors the CLI's --strict exit code
		}
	}
	os.Exit(exit)
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
