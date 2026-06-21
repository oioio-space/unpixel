// P6.4 whole-line re-ranking (DecodeLineWhole) and P6.5 font-family sweep
// (Recover / BundledRenderers).
//
// # Why per-word isolated scoring fails for whole-line bands
//
// When a line is pixelated as a whole, each word's band inside the result
// reflects its neighbours' ink contributions: the per-block average fuses
// pixels from adjacent glyphs.  Re-pixelating a candidate word in isolation
// gives a different image than the band extracted from the whole-line
// pixelation, so the image distance is noisy and the correct word often ranks
// below several wrong ones.  This is the documented P5.4 / P6.4 problem.
//
// # Solution: whole-line Cartesian-product scoring
//
// DecodeLineWhole avoids the broken per-word isolated scorer:
//
//  1. Segment the line into word sub-bands; estimate each band's rune count
//     from its width and the calibrated avgAdvance.
//  2. For each band, fetch the top-k dictionary words of matching rune length
//     (±1), ranked by language prior within each rune-length tier so that
//     words at per-tier rank ≤ k are always included regardless of
//     cross-tier prior competition.
//  3. Enumerate all combinations of the per-band pools; score each by
//     rendering the full joined text as ONE string, pixelating in one shot
//     (correct grid phase by construction), and comparing to the whole line
//     band.  Sort by image distance (ascending); return the BeamWidth×4
//     best candidates.
//
// # Font-family sweep (P6.5)
//
// Recover runs DecodeLineWhole for every NamedRenderer in the sweep set and
// picks the font whose mean per-line Dist is lowest.  BundledRenderers builds
// the sweep set from the fonts package, optionally filtered by Style.

package blinddecode

import (
	"cmp"
	"fmt"
	"image"
	"math"
	"slices"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/segment"
)

// LineCandidate is one scored hypothesis for a full line of text.
type LineCandidate struct {
	// Text is the recovered string (words joined by spaces).
	Text string
	// Dist is the whole-line image distance in [0,1] (lower = better match).
	Dist float64
	// Prior is the fused language-model score for Text (higher = more plausible).
	Prior float64
	// Cost is Alpha·Dist + Beta·(−Prior). Candidates are sorted ascending by Cost.
	Cost float64
}

// defaultBeamWidth is the beam width when Options.BeamWidth is zero.
const defaultBeamWidth = 8

// defaultPoolK is the per-word-band dictionary pool size used when Options.TopK
// is zero.  Larger values increase recall at the cost of a bigger Cartesian
// product; 30 gives good coverage for typical 3–6 letter words while keeping
// the adaptive-cap effective for lines with many words.
const defaultPoolK = 30

// DecodeLineWhole recovers a full line from its pixelated band.
//
// Algorithm (see package doc for rationale):
//  1. Segment line into word sub-bands; estimate each band's rune count from
//     width ÷ avgAdvance (±1 rune).
//  2. Build a per-band pool from the dictionary, capped at PoolK entries ranked
//     by language prior.
//  3. Enumerate all combinations of the per-band pools (up to BeamWidth×PoolK
//     candidates); score each by rendering the full joined text as ONE string,
//     pixelating in one shot (correct grid phase), and comparing to the whole
//     line band.  Fuse image distance and prior into Cost.
//  4. Return candidates sorted ascending by Cost (best first).
//
// The enumeration strategy avoids sequential-beam prefix-scoring, which gives
// ambiguous signal for short words and commits to wrong prefixes early.
// Instead every complete hypothesis is evaluated at full-line resolution where
// the discriminative SSIM signal lives.  wordPool builds up to k entries per
// rune-length tier (nEst-1, nEst, nEst+1), so the merged per-band pool is up
// to 3×k entries and the total combination count is (3×PoolK)^nWords; BeamWidth
// caps the output list but the inner loop is bounded by (3×PoolK)^nWords.  When
// the caller passes an explicit TopK > 0 (e.g. blind.Recover uses TopK=50), the
// adaptive maxCombinations cap is bypassed entirely and the per-tier cap is
// honoured as given — a known follow-up for very long lines.
//
// Returns nil when line contains no segmentable ink.
func (d *Decoder) DecodeLineWhole(line *image.RGBA) []LineCandidate {
	beamWidth := d.opts.BeamWidth
	if beamWidth <= 0 {
		beamWidth = defaultBeamWidth
	}
	// userTopK is the TopK the caller explicitly requested (>0) or 0 for default.
	userTopK := d.opts.TopK
	poolK := userTopK
	if poolK <= 0 {
		poolK = defaultPoolK
	}

	lineRects := segment.Lines(line)
	if len(lineRects) == 0 {
		return nil
	}
	wordRects := segment.Words(line, lineRects[0])
	if len(wordRects) == 0 {
		return nil
	}

	// Adaptive per-tier pool cap.
	//
	// wordPool builds up to k words per rune-length tier (nEst-1, nEst,
	// nEst+1) so the merged pool can be up to 3k entries.  The per-tier cap
	// k is what governs recall: TopK=50 guarantees words at within-tier rank
	// ≤50 are included (e.g. "cat" at rank 41 in 3-letter words).
	//
	// For the adaptive (TopK=0) path, we compute the per-tier k so the total
	// Cartesian-product size (≤ (3k)^nWords) stays ≤ maxCombinations, with a
	// floor of minEffectiveK to keep recall reasonable.
	//
	// When the caller passes an explicit TopK > 0 we honour it as the per-tier
	// cap and skip the combination-budget check entirely.
	nWords := len(wordRects)
	effectiveK := poolK
	if userTopK == 0 && nWords > 1 {
		const (
			maxCombinations = 500_000
			minEffectiveK   = 50 // includes rank-41 "cat" and rank-15 "chat"
		)
		// (3k)^nWords ≤ maxCombinations  →  k ≤ (maxCombinations^(1/nWords))/3
		kf := math.Pow(float64(maxCombinations), 1.0/float64(nWords)) / 3.0
		capped := max(minEffectiveK, int(kf))
		if capped < effectiveK {
			effectiveK = capped
		}
	}

	// Build per-band candidate pools using effectiveK as the per-tier cap.
	// wordPool returns up to 3×effectiveK merged words; no post-truncation is
	// applied so that per-tier rank is preserved across the merge.
	pools := make([][]string, nWords)
	for i, wr := range wordRects {
		pools[i] = d.wordPool(wr.Dx(), effectiveK)
	}

	alpha := d.opts.Alpha
	beta := d.opts.Beta

	// Iterative index-based Cartesian product (avoids deep recursion).
	sizes := make([]int, nWords)
	for i, p := range pools {
		sizes[i] = len(p)
	}
	indices := make([]int, nWords)
	words := make([]string, nWords) // reused across iterations

	var results []LineCandidate
	for {
		for i := range nWords {
			words[i] = pools[i][indices[i]]
		}
		text := strings.Join(words, " ")
		dist := d.scoreWholeLine(text, line)
		prior := d.opts.Prior(text)
		// Cost uses the caller's Beta for informational purposes, but the
		// final sort is by Dist alone: at whole-line resolution the image
		// signal is the dominant discriminator (correct phrase scores ≈0,
		// near-misses score 0.002–0.01) and prior weighting calibrated for
		// per-word scoring would incorrectly penalise low-frequency but
		// correct words (e.g. "dort", "cat").
		cost := alpha*dist + beta*(-prior)
		results = append(results, LineCandidate{
			Text:  text,
			Dist:  dist,
			Prior: prior,
			Cost:  cost,
		})

		// Advance indices (rightmost position increments first).
		carry := true
		for i := nWords - 1; i >= 0 && carry; i-- {
			indices[i]++
			if indices[i] < sizes[i] {
				carry = false
			} else {
				indices[i] = 0
			}
		}
		if carry {
			break
		}
	}

	// Sort by Dist (whole-line image distance) as the primary key; Cost as
	// tie-breaker so callers that want prior-fused ranking can use Cost.
	slices.SortFunc(results, func(a, b LineCandidate) int {
		if d := cmp.Compare(a.Dist, b.Dist); d != 0 {
			return d
		}
		return cmp.Compare(a.Cost, b.Cost)
	})
	// Cap output to keep callers' loops bounded.
	if len(results) > beamWidth*4 {
		results = results[:beamWidth*4]
	}
	return results
}

// wordPool returns up to k dictionary words per rune-length tier whose rune
// length matches the estimated count for a band of given pixel width, ranked
// descending by prior within each tier.
//
// Per-tier ranking (k per length, not k total) guarantees that a low-prior
// word at its correct rune-length rank r is included whenever k ≥ r. Mixing
// all rune lengths into one ranked list would let common short words (2-letter
// "in", "or" …) fill the quota and push out correct longer words. With
// separate tiers, TopK=50 reliably includes "cat" (rank 41 in 3-letter words).
//
// The returned slice is deduplicated; within each tier words appear in
// prior-descending order. No cross-tier sort is applied — the Cartesian
// product scores all combinations, so order has no correctness impact.
// Falls back to a "?" placeholder when the dictionary has no matches at any
// neighbouring rune length.
func (d *Decoder) wordPool(bandWidth, k int) []string {
	if d.avgAdvance <= 0 || bandWidth <= 0 {
		return []string{"?"}
	}
	nEst := int(math.Round(float64(bandWidth) / d.avgAdvance))
	if nEst < 1 {
		nEst = 1
	}

	// scored pairs a word with its pre-computed prior so each word is scored
	// exactly once per tier (not once per comparison in the sort).
	type scored struct {
		word  string
		score float64
	}

	seen := make(map[string]struct{})
	var words []string
	for delta := -1; delta <= 1; delta++ {
		n := nEst + delta
		if n < 1 {
			continue
		}
		// Clone the cached slice before sorting so the shared cache is not mutated.
		tier := slices.Clone(d.opts.Dict.ByRuneLen(n))
		// Score each candidate once, then sort by score descending.
		pairs := make([]scored, len(tier))
		for i, w := range tier {
			pairs[i] = scored{word: w, score: d.opts.Prior(w)}
		}
		slices.SortFunc(pairs, func(a, b scored) int {
			return cmp.Compare(b.score, a.score) // descending
		})
		for _, p := range pairs[:min(k, len(pairs))] {
			if _, dup := seen[p.word]; !dup {
				seen[p.word] = struct{}{}
				words = append(words, p.word)
			}
		}
	}
	if len(words) == 0 {
		return []string{"?"}
	}
	// No cross-tier sort: all combinations are scored exhaustively, so
	// iteration order within the pool has no correctness impact.
	return words
}

// scoreWholeLine renders text as a single string, pixelates the full result
// with the configured grid offset, and compares it to target.
//
// This is the key operation of DecodeLineWhole: the joined candidate is
// rendered and pixelated as one unit so the block grid aligns to the true
// glyph sequence — fixing the phase mismatch that per-word isolated scoring
// cannot avoid.
//
// Hot-path optimisations vs the naïve inkBounds approach:
//
//   - Y-crop uses the ink Y-range calibrated once at construction (d.inkY0,
//     d.inkH), eliminating the O(W×H) pixel scan that would otherwise run for
//     every combination in the Cartesian product.
//   - The pixelated candidate image is computed once and reused for both the
//     width-mismatch penalty and the metric comparison, avoiding a second
//     Pixelate call that scoreCandidate would otherwise introduce.
//
// Width mismatch handling: the correct candidate pixelates to exactly the same
// width as the target (same font, same block size, same pixelator). A candidate
// that pixelates wider than the target would be clipped during compositing,
// giving an artificially low distance. A fractional width-mismatch penalty
// |pw−tw|/tw avoids this by adding a cost proportional to the width error.
func (d *Decoder) scoreWholeLine(text string, target *image.RGBA) float64 {
	if text == "" || text == "?" {
		return 1.0
	}
	img, sx, err := d.opts.Renderer.Render(text, unpixel.Style{FontSize: d.opts.FontSize})
	if err != nil {
		return 1.0
	}
	if sx <= 0 {
		return 1.0
	}

	// Fast ink crop: use calibrated Y-range and a right-edge scan (O(imgH))
	// instead of the full O(W×H) inkBounds pixel scan.
	//
	// Y-bounds: calibrated inkY0 / inkH are valid for all renders at this
	// font/size (ascent and descent are font-level constants). When the phrase
	// has no descenders, inkH from the alphabet overestimates by a few rows,
	// but this only affects height — not the X boundary — and SSIM is not
	// sensitive to extra white rows at the bottom.
	//
	// X-bounds: scan from x = sx-1 leftward (O(imgH)) to find the actual ink
	// right edge.  Using sx directly introduces a 1-px ceiling rounding error
	// that shifts the pixelated block count by one block (e.g. 128→136 at
	// block=8), falsely triggering the width-mismatch penalty for the correct
	// phrase.
	y0 := d.inkY0
	h := d.inkH
	if h <= 0 {
		h = img.Bounds().Dy()
	}
	y1 := min(y0+h, img.Bounds().Dy())

	// Scan the left and right column edges (O(imgH) each) to find tight ink
	// X-boundaries. Using sx as-is introduces a 1-px ceiling rounding error;
	// some glyphs also have a left side-bearing that shifts the ink right of
	// x=0 (e.g. "le chat est" has x0=2). Both mismatches corrupt the
	// pixelated block grid and produce non-zero distance for the correct phrase.
	imgH := img.Bounds().Dy()
	isInk := func(x int) bool {
		for y := range imgH {
			c := img.RGBAAt(x, y)
			if (299*int(c.R)+587*int(c.G)+114*int(c.B))/1000 < 240 {
				return true
			}
		}
		return false
	}
	x0 := 0
	for x := range sx {
		if isInk(x) {
			x0 = x
			break
		}
	}
	x1 := 0
	for x := sx - 1; x >= x0; x-- {
		if isInk(x) {
			x1 = x + 1
			break
		}
	}
	if x1 <= x0 {
		return 1.0
	}

	cropRect := image.Rect(x0, y0, x1, y1)
	if cropRect.Empty() {
		return 1.0
	}
	cropped := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
	xdraw.Draw(cropped, cropped.Bounds(), img, cropRect.Min, xdraw.Src)

	// Pixelate once; reuse the result for both width measurement and scoring.
	pixelated := d.opts.Pixelator.Pixelate(cropped, d.opts.OffsetX, d.opts.OffsetY)
	pw := pixelated.Bounds().Dx()
	ph := pixelated.Bounds().Dy()
	tw := target.Bounds().Dx()
	th := target.Bounds().Dy()

	// Width-mismatch penalty: a 6-pixel error in a 128-pixel line (≈5%) costs
	// ≈0.024 — enough to rank below the correct candidate's dist≈0 while small
	// enough not to dominate for close near-misses where width is nearly equal.
	const widthPenaltyK = 0.5
	var widthPenalty float64
	if tw > 0 {
		widthPenalty = widthPenaltyK * float64(max(pw-tw, tw-pw)) / float64(tw)
	}

	// Composite pixelated candidate onto a white canvas of target size (same
	// logic as scoreCandidate, but without the second Pixelate call).
	canvas := image.NewRGBA(image.Rect(0, 0, tw, th))
	imutil.FillWhite(canvas)
	dy := (th - ph) / 2
	if dy < 0 {
		dy = 0
	}
	copyH := min(ph, th)
	copyW := min(pw, tw)
	xdraw.Draw(canvas, image.Rect(0, dy, copyW, dy+copyH), pixelated, pixelated.Bounds().Min, xdraw.Src)

	dist := d.opts.Metric.Compare(canvas, target)
	return dist + widthPenalty
}

// NamedRenderer pairs a human-readable font name with a Renderer.
type NamedRenderer struct {
	// Name is the font family identifier, e.g. "Liberation Sans".
	Name string
	// R is the Renderer for this font.
	R unpixel.Renderer
}

// ImageResult is the outcome of a font-sweep Recover call.
type ImageResult struct {
	// Text is the recovered text. When the image contains multiple lines they
	// are joined by newlines.
	Text string
	// Font is the Name of the NamedRenderer that produced the lowest mean Dist.
	Font string
	// Dist is the mean whole-line image distance for the winning font (lower = better).
	Dist float64
	// PerLine holds the top LineCandidate for each line in the winning font's pass.
	PerLine []LineCandidate
}

// Recover runs the full blind pipeline over a redaction image, sweeping the
// given font renderers.
//
// For each renderer in renderers it:
//  1. Builds a Decoder with that renderer (all other opts fields unchanged).
//  2. Segments img into lines via segment.Lines.
//  3. Calls DecodeLineWhole on each line band.
//  4. Accumulates the top LineCandidate per line and computes the mean Dist.
//
// The font with the lowest mean Dist wins. Block size, grid offset, font size,
// and language prior come from opts; renderers is the sweep set (typically
// built with BundledRenderers).
//
// Recover panics if renderers is empty.
func Recover(img *image.RGBA, opts Options, renderers []NamedRenderer) ImageResult {
	if len(renderers) == 0 {
		panic("blinddecode.Recover: renderers must not be empty")
	}

	type candidate struct {
		name     string
		meanDist float64
		perLine  []LineCandidate
	}

	best := candidate{meanDist: 2.0} // sentinel above any real [0,1] distance

	for _, nr := range renderers {
		o := opts
		o.Renderer = nr.R

		d := New(o)

		lineRects := segment.Lines(img)
		var perLine []LineCandidate
		var sumDist float64
		ib := img.Bounds()

		for _, lr := range lineRects {
			// Use the full image width, not lr.Dx(): segment.Lines returns
			// ink-cropped rects and using lr.Dx() cuts trailing white blocks,
			// corrupting word-band widths in DecodeLineWhole.
			lw := ib.Dx()
			lh := lr.Dy()
			if lw == 0 || lh == 0 {
				continue
			}
			band := image.NewRGBA(image.Rect(0, 0, lw, lh))
			imutil.FillWhite(band)
			srcRect := image.Rect(ib.Min.X, lr.Min.Y, ib.Max.X, lr.Max.Y).Intersect(ib)
			if !srcRect.Empty() {
				xdraw.Draw(band, image.Rect(0, 0, srcRect.Dx(), srcRect.Dy()), img, srcRect.Min, xdraw.Src)
			}
			cands := d.DecodeLineWhole(band)
			if len(cands) == 0 {
				continue
			}
			top := cands[0]
			perLine = append(perLine, top)
			sumDist += top.Dist
		}

		meanDist := sumDist
		if len(perLine) > 0 {
			meanDist = sumDist / float64(len(perLine))
		}
		if meanDist < best.meanDist {
			best = candidate{name: nr.Name, meanDist: meanDist, perLine: perLine}
		}
	}

	texts := make([]string, len(best.perLine))
	for i, lc := range best.perLine {
		texts[i] = lc.Text
	}
	return ImageResult{
		Text:    strings.Join(texts, "\n"),
		Font:    best.name,
		Dist:    best.meanDist,
		PerLine: best.perLine,
	}
}

// BundledRenderers builds NamedRenderers from the bundled font families,
// filtered by Style when styles is non-empty.
//
// Recognised style values: "sans", "serif", "mono" (matching fonts.Font.Style).
// Pass no arguments to include all bundled fonts.
//
// Defaulting to "sans" covers Liberation Sans and Carlito (≈Arial / ≈Calibri),
// the two most common redaction faces, with only 2 renders per candidate
// instead of 9.  Callers needing broader coverage can pass multiple styles or
// no filter.
//
// Returns an error only if a bundled font fails to parse (indicates a corrupt
// build).
func BundledRenderers(styles ...string) ([]NamedRenderer, error) {
	styleSet := make(map[string]struct{}, len(styles))
	for _, s := range styles {
		styleSet[s] = struct{}{}
	}

	all := fonts.All()
	out := make([]NamedRenderer, 0, len(all))
	for _, f := range all {
		if len(styleSet) > 0 {
			if _, ok := styleSet[f.Style]; !ok {
				continue
			}
		}
		r, err := defaults.RendererFromFonts(f.Data, nil)
		if err != nil {
			return nil, fmt.Errorf("blinddecode: renderer for %s: %w", f.Name, err)
		}
		out = append(out, NamedRenderer{Name: f.Name, R: r})
	}
	return out, nil
}
