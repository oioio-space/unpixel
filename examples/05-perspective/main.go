// Command perspective recovers text from a mosaic that was photographed at an angle,
// so the redaction is a tilted quadrilateral rather than an axis-aligned rectangle.
// mosaictext.DecodePerspective detects the redaction's four corners, un-warps them
// back to a flat rectangle (a homography), and then runs the normal recovery on the
// rectified image. This is a library API; there is no CLI sub-command for it.
package main

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel/mosaictext"
)

func main() {
	// Each image is a short word photographed with a different tilt.
	// WithPerspectiveAutoQuad detects the redaction's four corners automatically —
	// exact for short words; for a longer word (hello) a few-pixel corner error
	// compounds into a near-miss, so it is best-effort there.
	samples := []struct {
		file, charset string
	}{
		{"go_tilted.png", "go abcd"},
		{"cat_tilted.png", "cat eoabd"},
		{"hello_tilted.png", "helo abcd"},
	}

	ctx := context.Background()
	for _, s := range samples {
		img := loadPNG(filepath.Join("images", s.file))
		res, err := mosaictext.DecodePerspective(ctx, img,
			mosaictext.WithPerspectiveAutoQuad(0), // detect the tilted redaction automatically
			mosaictext.WithPerspectiveCharset(s.charset),
			mosaictext.WithPerspectiveFontSize(32),
			mosaictext.WithPerspectiveBlockSize(8),
		)
		if err != nil {
			fmt.Printf("%-16s error: %v\n", s.file, err)
			continue
		}
		fmt.Printf("%-16s -> %-6q (distance %.4f)\n", s.file, res.Text, res.Distance)
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
