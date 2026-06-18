// Package search (scorer.go) provides the pipeline Scorer that connects the
// renderer, pixelator, imutil helpers, and metric into the Eval call used by
// GuidedDFS and DiscoverOffsets.
package search

import (
	"context"
	"image"
	"image/color"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// PipelineScorer implements Scorer using the pluggable Renderer, Pixelator,
// and Metric from a Config.
type PipelineScorer struct {
	redacted *image.RGBA
	cfg      unpixel.Config
}

// NewPipelineScorer returns a PipelineScorer for the given redacted image and config.
func NewPipelineScorer(redacted *image.RGBA, cfg unpixel.Config) *PipelineScorer {
	return &PipelineScorer{redacted: redacted, cfg: cfg}
}

// Eval renders guess, pixelates it, performs the marginal-region crop, and
// returns the diff score against the redacted image.
//
// faithful: main.ts redact() steps 1–6, see DESIGN.md §Faithful pipeline.
func (s *PipelineScorer) Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) EvalResult {
	if ctx.Err() != nil {
		return EvalResult{Score: 1}
	}

	// Step 1: Render.
	img, blueMargin, err := s.cfg.Renderer.Render(guess, s.cfg.Style)
	if err != nil {
		return EvalResult{Score: 1}
	}

	// Step 2: BlueMargin — right edge of text + vertical center.
	bm, imageCenter := imutil.BlueMargin(img)
	if bm == 0 {
		bm = blueMargin // fall back to measured advance if scan finds nothing
	}

	// Step 3: Crop to grid origin.
	ox, oy := offset.X, offset.Y
	cropW := bm - ox
	if cropW <= 0 {
		return EvalResult{Score: 1}
	}
	img = imutil.Crop(img, ox, oy, cropW, img.Bounds().Dy()-oy)
	imageCenter -= oy

	// Step 4: White-pad to block multiple.
	bs := s.cfg.BlockSize
	w := img.Bounds().Dx()
	if rem := bs - (w % bs); rem < bs {
		img = imutil.PadWhite(img, w+rem, img.Bounds().Dy())
	}

	// Step 5: Pixelate.
	img = s.cfg.Pixelator.Pixelate(img, 0, 0)

	// Step 6: LeftEdge.
	leftEdge := imutil.LeftEdge(img)

	// Step 7: Vertical crop around block-aligned center.
	redactedH := s.redacted.Bounds().Dy()
	adjustedCenter := imageCenter - (imageCenter % bs) + 4
	cropY := adjustedCenter - redactedH/2
	img = imutil.Crop(img, leftEdge, cropY, img.Bounds().Dx()-leftEdge, redactedH)

	// Step 8: Marginal region — diff against previous guess to find changed band.
	guessImg := img // save for totalScore
	leftBoundary := 0
	if prevGuess != "" {
		prevImg, prevBlue, err := s.cfg.Renderer.Render(prevGuess, s.cfg.Style)
		if err == nil {
			prevBm, _ := imutil.BlueMargin(prevImg)
			if prevBm == 0 {
				prevBm = prevBlue
			}
			prevImg = imutil.Crop(prevImg, ox, oy, prevBm-ox, prevImg.Bounds().Dy()-oy)
			if rem := bs - (prevImg.Bounds().Dx() % bs); rem < bs {
				prevImg = imutil.PadWhite(prevImg, prevImg.Bounds().Dx()+rem, prevImg.Bounds().Dy())
			}
			prevImg = s.cfg.Pixelator.Pixelate(prevImg, 0, 0)
			prevLeftEdge := imutil.LeftEdge(prevImg)
			prevImg = imutil.Crop(prevImg, prevLeftEdge, cropY, prevImg.Bounds().Dx()-prevLeftEdge, redactedH)

			// Pad prevImg to match current img width for diffing.
			if prevImg.Bounds().Dx() < img.Bounds().Dx() {
				prevImg = imutil.PadWhite(prevImg, img.Bounds().Dx(), img.Bounds().Dy())
			} else if prevImg.Bounds().Dx() > img.Bounds().Dx() {
				prevImg = imutil.Crop(prevImg, 0, 0, img.Bounds().Dx(), prevImg.Bounds().Dy())
			}

			// Diff current vs previous to find changed band.
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
	// faithful: main.ts step 4 — adjustedBlueMargin = (blueMargin-left_boundary)-leftEdge-offset_x
	adjustedBM := (bm - leftBoundary) - leftEdge - ox

	imgCropped := imutil.Crop(img, leftBoundary, 0, img.Bounds().Dx()-leftBoundary, img.Bounds().Dy())
	redactedCropped := imutil.Crop(s.redacted, leftBoundary, 0, s.redacted.Bounds().Dx()-leftBoundary, redactedH)

	if imgCropped.Bounds().Dx() > adjustedBM {
		imgCropped = imutil.Crop(imgCropped, 0, 0, adjustedBM, imgCropped.Bounds().Dy())
	}
	if redactedCropped.Bounds().Dx() > adjustedBM {
		redactedCropped = imutil.Crop(redactedCropped, 0, 0, adjustedBM, redactedCropped.Bounds().Dy())
	}

	// Equalise dimensions before comparing.
	imgCropped, redactedCropped = equalise(imgCropped, redactedCropped)

	// Step 10: Score.
	// faithful: main.ts Jimp.diff threshold 0.02
	score := s.cfg.Metric.Compare(imgCropped, redactedCropped)

	// TooBig: redacted width < scaled guess width.
	scaledGuessW := guessImg.Bounds().Dx()
	tooBig := s.redacted.Bounds().Dx() < scaledGuessW

	// TotalScore: diff whole guess vs whole redacted.
	paddedGuess, paddedRedacted := equalise(guessImg, s.redacted)
	totalScore := s.cfg.Metric.Compare(paddedGuess, paddedRedacted)

	return EvalResult{
		Score:      score,
		TotalScore: totalScore,
		TooBig:     tooBig,
	}
}

// diffRed produces an image whose pixels are red where a and b differ and
// white elsewhere, matching the output that Jimp.diff produces for getMargins.
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

// equalise pads the smaller image with white so both have identical bounds,
// then returns updated (a, b).
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
