// Command propose-and-verify shows the other half of UnPixel: instead of searching
// for the text, you *propose* candidate strings (from a human, an OSINT list, or an
// LLM) and let UnPixel physically *verify* them — it renders each candidate, applies
// the same pixelation, and reports how closely it reproduces the redaction. This is
// the reliable path for real screenshots and the anti-hallucination gate for an
// external guesser. Verify is a library API; there is no CLI sub-command for it.
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
	// For each redaction we propose a few candidates — the truth plus look-alikes.
	// Verify confirms the truth (Match, distance ≈ 0) and rejects the decoys.
	samples := []struct {
		file       string
		candidates []string
	}{
		{"admin.png", []string{"admin", "adman", "user0"}},
		{"hello.png", []string{"hello", "hallo", "world"}},
	}

	ctx := context.Background()
	for _, s := range samples {
		img := loadPNG(filepath.Join("images", s.file))
		verdicts, err := unpixel.Verify(ctx, img, s.candidates,
			unpixel.WithBlockSize(8),
			// Demand a close physical fit before calling it a Match (default 0.10).
			unpixel.WithVerifyThreshold(0.05),
		)
		if err != nil {
			fmt.Printf("%s error: %v\n", s.file, err)
			continue
		}
		fmt.Printf("%s\n", s.file)
		for _, v := range verdicts {
			mark := "·"
			if v.Match {
				mark = "✓ confirmed"
			}
			fmt.Printf("  %-8q distance %.4f  %s\n", v.Text, v.Distance, mark)
		}
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
