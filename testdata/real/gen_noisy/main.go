//go:build ignore

// gen_noisy generates hello-world-noisy.png by adding deterministic
// salt-and-pepper noise to hello-world.png.
//
// Noise parameters: density = 4%, seed = rand.NewPCG(1, 2).
// Run from the repository root:
//
//	go run testdata/real/gen_noisy/main.go
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math/rand/v2"
	"os"
)

func main() {
	src, err := openRGBA("testdata/real/hello-world.png")
	if err != nil {
		log.Fatalf("open source: %v", err)
	}

	noisy := addSaltPepper(src, rand.New(rand.NewPCG(1, 2)), 0.04)

	out, err := os.Create("testdata/real/hello-world-noisy.png")
	if err != nil {
		log.Fatalf("create output: %v", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil {
			log.Fatalf("close output: %v", cerr)
		}
	}()
	if err := png.Encode(out, noisy); err != nil {
		log.Fatalf("encode PNG: %v", err)
	}
	log.Printf("wrote testdata/real/hello-world-noisy.png (%d×%d, 4%% S&P noise, seed=PCG(1,2))",
		noisy.Bounds().Dx(), noisy.Bounds().Dy())
}

// openRGBA decodes a PNG file into *image.RGBA.
func openRGBA(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	decoded, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	b := decoded.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), decoded, b.Min, draw.Src)
	return rgba, nil
}

// addSaltPepper returns a copy of src with density fraction of pixels
// replaced by alternating pure-black and pure-white pixels.
// The RNG is seeded externally for full reproducibility.
func addSaltPepper(src *image.RGBA, rng *rand.Rand, density float64) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	copy(dst.Pix, src.Pix)
	total := b.Dx() * b.Dy()
	n := int(float64(total) * density)
	for i := range n {
		x := rng.IntN(b.Dx())
		y := rng.IntN(b.Dy())
		var c color.RGBA
		if i%2 == 0 {
			c = color.RGBA{A: 255} // pure black (pepper)
		} else {
			c = color.RGBA{R: 255, G: 255, B: 255, A: 255} // pure white (salt)
		}
		dst.SetRGBA(x, y, c)
	}
	return dst
}
