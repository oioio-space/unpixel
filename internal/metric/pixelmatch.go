package metric

// In-repo pixelmatch counting path — operates directly on *image.RGBA.Pix,
// replicating github.com/orisano/pixelmatch bit-for-bit for the counting-only
// use case (no diff image output, includeAA=false, diffMask=false).
//
// Bit-exactness measures:
//   - 16-bit channel expansion: r16 = uint32(s[0])<<8 | uint32(s[0]) (same as reference)
//   - colorDelta float arithmetic uses plain * then + (no FMA) to preserve reference rounding
//   - rgbaToY/I/Q constants identical to reference
//   - maxDelta formula: 35215 * threshold * threshold (identical)
//   - isAntiAliased / hasManySiblings 3×3 neighborhood with identical bounds clamping
//   - isIdentical fast path via bytes.Equal on Pix rows (same as reference equals())
//
// Performance: rows are pre-expanded to rgba16 in a 5-row sliding window
// (matching the reference imageLineReader layout). Both caches (for a and b)
// share a single pooled flat []rgba16 slab, so CountPixels makes at most 1
// pool Get/Put per call on the hot path.

import (
	"bytes"
	"image"
	"math"
	"sync"
)

// rgba16 holds a single pixel's channels pre-expanded to 16-bit range [0, 65535],
// matching the uint32 field semantics from github.com/orisano/pixelmatch.
type rgba16 struct {
	r, g, b, a uint32
}

// slabPool pools *[]rgba16 backing arrays shared by both row caches in a
// single CountPixels call. Each slab holds 10 × width pixels (5 rows × 2 images).
var slabPool = sync.Pool{New: func() any { return new([]rgba16) }}

// expandRow fills dst with rgba16-expanded pixels from absolute row y of img.
// dst must have length == img.Bounds().Dx().
func expandRow(dst []rgba16, img *image.RGBA, y int) {
	off := img.PixOffset(img.Rect.Min.X, y)
	pix := img.Pix
	for i := range dst {
		base := off + i*4
		s := pix[base : base+4 : base+4]
		r := uint32(s[0])
		g := uint32(s[1])
		b := uint32(s[2])
		a := uint32(s[3])
		dst[i] = rgba16{r<<8 | r, g<<8 | g, b<<8 | b, a<<8 | a}
	}
}

// rowCache is a 5-slot sliding window of pre-expanded rgba16 rows centred on
// the current scan-line. Slot i covers absolute row (y + i - 2). A zero-length
// slice in slots signals an out-of-bounds row.
//
// All row slices are sub-slices of a single pre-allocated slab so that the
// sliding window advance is a pure in-memory rotate with no extra allocation.
type rowCache struct {
	img   *image.RGBA
	rect  image.Rectangle
	width int
	y     int         // current scan-line (absolute image y)
	slots [5][]rgba16 // circular row buffer; slots[i] is row y+i-2
}

// initRowCache initialises a rowCache for img at startY, using slab[off:] for
// the 5 × width row buffers (slab must have at least 5*width elements
// starting at off). The slab ownership stays with the caller.
func initRowCache(rc *rowCache, img *image.RGBA, startY int, slab []rgba16) {
	rc.img = img
	rc.rect = img.Bounds()
	rc.width = rc.rect.Dx()
	rc.y = startY
	for i := range 5 {
		buf := slab[i*rc.width : (i+1)*rc.width]
		ry := startY + i - 2
		if ry >= rc.rect.Min.Y && ry < rc.rect.Max.Y {
			expandRow(buf, img, ry)
			rc.slots[i] = buf
		} else {
			rc.slots[i] = buf[:0] // out-of-bounds row
		}
	}
}

// advance slides the window forward by one row, expanding the new trailing row.
func (rc *rowCache) advance() {
	// Shift the window forward one row; the recycled slot-0 buffer becomes the
	// new trailing slot (slot 4).
	old := rc.slots[0]
	copy(rc.slots[:4], rc.slots[1:])
	rc.y++
	nextY := rc.y + 2
	if nextY >= rc.rect.Min.Y && nextY < rc.rect.Max.Y {
		// Restore the full-width slice header on the recycled buffer.
		old = old[:rc.width]
		expandRow(old, rc.img, nextY)
		rc.slots[4] = old
	} else {
		rc.slots[4] = old[:0] // mark as out-of-bounds
	}
}

// row returns the pre-expanded slice for absolute image row ry.
// Returns a zero-length slice if ry is outside the ±2 window or image bounds.
func (rc *rowCache) row(ry int) []rgba16 {
	idx := ry - rc.y + 2
	if idx < 0 || idx >= 5 {
		return nil
	}
	return rc.slots[idx]
}

// at returns the rgba16 pixel at absolute image coordinate (x, y).
// x is converted to a zero-based column index by subtracting rect.Min.X.
func (rc *rowCache) at(x, y int) rgba16 {
	return rc.row(y)[x-rc.rect.Min.X]
}

// rgbaToFloats converts a rgba16 to float64 channels by multiplying by 1/256.
// Matches rgbaFromColor in the reference (same constant).
func rgbaToFloats(p rgba16) (r, g, b, a float64) {
	const x = 1.0 / 256.0
	return float64(p.r) * x, float64(p.g) * x, float64(p.b) * x, float64(p.a) * x
}

// yiqY returns the Y (luma) component. Constants match rgbaToY in the reference.
func yiqY(r, g, b float64) float64 {
	return r*0.29889531 + g*0.58662247 + b*0.11448223
}

// yiqI returns the I component. Constants match rgbaToI.
func yiqI(r, g, b float64) float64 {
	return r*0.59597799 - g*0.27417610 - b*0.32180189
}

// yiqQ returns the Q component. Constants match rgbaToQ.
func yiqQ(r, g, b float64) float64 {
	return r*0.21147017 - g*0.52261711 + b*0.31114694
}

// pixBlend applies 255 + (c-255)*a. Matches blend / blendRGBA in the reference.
func pixBlend(c, a float64) float64 {
	return 255 + (c-255)*a
}

// colorDelta returns the perceptual colour difference between pixels pa and pb.
// If yOnly is true, only the luma difference is returned (used by isAntiAliased).
//
// Arithmetic order and absence of math.FMA are intentional — they replicate
// the exact float64 rounding of the reference implementation.
func colorDelta(pa, pb rgba16, yOnly bool) float64 {
	if pa.a == pb.a && pa.r == pb.r && pa.g == pb.g && pa.b == pb.b {
		return 0
	}

	ar, ag, ab, aa := rgbaToFloats(pa)
	if aa < 255 {
		aa /= 255
		ar = pixBlend(ar, aa)
		ag = pixBlend(ag, aa)
		ab = pixBlend(ab, aa)
	}

	br, bg, bb, ba := rgbaToFloats(pb)
	if ba < 255 {
		ba /= 255
		br = pixBlend(br, ba)
		bg = pixBlend(bg, ba)
		bb = pixBlend(bb, ba)
	}

	ya := yiqY(ar, ag, ab)
	yb := yiqY(br, bg, bb)
	y := ya - yb
	if yOnly {
		return y
	}
	i := yiqI(ar, ag, ab) - yiqI(br, bg, bb)
	q := yiqQ(ar, ag, ab) - yiqQ(br, bg, bb)
	// Plain * then + (no math.FMA) to match reference scalar rounding.
	delta := 0.5053*y*y + 0.299*i*i + 0.1957*q*q
	if ya > yb {
		return -delta
	}
	return delta
}

// hasManySiblings reports whether the pixel at (x1, y1) in rc has more than
// two neighbours in the 3×3 neighbourhood that are identical to it.
// Replicates the reference hasManySiblings exactly.
func hasManySiblings(rc *rowCache, x1, y1 int) bool {
	r := rc.rect
	x0 := max(x1-1, r.Min.X)
	y0 := max(y1-1, r.Min.Y)
	x2 := min(x1+1, r.Max.X-1)
	y2 := min(y1+1, r.Max.Y-1)
	zeroes := 0
	if x1 == x0 || x1 == x2 || y1 == y0 || y1 == y2 {
		zeroes = 1
	}

	a := rc.at(x1, y1)
	for x := x0; x <= x2; x++ {
		for y := y0; y <= y2; y++ {
			if x == x1 && y == y1 {
				continue
			}
			b := rc.at(x, y)
			if a.r == b.r && a.g == b.g && a.b == b.b && a.a == b.a {
				zeroes++
			}
			if zeroes > 2 {
				return true
			}
		}
	}
	return false
}

// isAntiAliased reports whether the pixel at (x1, y1) in image ac looks like
// part of an anti-aliased edge. Replicates the reference isAntiAliased exactly.
func isAntiAliased(ac, bc *rowCache, x1, y1 int) bool {
	r := ac.rect
	x0 := max(x1-1, r.Min.X)
	y0 := max(y1-1, r.Min.Y)
	x2 := min(x1+1, r.Max.X-1)
	y2 := min(y1+1, r.Max.Y-1)
	zeroes := 0
	if x1 == x0 || x1 == x2 || y1 == y0 || y1 == y2 {
		zeroes = 1
	}

	var minVal, maxVal float64
	var minX, minY, maxX, maxY int
	c := ac.at(x1, y1)

	for x := x0; x <= x2; x++ {
		for y := y0; y <= y2; y++ {
			if x == x1 && y == y1 {
				continue
			}
			delta := colorDelta(c, ac.at(x, y), true)
			switch {
			case delta == 0:
				zeroes++
				if zeroes > 2 {
					return false
				}
			case delta < minVal:
				minVal = delta
				minX, minY = x, y
			case delta > maxVal:
				maxVal = delta
				maxX, maxY = x, y
			}
		}
	}

	if maxVal == 0 || minVal == 0 {
		return false
	}

	return (hasManySiblings(ac, minX, minY) && hasManySiblings(bc, minX, minY)) ||
		(hasManySiblings(ac, maxX, maxY) && hasManySiblings(bc, maxX, maxY))
}

// rgbaIdentical reports whether the active pixel region of a and b are
// byte-for-byte identical. Matches the reference equals() logic.
func rgbaIdentical(a, b *image.RGBA) bool {
	r := a.Bounds()
	w := r.Dx()
	h := r.Dy()
	rowBytes := w * 4
	if w*h*4 == len(a.Pix) && w*h*4 == len(b.Pix) {
		return bytes.Equal(a.Pix, b.Pix)
	}
	for y := range h {
		offA := a.PixOffset(r.Min.X, r.Min.Y+y)
		offB := b.PixOffset(b.Rect.Min.X, b.Rect.Min.Y+y)
		if !bytes.Equal(a.Pix[offA:offA+rowBytes], b.Pix[offB:offB+rowBytes]) {
			return false
		}
	}
	return true
}

// countPixels is the shared core for CountPixels and CountPixelsNoAA.
// When skipAA is false the algorithm is bit-identical to
// github.com/orisano/pixelmatch MatchPixel (counting-only path). When skipAA
// is true every pixel whose perceptual delta exceeds maxDelta is counted
// unconditionally — no anti-aliasing neighbourhood scan — which is correct and
// faster for block-constant (mosaic-pixelated) images that contain no real AA.
func countPixels(a, b *image.RGBA, threshold float64, skipAA bool) int {
	if !a.Bounds().Eq(b.Bounds()) {
		return 0
	}
	if rgbaIdentical(a, b) {
		return 0
	}

	maxDelta := 35215 * threshold * threshold
	rect := a.Bounds()
	w := rect.Dx()

	// Borrow a single slab for 10 row buffers (5 per image).
	// When skipAA is true only the current row is accessed, but we still
	// allocate the full slab so the same pool entry can serve both modes.
	need := 10 * w
	pslab := slabPool.Get().(*[]rgba16)
	if cap(*pslab) < need {
		*pslab = make([]rgba16, need)
	}
	*pslab = (*pslab)[:need]
	slab := *pslab

	var ac, bc rowCache
	initRowCache(&ac, a, rect.Min.Y, slab[:5*w])
	initRowCache(&bc, b, rect.Min.Y, slab[5*w:])

	diff := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		if y > rect.Min.Y {
			ac.advance()
			bc.advance()
		}
		rowA := ac.row(y)
		rowB := bc.row(y)
		for xi := range w {
			delta := colorDelta(rowA[xi], rowB[xi], false)
			if math.Abs(delta) > maxDelta {
				if skipAA {
					diff++
				} else {
					x := rect.Min.X + xi
					if !isAntiAliased(&ac, &bc, x, y) && !isAntiAliased(&bc, &ac, x, y) {
						diff++
					}
				}
			}
		}
	}

	// Not deferred: the scan loop is panic-free for equal-bounds RGBA inputs
	// (validated above), and avoiding defer keeps the hot path lean — matching
	// the SSIM grayscalePool precedent.
	slabPool.Put(pslab)
	return diff
}

// CountPixels returns the number of pixels that differ perceptually between a
// and b beyond the given threshold, using the in-repo pixelmatch counting path.
// This is the raw integer form of Pixelmatch.Compare; callers that need the
// fraction should use Compare directly. Both images must have equal bounds.
//
// The algorithm is bit-identical to github.com/orisano/pixelmatch MatchPixel
// for the counting-only path (writeTo=nil, includeAA=false, diffMask=false).
func CountPixels(a, b *image.RGBA, threshold float64) int {
	return countPixels(a, b, threshold, false)
}

// CountPixelsNoAA returns the number of pixels that differ perceptually between
// a and b beyond the given threshold, omitting the anti-aliasing neighbourhood
// exclusion that CountPixels applies. Every pixel whose YIQ colour delta exceeds
// the threshold is counted directly.
//
// This is equivalent to CountPixels for block-constant (mosaic-pixelated) images
// where no real anti-aliasing exists — skipping the AA scan is a no-op
// behaviourally but eliminates the 3×3 neighbourhood reads, yielding roughly 2×
// throughput on the dense-diff path. It is NOT appropriate for images that may
// contain sub-pixel rendering from different engines (use CountPixels there).
//
// CountPixelsNoAA always returns a value ≥ CountPixels for the same inputs.
func CountPixelsNoAA(a, b *image.RGBA, threshold float64) int {
	return countPixels(a, b, threshold, true)
}

// CountPixelsNoAABounded is the early-exit variant of CountPixelsNoAA. It
// aborts the scan as soon as the running diff count reaches maxDiff, returning
// maxDiff immediately. The result equals CountPixelsNoAA(a, b, threshold) when
// the true count is < maxDiff (accepted-candidate invariant); when the count
// reaches maxDiff the returned value is maxDiff, which may be less than the
// true total.
//
// maxDiff <= 0 disables the ceiling and behaves identically to CountPixelsNoAA.
// The early-exit is sound because the no-AA count is monotone: each pixel can
// only increase diff, so reaching the ceiling guarantees the full scan would
// also reach or exceed it.
func CountPixelsNoAABounded(a, b *image.RGBA, threshold float64, maxDiff int) int {
	if maxDiff <= 0 {
		return countPixels(a, b, threshold, true)
	}
	if !a.Bounds().Eq(b.Bounds()) {
		return 0
	}
	if rgbaIdentical(a, b) {
		return 0
	}

	maxDelta := 35215 * threshold * threshold
	rect := a.Bounds()
	w := rect.Dx()

	need := 10 * w
	pslab := slabPool.Get().(*[]rgba16)
	if cap(*pslab) < need {
		*pslab = make([]rgba16, need)
	}
	*pslab = (*pslab)[:need]
	slab := *pslab

	var ac, bc rowCache
	initRowCache(&ac, a, rect.Min.Y, slab[:5*w])
	initRowCache(&bc, b, rect.Min.Y, slab[5*w:])

	diff := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		if y > rect.Min.Y {
			ac.advance()
			bc.advance()
		}
		rowA := ac.row(y)
		rowB := bc.row(y)
		for xi := range w {
			if math.Abs(colorDelta(rowA[xi], rowB[xi], false)) > maxDelta {
				diff++
				if diff >= maxDiff {
					slabPool.Put(pslab)
					return diff
				}
			}
		}
	}

	slabPool.Put(pslab)
	return diff
}
