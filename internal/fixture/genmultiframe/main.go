// Command genmultiframe generates multi-frame test fixtures from a SHARP source:
// it renders text once, then pixelates that sharp image at N distinct grid
// phases, producing phase-diverse mosaics that genuinely reveal different
// sub-block information. Each case writes 4 PNGs plus a manifest.json under
// testdata/multiframe/.
//
// Usage:
//
//	go run ./internal/fixture/genmultiframe -out testdata/multiframe
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

func main() {
	out := flag.String("out", "testdata/multiframe", "output directory for images + manifest")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "genmultiframe:", err)
		os.Exit(1)
	}
}

// Case describes one multi-frame test scenario.
type Case struct {
	// Name is the unique identifier used as the filename prefix.
	Name string `json:"name"`
	// Text is the plaintext hidden by the pixelation.
	Text string `json:"text"`
	// BlockSize is the pixelation block side length in pixels.
	BlockSize int `json:"block_size"`
	// Frames lists the phase-diverse pixelated observations produced from the
	// same sharp source.
	Frames []FrameEntry `json:"frames"`
}

// FrameEntry describes one pixelated frame in a multi-frame case.
type FrameEntry struct {
	// File is the PNG filename (relative to the manifest directory).
	File string `json:"file"`
	// OffsetX is the horizontal grid phase at which the frame was produced.
	OffsetX int `json:"offset_x"`
	// OffsetY is the vertical grid phase at which the frame was produced.
	OffsetY int `json:"offset_y"`
}

// phases is the fixed set of grid phases used for all cases (4 distinct
// sub-block offsets covering both axes and diagonal directions).
var phases = [][2]int{
	{0, 0},
	{3, 0},
	{0, 4},
	{5, 2},
}

// cases is the set of multi-frame test scenarios. Block size 8 is the
// standard fixture block; font size 32 leaves 4 px/block for adequate signal.
func cases() []struct {
	text  string
	block int
	name  string
} {
	return []struct {
		text  string
		block int
		name  string
	}{
		{name: "hello_b8", text: "hello", block: 8},
		{name: "Go2_b8", text: "Go2", block: 8},
		{name: "cat_b8", text: "cat", block: 8},
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", out, err)
	}

	r, err := render.NewXImage()
	if err != nil {
		return fmt.Errorf("renderer: %w", err)
	}

	var manifest []Case
	for _, c := range cases() {
		mfCase, err := generateCase(r, out, c.name, c.text, c.block)
		if err != nil {
			return fmt.Errorf("case %s: %w", c.name, err)
		}
		manifest = append(manifest, mfCase)
	}

	if err := writeManifest(filepath.Join(out, "manifest.json"), manifest); err != nil {
		return err
	}
	fmt.Printf("genmultiframe: wrote %d cases × %d frames + manifest.json to %s\n",
		len(manifest), len(phases), out)
	return nil
}

// generateCase renders text sharply, then pixelates at each phase. The sharp
// image is cropped and white-padded to a block multiple before pixelation so
// all frames share identical dimensions.
func generateCase(r *render.XImage, out, name, text string, block int) (Case, error) {
	const (
		fontSize    = 32.0
		paddingTop  = 8
		paddingLeft = 8
	)

	style := unpixel.Style{
		FontSize:    fontSize,
		PaddingTop:  paddingTop,
		PaddingLeft: paddingLeft,
	}

	// Render the sharp text image.
	img, sentinelX, err := r.Render(text, style)
	if err != nil {
		return Case{}, fmt.Errorf("render %q: %w", text, err)
	}

	// Locate the text's right edge from the blue sentinel.
	bm, _ := imutil.BlueMargin(img)
	if bm == 0 {
		bm = sentinelX
	}

	// Crop to the grid origin, then white-pad to a block multiple. This produces
	// the canonical sharp source; all frames are pixelations of this same image.
	sharp := imutil.Crop(img, 0, 0, bm, img.Bounds().Dy())
	if w := sharp.Bounds().Dx(); w%block != 0 {
		sharp = imutil.PadWhite(sharp, w+block-(w%block), sharp.Bounds().Dy())
	}
	// Remove any blue sentinel artefacts before pixelation.
	cleanSentinel(sharp)

	pix := pixelate.NewBlockAverage(block)

	mfCase := Case{Name: name, Text: text, BlockSize: block}
	for i, ph := range phases {
		ox, oy := ph[0], ph[1]
		frame := pix.Pixelate(sharp, ox, oy)
		fname := fmt.Sprintf("%s_f%d.png", name, i)
		if err := writePNG(filepath.Join(out, fname), frame); err != nil {
			return Case{}, fmt.Errorf("write frame %d: %w", i, err)
		}
		mfCase.Frames = append(mfCase.Frames, FrameEntry{
			File:    fname,
			OffsetX: ox,
			OffsetY: oy,
		})
	}
	return mfCase, nil
}

// cleanSentinel replaces every pure-blue pixel (B=255, R≠255, G≠255) in img
// with opaque white. The renderer appends a blue sentinel to locate text width;
// we strip it from the sharp source so committed PNGs contain only text.
func cleanSentinel(img *image.RGBA) {
	b := img.Bounds()
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.B == 255 && c.R != 255 && c.G != 255 {
				img.SetRGBA(x, y, white)
			}
		}
	}
}

// writePNG encodes img as a PNG at path.
func writePNG(path string, img *image.RGBA) (err error) {
	f, err := os.Create(path) // #nosec G304 -- generator writes to controlled fixture paths
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if err = png.Encode(f, img); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return nil
}

// writeManifest encodes the manifest as indented JSON at path.
func writeManifest(path string, manifest []Case) (err error) {
	f, err := os.Create(path) // #nosec G304 -- generator writes to controlled fixture paths
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err = enc.Encode(manifest); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}
