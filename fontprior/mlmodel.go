//go:build ml

package fontprior

// mlmodel.go — the trained font-ID model behind the //go:build ml seam. It is a
// self-contained, pure-Go pipeline (no CGO, no external training framework, no
// embedded weights): the renderer is the labeller, so the model TRAINS ITSELF at
// first use on synthetic render→pixelate samples of the bundled fonts, then does a
// pure-Go softmax forward pass to rank fonts.
//
// Feature: a font is identified blind (unknown plaintext) from the DISTRIBUTION of
// its INK block luminances — a font's stroke weight/aperture sets how dark its
// strokes average at a given block size. lumHist captures that as the ink fraction
// plus a normalised histogram over ink-bearing blocks only (background is excluded,
// or it would swamp the signal), which is text-length- and position-invariant. A
// softmax over that feature, trained discriminatively across fonts and block sizes,
// separates families cleanly; the residual confusions are same-family faces (mono↔
// mono, sans↔sans), the information limit of blind font-ID from a coarse mosaic.

import (
	"image"
	"sort"
	"strings"
	"sync"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/linearml"
)

const (
	histBins       = 24  // block-luminance histogram resolution
	mlEpochs       = 300 // SGD epochs
	mlLR           = 0.3 // learning rate
	mlL2           = 1e-4
	samplesPerFont = 6 // renders per (font, block, text) combination
)

// mlBlocks is the range of block sizes trained over so the model generalises
// across the redaction's (unknown) mosaic block size.
var mlBlocks = []int{4, 5, 6, 8, 10}

// mlTexts are exemplar strings spanning glyph shapes; the label is font, not text,
// so variety here makes the feature text-invariant.
var mlTexts = []string{"the quick brown fox", "PASSWORD 12345", "Hello, World!", "aeiou nmwWMH loql"}

// inkThreshold is the block-mean-luminance below which a mosaic block is treated
// as ink-bearing (text) rather than background. Above it, block-average white pads
// the histogram and washes out the font signature.
const inkThreshold = 235.0

// lumHist returns the model's feature (length histBins) from img pixelated at
// block. Feature[0] is the ink fraction (share of blocks carrying text); the
// remaining histBins-1 entries are a normalised histogram of the *ink* blocks'
// mean luminance. Focusing on ink blocks captures the font's stroke-darkness
// signature — how black its strokes average at this block size — instead of the
// background that dominates a raw all-block histogram. It stays text-length- and
// position-invariant (both terms are normalised counts).
func lumHist(img *image.RGBA, block int) []float64 {
	if block < 2 {
		block = 8
	}
	b := img.Bounds()
	h := make([]float64, histBins)
	inkBins := histBins - 1
	total, ink := 0, 0
	for by := b.Min.Y; by < b.Max.Y; by += block {
		for bx := b.Min.X; bx < b.Max.X; bx += block {
			var sum, cnt float64
			for y := by; y < by+block && y < b.Max.Y; y++ {
				for x := bx; x < bx+block && x < b.Max.X; x++ {
					c := img.RGBAAt(x, y)
					sum += float64(299*int(c.R)+587*int(c.G)+114*int(c.B)) / 1000
					cnt++
				}
			}
			if cnt == 0 {
				continue
			}
			total++
			lum := sum / cnt // [0,255]
			if lum >= inkThreshold {
				continue
			}
			ink++
			bin := int(lum / inkThreshold * float64(inkBins))
			if bin >= inkBins {
				bin = inkBins - 1
			}
			h[1+bin]++
		}
	}
	if total > 0 {
		h[0] = float64(ink) / float64(total)
	}
	if ink > 0 {
		for i := 1; i < histBins; i++ {
			h[i] /= float64(ink)
		}
	}
	return h
}

// genSamples renders each font's exemplars, pixelates at each mlBlocks size, and
// returns (features, labels) for training. Label is the font index into fnts.
func genSamples(fnts []fonts.Font) (X [][]float64, y []int) {
	for fi, f := range fnts {
		r, err := defaults.RendererFromFonts(f.Data, nil)
		if err != nil {
			continue
		}
		for _, block := range mlBlocks {
			px := defaults.BlockAverage(block)
			for ti, text := range mlTexts {
				for s := 0; s < samplesPerFont; s++ {
					size := 24.0 + float64((ti+s)%5)*4 // 24..40 pt spread
					img, _, rerr := r.Render(text, unpixel.Style{FontSize: size})
					if rerr != nil {
						continue
					}
					mosaic := px.Pixelate(imutil.ToRGBA(img), 0, 0)
					X = append(X, lumHist(mosaic, block))
					y = append(y, fi)
				}
			}
		}
	}
	return X, y
}

// fontModel is the trained classifier plus the class→font-name mapping.
type fontModel struct {
	clf   *linearml.Softmax
	names []string // class index → font name
}

// trainedModel caches the model trained on the bundled fonts. Training is
// deterministic and cheap (~a few hundred synthetic samples), so it runs once on
// first use rather than shipping embedded weights.
var trainedModel = sync.OnceValue(func() *fontModel {
	all := fonts.All()
	names := make([]string, len(all))
	for i, f := range all {
		names[i] = f.Name
	}
	X, y := genSamples(all)
	clf := linearml.Train(X, y, len(names), linearml.Options{Epochs: mlEpochs, LR: mlLR, L2: mlL2})
	return &fontModel{clf: clf, names: names}
})

// rankWithModel featurises img at blockSize and ranks fnts by the model's class
// probabilities (best-first: higher probability → lower Score). Fonts absent from
// the trained model fall to the back with Score 1.
func rankWithModel(img image.Image, blockSize int, fnts []fonts.Font) []Ranked {
	block := blockSize
	if block < 2 {
		if s := unpixel.InferBlockSize(imutil.ToRGBA(img)); s >= 2 {
			block = s
		} else {
			block = 8
		}
	}
	m := trainedModel()
	probs := m.clf.Predict(lumHist(imutil.ToRGBA(img), block))
	prob := make(map[string]float64, len(m.names))
	for c, name := range m.names {
		prob[name] = probs[c]
	}
	ranked := make([]Ranked, len(fnts))
	for i, f := range fnts {
		ranked[i] = Ranked{Name: f.Name, Score: 1 - prob[f.Name]}
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].Score != ranked[b].Score {
			return ranked[a].Score < ranked[b].Score
		}
		return strings.Compare(ranked[a].Name, ranked[b].Name) < 0
	})
	return ranked
}
