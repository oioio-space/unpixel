// Command gencontext renders the context-corpus reference images to an output
// directory alongside a manifest.json. Each image contains a VISIBLE cleartext
// region (sharp, for font calibration — C1) and an ADJACENT MOSAIC-PIXELATED
// region of a secret string, both rendered in the SAME font, size and gamma.
//
// Two layouts are supported:
//   - same-line: visible label immediately followed by the mosaic on the same baseline.
//   - label-above: visible label on the first line, mosaic region on the line below.
//
// Pixel coordinates for both regions are measured exactly from the rendered
// geometry and written into manifest.json — no guessing required.
//
// Usage:
//
//	go run ./internal/fixture/gencontext -out testdata/context
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
)

func main() {
	out := flag.String("out", "testdata/context", "output directory for images + manifest")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gencontext:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", out, err)
	}

	// Load the bundled font catalog once; build a name → data map.
	all := fonts.All()
	byName := make(map[string]fonts.Font, len(all))
	for _, f := range all {
		byName[f.Name] = f
	}

	specs := fixture.ContextMatrix()
	result := make([]fixture.ContextSpec, 0, len(specs))

	for _, s := range specs {
		rendered, err := renderContext(s, byName)
		if err != nil {
			return fmt.Errorf("render %q: %w", s.Name, err)
		}
		if err := writePNG(filepath.Join(out, s.File()), rendered.img); err != nil {
			return err
		}
		// For C1b cross-image specs, also generate the companion sample PNG.
		if rendered.spec.FontSample != nil {
			if err := renderFontSample(out, &rendered.spec, byName); err != nil {
				return fmt.Errorf("font sample for %q: %w", s.Name, err)
			}
		}
		result = append(result, rendered.spec)
	}

	if err := writeManifest(filepath.Join(out, "manifest.json"), result); err != nil {
		return err
	}
	fmt.Printf("gencontext: wrote %d images + manifest.json to %s\n", len(result), out)
	return nil
}

// renderFontSample generates the companion font-sample PNG for a C1b cross-image
// spec. It renders the FontSample.SampleText in the SAME font and weight as the
// parent spec, as SHARP (un-pixelated) text, and writes the PNG alongside the
// redaction image. It also populates FontSample.SampleRect with the exact bounds.
func renderFontSample(out string, spec *fixture.ContextSpec, _ map[string]fonts.Font) error {
	fs := spec.FontSample
	if fs == nil {
		return nil
	}

	// Build a varfont renderer at the same wght as the parent.
	axes := []varfont.Axis{{Tag: "wght", Value: spec.VarWght}}
	r, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), axes)
	if err != nil {
		return fmt.Errorf("varfont renderer: %w", err)
	}

	style := unpixel.Style{FontSize: spec.FontSize, PaddingTop: 8, PaddingLeft: 8}
	img, sentinelX, err := r.Render(fs.SampleText, style)
	if err != nil {
		return fmt.Errorf("render sample text %q: %w", fs.SampleText, err)
	}
	// Crop to the text area; drop the blue sentinel column.
	img = imutil.Crop(img, 0, 0, sentinelX, img.Bounds().Dy())

	fs.SampleRect = fixture.Rect{X: 0, Y: 0, W: img.Bounds().Dx(), H: img.Bounds().Dy()}

	return writePNG(filepath.Join(out, fs.File()), img)
}

// contextRender holds the rendered image and the spec with exact pixel rects filled in.
type contextRender struct {
	img  *image.RGBA
	spec fixture.ContextSpec
}

// renderContext renders one context fixture and returns the composed image plus
// the spec with VisibleRect and RedactedRect populated from the actual geometry.
func renderContext(s fixture.ContextSpec, byName map[string]fonts.Font) (contextRender, error) {
	pix := newPixelator(s)

	if s.VarFont {
		return renderVarFont(s, pix)
	}
	return renderStaticFont(s, byName, pix)
}

// newPixelator builds the appropriate pixelator for the spec.
func newPixelator(s fixture.ContextSpec) unpixel.Pixelator {
	if s.Linear {
		return pixelate.NewLinearBlockAverage(s.BlockSize)
	}
	return pixelate.NewBlockAverage(s.BlockSize)
}

// renderStaticFont handles Liberation Sans / Liberation Mono / Carlito fixtures.
func renderStaticFont(s fixture.ContextSpec, byName map[string]fonts.Font, pix unpixel.Pixelator) (contextRender, error) {
	f, ok := byName[s.Font]
	if !ok {
		return contextRender{}, fmt.Errorf("font %q not found in bundled catalog", s.Font)
	}

	r, err := render.NewXImageFromFonts(f.Data, nil)
	if err != nil {
		return contextRender{}, fmt.Errorf("renderer for %s: %w", s.Font, err)
	}

	style := unpixel.Style{FontSize: s.FontSize, PaddingTop: 8, PaddingLeft: 8}

	switch s.Layout {
	case fixture.LayoutSameLine:
		return composeSameLine(s, r, pix, style)
	case fixture.LayoutLabelAbove:
		return composeLabelAbove(s, r, pix, style)
	default:
		return contextRender{}, fmt.Errorf("unknown layout %q", s.Layout)
	}
}

// renderVarFont handles Nunito variable-font fixtures.
func renderVarFont(s fixture.ContextSpec, pix unpixel.Pixelator) (contextRender, error) {
	axes := []varfont.Axis{{Tag: "wght", Value: s.VarWght}}
	r, err := varfont.NewVarRenderer(bytes.NewReader(vfembed.NunitoVFWght), axes)
	if err != nil {
		return contextRender{}, fmt.Errorf("varfont renderer: %w", err)
	}

	style := unpixel.Style{FontSize: s.FontSize, PaddingTop: 8, PaddingLeft: 8}

	switch s.Layout {
	case fixture.LayoutSameLine:
		return composeSameLine(s, r, pix, style)
	case fixture.LayoutLabelAbove:
		return composeLabelAbove(s, r, pix, style)
	default:
		return contextRender{}, fmt.Errorf("unknown layout %q", s.Layout)
	}
}

// renderer is the common interface satisfied by both *render.XImage and
// *varfont.VarRenderer.
type renderer interface {
	Render(text string, style unpixel.Style) (*image.RGBA, int, error)
}

// composeSameLine renders a same-line fixture: visible label immediately
// followed by the pixelated mosaic of the secret on the same baseline.
//
// Layout (single row, left-to-right):
//
//	[  visible cleartext  |  mosaic  ]
func composeSameLine(s fixture.ContextSpec, r renderer, pix unpixel.Pixelator, style unpixel.Style) (contextRender, error) {
	// Render visible text (sharp) to measure its width.
	visImg, visX, err := r.Render(s.VisibleText, style)
	if err != nil {
		return contextRender{}, fmt.Errorf("render visible %q: %w", s.VisibleText, err)
	}
	visW := visX // sentinel = text right edge = width of text area
	visH := visImg.Bounds().Dy()

	// Render and pixelate the secret.
	secImg, secX, err := r.Render(s.Secret, style)
	if err != nil {
		return contextRender{}, fmt.Errorf("render secret %q: %w", s.Secret, err)
	}
	// Crop secret to the text area (drop sentinel).
	secCrop := imutil.Crop(secImg, 0, 0, secX, secImg.Bounds().Dy())
	// Block-align width.
	secCrop = blockAlign(secCrop, s.BlockSize)
	secPixelated := pix.Pixelate(secCrop, 0, 0)
	secW := secPixelated.Bounds().Dx()
	secH := secPixelated.Bounds().Dy()

	// Compose: canvas is wide enough for both side by side.
	totalH := max(visH, secH)
	totalW := visW + secW

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	imutil.FillWhite(canvas)

	// Blit visible text at (0, 0) — crop to its sentinel width.
	visCrop := imutil.Crop(visImg, 0, 0, visW, visH)
	imutil.Compose(canvas, visCrop, 0, 0)

	// Blit pixelated secret immediately to the right.
	imutil.Compose(canvas, secPixelated, visW, 0)

	spec := s
	spec.VisibleRect = fixture.Rect{X: 0, Y: 0, W: visW, H: visH}
	spec.RedactedRect = fixture.Rect{X: visW, Y: 0, W: secW, H: secH}

	return contextRender{img: canvas, spec: spec}, nil
}

// composeLabelAbove renders a label-above fixture: visible label on the first
// line, pixelated mosaic of the secret on the line below.
//
// Layout (two rows, same left margin):
//
//	[ visible label  ]
//	[    mosaic      ]
func composeLabelAbove(s fixture.ContextSpec, r renderer, pix unpixel.Pixelator, style unpixel.Style) (contextRender, error) {
	// Render visible label (sharp).
	visImg, visX, err := r.Render(s.VisibleText, style)
	if err != nil {
		return contextRender{}, fmt.Errorf("render visible %q: %w", s.VisibleText, err)
	}
	visW := visX
	visH := visImg.Bounds().Dy()

	// Render and pixelate the secret.
	secImg, secX, err := r.Render(s.Secret, style)
	if err != nil {
		return contextRender{}, fmt.Errorf("render secret %q: %w", s.Secret, err)
	}
	secCrop := imutil.Crop(secImg, 0, 0, secX, secImg.Bounds().Dy())
	secCrop = blockAlign(secCrop, s.BlockSize)
	secPixelated := pix.Pixelate(secCrop, 0, 0)
	secW := secPixelated.Bounds().Dx()
	secH := secPixelated.Bounds().Dy()

	// Compose: two rows stacked vertically.
	totalW := max(visW, secW)
	totalH := visH + secH

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	imutil.FillWhite(canvas)

	// Row 0: visible label (crop to sentinel width).
	visCrop := imutil.Crop(visImg, 0, 0, visW, visH)
	imutil.Compose(canvas, visCrop, 0, 0)

	// Row 1: mosaic (same left margin, directly below).
	imutil.Compose(canvas, secPixelated, 0, visH)

	spec := s
	spec.VisibleRect = fixture.Rect{X: 0, Y: 0, W: visW, H: visH}
	spec.RedactedRect = fixture.Rect{X: 0, Y: visH, W: secW, H: secH}

	return contextRender{img: canvas, spec: spec}, nil
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

// writePNG encodes img as a PNG at path. The #nosec annotation suppresses
// G304 (file path from variable) — the generator writes to a controlled output
// directory supplied by the caller.
func writePNG(path string, img *image.RGBA) (err error) {
	// Stamp any blue sentinel pixels white so the committed PNG contains only
	// sharp text and mosaic blocks — no rendering artefacts.
	cleanSentinel(img)

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

// cleanSentinel replaces every pure-blue pixel (B=255, R≠255, G≠255) in img
// with opaque white. The renderer appends a blue sentinel to locate text width;
// we strip it from the committed fixture so only text and mosaic remain.
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
	// #nosec G117 -- the "secret" field is this test corpus's redaction
	// ground-truth text (what a decoder must recover), not a real credential.
	if err = enc.Encode(specs); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}
