package unpixel_test

import (
	"image"
	"image/png"
	"io"
	"os"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/metric"
)

// decodePNG decodes a PNG stream into an *image.RGBA.
func decodePNG(r io.Reader) (*image.RGBA, error) {
	img, err := png.Decode(r)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	xdraw.Draw(out, out.Bounds(), img, b.Min, xdraw.Src)
	return out, nil
}

// contentBounds returns the bounding box of non-background (luminance < 244)
// pixels — the tight extent of a light mosaic redaction within its margins.
func contentBounds(img *image.RGBA) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if (299*int(c.R)+587*int(c.G)+114*int(c.B))/1000 < 244 {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x+1), max(y1, y+1)
			}
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return b
	}
	return image.Rect(x0, y0, x1, y1)
}

// realMosaicSample is a real-world mosaic redaction of the text "Hello World !"
// (1450×509, large white margins), created in GIMP: text set in "Monospace"
// (Noto Sans Mono) at 62 px, GEGL Pixelize at a 16-px block, then the layer
// scaled ~2× — giving 32-px square blocks in the export. It is a hand-contributed
// sample committed under testdata/fixtures (exempt from the generator's manifest
// cross-check; see handContributedFixtures), so it exercises the pipeline on
// genuine third-party output rather than the engine's own renderer/pixelator
// round-tripped against itself.
const realMosaicSample = "testdata/fixtures/text_hello-world.png"

// notoMonoRenderer returns a renderer using the bundled Noto Sans Mono, the font
// Fedora's "Monospace" alias resolves to (the font used to create the sample).
func notoMonoRenderer(t *testing.T) unpixel.Renderer {
	t.Helper()
	for _, f := range fonts.All() {
		if f.Name == "Noto Sans Mono" {
			r, err := defaults.RendererFromFonts(f.Data, nil)
			if err != nil {
				t.Fatalf("build Noto Sans Mono renderer: %v", err)
			}
			return r
		}
	}
	t.Fatal("Noto Sans Mono not found in bundled fonts")
	return nil
}

// inkBounds returns the bounding box of the non-background (inked) pixels in the
// [0,sentinelX) region of a freshly rendered glyph image.
func inkBounds(img *image.RGBA, sentinelX int) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := sentinelX, b.Dy(), 0, 0
	for y := range b.Dy() {
		for x := range sentinelX {
			c := img.RGBAAt(x, y)
			if (299*int(c.R)+587*int(c.G)+114*int(c.B))/1000 < 240 {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x+1), max(y1, y+1)
			}
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return image.Rect(0, 0, 1, 1)
	}
	return image.Rect(x0, y0, x1, y1)
}

// renderStretched reproduces the pre-pixelation half of the GIMP pipeline for
// text in Noto Sans Mono: render → crop to ink → stretch horizontally by xScale.
func renderStretched(t *testing.T, r unpixel.Renderer, text string, fontSize, xScale float64) *image.RGBA {
	t.Helper()
	img, sx, err := r.Render(text, unpixel.Style{FontSize: fontSize})
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	bb := inkBounds(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	nw := int(float64(bb.Dx()) * xScale)
	stretched := image.NewRGBA(image.Rect(0, 0, nw, bb.Dy()))
	xdraw.CatmullRom.Scale(stretched, stretched.Bounds(), ink, ink.Bounds(), xdraw.Over, nil)
	return stretched
}

// bestDistance pixelates the stretched candidate (over a search of the block
// grid phase, since the sample's grid origin is unknown) and slides it over the
// target, returning the minimum metric distance — the score the generate-and-test
// attack would assign this candidate at its best grid origin and placement.
func bestDistance(stretched, target *image.RGBA, p unpixel.Pixelator, m unpixel.Metric, block int) float64 {
	tw, th := target.Bounds().Dx(), target.Bounds().Dy()
	best := 1.0
	for px := 0; px < block; px += 8 {
		for py := 0; py < block; py += 8 {
			// Pad by (px,py) so the pixelation grid phase shifts within the block.
			pad := image.NewRGBA(image.Rect(0, 0, stretched.Bounds().Dx()+px, stretched.Bounds().Dy()+py))
			xdraw.Draw(pad, pad.Bounds(), image.White, image.Point{}, xdraw.Src)
			xdraw.Draw(pad, image.Rect(px, py, px+stretched.Bounds().Dx(), py+stretched.Bounds().Dy()), stretched, stretched.Bounds().Min, xdraw.Src)
			cand := p.Pixelate(pad, 0, 0)
			cw, ch := cand.Bounds().Dx(), cand.Bounds().Dy()
			if cw > tw || ch > th {
				continue
			}
			for ox := 0; ox <= tw-cw && ox <= 64; ox += 4 {
				for oy := 0; oy <= th-ch && oy <= 64; oy += 4 {
					c := image.NewRGBA(image.Rect(0, 0, tw, th))
					xdraw.Draw(c, c.Bounds(), image.White, image.Point{}, xdraw.Src)
					xdraw.Draw(c, image.Rect(ox, oy, ox+cw, oy+ch), cand, cand.Bounds().Min, xdraw.Src)
					if d := m.Compare(c, target); d < best {
						best = d
					}
				}
			}
		}
	}
	return best
}

// TestRealMosaic_HelloWorld is the end-to-end confirmation for the real GIMP
// mosaic sample: re-running the generate-and-test forward model on the true
// plaintext "Hello World !" — in the matching typography (Noto Sans Mono) and
// with linear-light pixelation (GEGL's behaviour) — reproduces the redaction
// almost exactly, and far better than near-miss strings. This both proves the
// recovered text and pins the two properties that make it work: the bundled
// Noto Sans Mono font and [defaults.LinearBlockAverage].
//
// (Blind end-to-end search for this sample is impractical — 13 very-low-ink
// monospace glyphs give too little per-character signal for the guided DFS, so
// the model-level confirmation is the meaningful guard.)
func TestRealMosaic_HelloWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-mosaic forward-model sweep in -short mode")
	}
	f, err := os.Open(realMosaicSample)
	if err != nil {
		t.Fatalf("open %s: %v", realMosaicSample, err)
	}
	defer func() { _ = f.Close() }()
	src, err := decodePNG(f)
	if err != nil {
		t.Fatalf("decode %s: %v", realMosaicSample, err)
	}

	// Locate the mosaic content (non-background) and crop to it, with white
	// margin so a candidate render aligns inside.
	rect := contentBounds(src)
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+128, rect.Dy()+32))
	xdraw.Draw(target, target.Bounds(), image.White, image.Point{}, xdraw.Src)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), src, rect.Min, xdraw.Src)

	r := notoMonoRenderer(t)
	m := metric.NewPixelmatch(0.1)

	// Forward model for the GIMP parameters (62 px × 2 ≈ 124 pt; the 2× layer
	// scale was slightly anisotropic, ≈1.06 wider than tall).
	const (
		fontSize = 124.0
		xScale   = 1.06
		block    = 32
	)
	linear := defaults.LinearBlockAverage(block)
	truthStretched := renderStretched(t, r, "Hello World !", fontSize, xScale)
	truth := bestDistance(truthStretched, target, linear, m, block)

	// The true plaintext must reproduce the redaction near-exactly.
	if truth > 0.05 {
		t.Errorf("'Hello World !' reproduces the redaction at distance %.4f, want <= 0.05", truth)
	}

	// …and clearly better than near-miss strings (a one-character change, a
	// case change, a transposition).
	for _, wrong := range []string{"Hallo World !", "hello world !", "Hello Wrold !"} {
		if d := bestDistance(renderStretched(t, r, wrong, fontSize, xScale), target, linear, m, block); d <= truth {
			t.Errorf("near-miss %q scored %.4f, not worse than truth %.4f", wrong, d, truth)
		}
	}

	// The sRGB block mean (the faithful default, wrong for GEGL output) should
	// be markedly worse, demonstrating why LinearBlockAverage is required here.
	srgb := bestDistance(truthStretched, target, defaults.BlockAverage(block), m, block)
	if srgb <= truth {
		t.Errorf("sRGB-mean model scored %.4f, expected worse than linear %.4f", srgb, truth)
	}
	t.Logf("Hello World ! linear=%.4f sRGB=%.4f", truth, srgb)
}
