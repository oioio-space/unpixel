//go:build ml

package rerank

// emission.go — the trained glyph-emission reranker behind the //go:build ml seam.
// It is the discriminative half of the ML tier: where the pure-Go Linguistic
// reranker blends only a language prior, this scores each candidate against the
// IMAGE via a learned per-glyph emission model P(char | tile) — the lever that can
// separate confusable homoglyphs a whole-string physical distance ties.
//
// Self-contained pure Go (no CGO, no framework, no embedded weights): the renderer
// labels the data, so the model trains itself once on synthetic render→pixelate
// glyph tiles of the bundled fonts, then does a linearml softmax forward pass. The
// tile feature is spatial (a fixed emGrid×emGrid map of block-mean luminances over
// the glyph's ink box), so it captures glyph SHAPE — the signal that survives
// coarse pixelation and distinguishes 0/O, r/n, T/X — not just ink density.
//
// Segmentation is monospace-style (candidate split into equal-width columns); it is
// most reliable on fixed-advance redactions (the sick digit/token corpus). The
// emission log-likelihood and language prior are fused into Ranked.Blended (the
// ordering key) while Ranked.Distance stays the untouched physical value; weight
// controls how strongly the image can reorder the physical ranking — enough to flip
// a confusable tie, which is the whole point of a discriminative reranker.

import (
	"context"
	"image"
	"math"
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
	emGrid   = 6   // glyph tile is emGrid×emGrid mean-luminance cells → emGrid² features
	emBlock  = 6   // block size the emission tiles are rendered/pixelated at
	emEpochs = 250 // softmax training passes
	// emLLFloor is the worst (most negative) per-glyph mean log-likelihood: both an
	// out-of-charset char and a floored in-charset probability clamp here, so the
	// emission score stays in [emLLFloor, 0] and the blend normalisation below is
	// consistent with the floor.
	emLLFloor = -8.0
)

// emCharset is the alphabet the emission model classifies. It spans the sick and
// context corpora (letters, digits, and the leetspeak substitutions that form the
// hard homoglyph ties).
const emCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#"

// emSizes are the font sizes trained over so the tile feature generalises across
// the redaction's (unknown) rendered size.
var emSizes = []float64{26, 30, 34}

// tileFeature resamples the ink region of a pixelated glyph image into an
// emGrid×emGrid map of mean luminances in [0,1] (1 = white). It returns a zero
// vector for a blank image. The fixed grid makes glyphs of different pixel sizes
// comparable — the spatial signature the emission model learns.
func tileFeature(img *image.RGBA) []float64 {
	b := img.Bounds()
	// Ink bounding box (any non-near-white pixel).
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			lum := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
			if lum < 235 {
				minX, minY = min(minX, x), min(minY, y)
				maxX, maxY = max(maxX, x), max(maxY, y)
			}
		}
	}
	feat := make([]float64, emGrid*emGrid)
	if maxX < minX || maxY < minY {
		for i := range feat {
			feat[i] = 1 // blank → all white
		}
		return feat
	}
	w, h := maxX-minX+1, maxY-minY+1
	for gy := range emGrid {
		for gx := range emGrid {
			x0 := minX + gx*w/emGrid
			x1 := minX + (gx+1)*w/emGrid
			y0 := minY + gy*h/emGrid
			y1 := minY + (gy+1)*h/emGrid
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var sum, n float64
			for y := y0; y < y1 && y < b.Max.Y; y++ {
				for x := x0; x < x1 && x < b.Max.X; x++ {
					c := img.RGBAAt(x, y)
					sum += float64(299*int(c.R)+587*int(c.G)+114*int(c.B)) / 1000
					n++
				}
			}
			if n > 0 {
				feat[gy*emGrid+gx] = sum / n / 255
			} else {
				feat[gy*emGrid+gx] = 1
			}
		}
	}
	return feat
}

// glyphTile renders one rune in the given font at size, pixelates at emBlock, and
// returns its tile feature. ok is false if the rune did not render.
func glyphTile(fontData []byte, ch rune, size float64) ([]float64, bool) {
	rend, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		return nil, false
	}
	img, _, rerr := rend.Render(string(ch), unpixel.Style{FontSize: size})
	if rerr != nil {
		return nil, false
	}
	mosaic := defaults.BlockAverage(emBlock).Pixelate(imutil.ToRGBA(img), 0, 0)
	return tileFeature(mosaic), true
}

// emissionModel is the trained glyph classifier plus its class→rune mapping.
type emissionModel struct {
	clf   *linearml.Softmax
	chars []rune
	index map[rune]int
}

// logProb returns log P(ch | feat) under the model, clamped to [emLLFloor, 0]. An
// out-of-charset rune and a vanishing in-charset probability both floor at emLLFloor.
func (m *emissionModel) logProb(feat []float64, ch rune) float64 {
	ci, ok := m.index[ch]
	if !ok {
		return emLLFloor
	}
	p := m.clf.Predict(feat)[ci]
	return max(math.Log(p), emLLFloor)
}

// trainedEmission caches the glyph-emission model trained on the bundled fonts.
var trainedEmission = sync.OnceValue(func() *emissionModel {
	chars := []rune(emCharset)
	index := make(map[rune]int, len(chars))
	for i, c := range chars {
		index[c] = i
	}
	var samples [][]float64
	var labels []int
	for _, f := range fonts.All() {
		for ci, ch := range chars {
			for _, size := range emSizes {
				if feat, ok := glyphTile(f.Data, ch, size); ok {
					samples = append(samples, feat)
					labels = append(labels, ci)
				}
			}
		}
	}
	clf := linearml.Train(samples, labels, len(chars), linearml.Options{Epochs: emEpochs})
	return &emissionModel{clf: clf, chars: chars, index: index}
})

// emissionLogLik scores candidate text against the redaction img: it splits img
// into len(text) equal-width column tiles (monospace segmentation) and returns the
// mean per-glyph log P(char | tile). Higher (less negative) is a better fit.
func emissionLogLik(img image.Image, text string) float64 {
	runes := []rune(text)
	if len(runes) == 0 {
		return emLLFloor
	}
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w := b.Dx()
	m := trainedEmission()
	var sum float64
	for i, ch := range runes {
		x0 := b.Min.X + i*w/len(runes)
		x1 := b.Min.X + (i+1)*w/len(runes)
		if x1 <= x0 {
			x1 = x0 + 1
		}
		col := imutil.Crop(rgba, x0, b.Min.Y, x1-x0, b.Dy())
		sum += m.logProb(tileFeature(col), ch)
	}
	return sum / float64(len(runes))
}

// Rerank orders candidates by a Blended key that fuses each verdict's physical
// distance with the image emission log-likelihood and the language prior; it keeps
// Distance as the untouched physical value (so downstream Match/Margin stay
// physical) and returns the candidates ascending by Blended. weight scales the
// discriminative tie-break — large weight lets the image override the physical order
// (the intended behaviour for confusable ties), so it is a reranker, not a mere
// re-sort. It returns nil, nil for no verdicts.
func (ctcReranker) Rerank(ctx context.Context, img image.Image, verdicts []unpixel.Verdict, lm func(string) float64, weight float64) ([]Ranked, error) {
	if len(verdicts) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ranked := make([]Ranked, len(verdicts))
	for i, v := range verdicts {
		blended := v.Distance
		var lmScore float64
		if weight > 0 {
			if img != nil {
				// Emission LL ∈ [emLLFloor, 0]; normalise to [0,1] (1 = best fit) so a
				// better image match lowers the blended key.
				blended -= weight * (emissionLogLik(img, v.Text) - emLLFloor) / -emLLFloor
			}
			if lm != nil {
				lmScore = lm(v.Text)
				blended -= 0.1 * weight * lmScore
			}
		}
		ranked[i] = Ranked{Text: v.Text, Distance: v.Distance, LMScore: lmScore, Blended: blended}
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].Blended != ranked[b].Blended {
			return ranked[a].Blended < ranked[b].Blended
		}
		if ranked[a].Distance != ranked[b].Distance {
			return ranked[a].Distance < ranked[b].Distance
		}
		return strings.Compare(ranked[a].Text, ranked[b].Text) < 0
	})
	return ranked, nil
}
