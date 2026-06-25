// Package search (scorer.go) provides the pipeline Scorer that connects the
// renderer, pixelator, imutil helpers, and metric into the Eval call used by
// GuidedDFS, BeamDFS, and DiscoverOffsets.
package search

import (
	"container/list"
	"context"
	"errors"
	"image"
	"sync"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/metric"
)

// renderCacheCap bounds the per-scorer render cache. Discovery renders the whole
// charset, and a DFS reuses each parent as the next level's prevGuess, so a few
// hundred entries capture the reuse while keeping memory bounded.
const renderCacheCap = 256

// prevStageCacheCap bounds the H1 prevGuess partial-stage cache. One entry per
// (prevGuess, offset) node visited by the DFS; 256 keeps memory bounded while
// covering all realistic search depths and charset sizes.
const prevStageCacheCap = 256

// redactedCropCacheCap bounds the H2 redacted-band crop cache.
// One entry per distinct (leftBoundary, width) pair seen during a search; 64
// covers the real-world range without unbounded growth.
const redactedCropCacheCap = 64

// renderEntry is one cached rendering, keyed by candidate text.
// O1: blueMargin and center are memoized alongside the render so that every
// caller of BlueMargin on this (immutable) image pays the scan cost at most once.
// blueMarginDone is set to true after the first computation; a zero center is
// valid (e.g. single-pixel glyph), so it cannot serve as a sentinel.
type renderEntry struct {
	text      string
	img       *image.RGBA
	sentinelX int

	// O1 — memoized BlueMargin result. Protected by the scorer's rmu until
	// blueMarginDone is set; after that the fields are read-only.
	blueMarginDone bool
	blueMargin     int
	center         int
}

// prevStageKey identifies a cached prevGuess partial pipeline result (H1).
// The key omits blockSize and styleKey because PipelineScorer is constructed
// with a fixed Config; those fields never change within one scorer's lifetime.
type prevStageKey struct {
	prevGuess string
	ox, oy    int
}

// prevStageEntry is the cached output of the prevGuess pipeline up to LeftEdge
// (render → BlueMargin → Crop-to-origin → PadWhite → Pixelate → LeftEdge),
// before the per-child Crop(sr.cropY) step. The prevPixelated image is
// read-only after creation (Crop always allocates a fresh image), so sharing
// across children and goroutines is safe — identical discipline to renderEntry.
//
// key is stored in the entry so that LRU eviction can remove the correct map
// entry without a separate key lookup (same pattern as renderEntry.text).
type prevStageEntry struct {
	key           prevStageKey
	prevPixelated *image.RGBA
	prevLE        int
}

// redactedCropKey identifies a cached crop of s.redacted (H2).
type redactedCropKey struct {
	leftBoundary int
	width        int
}

// redactedCropEntry wraps a cached crop of s.redacted together with its map key
// so that LRU eviction can remove the correct entry (same pattern as renderEntry).
type redactedCropEntry struct {
	key     redactedCropKey
	cropped *image.RGBA
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

	// H1 — prevGuess partial-stage cache.
	// Keyed by prevStageKey; LRU values are *prevStageEntry (which carries its
	// own key for O(1) eviction). Lock discipline: pmu guards plru/pcache, held
	// only long enough to read/write the map and LRU pointer — identical to rmu.
	// Cached prevPixelated images are read-only; only the per-child Crop(cropY)
	// tail is computed fresh per evalFromStage call.
	pmu    sync.Mutex
	plru   *list.List
	pcache map[prevStageKey]*list.Element

	// H2 — redacted-band crop cache.
	// Keyed by redactedCropKey; LRU values are *redactedCropEntry.
	// s.redacted is fixed so the crop result is deterministic and immutable.
	// The cached image is read-only (equalise/PadWhite allocate new images;
	// Compare is read-only). Same lock discipline as rmu/pmu.
	cmu    sync.Mutex
	clru   *list.List
	ccache map[redactedCropKey]*list.Element
}

// NewPipelineScorer returns a PipelineScorer for the given redacted image and config.
func NewPipelineScorer(redacted *image.RGBA, cfg unpixel.Config) *PipelineScorer {
	return &PipelineScorer{
		redacted: redacted,
		cfg:      cfg,
		rlru:     list.New(),
		rcache:   make(map[string]*list.Element),
		plru:     list.New(),
		pcache:   make(map[prevStageKey]*list.Element),
		clru:     list.New(),
		ccache:   make(map[redactedCropKey]*list.Element),
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

// blueMarginOf returns the (blueMargin, center) pair for the rendered entry,
// computing and memoizing it on first call (O1).
//
// The caller must hold rmu on entry. rmu is released while calling
// imutil.BlueMargin (pure, no lock needed) and re-acquired to store the result.
// A benign duplicate computation may occur on a concurrent first call; the
// result is always the same pure function of the immutable entry.img.
func (s *PipelineScorer) blueMarginOf(el *list.Element) (bm, center int) {
	e := el.Value.(*renderEntry)
	if e.blueMarginDone {
		return e.blueMargin, e.center
	}
	img := e.img
	sx := e.sentinelX
	s.rmu.Unlock()

	computedBM, computedCenter := imutil.BlueMargin(img)
	if computedBM == 0 {
		computedBM = sx
	}

	s.rmu.Lock()
	// Re-read in case another goroutine raced and filled it first.
	e = el.Value.(*renderEntry)
	if !e.blueMarginDone {
		e.blueMargin = computedBM
		e.center = computedCenter
		e.blueMarginDone = true
	}
	return e.blueMargin, e.center
}

// renderWithBM returns the rendered image, blueMargin, and imageCenter for text.
// It uses the render cache and the O1 BlueMargin memo; sentinelX is consumed
// internally as the BlueMargin fallback and is not exposed to callers.
func (s *PipelineScorer) renderWithBM(text string) (img *image.RGBA, bm, imageCenter int, err error) {
	s.rmu.Lock()
	if el, ok := s.rcache[text]; ok {
		s.rlru.MoveToFront(el)
		bm, imageCenter = s.blueMarginOf(el) // may drop + re-acquire rmu
		img = el.Value.(*renderEntry).img
		s.rmu.Unlock()
		return img, bm, imageCenter, nil
	}
	s.rmu.Unlock()

	// Cache miss: render outside the lock.
	var sx int
	img, sx, err = s.cfg.Renderer.Render(text, s.cfg.Style)
	if err != nil {
		return nil, 0, 0, err
	}
	// Compute BlueMargin outside the lock (pure function, no contention).
	bm, imageCenter = imutil.BlueMargin(img)
	if bm == 0 {
		bm = sx
	}

	s.rmu.Lock()
	defer s.rmu.Unlock()
	if el, ok := s.rcache[text]; ok { // concurrent miss stored it first
		s.rlru.MoveToFront(el)
		e := el.Value.(*renderEntry)
		// Store our freshly computed BlueMargin if the existing entry hasn't set it yet.
		if !e.blueMarginDone {
			e.blueMargin = bm
			e.center = imageCenter
			e.blueMarginDone = true
		}
		return e.img, e.blueMargin, e.center, nil
	}
	entry := &renderEntry{
		text: text, img: img, sentinelX: sx,
		blueMarginDone: true, blueMargin: bm, center: imageCenter,
	}
	s.rcache[text] = s.rlru.PushFront(entry)
	if s.rlru.Len() > renderCacheCap {
		if back := s.rlru.Back(); back != nil {
			delete(s.rcache, back.Value.(*renderEntry).text)
			s.rlru.Remove(back)
		}
	}
	return img, bm, imageCenter, nil
}

// prevStage returns the H1-cached partial pipeline for prevGuess at offset:
// render → BlueMargin → Crop-to-origin → PadWhite → Pixelate → LeftEdge.
// The returned prevStageEntry.prevPixelated must not be mutated.
func (s *PipelineScorer) prevStage(prevGuess string, offset unpixel.Offset) (*prevStageEntry, error) {
	k := prevStageKey{prevGuess: prevGuess, ox: offset.X, oy: offset.Y}

	s.pmu.Lock()
	if el, ok := s.pcache[k]; ok {
		s.plru.MoveToFront(el)
		ent := el.Value.(*prevStageEntry)
		s.pmu.Unlock()
		return ent, nil
	}
	s.pmu.Unlock()

	// Cache miss: compute the partial pipeline outside the lock.
	img, bm, _, err := s.renderWithBM(prevGuess)
	if err != nil {
		return nil, err
	}
	ox, oy := offset.X, offset.Y
	cropW := bm - ox
	if cropW <= 0 {
		return nil, errCropEmpty
	}
	cropped := imutil.Crop(img, ox, oy, cropW, img.Bounds().Dy()-oy)
	bs := s.cfg.BlockSize
	if rem := bs - (cropped.Bounds().Dx() % bs); rem < bs {
		cropped = imutil.PadWhite(cropped, cropped.Bounds().Dx()+rem, cropped.Bounds().Dy())
	}
	pixelated := s.cfg.Pixelator.Pixelate(cropped, 0, 0)
	le := imutil.LeftEdge(pixelated)
	ent := &prevStageEntry{key: k, prevPixelated: pixelated, prevLE: le}

	s.pmu.Lock()
	defer s.pmu.Unlock()
	if el, ok := s.pcache[k]; ok { // concurrent miss stored it first
		s.plru.MoveToFront(el)
		return el.Value.(*prevStageEntry), nil
	}
	s.pcache[k] = s.plru.PushFront(ent)
	if s.plru.Len() > prevStageCacheCap {
		if back := s.plru.Back(); back != nil {
			delete(s.pcache, back.Value.(*prevStageEntry).key)
			s.plru.Remove(back)
		}
	}
	return ent, nil
}

// redactedCrop returns a crop of s.redacted at (leftBoundary, 0, width, redactedH),
// using the H2 cache. The returned image must not be mutated.
func (s *PipelineScorer) redactedCrop(leftBoundary, width, redactedH int) *image.RGBA {
	k := redactedCropKey{leftBoundary: leftBoundary, width: width}

	s.cmu.Lock()
	if el, ok := s.ccache[k]; ok {
		s.clru.MoveToFront(el)
		img := el.Value.(*redactedCropEntry).cropped
		s.cmu.Unlock()
		return img
	}
	s.cmu.Unlock()

	// Compute outside the lock.
	cropped := imutil.Crop(s.redacted, leftBoundary, 0, width, redactedH)

	s.cmu.Lock()
	defer s.cmu.Unlock()
	if el, ok := s.ccache[k]; ok { // concurrent miss stored it first
		s.clru.MoveToFront(el)
		return el.Value.(*redactedCropEntry).cropped
	}
	s.ccache[k] = s.clru.PushFront(&redactedCropEntry{key: k, cropped: cropped})
	if s.clru.Len() > redactedCropCacheCap {
		if back := s.clru.Back(); back != nil {
			delete(s.ccache, back.Value.(*redactedCropEntry).key)
			s.clru.Remove(back)
		}
	}
	return cropped
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

	// Steps 1 + 2: Render (cached) and BlueMargin (O1 memoized in renderEntry).
	rendered, bm, imageCenter, err := s.renderWithBM(guess)
	if err != nil {
		return stageResult{}, err
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

// TotalScore renders guess and scores the entire pixelated candidate against the
// entire redacted image (white-padding the narrower one to equal width), with no
// marginal-region cropping. It measures how well the full candidate explains the
// whole redaction, so it disambiguates the final answer: a correct prefix leaves
// the rest of the redaction unexplained (high total score) and a coincidental
// glyph match differs across the image, while the complete string scores lowest.
// It returns 1 (worst) if the candidate cannot be staged.
//
// faithful: main.ts computed this same full-image diff as totalScore; UnPixel
// drops it from the per-candidate hot loop (P4.1) and uses it only for ranking.
func (s *PipelineScorer) TotalScore(ctx context.Context, guess string, offset unpixel.Offset) float64 {
	sr, err := s.stageImage(ctx, guess, offset)
	if err != nil {
		return 1
	}
	g, red := equalise(sr.img, s.redacted)
	return s.cfg.Metric.Compare(g, red)
}

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
	return s.evalFromStage(ctx, sr, prevGuess, offset, 0)
}

// EvalBounded is like Eval but passes maxDiffRatio to the metric's
// [metric.BoundedComparer] when available, allowing the pixel scan to abort
// once rejection is certain. For accepted candidates (score < maxDiffRatio)
// the returned score is identical to Eval; for rejected candidates the score
// is >= maxDiffRatio (exact value unspecified). Pass 0 to disable the ceiling.
func (s *PipelineScorer) EvalBounded(ctx context.Context, guess, prevGuess string, offset unpixel.Offset, maxDiffRatio float64) EvalResult {
	if ctx.Err() != nil {
		return EvalResult{Score: 1}
	}
	sr, err := s.stageImage(ctx, guess, offset)
	if err != nil {
		return EvalResult{Score: 1}
	}
	return s.evalFromStage(ctx, sr, prevGuess, offset, maxDiffRatio)
}

// evalFromStage completes steps 8–10 given the already-staged image for guess.
// It is called by both Eval and CachingScorer.Eval (which supplies the cached
// stageResult). sr.cropY is reused for the prevGuess re-render so that both
// images are vertically aligned to the same row band — faithful to the original
// which derived cropY once from the current guess.
//
// maxDiffRatio > 0 activates the early-exit ceiling: when cfg.Metric implements
// [metric.BoundedComparer], the pixel scan aborts once the running diff reaches
// maxDiffRatio, returning a score >= maxDiffRatio for rejected candidates while
// returning the exact same score as Compare for accepted ones. Pass 0 to use
// the full scan unconditionally.
//
// H1: the expensive prevGuess pipeline (render→BlueMargin→Crop→PadWhite→
// Pixelate→LeftEdge) is computed once per (prevGuess, offset) and cached in
// pcache; only the per-child Crop(sr.cropY) tail is run fresh each call.
// H2: the crop of s.redacted is cached per (leftBoundary, width) in ccache.
func (s *PipelineScorer) evalFromStage(
	ctx context.Context,
	sr stageResult,
	prevGuess string,
	offset unpixel.Offset,
	maxDiffRatio float64,
) EvalResult {
	// Steps 8–10 do real work (a prevGuess re-render), so bail early on cancel.
	if ctx.Err() != nil {
		return EvalResult{Score: 1}
	}
	img := sr.img
	bm := sr.blueMargin
	le := sr.leftEdge
	ox := offset.X
	redactedH := s.redacted.Bounds().Dy()

	// Step 8: Marginal region — diff against previous guess to find changed band.
	// faithful: main.ts uses the same cropY derived from the current guess for both renders.
	guessImg := img // saved for tooBig check
	leftBoundary := 0
	if prevGuess != "" {
		// H1: fetch (or compute-and-cache) the prevGuess partial pipeline up to
		// LeftEdge. Only the final Crop(sr.cropY) is done here per child, because
		// cropY varies per child and is cheap relative to Pixelate.
		ps, prevErr := s.prevStage(prevGuess, offset)
		if prevErr == nil {
			prevImg := imutil.Crop(ps.prevPixelated, ps.prevLE, sr.cropY,
				ps.prevPixelated.Bounds().Dx()-ps.prevLE, redactedH)

			// Equalise widths before diffing.
			switch {
			case prevImg.Bounds().Dx() < img.Bounds().Dx():
				prevImg = imutil.PadWhite(prevImg, img.Bounds().Dx(), img.Bounds().Dy())
			case prevImg.Bounds().Dx() > img.Bounds().Dx():
				prevImg = imutil.Crop(prevImg, 0, 0, img.Bounds().Dx(), prevImg.Bounds().Dy())
			}

			lb := marginColumn(img, prevImg)
			if lb == 0 {
				// Identical (e.g. consecutive spaces) — use prev width as boundary.
				leftBoundary = prevImg.Bounds().Dx()
			} else {
				leftBoundary = lb
			}
		}
	}

	// Step 9: Crop to changed band and trim rightmost block.
	// faithful: main.ts — adjustedBlueMargin = (blueMargin-left_boundary)-leftEdge-offset_x.
	// The band crop (x=leftBoundary) and the adjustedBM width-trim compose into a
	// single horizontal sub-rectangle, so do one Crop each instead of two: a copy
	// of [leftBoundary, leftBoundary+w) equals cropping the band then trimming.
	adjustedBM := (bm - leftBoundary) - le - ox

	imgCropped := imutil.Crop(img, leftBoundary, 0, min(img.Bounds().Dx()-leftBoundary, adjustedBM), img.Bounds().Dy())

	// H2: reuse the cached crop of s.redacted for this (leftBoundary, width).
	redactedW := min(s.redacted.Bounds().Dx()-leftBoundary, adjustedBM)
	redactedCropped := s.redactedCrop(leftBoundary, redactedW, redactedH)

	imgCropped, redactedCropped = equalise(imgCropped, redactedCropped)

	// Step 10: Score.
	// faithful: main.ts Jimp.diff threshold 0.02
	// When the metric supports BoundedComparer and a ceiling is set, use it so
	// the pixel scan aborts early for rejected candidates. Accepted candidates
	// (score < maxDiffRatio) always get the exact same score as Compare.
	var score float64
	if maxDiffRatio > 0 {
		if bc, ok := s.cfg.Metric.(metric.BoundedComparer); ok {
			score = bc.CompareBounded(imgCropped, redactedCropped, maxDiffRatio)
		} else {
			score = s.cfg.Metric.Compare(imgCropped, redactedCropped)
		}
	} else {
		score = s.cfg.Metric.Compare(imgCropped, redactedCropped)
	}

	// TooBig: redacted width < scaled guess width.
	tooBig := s.redacted.Bounds().Dx() < guessImg.Bounds().Dx()

	return EvalResult{
		Score:  score,
		TooBig: tooBig,
	}
}

// marginColumn returns the x of the first column on the middle row where a and b
// differ in RGB, or 0 if they are identical there. It equals
// imutil.Margins(diffRed(a, b)) — the first red column of the faithful diff
// image — without materialising that full-image diff (only the middle row is
// read). a and b are expected to have the same dimensions and origin (0,0).
func marginColumn(a, b *image.RGBA) int {
	ab, bb := a.Bounds(), b.Bounds()
	w := min(ab.Dx(), bb.Dx())
	midY := ab.Dy() / 2
	// Read the middle row's RGB straight from Pix[] (4-byte stride) instead of
	// per-pixel RGBAAt; identical comparison, fewer bounds checks per column.
	ap, bp := a.Pix, b.Pix
	ao := a.PixOffset(ab.Min.X, ab.Min.Y+midY)
	bo := b.PixOffset(bb.Min.X, bb.Min.Y+midY)
	for x := range w {
		i, j := ao+x*4, bo+x*4
		if ap[i] != bp[j] || ap[i+1] != bp[j+1] || ap[i+2] != bp[j+2] {
			return x
		}
	}
	return 0
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
