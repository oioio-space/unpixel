// Package render provides the XImage text renderer for the unpixel pipeline.
// It draws candidate text on a white RGBA image using Liberation Sans (metrically
// identical to Arial) and appends a solid blue sentinel block to mark the text's
// right edge.
package render

import (
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
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

// faceMetrics holds derived pixel metrics for a font at a given size.
// It does not hold a font.Face — faces are managed per-goroutine via facePool.
type faceMetrics struct {
	ascent  int // fixed.Int26_6.Ceil() of face.Metrics().Ascent
	descent int // fixed.Int26_6.Ceil() of face.Metrics().Descent
}

// faceKey identifies a pool of font faces by boldness and point size.
type faceKey struct {
	bold bool
	size float64
}

// poolEntry pairs the cached pixel metrics with the face pool for one
// (bold, size) combination.
type poolEntry struct {
	metrics faceMetrics
	pool    *sync.Pool
}

// XImage implements unpixel.Renderer using x/image/font/opentype.
// The font metrics match Arial (Liberation Sans is metrically identical).
//
// Each call to Render borrows a font.Face from a per-(bold,size) sync.Pool,
// uses it without sharing glyph-cache state with other goroutines, then returns
// it. This eliminates the former glyphMu bottleneck: concurrent Render calls on
// the same XImage scale with GOMAXPROCS instead of serialising on a mutex.
type XImage struct {
	black image.Image // reused uniform black source

	// regularFont and boldFont are the parsed sfnt.Font values shared across all
	// face instances. sfnt.Font is safe for concurrent use when each caller
	// supplies its own *sfnt.Buffer — opentype.Face does exactly that.
	regularFont *opentype.Font
	boldFont    *opentype.Font

	// faceCache maps faceKey → poolEntry. sync.Map.Load is lock-free once a key
	// is stored, so steady-state Render calls incur no mutex cost at all — only
	// the first Store for a new (bold, size) pair takes the slow path.
	faceCache sync.Map

	// initMu serialises pool construction for new (bold, size) pairs only; it is
	// never held during glyph work or on the common cached-hit path.
	initMu sync.Mutex
}

// NewXImage parses the embedded TTF fonts (Liberation Sans, metrically identical
// to Arial) and returns an XImage renderer. Use NewXImageFromFonts to render with
// a different font, e.g. a monospace face matching a Consolas-pixelated redaction.
func NewXImage() (*XImage, error) {
	return NewXImageFromFonts(regularTTF, boldTTF)
}

// NewXImageFromFonts returns an XImage renderer that rasterises text with the
// given TrueType/OpenType font data instead of the embedded default. This lets
// callers match the exact typeface of a redacted screenshot — for example a
// user-supplied Consolas.ttf for source-code redactions, avoiding any font
// licensing concern on our side.
//
// regularTTF is required. boldTTF may be nil, in which case the regular font is
// reused for bold text. It returns an error if regularTTF is empty or either
// font fails to parse.
func NewXImageFromFonts(regularTTFData, boldTTFData []byte) (*XImage, error) {
	if len(regularTTFData) == 0 {
		return nil, errors.New("regular font data is empty")
	}
	if len(boldTTFData) == 0 {
		boldTTFData = regularTTFData
	}

	regFont, err := opentype.Parse(regularTTFData)
	if err != nil {
		return nil, fmt.Errorf("parse regular font: %w", err)
	}
	boldFont, err := opentype.Parse(boldTTFData)
	if err != nil {
		return nil, fmt.Errorf("parse bold font: %w", err)
	}

	r := &XImage{
		black:       image.NewUniform(color.Black),
		regularFont: regFont,
		boldFont:    boldFont,
	}
	// Pre-warm pool entries for the default size so the first Render at size 32
	// finds an already-populated entry (zero-allocation fast path).
	if _, _, err := r.faceFor(false, 32); err != nil {
		return nil, fmt.Errorf("pre-warm regular face: %w", err)
	}
	if _, _, err := r.faceFor(true, 32); err != nil {
		return nil, fmt.Errorf("pre-warm bold face: %w", err)
	}
	return r, nil
}

// faceFor returns the cached faceMetrics and the *sync.Pool for the given bold
// flag and point size, building the pool entry on first use.
//
// The fast path (key already in faceCache) is a single lock-free sync.Map.Load —
// no mutex, no allocation. Only the very first call for a new (bold, size) pair
// takes initMu to construct the pool.
//
// The caller MUST call pool.Put(face) when done so the face is reused on the
// same P.
func (r *XImage) faceFor(bold bool, size float64) (faceMetrics, *sync.Pool, error) {
	key := faceKey{bold: bold, size: size}

	// Fast path: lock-free load from sync.Map.
	if v, ok := r.faceCache.Load(key); ok {
		e := v.(poolEntry)
		return e.metrics, e.pool, nil
	}

	// Slow path: first call for this (bold, size). Serialise construction so only
	// one goroutine builds and stores the entry; others wait on initMu then hit
	// the fast path on retry.
	r.initMu.Lock()
	defer r.initMu.Unlock()

	// Re-check under the lock: another goroutine may have stored while we waited.
	if v, ok := r.faceCache.Load(key); ok {
		e := v.(poolEntry)
		return e.metrics, e.pool, nil
	}

	parsed := r.regularFont
	if bold {
		parsed = r.boldFont
	}
	opts := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}

	// Build one seed face to derive metrics; seed it into the pool so pool.New
	// is not called on the very first borrow.
	seedFace, err := opentype.NewFace(parsed, opts)
	if err != nil {
		return faceMetrics{}, nil, err
	}
	m := seedFace.Metrics()
	fm := faceMetrics{ascent: m.Ascent.Ceil(), descent: m.Descent.Ceil()}

	// pool.New wraps the shared *opentype.Font with a fresh per-face glyph buffer
	// (cheap). It is only called when all borrowed faces are in use.
	pool := &sync.Pool{
		New: func() any {
			f, err := opentype.NewFace(parsed, opts)
			if err != nil {
				return nil // handled by borrowFace
			}
			return f
		},
	}
	pool.Put(seedFace)

	r.faceCache.Store(key, poolEntry{metrics: fm, pool: pool})
	return fm, pool, nil
}

// borrowFace retrieves a font.Face from pool. It returns an error only when
// pool.New returns nil, which only happens when opentype.NewFace fails on a
// corrupt font — not on the normal hot path.
func borrowFace(pool *sync.Pool) (font.Face, error) {
	v := pool.Get()
	if v == nil {
		return nil, errors.New("render: face pool returned nil (font construction failure)")
	}
	f, ok := v.(font.Face)
	if !ok {
		return nil, errors.New("render: face pool contained unexpected type")
	}
	return f, nil
}

// measureSpaced returns the total advance of text rendered with per-glyph
// letter spacing: the sum of each rune's own advance plus spacing after it. It
// mirrors the per-glyph draw loop in Render exactly so the sentinel lands where
// drawing ends.
func measureSpaced(face font.Face, text string, spacing fixed.Int26_6) fixed.Int26_6 {
	var w fixed.Int26_6
	for _, c := range text {
		w += font.MeasureString(face, string(c)) + spacing
	}
	return w
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
//
// Render is safe for concurrent use. Each call borrows an independent font.Face
// from an internal sync.Pool; no glyph-cache state is shared between callers.
func (r *XImage) Render(text string, style unpixel.Style) (*image.RGBA, int, error) {
	fm, pool, err := r.faceFor(style.Bold, style.FontSize)
	if err != nil {
		return nil, 0, fmt.Errorf("parse face at size %v: %w", style.FontSize, err)
	}

	face, err := borrowFace(pool)
	if err != nil {
		return nil, 0, err
	}
	defer pool.Put(face)

	paddingLeft := style.PaddingLeft
	paddingTop := style.PaddingTop

	// letterSpacing in 26.6 fixed-point pixels; zero keeps the fast path below.
	spacing := fixed.Int26_6(math.Round(style.LetterSpacing * 64))

	// Measure the text advance. The face is exclusively owned by this goroutine
	// for the duration of Render — no mutex needed.
	var textW int
	if spacing == 0 {
		textW = font.MeasureString(face, text).Ceil()
	} else {
		textW = measureSpaced(face, text, spacing).Ceil()
	}

	imgH := paddingTop + fm.ascent + fm.descent + 4 // small bottom margin
	imgW := paddingLeft + textW + sentinelWidth + 8 // right margin

	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	imutil.FillWhite(img)

	// Draw text.
	drawer := &font.Drawer{
		Dst:  img,
		Src:  r.black,
		Face: face,
		Dot: fixed.Point26_6{
			X: fixed.I(paddingLeft),
			Y: fixed.I(paddingTop + fm.ascent),
		},
	}
	if spacing == 0 {
		drawer.DrawString(text)
	} else {
		// Per-glyph draw so letter-spacing can be inserted after each rune. This
		// drops cross-glyph kerning, which is fine for the monospace faces this
		// path targets (Consolas-class code redactions); the zero-spacing path
		// above keeps kerning and exact prior behaviour.
		for _, c := range text {
			drawer.DrawString(string(c))
			drawer.Dot.X += spacing
		}
	}

	// sentinelX is the pixel column where the blue sentinel starts.
	// We use paddingLeft + textW (the measured advance) so that it tracks
	// font.MeasureString exactly.
	sentinelX := paddingLeft + textW

	// Draw the blue sentinel block from sentinelX to sentinelX+sentinelWidth,
	// spanning the full height of the text (ascent+descent), vertically centred
	// on the text baseline.
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
