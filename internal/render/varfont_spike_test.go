// Package render — variable-font feasibility spike test.
//
// This file is a bounded spike: it proves that pure-Go variable-font axis
// instancing (via github.com/go-text/typesetting) actually changes glyph
// outlines and rasterized pixels.  It does NOT wire into the production
// renderer.  Run in isolation:
//
//	scripts/gotest-caged.sh go test ./internal/render/ -run SpikeVarFont -count=1 -v
package render

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"testing"

	gtfont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/vector"
)

//go:embed fonts/variable/NunitoVF-wght.ttf
var nunitoVFData []byte

// TestSpikeVarFont_AxisInstancingChangesRaster verifies three things:
//
//  1. go-text/typesetting loads the embedded Nunito VF font (pure Go, no CGO).
//  2. SetVariations with different wght values produces different normalised
//     coordinates and different glyph-outline segment coordinates.
//  3. Rasterising those outlines at the same ppem yields bitmaps with a
//     statistically meaningful number of differing pixels.
//
// A positive result de-risks big-bet B1 (continuous variable-font fitting).
func TestSpikeVarFont_AxisInstancingChangesRaster(t *testing.T) {
	t.Log("=== B1 variable-font feasibility spike ===")

	face := mustLoadNunitoFace(t)

	const ppem = 64 // pixels-per-em; large enough to see weight differences
	upem := float32(face.Upem())

	// Scale from font units to pixel space: pixelCoord = fontCoord * ppem / upem.
	// The font coordinate system has Y increasing upward; the rasterizer has Y
	// increasing downward, so we negate Y and offset by the ascender height.
	const (
		wghtLight = 200
		wghtHeavy = 900
	)
	gid, ok := face.NominalGlyph('a')
	if !ok {
		t.Fatal("font does not contain glyph 'a'")
	}

	imgLight := renderGlyph(t, face, gid, wghtLight, ppem, upem)
	imgHeavy := renderGlyph(t, face, gid, wghtHeavy, ppem, upem)

	// Count differing pixels.
	diffPx := countDifferingPixels(imgLight, imgHeavy)
	t.Logf("wght=%-3d vs wght=%-3d: %d differing pixels (image %dx%d = %d total)",
		wghtLight, wghtHeavy,
		diffPx,
		imgLight.Bounds().Dx(), imgLight.Bounds().Dy(),
		imgLight.Bounds().Dx()*imgLight.Bounds().Dy())

	const minDiff = 50 // conservative floor — any real weight change should exceed this
	if got, want := diffPx, minDiff; got < want {
		t.Errorf("too few differing pixels: got %d < want >= %d — axis instancing may not be working", got, want)
	}
}

// TestSpikeVarFont_OutlineSegmentsDiffer checks the outline layer directly,
// without rasterisation, to confirm that segment coordinates shift between
// wght instances.
func TestSpikeVarFont_OutlineSegmentsDiffer(t *testing.T) {
	face := mustLoadNunitoFace(t)

	gid, ok := face.NominalGlyph('a')
	if !ok {
		t.Fatal("font does not contain glyph 'a'")
	}

	segs200 := outlineSegments(t, face, gid, 200)
	segs900 := outlineSegments(t, face, gid, 900)

	if got, want := len(segs200), len(segs900); got != want {
		t.Logf("segment count: wght=200 → %d, wght=900 → %d (may differ for complex glyphs)", got, want)
	}

	var diffCount int
	for i := range min(len(segs200), len(segs900)) {
		if segs200[i] != segs900[i] {
			diffCount++
		}
	}
	t.Logf("wght=200 vs wght=900: %d/%d outline segments differ", diffCount, len(segs200))

	if got, want := diffCount, 1; got < want {
		t.Errorf("got %d differing segments, want >= %d — axis instancing not changing the outline", got, want)
	}
}

// mustLoadNunitoFace loads the embedded Nunito VF font and fails the test on
// any parse error.
func mustLoadNunitoFace(t *testing.T) *gtfont.Face {
	t.Helper()
	face, err := gtfont.ParseTTF(bytes.NewReader(nunitoVFData))
	if err != nil {
		t.Fatalf("ParseTTF: %v", err)
	}
	return face
}

// outlineSegments instances the face at the given wght design value and returns
// the raw Segment slice for the given glyph.
func outlineSegments(t *testing.T, face *gtfont.Face, gid gtfont.GID, wght float32) []gtfont.Segment {
	t.Helper()
	face.SetVariations([]gtfont.Variation{{Tag: ot.MustNewTag("wght"), Value: wght}})
	t.Logf("wght=%.0f → normalised coords %v", wght, face.Coords())

	gd := face.GlyphData(gid)
	outline, ok := gd.(gtfont.GlyphOutline)
	if !ok {
		t.Fatalf("GlyphData returned %T, want GlyphOutline", gd)
	}
	// Return a copy so subsequent SetVariations calls do not mutate this slice.
	return slices.Clone(outline.Segments)
}

// renderGlyph instances the face at wght, obtains the glyph outline, and
// rasterises it to a grayscale image at the given ppem.
func renderGlyph(t *testing.T, face *gtfont.Face, gid gtfont.GID, wght, ppem int, upem float32) *image.Gray {
	t.Helper()
	segs := outlineSegments(t, face, gid, float32(wght))

	scale := float32(ppem) / upem

	// Compute the bounding box of the glyph in font units.
	// Font Y increases upward; raster Y increases downward.
	minX := float32(math.MaxFloat32)
	minY := float32(math.MaxFloat32)
	maxX := float32(-math.MaxFloat32)
	maxY := float32(-math.MaxFloat32)
	for _, s := range segs {
		for _, pt := range s.ArgsSlice() {
			minX = min(minX, pt.X)
			maxX = max(maxX, pt.X)
			minY = min(minY, pt.Y)
			maxY = max(maxY, pt.Y)
		}
	}

	// Pixel dimensions with a small margin.
	const margin = 4
	w := int(float32(maxX-minX)*scale) + margin*2 + 1
	h := int(float32(maxY-minY)*scale) + margin*2 + 1

	// offsetX/offsetY map the glyph's min coordinate to the top-left margin.
	offsetX := float32(margin) - minX*scale
	// Y flip: font maxY corresponds to pixel top (y=margin).
	offsetY := float32(margin) + maxY*scale

	ras := vector.NewRasterizer(w, h)

	for _, s := range segs {
		switch s.Op {
		case ot.SegmentOpMoveTo:
			px, py := toPixel(s.Args[0], scale, offsetX, offsetY)
			ras.MoveTo(px, py)
		case ot.SegmentOpLineTo:
			px, py := toPixel(s.Args[0], scale, offsetX, offsetY)
			ras.LineTo(px, py)
		case ot.SegmentOpQuadTo:
			p1x, p1y := toPixel(s.Args[0], scale, offsetX, offsetY)
			p2x, p2y := toPixel(s.Args[1], scale, offsetX, offsetY)
			ras.QuadTo(p1x, p1y, p2x, p2y)
		case ot.SegmentOpCubeTo:
			p1x, p1y := toPixel(s.Args[0], scale, offsetX, offsetY)
			p2x, p2y := toPixel(s.Args[1], scale, offsetX, offsetY)
			p3x, p3y := toPixel(s.Args[2], scale, offsetX, offsetY)
			ras.CubeTo(p1x, p1y, p2x, p2y, p3x, p3y)
		}
	}
	ras.ClosePath()

	alpha := image.NewAlpha(image.Rect(0, 0, w, h))
	ras.Draw(alpha, alpha.Bounds(), image.Opaque, image.Point{})

	// Convert alpha mask to Gray for easy pixel comparison.
	gray := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(gray, gray.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	for y := range h {
		for x := range w {
			a := alpha.AlphaAt(x, y).A
			// filled pixel → black; transparent → white (white background)
			if a > 127 {
				gray.SetGray(x, y, color.Gray{Y: 0})
			}
		}
	}
	return gray
}

// toPixel converts a font-unit point to pixel coordinates, applying the scale
// factor and Y-flip.
func toPixel(pt gtfont.SegmentPoint, scale, offsetX, offsetY float32) (float32, float32) {
	return pt.X*scale + offsetX, -pt.Y*scale + offsetY
}

// countDifferingPixels counts pixels that differ between two grayscale images.
// It only looks at the overlap of their bounds.
func countDifferingPixels(a, b *image.Gray) int {
	bounds := a.Bounds().Intersect(b.Bounds())
	var count int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if a.GrayAt(x, y) != b.GrayAt(x, y) {
				count++
			}
		}
	}
	return count
}
