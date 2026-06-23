// Package capacity measures the information-theoretic recoverability of a
// pixelated-text image for a given rendering geometry (font size, block size,
// grid phase).
//
// # Rationale
//
// The mosaic forward operator A (box-average over b×b blocks) is rank-deficient:
// each block collapses b² pixel values down to a single mean, so all within-block
// detail is information-theoretically destroyed. For a given (font, fontSize,
// block, phase), many glyphs collapse to the same block-average signature and
// become indistinguishable to any decoder. Measuring this per position tells us:
//
//   - which positions are recoverable at all,
//   - the surviving information: log₂(#distinguishable classes) bits per glyph,
//   - and which images are even winnable before committing search budget.
//
// [Analyze] renders every charset glyph with the provided [unpixel.Renderer],
// pixelates the result at the given block size and phase, extracts the
// per-block mean signature, and clusters identical (or near-identical) signatures
// into equivalence classes using an L2 tolerance τ. Glyphs within τ of each
// other are indistinguishable after mosaicing and land in the same class.
package capacity

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"math"
	"slices"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// GlyphClass groups charset glyphs that collapse to an indistinguishable
// pixelated block-signature at a given geometry. Members is sorted by rune
// value for determinism.
type GlyphClass struct {
	// Members holds the runes that produce identical (within tolerance τ)
	// block-average signatures after pixelation at the analysis geometry.
	Members []rune
	// Centroid is the mean signature vector of the class, computed as the
	// per-element average of all member signatures. It can serve as a compact
	// reference for fast nearest-class lookup during decoding.
	Centroid []float64
}

// Capacity summarises the recoverability of glyph positions for one geometry.
// It is the primary output of [Analyze].
type Capacity struct {
	// Block is the pixelation block side length in pixels.
	Block int
	// FontSize is the font size in points used for glyph rendering.
	FontSize float64
	// Phase is the grid origin (sub-block offset) applied during pixelation.
	Phase image.Point

	// Classes is the set of glyph equivalence classes: glyphs within the same
	// class are indistinguishable from each other after mosaicing. The slice is
	// sorted by the rune value of the first member for determinism.
	Classes []GlyphClass

	// BitsPerGlyph is log₂(len(Classes)) — the surviving information per glyph
	// position. A value near log₂(|charset|) means the geometry is highly
	// recoverable; a value near 0 means all glyphs collapsed to one class and
	// recovery is impossible.
	BitsPerGlyph float64

	// Confusable maps each glyph to the other glyphs it is indistinguishable
	// from (i.e. the other members of its equivalence class). A glyph that is
	// perfectly distinguishable has no entry (or an empty slice). The map is
	// nil when every glyph is in its own class.
	Confusable map[rune][]rune
}

// Option configures [Analyze].
type Option func(*options)

type options struct {
	// tau is the L2 distance tolerance below which two normalised signatures
	// are considered identical (same equivalence class). Default: 0.05.
	tau float64
	// linear selects linear-light block averaging (GEGL/GIMP/browser behaviour)
	// rather than sRGB averaging (the default unredacter behaviour).
	linear bool
}

func defaultOptions() options {
	return options{tau: 0.05}
}

// WithTolerance sets the L2 distance threshold τ used to decide whether two
// glyph signatures are indistinguishable. Signatures whose L2 distance is
// below τ are merged into one equivalence class. The default 0.05 is tuned so
// that pixel-identical renders always merge while clearly distinct glyphs
// (e.g. 'i' vs 'm' at fine block sizes) stay separate.
func WithTolerance(tau float64) Option {
	return func(o *options) { o.tau = tau }
}

// WithLinear selects linear-light block averaging, matching GEGL-based tools
// such as GIMP's Pixelize filter. The default is sRGB averaging, which matches
// the original unredacter/Jimp pipeline.
func WithLinear() Option {
	return func(o *options) { o.linear = true }
}

// Analyze renders each rune in charset with r at the given fontSize, pixelates
// at block aligned to phase, extracts the per-block mean signature, and
// clusters signatures into equivalence classes. Two glyphs are in the same
// class when their normalised signature L2 distance is below τ (configurable
// via [WithTolerance]; default 0.05).
//
// It returns a [Capacity] describing the number of distinguishable classes and
// the surviving information in bits per glyph position. The result is fully
// deterministic for equal inputs.
//
// ctx is checked after each glyph render; a cancelled context returns
// ctx.Err() wrapped in a descriptive error.
func Analyze(ctx context.Context, r unpixel.Renderer, charset string, fontSize float64, block int, phase image.Point, opts ...Option) (Capacity, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	var pix *pixelate.BlockAverage
	if o.linear {
		pix = pixelate.NewLinearBlockAverage(block)
	} else {
		pix = pixelate.NewBlockAverage(block)
	}

	style := unpixel.Style{
		FontSize:    fontSize,
		PaddingTop:  0,
		PaddingLeft: 0,
	}

	runes := []rune(charset)
	sigs := make([][]float64, len(runes))

	for i, ch := range runes {
		if err := ctx.Err(); err != nil {
			return Capacity{}, fmt.Errorf("capacity.Analyze cancelled at glyph %q: %w", ch, err)
		}

		img, _, err := r.Render(string(ch), style)
		if err != nil {
			return Capacity{}, fmt.Errorf("capacity.Analyze: render %q: %w", ch, err)
		}

		mosaiced := pix.Pixelate(img, phase.X, phase.Y)
		sigs[i] = extractSignature(mosaiced, block)
	}

	classes := cluster(runes, sigs, o.tau)

	// Sort classes by first member for determinism.
	slices.SortFunc(classes, func(a, b GlyphClass) int {
		return cmp.Compare(a.Members[0], b.Members[0])
	})

	var bitsPerGlyph float64
	if n := len(classes); n > 1 {
		bitsPerGlyph = math.Log2(float64(n))
	}

	confusable := buildConfusable(classes)

	return Capacity{
		Block:        block,
		FontSize:     fontSize,
		Phase:        phase,
		Classes:      classes,
		BitsPerGlyph: bitsPerGlyph,
		Confusable:   confusable,
	}, nil
}

// extractSignature extracts a flat float64 vector of per-block mean values
// from a pixelated image. Each block contributes three values (R, G, B mean
// in [0,1]). The vector length is 3 × (number of distinct block cells in the
// image), where cells are identified by their top-left corner at multiples of
// block. The vector is normalised to unit L2 length before return so that
// comparisons are scale-independent.
func extractSignature(img *image.RGBA, block int) []float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	// Count blocks.
	cols := (w + block - 1) / block
	rows := (h + block - 1) / block
	sig := make([]float64, 0, cols*rows*3)

	for row := range rows {
		y := b.Min.Y + row*block
		x := b.Min.X // start of this block row
		// Sample the top-left pixel of each block; all pixels in the block have
		// been set to the block mean by Pixelate, so any pixel suffices.
		for col := range cols {
			px := x + col*block
			if px >= b.Max.X || y >= b.Max.Y {
				sig = append(sig, 0, 0, 0)
				continue
			}
			c := img.RGBAAt(px, y)
			sig = append(
				sig,
				float64(c.R)/255,
				float64(c.G)/255,
				float64(c.B)/255,
			)
		}
	}

	normalise(sig)
	return sig
}

// normalise scales sig to unit L2 length in place. A zero vector is left as-is.
func normalise(sig []float64) {
	var sum float64
	for _, v := range sig {
		sum += v * v
	}
	if sum == 0 {
		return
	}
	inv := 1 / math.Sqrt(sum)
	for i := range sig {
		sig[i] *= inv
	}
}

// l2Dist returns the L2 distance between two vectors. If the vectors differ
// in length the shorter one is zero-padded for comparison.
func l2Dist(a, b []float64) float64 {
	n := max(len(a), len(b))
	var sum float64
	for i := range n {
		var va, vb float64
		if i < len(a) {
			va = a[i]
		}
		if i < len(b) {
			vb = b[i]
		}
		d := va - vb
		sum += d * d
	}
	return math.Sqrt(sum)
}

// cluster groups runes into equivalence classes by comparing their signatures
// with L2 tolerance tau using a greedy nearest-centroid strategy: each rune is
// assigned to the first existing class whose centroid is within tau, or starts
// a new class. The greedy approach is O(n²) but n is at most ~96 (printable
// ASCII) and is called once (off the hot path), so it is acceptable.
//
// Signatures may differ in length because different glyphs render to different
// image widths. l2Dist zero-pads the shorter vector, and centroids are grown
// to accommodate the longest member seen so far.
func cluster(runes []rune, sigs [][]float64, tau float64) []GlyphClass {
	// centroids[i] is the running centroid of class i (zero-padded to the
	// length of the longest member signature seen so far).
	var centroids [][]float64
	var classes []GlyphClass

	for i, ch := range runes {
		sig := sigs[i]
		assigned := false
		for j, centroid := range centroids {
			if l2Dist(sig, centroid) < tau {
				classes[j].Members = append(classes[j].Members, ch)
				centroids[j] = onlineMean(centroid, sig)
				assigned = true
				break
			}
		}
		if !assigned {
			c := slices.Clone(sig)
			centroids = append(centroids, c)
			classes = append(classes, GlyphClass{
				Members:  []rune{ch},
				Centroid: c,
			})
		}
	}

	// Build rune→sig index once for the exact centroid recomputation below.
	runeIdx := make(map[rune]int, len(runes))
	for i, r := range runes {
		runeIdx[r] = i
	}

	// Sort members within each class for determinism and recompute exact centroid.
	for i := range classes {
		slices.Sort(classes[i].Members)
		classes[i].Centroid = recomputeCentroid(classes[i].Members, runeIdx, sigs)
	}

	return classes
}

// onlineMean returns a new vector that is the element-wise mean of a and b,
// zero-padding the shorter one so the result has length max(len(a), len(b)).
// This approximation is replaced by an exact recomputation after clustering.
func onlineMean(a, b []float64) []float64 {
	n := max(len(a), len(b))
	out := make([]float64, n)
	for i := range n {
		var va, vb float64
		if i < len(a) {
			va = a[i]
		}
		if i < len(b) {
			vb = b[i]
		}
		out[i] = (va + vb) / 2
	}
	return out
}

// recomputeCentroid returns the exact mean signature for the given members,
// zero-padding shorter signatures to the length of the longest one.
func recomputeCentroid(members []rune, runeIdx map[rune]int, allSigs [][]float64) []float64 {
	if len(members) == 0 {
		return nil
	}
	maxLen := 0
	for _, m := range members {
		if i, ok := runeIdx[m]; ok {
			maxLen = max(maxLen, len(allSigs[i]))
		}
	}
	centroid := make([]float64, maxLen)
	for _, m := range members {
		i, ok := runeIdx[m]
		if !ok {
			continue
		}
		for j, v := range allSigs[i] {
			centroid[j] += v
		}
	}
	n := float64(len(members))
	for i := range centroid {
		centroid[i] /= n
	}
	return centroid
}

// buildConfusable constructs the Confusable map from the equivalence classes.
// It returns nil when every class has exactly one member (no confusion).
func buildConfusable(classes []GlyphClass) map[rune][]rune {
	// Check whether any class has multiple members.
	hasConfusion := false
	for _, cls := range classes {
		if len(cls.Members) > 1 {
			hasConfusion = true
			break
		}
	}
	if !hasConfusion {
		return nil
	}

	m := make(map[rune][]rune)
	for _, cls := range classes {
		if len(cls.Members) < 2 {
			continue
		}
		for _, ch := range cls.Members {
			others := make([]rune, 0, len(cls.Members)-1)
			for _, other := range cls.Members {
				if other != ch {
					others = append(others, other)
				}
			}
			m[ch] = others
		}
	}
	return m
}
