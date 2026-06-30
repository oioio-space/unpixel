// Command gengeometry renders the geometry-calibration corpus to an output
// directory alongside a manifest.json. Each image contains:
//   - a VISIBLE cleartext region rendered sharp and horizontally scaled by
//     XStretch (CatmullRom bicubic — the same kernel used by CalibrateGeometry).
//   - an ADJACENT MOSAIC-PIXELATED region of a secret string rendered at the
//     same font size and stretch so the forward model is consistent.
//
// The VisibleRect and RedactedRect fields in the manifest record exact pixel
// coordinates so tests can crop each region precisely.
//
// The Nunito variable font (bundled at VarWght=400) is used throughout so the
// axis is not a variable here — only font size and x-stretch vary, which is
// exactly what CalibrateGeometry is designed to recover.
//
// Usage:
//
//	go run ./internal/fixture/gengeometry -out testdata/geometry
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
)

func main() {
	out := flag.String("out", "testdata/geometry", "output directory for images + manifest")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gengeometry:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", out, err)
	}

	specs := fixture.GeometryMatrix()
	result := make([]fixture.ContextSpec, 0, len(specs))

	for _, s := range specs {
		rendered, err := renderGeometry(s)
		if err != nil {
			return fmt.Errorf("render %q: %w", s.Name, err)
		}
		if err := writePNG(filepath.Join(out, s.File()), rendered.img); err != nil {
			return err
		}
		result = append(result, rendered.spec)
	}

	if err := writeManifest(filepath.Join(out, "manifest.json"), result); err != nil {
		return err
	}
	fmt.Printf("gengeometry: wrote %d images + manifest.json to %s\n", len(result), out)
	return nil
}

// geomRender holds the composed image and the spec with pixel rects filled in.
type geomRender struct {
	img  *image.RGBA
	spec fixture.ContextSpec
}

// renderGeometry renders one geometry-calibration fixture and returns the
// composed image plus the spec with VisibleRect and RedactedRect populated.
//
// The visible region is a SHARP x-stretched render of VisibleText; the
// redacted region is the same text (Secret) rendered, stretched, and pixelated.
// Both regions share the same font size and stretch so the forward model used
// by CalibrateGeometry is internally consistent.
func renderGeometry(s fixture.ContextSpec) (geomRender, error) {
	xStretch := s.XStretch
	if xStretch <= 0 {
		xStretch = 1.0
	}

	// Build a Nunito VarRenderer at the default wght (400). Only size and
	// stretch vary in the geometry corpus; the axis value is a constant.
	axes := []varfont.Axis{{Tag: "wght", Value: s.VarWght}}
	r, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), axes)
	if err != nil {
		return geomRender{}, fmt.Errorf("varfont renderer: %w", err)
	}

	style := unpixel.Style{FontSize: s.FontSize, PaddingTop: 8, PaddingLeft: 8}

	// ── Visible region ────────────────────────────────────────────────────────
	visImg, visX, err := r.Render(s.VisibleText, style)
	if err != nil {
		return geomRender{}, fmt.Errorf("render visible %q: %w", s.VisibleText, err)
	}
	// Drop the blue sentinel column, then clean any sentinel artefacts.
	visSentinelCrop := imutil.Crop(visImg, 0, 0, visX, visImg.Bounds().Dy())
	cleanSentinel(visSentinelCrop)
	// Crop further to tight ink bounds so the visible crop is all-ink (no
	// padding rows). CalibrateGeometry compares ink-tight candidates against the
	// target; if the target has large white margins a tiny candidate wins by
	// being "also mostly white" — the ink-tight crop removes that degeneracy.
	inkRect := inkBounds(visSentinelCrop)
	if inkRect.Empty() {
		return geomRender{}, fmt.Errorf("render visible %q: no ink found", s.VisibleText)
	}
	visInkCrop := imutil.Crop(visSentinelCrop, inkRect.Min.X, inkRect.Min.Y, inkRect.Dx(), inkRect.Dy())
	visStretched := applyStretch(visInkCrop, xStretch)
	visW := visStretched.Bounds().Dx()
	visH := visStretched.Bounds().Dy()

	// ── Redacted region ───────────────────────────────────────────────────────
	secImg, secX, err := r.Render(s.Secret, style)
	if err != nil {
		return geomRender{}, fmt.Errorf("render secret %q: %w", s.Secret, err)
	}
	secCrop := imutil.Crop(secImg, 0, 0, secX, secImg.Bounds().Dy())
	cleanSentinel(secCrop)
	secStretched := applyStretch(secCrop, xStretch)
	// Block-align width after stretch.
	secAligned := blockAlign(secStretched, s.BlockSize)

	var pix unpixel.Pixelator
	if s.Linear {
		pix = pixelate.NewLinearBlockAverage(s.BlockSize)
	} else {
		pix = pixelate.NewBlockAverage(s.BlockSize)
	}
	secPixelated := pix.Pixelate(secAligned, 0, 0)
	secW := secPixelated.Bounds().Dx()
	secH := secPixelated.Bounds().Dy()

	// ── Compose: visible left, mosaic right (same-line layout) ───────────────
	totalH := max(visH, secH)
	totalW := visW + secW

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	imutil.FillWhite(canvas)
	imutil.Compose(canvas, visStretched, 0, 0)
	imutil.Compose(canvas, secPixelated, visW, 0)

	spec := s
	spec.VisibleRect = fixture.Rect{X: 0, Y: 0, W: visW, H: visH}
	spec.RedactedRect = fixture.Rect{X: visW, Y: 0, W: secW, H: secH}

	return geomRender{img: canvas, spec: spec}, nil
}

// inkLumCutoff is the per-channel luminance below which a pixel is considered
// ink. Must match the value used in varfont.inkBoundsGeom (244) so that the
// target crop produced here and the candidate crop inside CalibrateGeometry are
// pixel-consistent.
const inkLumCutoff = uint8(244)

// inkBounds returns the tight bounding rectangle of non-white (ink) pixels in
// img. Returns an empty rectangle when the image contains no ink.
func inkBounds(img *image.RGBA) image.Rectangle {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.R < inkLumCutoff || c.G < inkLumCutoff || c.B < inkLumCutoff {
				minX = min(minX, x)
				minY = min(minY, y)
				maxX = max(maxX, x+1)
				maxY = max(maxY, y+1)
			}
		}
	}
	if minX >= maxX || minY >= maxY {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// applyStretch scales img horizontally by the given factor using CatmullRom
// bicubic interpolation — the same kernel used by CalibrateGeometry and the
// mosaictext decode path. Returns img unchanged when stretch is exactly 1.0.
func applyStretch(img *image.RGBA, stretch float64) *image.RGBA {
	if stretch == 1.0 {
		return img
	}
	b := img.Bounds()
	nw := int(math.Round(float64(b.Dx()) * stretch))
	if nw < 1 {
		nw = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, nw, b.Dy()))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, b, xdraw.Over, nil)
	return out
}

// blockAlign white-pads img's width to the next multiple of blockSize.
// Returns img unchanged when already aligned.
func blockAlign(img *image.RGBA, blockSize int) *image.RGBA {
	w := img.Bounds().Dx()
	rem := w % blockSize
	if rem == 0 {
		return img
	}
	return imutil.PadWhite(img, w+blockSize-rem, img.Bounds().Dy())
}

// cleanSentinel replaces every pure-blue pixel (B=255, R≠255, G≠255) in img
// with opaque white. The renderer appends a blue sentinel to locate text width;
// we strip it before cropping and stretching so committed PNGs contain only ink.
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

// writeManifest encodes specs as indented JSON at path.
func writeManifest(path string, specs []fixture.ContextSpec) (err error) {
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
	// #nosec G117 -- the "secret" field is test-corpus ground-truth, not a real credential.
	if err = enc.Encode(specs); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}
