// Package infoleak quantifies how much exploitable information a block-average
// mosaic leaks under anti-aliased rendering and JPEG compression. It is a
// measurement/feasibility study, not a decoder: for a true block-average mosaic
// the only recoverable signal is the block values themselves (the "missing
// avalanche effect" the engine already exploits via generate-and-test). These
// primitives let the //go:build infoleak study runner put numbers on the
// information boundary; see docs/JOURNAL.md for the recorded findings.
package infoleak

import (
	"bytes"
	"image"
	"image/jpeg"

	unpixel "github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// Separability is the mean per-pixel absolute luminance difference between a and
// b, normalised to [0,1] (0 means indistinguishable). The images are compared
// over their common minimum width and height, so near-equal-width candidates
// (e.g. the confusable pair "rn"/"m") can be compared directly.
func Separability(a, b *image.RGBA) float64 {
	ab, bb := a.Bounds(), b.Bounds()
	w := min(ab.Dx(), bb.Dx())
	h := min(ab.Dy(), bb.Dy())
	if w == 0 || h == 0 {
		return 0
	}
	var sum int
	for y := range h {
		for x := range w {
			ao := a.PixOffset(ab.Min.X+x, ab.Min.Y+y)
			bo := b.PixOffset(bb.Min.X+x, bb.Min.Y+y)
			la := imutil.Lum601(a.Pix[ao], a.Pix[ao+1], a.Pix[ao+2])
			lb := imutil.Lum601(b.Pix[bo], b.Pix[bo+1], b.Pix[bo+2])
			d := la - lb
			if d < 0 {
				d = -d
			}
			sum += d
		}
	}
	return float64(sum) / (float64(w*h) * 255.0)
}

// JPEGRoundTrip encodes img as JPEG at the given quality (1..100) and decodes it
// back to RGBA, simulating a JPEG-compressed capture of a mosaic. Lower quality
// adds more signal-dependent noise to the (otherwise block-constant) values.
func JPEGRoundTrip(img *image.RGBA, quality int) (*image.RGBA, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	decoded, err := jpeg.Decode(&buf)
	if err != nil {
		return nil, err
	}
	return imutil.ToRGBA(decoded), nil
}

// PairResult is the separability of one confusable pair under AA vs hard-edge
// rendering. Gain = AASep − HardSep is how much sub-pixel anti-aliasing adds to
// the pair's distinguishability after block-averaging.
type PairResult struct {
	A, B                 string
	AASep, HardSep, Gain float64
}

// AAReport aggregates MeasureAALeak over a set of confusable pairs for one font.
type AAReport struct {
	Font                             string
	Pairs                            []PairResult
	MeanAASep, MeanHardSep, MeanGain float64
}

// MeasureAALeak renders each confusable pair with renderer r at fontSize,
// block-averages at block, and reports the pair's separability under
// anti-aliased rendering (AASep) versus a hard-edge (binarised) render
// (HardSep). A positive mean Gain means anti-aliasing leaves more sub-pixel
// signal in the mosaic that distinguishes the pair. It returns an error if
// rendering fails.
func MeasureAALeak(r unpixel.Renderer, fontName string, pairs [][2]string, block int, fontSize float64) (AAReport, error) {
	pix := pixelate.NewBlockAverage(block)
	rep := AAReport{Font: fontName, Pairs: make([]PairResult, 0, len(pairs))}
	var sumAA, sumHard, sumGain float64
	for _, p := range pairs {
		aImg, _, err := r.Render(p[0], unpixel.Style{FontSize: fontSize})
		if err != nil {
			return AAReport{}, err
		}
		bImg, _, err := r.Render(p[1], unpixel.Style{FontSize: fontSize})
		if err != nil {
			return AAReport{}, err
		}
		aaSep := Separability(pix.Pixelate(aImg, 0, 0), pix.Pixelate(bImg, 0, 0))
		hardSep := Separability(
			pix.Pixelate(binarizeHardEdge(aImg, 128), 0, 0),
			pix.Pixelate(binarizeHardEdge(bImg, 128), 0, 0),
		)
		pr := PairResult{A: p[0], B: p[1], AASep: aaSep, HardSep: hardSep, Gain: aaSep - hardSep}
		rep.Pairs = append(rep.Pairs, pr)
		sumAA += aaSep
		sumHard += hardSep
		sumGain += pr.Gain
	}
	if n := float64(len(pairs)); n > 0 {
		rep.MeanAASep, rep.MeanHardSep, rep.MeanGain = sumAA/n, sumHard/n, sumGain/n
	}
	return rep, nil
}

// binarizeHardEdge thresholds an anti-aliased render to two luminance levels —
// black (Lum < threshold) or white — removing the sub-pixel AA coverage so its
// contribution can be isolated by comparison against the AA original.
func binarizeHardEdge(img *image.RGBA, threshold int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			var v uint8 = 255
			if imutil.Lum601(img.Pix[o], img.Pix[o+1], img.Pix[o+2]) < threshold {
				v = 0
			}
			oo := out.PixOffset(x, y)
			out.Pix[oo], out.Pix[oo+1], out.Pix[oo+2], out.Pix[oo+3] = v, v, v, 255
		}
	}
	return out
}
