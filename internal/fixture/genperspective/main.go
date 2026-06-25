// Command genperspective renders perspective-distorted redaction fixtures: each
// is a normal pixelated text redaction (via fixture.Redact) warped into a tilted
// quadrilateral on a mid-gray photo canvas (so the patch is distinguishable for
// rectify.DetectQuad), simulating a redaction photographed at an angle. It writes
// the PNGs plus a manifest.json recording, per image, the
// source text/parameters and the quad corners — the image ↔ ground-truth link
// the perspective forward-model decode is tested against.
//
// Usage: go run ./internal/fixture/genperspective -out testdata/perspective
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/rectify"
)

// spec is one perspective fixture: the underlying redaction parameters plus the
// destination quad (top-left, top-right, bottom-right, bottom-left, photo px).
type spec struct {
	Name        string        `json:"name"`
	File        string        `json:"file"`
	Text        string        `json:"text"`
	Charset     string        `json:"charset"`
	FontSize    float64       `json:"font_size"`
	BlockSize   int           `json:"block_size"`
	PaddingTop  int           `json:"padding_top"`
	PaddingLeft int           `json:"padding_left"`
	RectW       int           `json:"rect_w"`
	RectH       int           `json:"rect_h"`
	PhotoW      int           `json:"photo_w"`
	PhotoH      int           `json:"photo_h"`
	Quad        [4][2]float64 `json:"quad"`
}

// cases are the source redactions; the quad for each is derived from the
// rendered redaction's size so it always fits the photo canvas.
var cases = []fixture.Spec{
	{Name: "persp_go", Text: "go", Charset: "go abcd", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
	{Name: "persp_cat", Text: "cat", Charset: "cat eoabd", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
	{Name: "persp_hello", Text: "hello", Charset: "helo abcd", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
}

func main() {
	out := flag.String("out", "testdata/perspective", "output directory for images + manifest")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "genperspective:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	specs := make([]spec, 0, len(cases))
	for _, c := range cases {
		red, err := fixture.Redact(c)
		if err != nil {
			return fmt.Errorf("redact %q: %w", c.Text, err)
		}
		rw, rh := red.Bounds().Dx(), red.Bounds().Dy()
		quad := tiltedQuad(rw, rh)
		photoW, photoH := rw+100, rh+100

		rectToPhoto, err := rectify.RectToQuad(float64(rw), float64(rh), quad)
		if err != nil {
			return fmt.Errorf("homography %q: %w", c.Name, err)
		}
		photoToRect, err := rectToPhoto.Inverse()
		if err != nil {
			return fmt.Errorf("inverse %q: %w", c.Name, err)
		}
		photo := rectify.Warp(red, photoToRect, photoW, photoH)
		// Repaint everything OUTSIDE the quad mid-gray so the (white-padded) patch
		// is distinguishable from the page — this is what lets rectify.DetectQuad
		// (and `--rectify auto`) recover the corners. In-quad pixels are untouched,
		// so manual-quad decoding is byte-for-byte unaffected.
		for y := range photoH {
			for x := range photoW {
				rp := photoToRect.Apply(rectify.Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
				if rp.X >= 0 && rp.Y >= 0 && rp.X < float64(rw) && rp.Y < float64(rh) {
					continue
				}
				o := photo.PixOffset(x, y)
				photo.Pix[o], photo.Pix[o+1], photo.Pix[o+2], photo.Pix[o+3] = 128, 128, 128, 255
			}
		}

		file := c.Name + ".png"
		if err := writePNG(filepath.Join(out, file), photo); err != nil {
			return err
		}
		specs = append(specs, spec{
			Name: c.Name, File: file, Text: c.Text, Charset: c.Charset,
			FontSize: c.FontSize, BlockSize: c.BlockSize,
			PaddingTop: c.PaddingTop, PaddingLeft: c.PaddingLeft,
			RectW: rw, RectH: rh,
			PhotoW: photoW, PhotoH: photoH,
			Quad: [4][2]float64{
				{quad[0].X, quad[0].Y},
				{quad[1].X, quad[1].Y},
				{quad[2].X, quad[2].Y},
				{quad[3].X, quad[3].Y},
			},
		})
	}

	if err := writeManifest(filepath.Join(out, "manifest.json"), specs); err != nil {
		return err
	}
	fmt.Printf("genperspective: wrote %d fixtures + manifest to %s\n", len(specs), out)
	return nil
}

// tiltedQuad returns a clockwise quad (TL,TR,BR,BL) placing a rw×rh rectangle at
// a fixed perspective slant inside a (rw+100)×(rh+100) canvas.
func tiltedQuad(rw, rh int) [4]rectify.Point {
	w, h := float64(rw), float64(rh)
	return [4]rectify.Point{
		{X: 40, Y: 30},          // top-left
		{X: 40 + w, Y: 30 + 18}, // top-right (tilted down)
		{X: 40 + w - 12, Y: 30 + h + 22},
		{X: 40 + 6, Y: 30 + h + 14},
	}
}

func writeManifest(path string, specs []spec) error {
	f, err := os.Create(path) // #nosec G304 -- generator writes to controlled fixture paths
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(specs); err != nil {
		_ = f.Close()
		return fmt.Errorf("encode manifest: %w", err)
	}
	return f.Close()
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path) // #nosec G304 — generator writes to the -out dir
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return f.Close()
}
