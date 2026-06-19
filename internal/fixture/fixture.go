// Package fixture generates synthetic pixelated-text reference images for the
// recovery test matrix. Each image is produced by the faithful
// render → crop-to-grid → white-pad → pixelate pipeline (the same steps the
// scorer reproduces), so the engine can recover the known plaintext from it.
//
// The canonical set of images is Matrix(); the cmd at internal/fixture/gen
// renders it to testdata/fixtures (PNG + manifest.json) via `go generate`.
package fixture

import (
	"fmt"
	"image"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// Spec describes one reference image: the plaintext to hide, the rendering style,
// the pixelation block size, and the charset a recovery run should use. The JSON
// tags define the on-disk manifest schema.
type Spec struct {
	Name        string  `json:"name"`
	Text        string  `json:"text"`
	Charset     string  `json:"charset"`
	FontSize    float64 `json:"font_size"`
	Bold        bool    `json:"bold"`
	BlockSize   int     `json:"block_size"`
	PaddingTop  int     `json:"padding_top"`
	PaddingLeft int     `json:"padding_left"`
	// Secret marks a fixture whose plaintext is a credential/structured token
	// (common password, PIN, …). Recovery harnesses use it to exercise the
	// structured-secret prior (internal/secrets) on these cases.
	Secret bool `json:"secret,omitempty"`
}

// Style returns the unpixel.Style described by the spec.
func (s Spec) Style() unpixel.Style {
	return unpixel.Style{
		FontSize:    s.FontSize,
		Bold:        s.Bold,
		PaddingTop:  s.PaddingTop,
		PaddingLeft: s.PaddingLeft,
	}
}

// File is the spec's reference image filename.
func (s Spec) File() string { return s.Name + ".png" }

// csLower is the full faithful default alphabet, used by the cases that prove
// recovery at realistic charset scale. Other cases use a compact charset (the
// target characters plus a few distractors) so the matrix stays fast: search
// cost grows with blockSize² (offset probes) × charset size × text length, so
// the large-block and multi-character cases trim the charset deliberately.
const csLower = unpixel.CharsetLower

// Matrix is the canonical reference set, spanning block sizes, font sizes and
// weights, charsets (incl. digits/uppercase/symbols), padding (grid offset) and
// text shapes. Every spec is recoverable by the engine; note a block size needs
// a font roughly 3× its size to leave enough pixels per glyph, so the block16
// case uses a larger font.
func Matrix() []Spec {
	return []Spec{
		// Block sizes (block04/08 prove the full lowercase charset).
		{Name: "block04_go", Text: "go", Charset: csLower, FontSize: 32, BlockSize: 4, PaddingTop: 8, PaddingLeft: 8},
		{Name: "block08_go", Text: "go", Charset: csLower, FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		{Name: "block16_go", Text: "go", Charset: "go abcdef", FontSize: 48, BlockSize: 16, PaddingTop: 8, PaddingLeft: 8},
		// Font sizes.
		{Name: "size24_go", Text: "go", Charset: csLower, FontSize: 24, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		{Name: "size40_go", Text: "go", Charset: csLower, FontSize: 40, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		// Font weight.
		{Name: "bold_go", Text: "go", Charset: csLower, FontSize: 32, Bold: true, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		// Charsets: uppercase + digits, and punctuation (code-like).
		{Name: "alnum_Go2", Text: "Go2", Charset: "Go2 abc019", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		{Name: "symbols_x_eq_1", Text: "x=1", Charset: "x=1 +-_a0", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		// Padding (exercises grid-offset discovery).
		{Name: "pad_04_04_go", Text: "go", Charset: csLower, FontSize: 32, BlockSize: 8, PaddingTop: 4, PaddingLeft: 4},
		{Name: "pad_12_12_go", Text: "go", Charset: csLower, FontSize: 32, BlockSize: 8, PaddingTop: 12, PaddingLeft: 12},
		// Text shapes.
		{Name: "text_single_x", Text: "x", Charset: csLower, FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		{Name: "text_cat", Text: "cat", Charset: "cat eoabd", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		{Name: "text_with_space", Text: "a b", Charset: "ab cde", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		{Name: "text_hello", Text: "hello", Charset: "helo abcd", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8},
		// Secrets: credential/structured-token plaintext, used to exercise the
		// structured-secret prior (internal/secrets). "admin" and "azerty" are
		// common passwords; "1234" is a digit PIN. Charsets stay compact so the
		// matrix stays fast (target chars plus a few distractors).
		{Name: "secret_admin", Text: "admin", Charset: "admin xyz0", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8, Secret: true},
		{Name: "secret_azerty", Text: "azerty", Charset: "azerty 0", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8, Secret: true},
		{Name: "secret_pin1234", Text: "1234", Charset: "0123456789", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8, Secret: true},
	}
}

// Redact renders the spec's text and returns the synthetic redacted image,
// mirroring the scorer's faithful pipeline so the result is recoverable. The
// returned image is a fresh *image.RGBA.
func Redact(s Spec) (*image.RGBA, error) {
	r, err := render.NewXImage()
	if err != nil {
		return nil, fmt.Errorf("renderer: %w", err)
	}
	pix := pixelate.NewBlockAverage(s.BlockSize)

	img, sentinelX, err := r.Render(s.Text, s.Style())
	if err != nil {
		return nil, fmt.Errorf("render %q: %w", s.Text, err)
	}

	// Locate the text's right edge and vertical centre from the blue sentinel.
	bm, imageCenter := imutil.BlueMargin(img)
	if bm == 0 {
		bm = sentinelX
	}

	// Crop to the grid origin (offset 0,0), white-pad to a block multiple, pixelate.
	img = imutil.Crop(img, 0, 0, bm, img.Bounds().Dy())
	if w := img.Bounds().Dx(); s.BlockSize-(w%s.BlockSize) < s.BlockSize {
		img = imutil.PadWhite(img, w+s.BlockSize-(w%s.BlockSize), img.Bounds().Dy())
	}
	img = pix.Pixelate(img, 0, 0)

	// Vertical crop to a block-aligned band around the text centre; height
	// 2*adjustedCenter keeps the band's top row at 0 so the scorer aligns exactly.
	leftEdge := imutil.LeftEdge(img)
	adjustedCenter := imageCenter - (imageCenter % s.BlockSize) + 4
	redactedH := 2 * adjustedCenter
	red := imutil.Crop(img, leftEdge, 0, img.Bounds().Dx()-leftEdge, img.Bounds().Dy())
	if red.Bounds().Dy() < redactedH {
		red = imutil.PadWhite(red, red.Bounds().Dx(), redactedH)
	}
	return red, nil
}
