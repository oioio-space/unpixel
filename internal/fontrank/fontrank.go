// Package fontrank ranks candidate fonts by how well their glyph exemplars
// visually match a mosaic-pixelated redaction image.
//
// # Motivation
//
// A full per-font mosaic decode (render → re-pixelate → character search) is
// expensive: it calibrates typography, sweeps character counts, and scores
// many candidate strings per font. When the font is unknown and many candidates
// are available, the decode cost is multiplied by the font count.
//
// RankFonts provides a cheap pre-filter: it scores each font by how closely
// a small set of representative exemplar glyphs — rendered and pixelated at
// the detected block size — matches the statistical profile of the target
// redaction, WITHOUT searching for the hidden text. The probe is fast because
// it renders only one short string per font (rather than every candidate
// string), and uses a per-column block-intensity histogram comparison rather
// than per-candidate full-image MSE.
//
// # Scoring signal
//
// Each block column of a mosaic image carries the average luminance of the
// ink+background in that column. Fonts that share similar glyph shapes and
// ink coverage produce similar per-column distributions even across different
// texts — a serif font's serifs lift low columns, a monospace font's even
// advance creates a flatter distribution than a proportional one, and so on.
//
// The ranker compares the sorted-luminance profile (a 16-bucket histogram
// over the block-column means of the target) against the same profile
// computed from a one-line exemplar render of a representative pangram.
// Sorting makes the comparison translation-invariant (the exemplar text
// differs from the hidden text, so positional alignment is meaningless).
// The distance is the L1 norm between the two normalised histograms — fast
// to compute and monotonically related to glyph-shape similarity.
//
// # Limitations
//
// The signal is a global statistical proxy, not a geometric match. Fonts
// whose exemplar ink-coverage distribution happens to match the target's —
// e.g. two metrically similar sans-serif faces — may tie or swap. In
// practice RankFonts reliably separates broad font categories (serif vs
// sans-serif, mono vs proportional) and typically places the true font in the
// top-3 across the bundled set. For exact ranking within a confusable family,
// a full decode is required.
//
// # Usage
//
//	named := []fontrank.NamedFont{{Name: "Liberation Sans", Data: ttfBytes}, ...}
//	scores, err := fontrank.RankFonts(ctx, mosaicImage, named)
//	// scores[0] is the best-matching font; prune to top-K before a full decode.
package fontrank

import (
	"cmp"
	"context"
	"image"
	"image/draw"
	"math"
	"slices"
	"sync"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// NamedFont pairs a human-readable font name with its TrueType/OpenType bytes.
type NamedFont struct {
	// Name is the human-readable identifier, e.g. "Liberation Sans".
	Name string
	// Data is the raw TTF/OTF font bytes.
	Data []byte
}

// FontScore is a single ranked result from RankFonts.
type FontScore struct {
	// Name is the font identifier, copied from the corresponding NamedFont.
	Name string
	// Score is the L1 histogram distance between the font's glyph-exemplar
	// luminance profile and the target redaction's profile. Lower is a better
	// visual match. The value is in [0, 1]; 0 means an identical profile.
	Score float64
}

// exemplarText is the pangram rendered to build the per-font luminance profile.
// It covers all 26 Latin letters plus digits and common punctuation so the
// per-column block distribution samples each glyph category. The exact string
// does not matter — it is never compared against the hidden text; only its
// aggregate block-intensity histogram is used.
const exemplarText = "The quick brown fox jumps over 42 lazy dogs!"

// exemplarFontSize is the font size used for exemplar rendering. It is chosen
// to be large enough that glyph details survive the block-averaging step and
// small enough that rendering stays fast. When the target block size is
// detected as larger, the exemplar is pixelated at that block size directly.
const exemplarFontSize = 28.0

// histBuckets is the number of luminance histogram buckets. 16 gives enough
// resolution to distinguish broad ink-coverage categories (serif drop-caps,
// thin sans strokes, monospace uniformity) while keeping the L1 distance
// computation cheap and robust to small render differences.
const histBuckets = 16

// RankFonts scores each candidate font by how well a small glyph exemplar
// matches the block-luminance profile of img, auto-detecting the mosaic block
// size, and returns the list sorted best-first (lowest Score first). See
// [RankFontsAt] for the full contract.
func RankFonts(ctx context.Context, img image.Image, fonts []NamedFont) ([]FontScore, error) {
	return RankFontsAt(ctx, img, fonts, 0)
}

// RankFontsAt is [RankFonts] with an explicit mosaic block size. When blockSize
// is <= 0 it auto-detects the block size from img (identical to RankFonts); when
// > 0 it uses the caller's known block size and skips detection. It is blind: it
// needs no known plaintext. RankFontsAt is safe for concurrent use; it returns a
// non-nil error only on context cancellation.
func RankFontsAt(ctx context.Context, img image.Image, fonts []NamedFont, blockSize int) ([]FontScore, error) {
	if len(fonts) == 0 {
		return nil, nil
	}

	target := imutil.ToRGBA(img)
	if blockSize <= 0 {
		blockSize = detectBlockSize(target)
	}
	targetHist := blockLumHistogram(target, blockSize)

	// Score each font concurrently; goroutines are bounded by font count (the
	// bundle is 9 fonts, so a semaphore is not needed — sync.WaitGroup suffices).
	scores := make([]FontScore, len(fonts))
	var wg sync.WaitGroup
	for i, f := range fonts {
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			scores[i] = FontScore{
				Name:  f.Name,
				Score: scoreFont(f.Data, blockSize, targetHist),
			}
		})
	}
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	slices.SortStableFunc(scores, func(a, b FontScore) int {
		return cmp.Compare(a.Score, b.Score)
	})
	return scores, nil
}

// scoreFont builds the exemplar histogram for a single font and returns its L1
// distance to targetHist. A parsing or rendering failure returns math.Inf(1) so
// the font sorts last rather than crashing the pipeline.
func scoreFont(data []byte, blockSize int, targetHist [histBuckets]float64) float64 {
	r, err := render.NewXImageFromFonts(data, nil)
	if err != nil {
		return math.Inf(1)
	}
	img, sx, err := r.Render(exemplarText, unpixel.Style{FontSize: exemplarFontSize})
	if err != nil || sx <= 0 {
		return math.Inf(1)
	}
	// Crop to the actual text (left of sentinel x).
	cropped := cropToSentinel(img, sx)
	// Pixelate at the same block size as the target.
	pix := pixelate.NewBlockAverage(blockSize)
	pixelated := pix.Pixelate(cropped, 0, 0)

	fontHist := blockLumHistogram(pixelated, blockSize)
	return l1Distance(targetHist, fontHist)
}

// detectBlockSize infers the mosaic block size from the image by looking for
// the largest run of identical-colour columns (a simple, fast proxy for the
// full InferBlockSize algorithm). Falls back to 8 when no regular grid is
// found — 8 px is the most common mosaic block size in practice.
func detectBlockSize(img *image.RGBA) int {
	b := img.Bounds()
	w := b.Dx()
	if w < 2 {
		return 8
	}
	// Sample the middle row; count the longest run of equal adjacent pixels.
	midY := b.Min.Y + b.Dy()/2
	maxRun, run := 1, 1
	prev := img.RGBAAt(b.Min.X, midY)
	for x := b.Min.X + 1; x < b.Max.X; x++ {
		cur := img.RGBAAt(x, midY)
		if cur == prev {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 1
		}
		prev = cur
	}
	if maxRun < 2 {
		return 8
	}
	return maxRun
}

// blockLumHistogram computes a normalised luminance histogram over the
// per-block-column mean luminances of img. Each block column contributes one
// sample: the mean luma of all pixels in that column. The histogram has
// histBuckets bins covering [0, 255]; values are normalised to sum to 1.
//
// Using block-column means rather than individual pixel lumas makes the
// histogram insensitive to horizontal text placement (a shifted exemplar has
// the same distribution as an aligned one) and focuses the comparison on the
// aggregate ink-coverage pattern that distinguishes font styles.
func blockLumHistogram(img *image.RGBA, blockSize int) [histBuckets]float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 || blockSize < 1 {
		return [histBuckets]float64{}
	}

	var hist [histBuckets]float64
	nCols := 0

	for colStart := b.Min.X; colStart < b.Max.X; colStart += blockSize {
		colEnd := min(colStart+blockSize, b.Max.X)
		var sumLum float64
		n := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := colStart; x < colEnd; x++ {
				c := img.RGBAAt(x, y)
				sumLum += float64(imutil.Lum601(c.R, c.G, c.B))
				n++
			}
		}
		if n == 0 {
			continue
		}
		meanLum := sumLum / float64(n) // in [0, 255]
		bucket := int(meanLum * histBuckets / 256)
		if bucket >= histBuckets {
			bucket = histBuckets - 1
		}
		hist[bucket]++
		nCols++
	}

	if nCols > 0 {
		for i := range hist {
			hist[i] /= float64(nCols)
		}
	}
	return hist
}

// l1Distance returns the L1 (Manhattan) distance between two normalised
// histograms. The result is in [0, 2]; for normalised distributions it is at
// most 2 (completely disjoint support). We divide by 2 so the score is in [0, 1].
func l1Distance(a, b [histBuckets]float64) float64 {
	var s float64
	for i := range a {
		s += math.Abs(a[i] - b[i])
	}
	return s / 2
}

// cropToSentinel returns the sub-image of img clipped to columns [0, sentinelX),
// so the blue sentinel block added by the renderer is excluded from the
// exemplar pixelation.
func cropToSentinel(img *image.RGBA, sentinelX int) *image.RGBA {
	b := img.Bounds()
	endX := min(sentinelX, b.Max.X)
	if endX <= b.Min.X {
		return img
	}
	sub := img.SubImage(image.Rect(b.Min.X, b.Min.Y, endX, b.Max.Y))
	// SubImage returns image.Image; convert back to *image.RGBA for Pixelate.
	r, ok := sub.(*image.RGBA)
	if ok {
		return r
	}
	sb := sub.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, sb.Dx(), sb.Dy()))
	draw.Draw(dst, dst.Bounds(), sub, sb.Min, draw.Src)
	return dst
}
