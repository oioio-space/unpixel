// Package blurfixture generates and describes synthetic Gaussian-blurred text
// images used by the blur-recovery matrix test. Each image is produced by the
// faithful render → crop-to-grid → white-pad → blur pipeline (BlockSize=1,
// which makes block grid steps no-ops), so RecoverBlurred can recover the
// known plaintext from it.
//
// The canonical set is Matrix(). The generator at testdata/blur/gen renders it
// to testdata/blur (PNG + manifest.json) via `go run ./testdata/blur/gen`.
// Fixtures are committed; regenerate them when the pipeline changes.
package blurfixture

import (
	"fmt"
	"image"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// Spec describes one blurred reference image. JSON tags define the on-disk
// manifest schema.
type Spec struct {
	// Name is the image's base name (without extension).
	Name string `json:"name"`
	// File is the image filename (Name + ".png").
	File string `json:"file"`
	// Text is the plaintext hidden by the blur.
	Text string `json:"text"`
	// Charset is a compact candidate alphabet for recovery tests (target chars
	// plus a few distractors, keeping the matrix fast).
	Charset string `json:"charset"`
	// FontSize is the rendering font size in points.
	FontSize float64 `json:"font_size"`
	// Sigma is the true Gaussian-blur standard deviation used when generating
	// the image, in pixels.
	Sigma float64 `json:"sigma"`
}

// Style returns the unpixel.Style for this spec (fixed padding to match the
// faithful pipeline defaults).
func (s Spec) Style() unpixel.Style {
	return unpixel.Style{FontSize: s.FontSize, PaddingTop: 8, PaddingLeft: 8}
}

// Matrix returns the canonical set of blur recovery fixtures, spanning a range
// of texts and sigma values that exercise zero-config σ-search.
//
// Text × σ coverage:
//   - {"go","cat","hello"} × {2, 3, 4, 6}
//
// Charsets are compact (target chars + distractors) so the recovery matrix
// stays fast; the σ values cover the range InferBlurSigma is expected to
// handle correctly.
func Matrix() []Spec {
	type entry struct {
		text    string
		charset string
	}
	texts := []entry{
		{"go", "go abcde"},
		{"cat", "cat eoab"},
		{"hello", "helo abcd"},
	}
	sigmas := []float64{2, 3, 4, 6}

	specs := make([]Spec, 0, len(texts)*len(sigmas))
	for _, e := range texts {
		for _, σ := range sigmas {
			name := fmt.Sprintf("blur_%s_s%.0f", e.text, σ)
			specs = append(specs, Spec{
				Name:     name,
				File:     name + ".png",
				Text:     e.text,
				Charset:  e.charset,
				FontSize: 32,
				Sigma:    σ,
			})
		}
	}
	return specs
}

// ConnectSpecs returns the two "connect" (7-char) fixtures used by the longer-
// word stress test: σ=3 and σ=6 only (slow under the full matrix, gated behind
// -short). The charset is compact: target letters plus a small distractor set.
func ConnectSpecs() []Spec {
	sigmas := []float64{3, 6}
	specs := make([]Spec, len(sigmas))
	for i, σ := range sigmas {
		name := fmt.Sprintf("blur_connect_s%.0f", σ)
		specs[i] = Spec{
			Name:     name,
			File:     name + ".png",
			Text:     "connect",
			Charset:  "connect abd",
			FontSize: 32,
			Sigma:    σ,
		}
	}
	return specs
}

// Redact renders s.Text and returns the blurred image produced by the faithful
// pipeline: render → locate sentinel → crop to grid → white-pad to block
// multiple → Gaussian-blur (BlockSize=1 so grid steps are no-ops) → vertical
// crop around text centre.
//
// The pipeline mirrors makeSyntheticRedacted in engine_test.go with blockSize=1
// and a GaussianBlur pixelator, ensuring RecoverBlurred can recover the text.
func Redact(s Spec) (*image.RGBA, error) {
	r, err := render.NewXImage()
	if err != nil {
		return nil, fmt.Errorf("renderer: %w", err)
	}
	blur := pixelate.NewGaussianBlur(s.Sigma)

	img, sentinelX, err := r.Render(s.Text, s.Style())
	if err != nil {
		return nil, fmt.Errorf("render %q: %w", s.Text, err)
	}

	// Locate text right edge and vertical centre from the blue sentinel.
	bm, imageCenter := imutil.BlueMargin(img)
	if bm == 0 {
		bm = sentinelX
	}

	const blockSize = 1

	// Crop to grid origin (offset 0,0 — with blockSize=1 the grid is trivial).
	img = imutil.Crop(img, 0, 0, bm, img.Bounds().Dy())

	// White-pad to block multiple (blockSize=1 → always a no-op, but kept for
	// pipeline parity so the fixture matches what RecoverBlurred generates).
	if w := img.Bounds().Dx(); blockSize-(w%blockSize) < blockSize {
		img = imutil.PadWhite(img, w+blockSize-(w%blockSize), img.Bounds().Dy())
	}

	// Blur (the forward operator).
	img = blur.Pixelate(img, 0, 0)

	// Vertical crop around text centre (same geometry as makeSyntheticRedacted).
	leftEdge := imutil.LeftEdge(img)
	adjustedCenter := imageCenter - (imageCenter % blockSize) + 4
	redactedH := 2 * adjustedCenter

	red := imutil.Crop(img, leftEdge, 0, img.Bounds().Dx()-leftEdge, img.Bounds().Dy())
	if red.Bounds().Dy() < redactedH {
		red = imutil.PadWhite(red, red.Bounds().Dx(), redactedH)
	}
	return red, nil
}
