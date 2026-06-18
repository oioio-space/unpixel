// Package render provides the XImage text renderer for the unpixel pipeline.
// It draws candidate text on a white RGBA image using Liberation Sans (metrically
// identical to Arial) and appends a solid blue sentinel block to mark the text's
// right edge.
package render

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

//go:embed fonts/LiberationSans-Regular.ttf
var regularTTF []byte

//go:embed fonts/LiberationSans-Bold.ttf
var boldTTF []byte

// faceMetrics caches a parsed font face and its derived pixel metrics.
type faceMetrics struct {
	face    font.Face
	ascent  int // fixed.Int26_6.Ceil() of face.Metrics().Ascent
	descent int // fixed.Int26_6.Ceil() of face.Metrics().Descent
}

// XImage implements unpixel.Renderer using x/image/font/opentype.
// The font metrics match Arial (Liberation Sans is metrically identical).
type XImage struct {
	regularTTF []byte
	boldTTF    []byte
	black      image.Image // reused uniform black source

	mu          sync.Mutex
	regularFace map[float64]faceMetrics
	boldFace    map[float64]faceMetrics
}

// NewXImage parses the embedded TTF fonts and returns an XImage renderer.
func NewXImage() (*XImage, error) {
	r := &XImage{
		regularTTF:  regularTTF,
		boldTTF:     boldTTF,
		black:       image.NewUniform(color.Black),
		regularFace: make(map[float64]faceMetrics),
		boldFace:    make(map[float64]faceMetrics),
	}
	// Pre-warm the default size so first Render at size 32 is zero-allocation.
	if _, err := r.faceFor(false, 32); err != nil {
		return nil, fmt.Errorf("parse regular font: %w", err)
	}
	if _, err := r.faceFor(true, 32); err != nil {
		return nil, fmt.Errorf("parse bold font: %w", err)
	}
	return r, nil
}

// faceFor returns the cached faceMetrics for the given bold flag and size,
// parsing and caching on first use.
func (r *XImage) faceFor(bold bool, size float64) (faceMetrics, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cache := r.regularFace
	ttf := r.regularTTF
	if bold {
		cache = r.boldFace
		ttf = r.boldTTF
	}
	if fm, ok := cache[size]; ok {
		return fm, nil
	}
	f, err := parseFace(ttf, size)
	if err != nil {
		return faceMetrics{}, err
	}
	m := f.Metrics()
	fm := faceMetrics{
		face:    f,
		ascent:  m.Ascent.Ceil(),
		descent: m.Descent.Ceil(),
	}
	cache[size] = fm
	return fm, nil
}

func parseFace(ttf []byte, size float64) (font.Face, error) {
	parsed, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// sentinelWidth is the pixel width of the blue sentinel block.
// faithful: main.ts renders "█" (U+2588 FULL BLOCK) in the same font at the
// same size; we use a fixed block equal to the font's cap height as a
// platform-independent approximation.
const sentinelWidth = 24

// Render draws text at the given style onto a white RGBA image and appends a
// solid blue sentinel block immediately to the right of the text.
// It returns the image and sentinelX — the x-coordinate where the sentinel
// begins, i.e. the text's right edge measured from image origin.
//
// faithful: main.ts CSS style — paddingLeft=8, paddingTop=8, fontSize=32,
// white background, normal weight, pre spacing.
func (r *XImage) Render(text string, style unpixel.Style) (*image.RGBA, int, error) {
	fm, err := r.faceFor(style.Bold, style.FontSize)
	if err != nil {
		return nil, 0, fmt.Errorf("parse face at size %v: %w", style.FontSize, err)
	}

	paddingLeft := style.PaddingLeft
	paddingTop := style.PaddingTop

	// Measure the text advance; fixed.Int26_6.Ceil() converts to pixels.
	textW := font.MeasureString(fm.face, text).Ceil()

	imgH := paddingTop + fm.ascent + fm.descent + 4 // small bottom margin
	imgW := paddingLeft + textW + sentinelWidth + 8 // right margin

	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	imutil.FillWhite(img)

	// Draw text.
	drawer := &font.Drawer{
		Dst:  img,
		Src:  r.black,
		Face: fm.face,
		Dot: fixed.Point26_6{
			X: fixed.I(paddingLeft),
			Y: fixed.I(paddingTop + fm.ascent),
		},
	}
	drawer.DrawString(text)

	// sentinelX is the pixel column where the blue sentinel starts.
	// We use paddingLeft + textW (the measured advance) so that it tracks
	// font.MeasureString exactly.
	sentinelX := paddingLeft + textW

	// Draw the blue sentinel block from sentinelX to sentinelX+sentinelWidth,
	// spanning the full height of the text (ascent+descent), vertically
	// centred on the text baseline.
	blue := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	sentinelTop := paddingTop
	sentinelBot := paddingTop + fm.ascent + fm.descent
	for y := sentinelTop; y < sentinelBot && y < imgH; y++ {
		for x := sentinelX; x < sentinelX+sentinelWidth && x < imgW; x++ {
			img.SetRGBA(x, y, blue)
		}
	}

	return img, sentinelX, nil
}
