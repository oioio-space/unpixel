// Package varfont provides a variable-font renderer and a coordinate-descent
// axis fitter for the unpixel pipeline.
//
// The renderer implements [unpixel.Renderer] using go-text/typesetting (pure
// Go, no CGO) and an embedded or caller-supplied variable TrueType font. The
// axis fitter ([FitAxes]) runs coordinate descent over the font's design axes
// (wght, wdth, opsz, …) to minimise the image distance between a pixelated
// rendering of the known text and a target redaction crop, using the same
// render → pixelate → metric pipeline as the rest of unpixel.
//
// Concurrency: [ParseFont] parses the font once and returns a read-only
// [*Font] that may be shared across goroutines. Each [VarRenderer] owns a
// [sync.Pool] of [*gtfont.Face] objects pre-instanced to its fixed axes.
// Render borrows one Face per call and returns it to the pool, so concurrent
// callers never share a Face (SetVariations never races) while the fitter's
// single-goroutine hot loop reuses the same Face across all evals (zero
// per-eval alloc after the first get).
package varfont

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"

	gtfont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/vector"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// Axis is a single font-variation axis setting in design-space units
// (e.g. Tag="wght", Value=700).
type Axis struct {
	// Tag is the four-character OpenType axis tag (e.g. "wght", "wdth").
	Tag string
	// Value is the design-space coordinate for this axis.
	Value float32
}

// Font is a parsed variable-font file. It is read-only after construction and
// safe to share across goroutines. Obtain one with [ParseFont].
type Font struct {
	raw  *gtfont.Font // parsed once; read-only
	upem float32      // font units per em, cached for speed
}

// ParseFont parses a TrueType / OpenType variable-font from r.
// r must implement io.Reader, io.Seeker, and io.ReaderAt (e.g. *bytes.Reader).
// The returned *Font is read-only and safe to share across goroutines.
func ParseFont(r gtfont.Resource) (*Font, error) {
	face, err := gtfont.ParseTTF(r)
	if err != nil {
		return nil, fmt.Errorf("parse variable font: %w", err)
	}
	return &Font{
		raw:  face.Font,
		upem: float32(face.Upem()),
	}, nil
}

// VarRenderer renders text using a variable TrueType font at fixed axis
// coordinates. It implements [unpixel.Renderer].
//
// The zero value is not usable; obtain one with [NewVarRenderer].
//
// Contract:
//   - The returned image has a white background; text is drawn in black.
//   - sentinelX is the x-pixel column where the blue sentinel begins, equal to
//     paddingLeft + the total horizontal advance of the text.
//   - Glyphs with no outline in the font (missing or .notdef) are skipped
//     silently: their advance is still counted so the sentinel lands correctly.
//   - The renderer is safe for concurrent use: each Render call borrows a
//     pre-instanced Face from a per-renderer sync.Pool and returns it when
//     done, so concurrent callers never share a Face.
type VarRenderer struct {
	font *Font
	axes []Axis
	// pool holds *gtfont.Face objects pre-instanced to axes.
	// New() allocates a fresh Face; Get/Put borrow and return without
	// re-running SetVariations, eliminating the per-Render alloc hot spot.
	pool sync.Pool
}

// NewVarRenderer returns a VarRenderer that renders text with the given font
// at the supplied axis coordinates. r must be a seekable/random-access reader
// (e.g. *bytes.Reader) of a TrueType variable-font file.
//
// axes configures the variation instance; construct a new VarRenderer to
// render at different coordinates.
func NewVarRenderer(r gtfont.Resource, axes []Axis) (*VarRenderer, error) {
	f, err := ParseFont(r)
	if err != nil {
		return nil, err
	}
	return newFontVarRenderer(f, axes), nil
}

// newFontVarRenderer is an internal constructor used by FitAxes so it can
// share the already-parsed Font without re-parsing the TTF bytes.
func newFontVarRenderer(f *Font, axes []Axis) *VarRenderer {
	vr := &VarRenderer{font: f, axes: axes}
	vr.pool.New = func() any {
		face := gtfont.NewFace(f.raw)
		applyAxes(face, axes)
		return face
	}
	return vr
}

// Render draws text on a white RGBA image at the renderer's current axis
// coordinates and appends a blue sentinel block to the right of the text.
// It returns the image and sentinelX — the x-column where the sentinel begins,
// which equals paddingLeft + the total horizontal advance of the text.
//
// style controls font size (points at 72 DPI), padding, and letter-spacing;
// see [unpixel.Style]. Bold is not supported by the variable-font path (the
// caller should pick a wght axis value instead); a non-zero Bold flag is
// silently ignored.
//
// Missing glyphs are skipped (their advance is still added); Render never
// returns an error for missing glyphs, only for unrecoverable layout failures.
func (r *VarRenderer) Render(text string, style unpixel.Style) (*image.RGBA, int, error) {
	// Borrow a pre-instanced Face from the pool; axes are already applied.
	// Put it back on every return path so the next call (or the same
	// goroutine in the single-threaded fitter hot loop) reuses it.
	face := r.pool.Get().(*gtfont.Face)
	defer r.pool.Put(face)
	img, sx := renderWithFace(face, r.font, text, style)
	return img, sx, nil
}

// renderWithFace is the infallible rendering kernel shared by Render (pool
// path) and the fitter's evaluate (single reused face path). face must already
// have the desired axis coords applied via applyAxes before the call.
func renderWithFace(face *gtfont.Face, f *Font, text string, style unpixel.Style) (*image.RGBA, int) {
	ppem := float32(style.FontSize) // points at 72 DPI → px (1 pt = 1 px at 72 DPI)
	if ppem <= 0 {
		ppem = 32
	}
	scale := ppem / f.upem

	paddingLeft := style.PaddingLeft
	if paddingLeft == 0 {
		paddingLeft = 8
	}
	paddingTop := style.PaddingTop
	if paddingTop == 0 {
		paddingTop = 8
	}

	// Ascender / descender from FontHExtents (font units → pixels, Y-up → Y-down).
	extents, ok := face.FontHExtents()
	ascenderPx := int(math.Ceil(float64(extents.Ascender * scale)))
	descenderPx := int(math.Ceil(float64(-extents.Descender * scale))) // descender is negative
	if !ok || ascenderPx <= 0 {
		// Fallback: use 80 % / 20 % of ppem when the font table is absent.
		ascenderPx = int(math.Ceil(float64(ppem) * 0.8))
		descenderPx = int(math.Ceil(float64(ppem) * 0.2))
	}

	// Baseline Y in pixel space (distance from top of image to baseline).
	baselineY := float32(paddingTop + ascenderPx)

	// Measure total text advance for image-width calculation and sentinelX.
	letterSpacing := float32(style.LetterSpacing)
	var totalAdvancePx float32
	for _, ch := range text {
		adv := glyphAdvancePx(face, ch, scale)
		totalAdvancePx += adv + letterSpacing
	}
	textW := int(math.Ceil(float64(totalAdvancePx)))

	const sentinelWidth = 24
	imgW := paddingLeft + textW + sentinelWidth + 8
	imgH := paddingTop + ascenderPx + descenderPx + 4

	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	imutil.FillWhite(img)

	// Draw each glyph at the current cursor X position.
	curX := float32(paddingLeft)
	for _, ch := range text {
		gid, found := face.NominalGlyph(ch)
		adv := glyphAdvancePx(face, ch, scale)
		if found {
			drawGlyph(img, face, gid, scale, curX, baselineY)
		}
		curX += adv + letterSpacing
	}

	sentinelX := paddingLeft + textW

	// Blue sentinel block: same height as text (ascender+descender).
	blue := color.RGBA{B: 255, A: 255}
	sentinelTop := paddingTop
	sentinelBot := paddingTop + ascenderPx + descenderPx
	for y := sentinelTop; y < sentinelBot && y < imgH; y++ {
		for x := sentinelX; x < sentinelX+sentinelWidth && x < imgW; x++ {
			img.SetRGBA(x, y, blue)
		}
	}

	return img, sentinelX
}

// applyAxes instances face at the given design-space axis coordinates.
func applyAxes(face *gtfont.Face, axes []Axis) {
	vars := make([]gtfont.Variation, len(axes))
	for i, a := range axes {
		vars[i] = gtfont.Variation{Tag: ot.MustNewTag(a.Tag), Value: a.Value}
	}
	face.SetVariations(vars)
}

// glyphAdvancePx returns the horizontal advance of ch in pixels.
// Returns a half-em approximation when the glyph is missing from the font.
func glyphAdvancePx(face *gtfont.Face, ch rune, scale float32) float32 {
	gid, ok := face.NominalGlyph(ch)
	if !ok {
		// Missing glyph: use ½ em as a placeholder advance.
		return float32(face.Upem()) * scale * 0.5
	}
	return face.HorizontalAdvance(gid) * scale
}

// drawGlyph rasterises glyph gid onto img at pixel position (originX, originY),
// where originY is the baseline. It is a no-op when the glyph has no outline
// (e.g. space); the caller is responsible for advancing the cursor regardless.
func drawGlyph(img *image.RGBA, face *gtfont.Face, gid gtfont.GID, scale, originX, baselineY float32) {
	gd := face.GlyphData(gid)
	outline, ok := gd.(gtfont.GlyphOutline)
	if !ok || len(outline.Segments) == 0 {
		return // space or missing outline — skip silently
	}

	// Tight bounding box in pixel space to size the rasterizer.
	minX, minY, maxX, maxY := glyphBounds(outline.Segments, scale, originX, baselineY)
	if maxX <= minX || maxY <= minY {
		return
	}

	// Rasterize into a local alpha mask sized to the glyph bounding box.
	bx0 := int(math.Floor(float64(minX)))
	by0 := int(math.Floor(float64(minY)))
	bx1 := int(math.Ceil(float64(maxX))) + 1
	by1 := int(math.Ceil(float64(maxY))) + 1

	w, h := bx1-bx0, by1-by0
	if w <= 0 || h <= 0 {
		return
	}

	// Shift all coordinates so the rasterizer origin is (bx0, by0).
	ox, oy := originX-float32(bx0), baselineY-float32(by0)
	ras := vector.NewRasterizer(w, h)
	for _, s := range outline.Segments {
		switch s.Op {
		case ot.SegmentOpMoveTo:
			px, py := fontToPixel(s.Args[0], scale, ox, oy)
			ras.MoveTo(px, py)
		case ot.SegmentOpLineTo:
			px, py := fontToPixel(s.Args[0], scale, ox, oy)
			ras.LineTo(px, py)
		case ot.SegmentOpQuadTo:
			p1x, p1y := fontToPixel(s.Args[0], scale, ox, oy)
			p2x, p2y := fontToPixel(s.Args[1], scale, ox, oy)
			ras.QuadTo(p1x, p1y, p2x, p2y)
		case ot.SegmentOpCubeTo:
			p1x, p1y := fontToPixel(s.Args[0], scale, ox, oy)
			p2x, p2y := fontToPixel(s.Args[1], scale, ox, oy)
			p3x, p3y := fontToPixel(s.Args[2], scale, ox, oy)
			ras.CubeTo(p1x, p1y, p2x, p2y, p3x, p3y)
		}
	}
	ras.ClosePath()

	alpha := image.NewAlpha(image.Rect(0, 0, w, h))
	ras.Draw(alpha, alpha.Bounds(), image.Opaque, image.Point{})

	// Composite alpha mask onto the RGBA image as black ink.
	ib := img.Bounds()
	for y := range h {
		for x := range w {
			px, py := bx0+x, by0+y
			if px < ib.Min.X || py < ib.Min.Y || px >= ib.Max.X || py >= ib.Max.Y {
				continue
			}
			a := alpha.AlphaAt(x, y).A
			if a == 0 {
				continue
			}
			// Alpha-blend black ink over the current pixel (background is white).
			dst := img.RGBAAt(px, py)
			af := float32(a) / 255
			dst.R = uint8(float32(dst.R) * (1 - af))
			dst.G = uint8(float32(dst.G) * (1 - af))
			dst.B = uint8(float32(dst.B) * (1 - af))
			img.SetRGBA(px, py, dst)
		}
	}
}

// glyphBounds computes the pixel-space bounding box of the glyph outline after
// applying the scale and Y-flip. minX, minY are the top-left corner in pixels.
func glyphBounds(segs []gtfont.Segment, scale, originX, baselineY float32) (minX, minY, maxX, maxY float32) {
	minX = math.MaxFloat32
	minY = math.MaxFloat32
	maxX = -math.MaxFloat32
	maxY = -math.MaxFloat32
	for _, s := range segs {
		for _, pt := range s.ArgsSlice() {
			px, py := fontToPixel(pt, scale, originX, baselineY)
			minX = min(minX, px)
			minY = min(minY, py)
			maxX = max(maxX, px)
			maxY = max(maxY, py)
		}
	}
	return minX, minY, maxX, maxY
}

// fontToPixel converts a font-unit point to pixel coordinates.
// Scale converts font units to pixels; Y is flipped (font Y-up → raster Y-down).
// originX and baselineY are the pixel offsets for the current glyph origin.
func fontToPixel(pt gtfont.SegmentPoint, scale, originX, baselineY float32) (float32, float32) {
	return pt.X*scale + originX, -pt.Y*scale + baselineY
}

// DefaultStyle returns an [unpixel.Style] with defaults matching the existing
// XImage renderer: 32 pt font, 8 px padding on both axes, no letter-spacing.
func DefaultStyle() unpixel.Style {
	return unpixel.Style{
		FontSize:    32,
		PaddingTop:  8,
		PaddingLeft: 8,
	}
}
