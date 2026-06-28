package mosaictext

import (
	"cmp"
	"errors"
	"image"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"

	xdraw "golang.org/x/image/draw"
)

// toRGBA returns src as *image.RGBA, delegating to imutil.ToRGBA.
// Kept package-private so the many mosaictext callers need no import change.
func toRGBA(src image.Image) *image.RGBA { return imutil.ToRGBA(src) }

// contentLumThreshold is the luminance below which a pixel is considered
// non-background content (ink is darker than this on a white background).
const contentLumThreshold = 244

// inkLumThreshold is the luminance below which a pixel is considered inked in a
// freshly rendered glyph image.
const inkLumThreshold = 240

// ErrNoMosaic is returned when no mosaic block grid can be detected in the image.
var ErrNoMosaic = errors.New("mosaictext: no mosaic block grid detected")

// ErrNoContent is returned when the image has no redacted (non-background) content.
var ErrNoContent = errors.New("mosaictext: no redacted content found")

// phase finds the grid phase for an n-character line at the given tracking by a
// full sweep: at each candidate phase it does a one-pass greedy reconstruction
// and keeps the phase whose reconstruction has the lowest MSE. It returns the
// phase and that MSE (a cheap proxy for how well this calibration fits).
func (d *decoder) phase(stretch float64, n int) (pox int, mse float64) {
	d.cache = newRenderCache(d.cacheCap)
	// Release the phase-1 memo before returning: Decode retains all 18 decoders in
	// combos through phase 2, and holding their caches simultaneously is what
	// summed to ~27 GB. Phase 2 rebuilds the cache for the single winning decoder.
	defer func() { d.cache = nil }()
	charset := []rune(defaultCharset)
	mse = math.Inf(1)
	for px := 0; px < d.block; px += 2 {
		s := d.greedyN(stretch, n, px, charset, 1)
		if dd := d.dist(s, d.fs, stretch, px); dd < mse {
			mse, pox = dd, px
		}
	}
	return pox, mse
}

// decode reconstructs the n-character line at a phase near px0 (refined ±4),
// then disambiguates with the language prior. It returns the text, its objective
// score (MSE tempered by the prior; lower is better, comparable across fonts),
// the raw MSE, and the phase used.
func (d *decoder) decode(n, px0 int, stretch float64) (text string, obj, mse float64, pox int) {
	d.cache = newRenderCache(d.cacheCap)
	charset := []rune(defaultCharset)

	// Refine the phase locally with a two-pass greedy.
	pox, best := px0, math.Inf(1)
	for px := max(px0-4, 0); px <= min(px0+4, d.block-1); px++ {
		s := d.greedyN(stretch, n, px, charset, 2)
		if dd := d.dist(s, d.fs, stretch, px); dd < best {
			best, pox = dd, px
		}
	}
	seedTxt := d.greedyN(stretch, n, pox, charset, 2)

	// Per-cell confusion sets (top-K by MSE) → beam over the lowest cumulative MSE.
	cands := d.confusion(seedTxt, d.fs, stretch, pox, charset, 6)
	beam := beamMSE(cands, 150)

	// Rerank the survivors by MSE tempered with a language prior: dictionary words
	// and a sentence-terminal-punctuation convention break the residual ties
	// (e.g. 'l' vs 'I', '!' vs '-') that raw MSE cannot.
	type scored struct {
		text     string
		obj, mse float64
	}
	var ranked []scored
	for i, s := range beam {
		if i >= 24 {
			break
		}
		m := d.dist(s, d.fs, stretch, pox)
		o := m - wordBonus*float64(dictWords(s))
		if terminalPunct(s) {
			o -= terminalBonus
		}
		ranked = append(ranked, scored{s, o, m})
	}
	if len(ranked) == 0 {
		return strings.TrimRight(seedTxt, " "), best, best, pox
	}
	slices.SortFunc(ranked, func(a, b scored) int { return cmp.Compare(a.obj, b.obj) })
	return strings.TrimRight(ranked[0].text, " "), ranked[0].obj, ranked[0].mse, pox
}

// Priors weighting the language model against the image MSE in decodeN's rerank.
// wordBonus rewards each dictionary word; terminalBonus rewards a line ending in
// sentence-terminal punctuation. Both are small relative to the MSE gap of a
// genuinely wrong glyph, so they only arbitrate near-ties.
const (
	wordBonus     = 25.0
	terminalBonus = 12.0
)

// greedyN reconstructs n cells left-to-right, picking at each cell the character
// minimizing the whole-image MSE (others held at a neutral filler), for a fixed
// grid phase. Extra passes let later context refine earlier cells.
func (d *decoder) greedyN(stretch float64, n, pox int, charset []rune, passes int) string {
	fs := d.fs
	cur := []rune(strings.Repeat("o", n))
	for pass := 0; pass < passes; pass++ {
		for i := range cur {
			bestC, bestD := cur[i], d.dist(string(cur), fs, stretch, pox)
			for _, ch := range charset {
				old := cur[i]
				cur[i] = ch
				if dd := d.dist(string(cur), fs, stretch, pox); dd < bestD-1e-9 {
					bestD, bestC = dd, ch
				} else {
					cur[i] = old
				}
			}
			cur[i] = bestC
		}
	}
	return string(cur)
}

// cellCand is a candidate character for one cell with its whole-image MSE cost.
type cellCand struct {
	ch   rune
	cost float64
}

// confusion returns, per cell, the top-k characters by whole-image MSE (others
// held at the seed) with their costs. Under MSE on monospace cells these sets
// reliably contain the true character even when greedy picked a near-miss.
func (d *decoder) confusion(seed string, fs, stretch float64, pox int, charset []rune, k int) [][]cellCand {
	cur := []rune(seed)
	out := make([][]cellCand, len(cur))
	for i := range cur {
		cds := make([]cellCand, 0, len(charset))
		for _, ch := range charset {
			old := cur[i]
			cur[i] = ch
			cds = append(cds, cellCand{ch, d.dist(string(cur), fs, stretch, pox)})
			cur[i] = old
		}
		slices.SortFunc(cds, func(a, b cellCand) int { return cmp.Compare(a.cost, b.cost) })
		if len(cds) > k {
			cds = cds[:k]
		}
		out[i] = cds
	}
	return out
}

// beamMSE beam-searches the confusion sets, keeping the width lowest-cumulative-
// MSE prefixes (cells are near-independent under MSE, so the sum approximates the
// whole-image MSE and the true string survives even if it is not the greedy pick).
// Returned strings are ordered best-first by cumulative cost.
func beamMSE(cands [][]cellCand, width int) []string {
	type node struct {
		s   string
		mse float64
	}
	beam := []node{{"", 0}}
	for i := range cands {
		next := make([]node, 0, len(beam)*len(cands[i]))
		for _, b := range beam {
			for _, c := range cands[i] {
				next = append(next, node{b.s + string(c.ch), b.mse + c.cost})
			}
		}
		slices.SortFunc(next, func(a, b node) int { return cmp.Compare(a.mse, b.mse) })
		if len(next) > width {
			next = next[:width]
		}
		beam = next
	}
	out := make([]string, len(beam))
	for i, b := range beam {
		out[i] = b.s
	}
	return out
}

// --- rendering & scoring ---

// stretched renders text at fs, crops to ink, and stretches horizontally by the
// tracking factor — the pre-pixelation candidate.
func (d *decoder) stretched(text string, fs, stretch float64) *image.RGBA {
	if d.cache != nil {
		if c, ok := d.cache.get(text); ok {
			return c
		}
	}
	st := d.renderStretched(text, fs, stretch)
	if d.cache != nil {
		d.cache.put(text, st)
	}
	return st
}

// renderStretched does the uncached render+crop+resample.
func (d *decoder) renderStretched(text string, fs, stretch float64) *image.RGBA {
	img, sx, err := d.r.Render(text, unpixel.Style{FontSize: fs})
	if err != nil || sx <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	bb := inkBounds(img, sx)
	ink := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	xdraw.Draw(ink, ink.Bounds(), img, bb.Min, xdraw.Src)
	nw := int(float64(bb.Dx()) * stretch)
	if nw < 1 {
		nw = 1
	}
	st := image.NewRGBA(image.Rect(0, 0, nw, bb.Dy()))
	// CatmullRom is load-bearing for decode accuracy: ApproxBiLinear (tried for a
	// ~44% speedup) changed the stretched render just enough to flip the real
	// hello-world decode off "Hello World !" — an exact-match regression caught by
	// TestDecode_HelloWorld (panel fixtures use unit stretch so they didn't catch
	// it). The kernel quality matters here; keep CatmullRom.
	xdraw.CatmullRom.Scale(st, st.Bounds(), ink, ink.Bounds(), xdraw.Over, nil)
	return st
}

// placed pixelates the stretched candidate at grid phase (pox,poy) and composites
// it onto a target-sized white canvas at (ox,oy).
func (d *decoder) placed(st *image.RGBA, pox, poy, ox, oy int) *image.RGBA {
	p := image.NewRGBA(image.Rect(0, 0, st.Bounds().Dx()+pox, st.Bounds().Dy()+poy))
	imutil.FillWhite(p)
	xdraw.Draw(p, image.Rect(pox, poy, pox+st.Bounds().Dx(), poy+st.Bounds().Dy()), st, st.Bounds().Min, xdraw.Src)
	rp := d.pixelate.Pixelate(p, 0, 0)
	rw, rh := rp.Bounds().Dx(), rp.Bounds().Dy()
	c := image.NewRGBA(image.Rect(0, 0, d.target.Bounds().Dx(), d.target.Bounds().Dy()))
	imutil.FillWhite(c)
	if ox+rw <= c.Bounds().Dx() && oy+rh <= c.Bounds().Dy() {
		xdraw.Draw(c, image.Rect(ox, oy, ox+rw, oy+rh), rp, rp.Bounds().Min, xdraw.Src)
	}
	return c
}

// dist is the whole-image block-value MSE at a fixed horizontal phase (the fast
// path used during the per-cell sweeps).
func (d *decoder) dist(text string, fs, stretch float64, pox int) float64 {
	return mseRGB(d.placed(d.stretched(text, fs, stretch), pox, 0, 0, 0), d.target)
}

// --- language priors ---

// dictWords counts the space-separated tokens of s that are dictionary words.
func dictWords(s string) int {
	d := lang.Dictionary()
	n := 0
	for _, w := range strings.Fields(s) {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) })
		if len(w) >= 2 && d.Contains(strings.ToLower(w)) {
			n++
		}
	}
	return n
}

// terminalPunct reports whether s ends in sentence-terminal punctuation, the
// typographic convention that resolves a faint trailing mark to '!'/'.'/'?'.
func terminalPunct(s string) bool {
	s = strings.TrimRight(s, " ")
	if s == "" {
		return false
	}
	switch s[len(s)-1] {
	case '!', '.', '?':
		return true
	}
	return false
}

// --- image helpers ---

// downscaleBox shrinks img by an integer factor f with an exact f×f box average,
// so each output pixel is the mean of its source block. This preserves block
// averages (the decode objective) far better than a resampling kernel, which is
// why the scoring pipeline can run at reduced resolution without shifting the MSE.
func downscaleBox(img *image.RGBA, f int) *image.RGBA {
	b := img.Bounds()
	ow, oh := b.Dx()/f, b.Dy()/f
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	area := f * f
	for oy := range oh {
		for ox := range ow {
			var r, g, bl, a int
			for dy := range f {
				for dx := range f {
					c := img.RGBAAt(ox*f+dx, oy*f+dy)
					r += int(c.R)
					g += int(c.G)
					bl += int(c.B)
					a += int(c.A)
				}
			}
			i := dst.PixOffset(ox, oy)
			dst.Pix[i+0] = mean8(r, area)
			dst.Pix[i+1] = mean8(g, area)
			dst.Pix[i+2] = mean8(bl, area)
			dst.Pix[i+3] = mean8(a, area)
		}
	}
	return dst
}

// mean8 returns sum/n as a uint8. Callers pass sum as the total of n channel
// bytes (each 0-255), so sum/n is always in [0,255] and the conversion cannot
// overflow.
func mean8(sum, n int) uint8 {
	return uint8(sum / n) // #nosec G115 -- sum is n bytes (0-255 each); sum/n ≤ 255
}

// contentBounds returns the bounding box of non-background (luminance < contentLumThreshold)
// pixels — the tight extent of a light mosaic redaction within its margins.
func contentBounds(img *image.RGBA) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if (299*int(c.R)+587*int(c.G)+114*int(c.B))/1000 < contentLumThreshold {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x+1), max(y1, y+1)
			}
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return image.Rectangle{}
	}
	return image.Rect(x0, y0, x1, y1)
}

// inkBounds returns the bounding box of inked (luminance < inkLumThreshold) pixels in
// the [0,sentinelX) region of a freshly rendered glyph image.
func inkBounds(img *image.RGBA, sentinelX int) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := sentinelX, b.Dy(), 0, 0
	for y := range b.Dy() {
		for x := 0; x < sentinelX; x++ {
			c := img.RGBAAt(x, y)
			if (299*int(c.R)+587*int(c.G)+114*int(c.B))/1000 < inkLumThreshold {
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

// mseRGB is the mean squared error over RGB channels of two equal-size images.
func mseRGB(a, b *image.RGBA) float64 {
	n := min(len(a.Pix), len(b.Pix))
	if n < 4 {
		return math.Inf(1)
	}
	cnt := 3 * (n / 4)
	var s float64
	for i := 0; i < n; i += 4 {
		for c := range 3 {
			d := float64(a.Pix[i+c]) - float64(b.Pix[i+c])
			s += d * d
		}
	}
	return s / float64(cnt)
}
