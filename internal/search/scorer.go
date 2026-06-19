// Package search (scorer.go) provides the pipeline Scorer that connects the
// renderer, pixelator, imutil helpers, and metric into the Eval call used by
// GuidedDFS, BeamDFS, and DiscoverOffsets.
package search

import (
	"container/list"
	"context"
	"errors"
	"image"
	"image/color"
	"sync"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// renderCacheCap bounds the per-scorer render cache. Discovery renders the whole
// charset, and a DFS reuses each parent as the next level's prevGuess, so a few
// hundred entries capture the reuse while keeping memory bounded.
const renderCacheCap = 256

// renderEntry is one cached rendering, keyed by candidate text.
type renderEntry struct {
	text      string
	img       *image.RGBA
	sentinelX int
}

// PipelineScorer implements Scorer using the pluggable Renderer, Pixelator,
// and Metric from a Config.
type PipelineScorer struct {
	redacted *image.RGBA
	cfg      unpixel.Config

	// Render cache: Render(text) is offset-independent and Style is fixed per
	// scorer, so the same text is otherwise rendered once per grid offset (64×
	// in discovery) and again as each child's prevGuess. Caching by text removes
	// that redundant glyph rasterisation; cached images are read-only (callers
	// only Crop them, which allocates), so sharing is safe.
	rmu    sync.Mutex
	rlru   *list.List // front = most recently used; values are *renderEntry
	rcache map[string]*list.Element
}

// NewPipelineScorer returns a PipelineScorer for the given redacted image and config.
func NewPipelineScorer(redacted *image.RGBA, cfg unpixel.Config) *PipelineScorer {
	return &PipelineScorer{
		redacted: redacted,
		cfg:      cfg,
		rlru:     list.New(),
		rcache:   make(map[string]*list.Element),
	}
}

// render returns the rendered image and sentinelX for text, using the per-scorer
// render cache. The render runs outside the lock (a benign duplicate may occur on
// a race; the result is identical). The returned image must not be mutated.
func (s *PipelineScorer) render(text string) (*image.RGBA, int, error) {
	s.rmu.Lock()
	if el, ok := s.rcache[text]; ok {
		s.rlru.MoveToFront(el)
		e := el.Value.(*renderEntry)
		img, sx := e.img, e.sentinelX
		s.rmu.Unlock()
		return img, sx, nil
	}
	s.rmu.Unlock()

	img, sx, err := s.cfg.Renderer.Render(text, s.cfg.Style)
	if err != nil {
		return nil, 0, err
	}

	s.rmu.Lock()
	defer s.rmu.Unlock()
	if el, ok := s.rcache[text]; ok { // a concurrent miss already stored it
		s.rlru.MoveToFront(el)
		e := el.Value.(*renderEntry)
		return e.img, e.sentinelX, nil
	}
	s.rcache[text] = s.rlru.PushFront(&renderEntry{text: text, img: img, sentinelX: sx})
	if s.rlru.Len() > renderCacheCap {
		if back := s.rlru.Back(); back != nil {
			delete(s.rcache, back.Value.(*renderEntry).text)
			s.rlru.Remove(back)
		}
	}
	return img, sx, nil
}

// RedactedImage returns the redacted image held by this scorer.
// It is used by tests that need access to the image after construction.
func (s *PipelineScorer) RedactedImage() *image.RGBA { return s.redacted }

// stageResult holds the outputs of stageImage (pipeline steps 1–7).
type stageResult struct {
	// img is the rendered, pixelated, vertically-cropped image for the guess.
	// Invariant: img is never mutated after stageImage returns.
	// imutil.Crop always allocates a fresh *image.RGBA, so sharing across
	// goroutines and caching are safe without copying.
	img *image.RGBA
	// blueMargin is the raw right-edge x-coordinate of the text (step 2).
	blueMargin int
	// leftEdge is the first non-white column after pixelation (step 6).
	leftEdge int
	// cropY is the top row used for the step-7 vertical crop. evalFromStage
	// reuses it when re-staging prevGuess so both images share the same band.
	cropY int
}

// stageImage executes pipeline steps 1–7 for guess at the given offset:
// render → blueMargin → crop-to-origin → white-pad → pixelate → leftEdge →
// vertical crop.
//
// The returned stageResult.img is a fresh allocation (imutil.Crop always
// allocates). Callers must not mutate it; see the stageResult.img doc comment.
func (s *PipelineScorer) stageImage(ctx context.Context, guess string, offset unpixel.Offset) (stageResult, error) {
	if ctx.Err() != nil {
		return stageResult{}, ctx.Err()
	}

	// Step 1: Render (cached by text; offset-independent).
	rendered, sentinelX, err := s.render(guess)
	if err != nil {
		return stageResult{}, err
	}

	// Step 2: BlueMargin — right edge of text + vertical center.
	bm, imageCenter := imutil.BlueMargin(rendered)
	if bm == 0 {
		bm = sentinelX // fall back to measured advance if scan finds nothing
	}

	// Step 3: Crop to grid origin.
	ox, oy := offset.X, offset.Y
	cropW := bm - ox
	if cropW <= 0 {
		return stageResult{}, errCropEmpty
	}
	rendered = imutil.Crop(rendered, ox, oy, cropW, rendered.Bounds().Dy()-oy)
	imageCenter -= oy

	// Step 4: White-pad to block multiple.
	bs := s.cfg.BlockSize
	w := rendered.Bounds().Dx()
	if rem := bs - (w % bs); rem < bs {
		rendered = imutil.PadWhite(rendered, w+rem, rendered.Bounds().Dy())
	}

	// Step 5: Pixelate.
	rendered = s.cfg.Pixelator.Pixelate(rendered, 0, 0)

	// Step 6: LeftEdge.
	le := imutil.LeftEdge(rendered)

	// Step 7: Vertical crop around block-aligned center.
	redactedH := s.redacted.Bounds().Dy()
	adjustedCenter := imageCenter - (imageCenter % bs) + 4
	cy := adjustedCenter - redactedH/2
	rendered = imutil.Crop(rendered, le, cy, rendered.Bounds().Dx()-le, redactedH)

	return stageResult{img: rendered, blueMargin: bm, leftEdge: le, cropY: cy}, nil
}

// errCropEmpty is returned by stageImage when the crop width is non-positive.
var errCropEmpty = errors.New("crop region is empty")

// Eval renders guess, pixelates it, performs the marginal-region crop, and
// returns the diff score against the redacted image.
//
// faithful: main.ts redact() steps 1–10, see DESIGN.md §Faithful pipeline.
func (s *PipelineScorer) Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) EvalResult {
	if ctx.Err() != nil {
		return EvalResult{Score: 1}
	}
	sr, err := s.stageImage(ctx, guess, offset)
	if err != nil {
		return EvalResult{Score: 1}
	}
	return s.evalFromStage(ctx, sr, prevGuess, offset)
}

// evalFromStage completes steps 8–10 given the already-staged image for guess.
// It is called by both Eval and CachingScorer.Eval (which supplies the cached
// stageResult). sr.cropY is reused for the prevGuess re-render so that both
// images are vertically aligned to the same row band — faithful to the original
// which derived cropY once from the current guess.
func (s *PipelineScorer) evalFromStage(
	ctx context.Context,
	sr stageResult,
	prevGuess string,
	offset unpixel.Offset,
) EvalResult {
	// Steps 8–10 do real work (a prevGuess re-render), so bail early on cancel.
	if ctx.Err() != nil {
		return EvalResult{Score: 1}
	}
	img := sr.img
	bm := sr.blueMargin
	le := sr.leftEdge
	ox := offset.X
	bs := s.cfg.BlockSize
	redactedH := s.redacted.Bounds().Dy()

	// Step 8: Marginal region — diff against previous guess to find changed band.
	// faithful: main.ts uses the same cropY derived from the current guess for both renders.
	guessImg := img // saved for totalScore
	leftBoundary := 0
	if prevGuess != "" {
		prevRendered, prevBlue, prevErr := s.render(prevGuess)
		if prevErr == nil {
			prevBm, _ := imutil.BlueMargin(prevRendered)
			if prevBm == 0 {
				prevBm = prevBlue
			}
			prevRendered = imutil.Crop(prevRendered, ox, offset.Y, prevBm-ox, prevRendered.Bounds().Dy()-offset.Y)
			if rem := bs - (prevRendered.Bounds().Dx() % bs); rem < bs {
				prevRendered = imutil.PadWhite(prevRendered, prevRendered.Bounds().Dx()+rem, prevRendered.Bounds().Dy())
			}
			prevRendered = s.cfg.Pixelator.Pixelate(prevRendered, 0, 0)
			prevLE := imutil.LeftEdge(prevRendered)
			// Reuse cropY from current guess — faithful to original behaviour.
			prevImg := imutil.Crop(prevRendered, prevLE, sr.cropY, prevRendered.Bounds().Dx()-prevLE, redactedH)

			// Equalise widths before diffing.
			switch {
			case prevImg.Bounds().Dx() < img.Bounds().Dx():
				prevImg = imutil.PadWhite(prevImg, img.Bounds().Dx(), img.Bounds().Dy())
			case prevImg.Bounds().Dx() > img.Bounds().Dx():
				prevImg = imutil.Crop(prevImg, 0, 0, img.Bounds().Dx(), prevImg.Bounds().Dy())
			}

			diffImg := diffRed(img, prevImg)
			lb := imutil.Margins(diffImg)
			if lb == 0 {
				// Identical (e.g. consecutive spaces) — use prev width as boundary.
				leftBoundary = prevImg.Bounds().Dx()
			} else {
				leftBoundary = lb
			}
		}
	}

	// Step 9: Crop to changed band and trim rightmost block.
	// faithful: main.ts — adjustedBlueMargin = (blueMargin-left_boundary)-leftEdge-offset_x
	adjustedBM := (bm - leftBoundary) - le - ox

	imgCropped := imutil.Crop(img, leftBoundary, 0, img.Bounds().Dx()-leftBoundary, img.Bounds().Dy())
	redactedCropped := imutil.Crop(s.redacted, leftBoundary, 0, s.redacted.Bounds().Dx()-leftBoundary, redactedH)

	if imgCropped.Bounds().Dx() > adjustedBM {
		imgCropped = imutil.Crop(imgCropped, 0, 0, adjustedBM, imgCropped.Bounds().Dy())
	}
	if redactedCropped.Bounds().Dx() > adjustedBM {
		redactedCropped = imutil.Crop(redactedCropped, 0, 0, adjustedBM, redactedCropped.Bounds().Dy())
	}

	imgCropped, redactedCropped = equalise(imgCropped, redactedCropped)

	// Step 10: Score.
	// faithful: main.ts Jimp.diff threshold 0.02
	score := s.cfg.Metric.Compare(imgCropped, redactedCropped)

	// TooBig: redacted width < scaled guess width.
	tooBig := s.redacted.Bounds().Dx() < guessImg.Bounds().Dx()

	return EvalResult{
		Score:  score,
		TooBig: tooBig,
	}
}

// diffRed produces an image whose pixels are red where a and b differ and
// white elsewhere, matching the output Jimp.diff produces for getMargins.
// a and b must have the same dimensions.
func diffRed(a, b *image.RGBA) *image.RGBA {
	bounds := a.Bounds()
	out := image.NewRGBA(bounds)
	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := range bounds.Dy() {
		for x := range bounds.Dx() {
			px, py := bounds.Min.X+x, bounds.Min.Y+y
			ca := a.RGBAAt(px, py)
			cb := b.RGBAAt(px, py)
			if ca.R != cb.R || ca.G != cb.G || ca.B != cb.B {
				out.SetRGBA(px, py, red)
			} else {
				out.SetRGBA(px, py, white)
			}
		}
	}
	return out
}

// equalise pads the smaller image with white so both have identical bounds.
func equalise(a, b *image.RGBA) (*image.RGBA, *image.RGBA) {
	aw, ah := a.Bounds().Dx(), a.Bounds().Dy()
	bw, bh := b.Bounds().Dx(), b.Bounds().Dy()
	w, h := max(aw, bw), max(ah, bh)
	if aw < w || ah < h {
		a = imutil.PadWhite(a, w, h)
	}
	if bw < w || bh < h {
		b = imutil.PadWhite(b, w, h)
	}
	return a, b
}
