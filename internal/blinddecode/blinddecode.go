// Package blinddecode recovers individual words from a mosaic-pixelated
// redaction band by combining the forward image model (render → re-pixelate →
// image distance) with a language prior (dictionary membership + infini-gram).
//
// It implements P6.3: dictionary-based word recovery as the building block for
// the LMGuidedBeam strategy (P6.3 → P6.4). A [Decoder] segments a line band
// into words, scores each candidate from the dictionary whose rendered width
// matches the band, and returns the ranked list.
//
// # Usage
//
//	d := blinddecode.New(blinddecode.Options{
//	    Renderer:  render.NewXImage(),
//	    Pixelator: pixelate.NewBlockAverage(block),
//	    Metric:    metric.NewSSIM(0),
//	    Dict:      lang.DictionaryFor(lang.French),
//	    Prior:     lang.PriorFor(lang.French),
//	    Block:     block,
//	    FontSize:  fontSize,
//	})
//	candidates := d.DecodeWord(band)
//	// candidates[0].Word is the best guess.
package blinddecode

import (
	"cmp"
	"image"
	"math"
	"slices"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/segment"
)

// Options configures a Decoder. All fields with a zero value use sensible
// defaults (see field comments). Renderer, Pixelator, Metric, Dict, and Prior
// must not be nil.
type Options struct {
	// Renderer rasterises candidate text. Must not be nil.
	Renderer unpixel.Renderer

	// Pixelator re-applies the same pixelation as the original redaction.
	// Use pixelate.NewLinearBlockAverage for GEGL/GIMP output. Must not be nil.
	Pixelator unpixel.Pixelator

	// Metric measures pixel-level distance between two images. SSIM is the
	// recommended default for whole-string discrimination; Pixelmatch also works.
	// Must not be nil.
	Metric unpixel.Metric

	// Dict is the candidate word source. Must not be nil.
	Dict *lang.Dict

	// Prior scores a string for linguistic plausibility (higher = more likely).
	// Typically lang.PriorFor(language). Each Decoder owns a private Prior closure
	// that is not shared; Prior calls are never concurrent inside the Decoder.
	// Must not be nil.
	Prior func(string) float64

	// Block is the pixelation block size in pixels, matching the redaction.
	Block int

	// OffsetX and OffsetY are the grid phase. When both are zero the scorer
	// sweeps a single representative offset (0,0); a future extension may sweep
	// the full block range.
	OffsetX, OffsetY int

	// FontSize is the font size in points used to render candidates.
	FontSize float64

	// LetterSpacing is the extra horizontal space in pixels inserted after each
	// glyph. Negative values tighten the advance; zero (the default) leaves glyph
	// advances and kerning untouched, producing byte-identical output to earlier
	// behaviour. The renderer honours fractional values via fixed-point rounding.
	LetterSpacing float64

	// Alpha weights the image distance term (default 1.0).
	Alpha float64

	// Beta weights the negative-prior term (default 0.005). Larger values give
	// more influence to the language model, which can pull image near-ties toward
	// the more plausible word. See defaultBeta for the calibration rationale.
	Beta float64

	// TopK is the maximum number of candidates returned per band (default 5).
	TopK int

	// BeamWidth is the maximum number of partial hypotheses kept alive at each
	// word position during DecodeLineWhole beam search (default 8). A larger
	// value improves recall at the cost of more whole-line renders.
	BeamWidth int

	// Elisions enables French apostrophe-elision candidate generation in
	// wordPool. When true, each band also receives candidates of the form
	// <prefix><word> where prefix ∈ FrenchElisionPrefixes and word is drawn
	// from the dictionary for the remaining rune width. The additive candidates
	// fire only for bands wide enough to hold prefix+1 character; they do not
	// affect the Cartesian-product budget for bands where no elision fits.
	//
	// Set to true when decoding French text; leave false (the default) for
	// English or any language without apostrophe elision — the non-elision path
	// is byte-identical to the pre-Q3 behaviour when Elisions is false.
	Elisions bool
}

// WordCandidate is one scored dictionary candidate for a word band.
type WordCandidate struct {
	// Word is the candidate string from the dictionary.
	Word string
	// ImageDist is the pixel-level image distance in [0,1] (lower = better).
	ImageDist float64
	// Prior is the language-model score (higher = more plausible).
	Prior float64
	// Cost is the combined objective: Alpha·ImageDist + Beta·(-Prior).
	// Candidates are ranked ascending by Cost.
	Cost float64
}

// Decoder segments a mosaic-redaction line band into words and recovers each
// word from the dictionary via the forward image model and a language prior.
//
// A Decoder caches rendered-and-ink-cropped candidate images by word across
// DecodeWord / DecodeLine calls so repeated queries for the same word reuse the
// rasterised image. The cache is not safe for concurrent use; create a separate
// Decoder per goroutine when parallelism is needed.
type Decoder struct {
	opts Options

	// avgAdvance is the average glyph advance in pixels, calibrated once from a
	// probe render and used to estimate candidate rune count from band width.
	avgAdvance float64

	// inkY0 and inkH are the vertical ink-crop parameters calibrated once from a
	// probe render. All renders at the same font/size share the same ascent and
	// descent, so the ink Y-range is constant — this lets scoreWholeLine skip the
	// O(W×H) inkBounds pixel scan for every combination.
	inkY0, inkH int

	// cache stores rendered, ink-cropped images keyed by word.
	cache map[string]*image.RGBA
}

// referenceString is used to calibrate avgAdvance. The full lowercase alphabet
// provides the best coverage of glyph widths: narrow glyphs (i, l, t) and wide
// glyphs (m, w) are both represented, so the mean advance is realistic for
// arbitrary dictionary words — including those with many wide characters
// (e.g. "document") or Unicode-accented letters (e.g. "période"). Using a
// narrow-biased reference like "nesaitil" underestimates the advance for wide
// words and pushes nEst out of the ±1 range needed to include the true word.
const referenceString = "abcdefghijklmnopqrstuvwxyz"

// defaultAlpha is the image-distance weight when Options.Alpha is zero.
const defaultAlpha = 1.0

// defaultBeta is the language-prior weight when Options.Beta is zero.
//
// Rationale: lang.PriorFor returns a fused score wDict·dictScore + wChar·infiniScore.
// dictScore ∈ [0, 1] and infiniScore ∈ roughly [-3, -1] for plausible text, so the
// prior spans a range of ~2 units. SSIM image distance for a near-miss word is
// typically 0.005–0.02 in this domain.
//
// With Beta=0.005, the prior contributes at most 0.005·3 = 0.015 to the cost —
// enough to reorder image near-ties (two words within 0.005 of each other) toward
// the more common word, while keeping the image signal dominant for all cases where
// one candidate clearly reproduces the redaction better than another (distance gap
// >> 0.015). Empirically verified: "histoire" (fr, dist=0) and "history" (en,
// dist=0) both rank first at Beta=0.005.
const defaultBeta = 0.005

// defaultTopK is the candidate list size when Options.TopK is zero.
const defaultTopK = 5

// New returns a Decoder configured by opts. It calibrates the average glyph
// advance from a probe render and initialises the word render cache.
//
// New panics if opts.Renderer, opts.Pixelator, opts.Metric, opts.Dict, or
// opts.Prior is nil.
func New(opts Options) *Decoder {
	if opts.Renderer == nil {
		panic("blinddecode: Renderer must not be nil")
	}
	if opts.Pixelator == nil {
		panic("blinddecode: Pixelator must not be nil")
	}
	if opts.Metric == nil {
		panic("blinddecode: Metric must not be nil")
	}
	if opts.Dict == nil {
		panic("blinddecode: Dict must not be nil")
	}
	if opts.Prior == nil {
		panic("blinddecode: Prior must not be nil")
	}
	if opts.Alpha == 0 {
		opts.Alpha = defaultAlpha
	}
	if opts.Beta == 0 {
		opts.Beta = defaultBeta
	}
	if opts.TopK == 0 {
		opts.TopK = defaultTopK
	}
	if opts.Block <= 0 {
		opts.Block = 8
	}
	if opts.FontSize <= 0 {
		opts.FontSize = 32
	}

	d := &Decoder{
		opts:  opts,
		cache: make(map[string]*image.RGBA),
	}
	d.avgAdvance = d.calibrateAvgAdvance()
	return d
}

// calibrateAvgAdvance renders referenceString, divides its ink width by the
// rune count to get the mean advance in pixels, and also captures the ink
// Y-range (inkY0, inkH) that is constant for all renders at this font/size.
// Falls back to block size when the render fails or yields a zero width.
func (d *Decoder) calibrateAvgAdvance() float64 {
	img, sx, err := d.opts.Renderer.Render(referenceString, unpixel.Style{FontSize: d.opts.FontSize, LetterSpacing: d.opts.LetterSpacing})
	if err != nil || sx <= 0 {
		return float64(d.opts.Block)
	}
	bb := inkBounds(img, sx)
	// Cache the ink Y-range so scoreWholeLine can skip the O(W×H) pixel scan.
	d.inkY0 = bb.Min.Y
	d.inkH = bb.Dy()
	w := float64(bb.Dx())
	n := float64(len([]rune(referenceString)))
	if n == 0 || w <= 0 {
		return float64(d.opts.Block)
	}
	return w / n
}

// renderWord returns the rendered, ink-cropped image for word, using the cache.
// Ink-cropped means: rendered → crop to the non-white pixels left of sentinelX.
func (d *Decoder) renderWord(word string) *image.RGBA {
	if img, ok := d.cache[word]; ok {
		return img
	}
	img, sx, err := d.opts.Renderer.Render(word, unpixel.Style{FontSize: d.opts.FontSize, LetterSpacing: d.opts.LetterSpacing})
	if err != nil {
		return nil
	}
	bb := inkBounds(img, sx)
	cropped := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(cropped, cropped.Bounds(), img, bb.Min, xdraw.Src)
	d.cache[word] = cropped
	return cropped
}

// scoreCandidate measures the forward-model image distance between a
// pre-rendered, ink-cropped candidate image and the target band.
//
// offsetX is the horizontal grid phase: when scoring a word extracted from a
// whole-line pixelation, pass the word's left column (mod block size) so the
// candidate is re-pixelated with the same block alignment as the original.
// For an isolated band (e.g. DecodeWord with a standalone pixelated word),
// pass opts.OffsetX (typically 0).
//
// The forward model (matching bestDistance in real_mosaic_test.go):
//  1. Re-pixelate the ink-cropped candidate at the given grid phase.
//  2. Place the pixelated result on a white canvas sized to match the target.
//  3. Compare canvas to target with the configured metric.
//
// The pixelation happens on the bare ink-crop (not after padding) so that
// the block grid aligns to the same glyph origin used when the target was
// created — padding before pixelating shifts the blocks and destroys signal.
func (d *Decoder) scoreCandidate(candCropped, target *image.RGBA, offsetX int) float64 {
	tw := target.Bounds().Dx()
	th := target.Bounds().Dy()

	// Step 1: pixelate the candidate ink-crop with the correct grid phase.
	pixelated := d.opts.Pixelator.Pixelate(candCropped, offsetX, d.opts.OffsetY)
	pw := pixelated.Bounds().Dx()
	ph := pixelated.Bounds().Dy()

	// Step 2: composite on a white canvas of target size.
	// Centre vertically; left-align horizontally (glyph origin matches).
	cmp := image.NewRGBA(image.Rect(0, 0, tw, th))
	imutil.FillWhite(cmp)
	dy := (th - ph) / 2
	if dy < 0 {
		dy = 0
	}
	copyH := min(ph, th)
	copyW := min(pw, tw)
	dstRect := image.Rect(0, dy, copyW, dy+copyH)
	xdraw.Draw(cmp, dstRect, pixelated, pixelated.Bounds().Min, xdraw.Src)

	// Step 3: compare.
	return d.opts.Metric.Compare(cmp, target)
}

// DecodeWord ranks dictionary candidates for a single word-band image (cropped
// to the band, white background, already pixelated). It estimates the candidate
// rune count from the band width and avg advance, fetches words of that length
// ±1 from the dictionary, scores each via the forward model and language prior,
// and returns up to TopK candidates sorted ascending by Cost.
//
// The returned slice is nil when no candidates match the estimated rune count.
func (d *Decoder) DecodeWord(band *image.RGBA) []WordCandidate {
	return d.decodeWordAt(band, d.opts.OffsetX)
}

// DecodeLine segments a line band into words (using segment.Words), calls
// decodeWordAt for each word band (passing the word's column as the grid
// phase), and returns:
//   - text: the best word per band joined by spaces.
//   - perWord: the full candidate list for each band, in left-to-right order.
//
// When a word band yields no candidates (vocabulary gap), the band is
// represented as "?" in text and by a nil slice in perWord.
func (d *Decoder) DecodeLine(line *image.RGBA) (text string, perWord [][]WordCandidate) {
	// Use segment.Lines to find the ink band, then Words within it.
	lines := segment.Lines(line)
	var wordRects []image.Rectangle
	if len(lines) > 0 {
		wordRects = segment.Words(line, lines[0])
	}
	if len(wordRects) == 0 {
		return "", nil
	}

	perWord = make([][]WordCandidate, len(wordRects))
	parts := make([]string, len(wordRects))
	b := line.Bounds()

	for i, wr := range wordRects {
		// Extract the word sub-image (already pixelated; white background).
		ww := wr.Dx()
		wh := wr.Dy()
		band := image.NewRGBA(image.Rect(0, 0, ww, wh))
		imutil.FillWhite(band)
		// Clamp the source rect to image bounds.
		srcRect := wr.Intersect(b)
		if !srcRect.Empty() {
			xdraw.Draw(band, image.Rect(0, 0, srcRect.Dx(), srcRect.Dy()), line, srcRect.Min, xdraw.Src)
		}

		// The word's left column within the line determines the block-grid
		// phase: blocks in the full-line pixelation are aligned to the line's
		// left edge (0), so a word starting at column X has its first block
		// beginning at X - (X % block). Passing X % block as offsetX to
		// scoreCandidate reproduces that alignment when re-pixelating the
		// candidate in isolation.
		colOffset := wr.Min.X % d.opts.Block

		candidates := d.decodeWordAt(band, colOffset)
		perWord[i] = candidates
		if len(candidates) > 0 {
			parts[i] = candidates[0].Word
		} else {
			parts[i] = "?"
		}
	}

	text = strings.Join(parts, " ")
	return text, perWord
}

// decodeWordAt is the internal scorer used by both DecodeWord and DecodeLine.
// offsetX is the horizontal grid phase to use when re-pixelating candidates;
// it should be opts.OffsetX for standalone bands and wordCol%block for bands
// extracted from a whole-line pixelation.
func (d *Decoder) decodeWordAt(band *image.RGBA, offsetX int) []WordCandidate {
	bandW := band.Bounds().Dx()
	if bandW == 0 || d.avgAdvance <= 0 {
		return nil
	}

	nEst := int(math.Round(float64(bandW) / d.avgAdvance))
	if nEst < 1 {
		nEst = 1
	}

	var words []string
	for delta := -1; delta <= 1; delta++ {
		n := nEst + delta
		if n < 1 {
			continue
		}
		words = append(words, d.opts.Dict.ByRuneLen(n)...)
	}
	if len(words) == 0 {
		return nil
	}

	alpha := d.opts.Alpha
	beta := d.opts.Beta

	scored := make([]WordCandidate, 0, len(words))
	for _, w := range words {
		rendered := d.renderWord(w)
		if rendered == nil {
			continue
		}
		dist := d.scoreCandidate(rendered, band, offsetX)
		prior := d.opts.Prior(w)
		cost := alpha*dist + beta*(-prior)
		scored = append(scored, WordCandidate{
			Word:      w,
			ImageDist: dist,
			Prior:     prior,
			Cost:      cost,
		})
	}
	if len(scored) == 0 {
		return nil
	}

	slices.SortFunc(scored, func(a, b WordCandidate) int {
		return cmp.Compare(a.Cost, b.Cost)
	})

	if len(scored) > d.opts.TopK {
		scored = scored[:d.opts.TopK]
	}
	return scored
}

// inkBounds returns the tight bounding box of non-white (inked) pixels in the
// region [0, sentinelX) of img. It uses BT.601 luminance < 240 as the ink
// test, matching the convention in real_mosaic_test.go.
func inkBounds(img *image.RGBA, sentinelX int) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := sentinelX, b.Dy(), 0, 0
	for y := range b.Dy() {
		for x := range sentinelX {
			c := img.RGBAAt(x, y)
			lum := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
			if lum < 240 {
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
