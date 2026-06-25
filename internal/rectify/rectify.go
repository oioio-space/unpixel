// Package rectify provides the planar-homography primitives UnPixel uses to
// decode redactions photographed under perspective.
//
// When a mosaic/blur redaction is photographed at an angle, the redaction's
// block grid is no longer axis-aligned: it is a projective image of the original
// square grid. The whole generate-and-test attack assumes an axis-aligned grid,
// so the perspective must be undone first. A planar homography (a 3×3 projective
// transform, defined up to scale) maps the four corners of the redaction
// quadrilateral in the photo to an axis-aligned rectangle, restoring square
// blocks.
//
// This package is deliberately tiny and dependency-free (pure Go, no CGO): a
// [Matrix3] type, an exact 4-point DLT solver [SolveDLT], point application,
// matrix inverse, and an inverse-mapped bilinear [Warp]. It underpins both
// rectify-then-decode and the forward-model decode (project a rendered candidate
// by H into the photo frame, then compare against the native pixels).
package rectify

import (
	"errors"
	"image"
	"math"
)

// Point is a 2-D point in pixel coordinates. Float64 so projective division and
// sub-pixel sampling are exact rather than rounded per step.
type Point struct {
	X, Y float64
}

// Matrix3 is a 3×3 homography in row-major order:
//
//	| m[0] m[1] m[2] |
//	| m[3] m[4] m[5] |
//	| m[6] m[7] m[8] |
//
// It maps a point p to q by q = M·p in homogeneous coordinates (see [Matrix3.Apply]).
// A homography is defined only up to a non-zero scale; SolveDLT fixes m[8]=1.
type Matrix3 [9]float64

// ErrSingular is returned when a system has no unique solution — e.g. SolveDLT
// with three collinear correspondences, or Inverse of a degenerate matrix.
var ErrSingular = errors.New("rectify: singular system (degenerate point configuration)")

// ErrNoRegion is returned by DetectQuad when no pixel differs from the inferred
// background beyond the tolerance — i.e. no redaction region is visible.
var ErrNoRegion = errors.New("rectify: no foreground region detected")

// DetectQuad estimates the four corners (top-left, top-right, bottom-right,
// bottom-left) of the redaction quadrilateral in img, so callers need not supply
// them by hand. It thresholds pixels that differ from the background colour by
// more than tol (sum of absolute R/G/B differences) and returns the extreme
// foreground points along the two diagonals: min(x+y)→TL, max(x+y)→BR,
// max(x−y)→TR, min(x−y)→BL. For a convex, roughly-filled region distinct from a
// uniform background (e.g. a mosaic patch on a page) these extreme points are the
// quad's vertices. The background colour is inferred from the mean of the four
// image-corner pixels. It returns [ErrNoRegion] when nothing exceeds tol.
//
// This is a deliberately simple detector: it assumes one convex region on a
// uniform background. Cluttered photos need the (future) vanishing-point path.
func DetectQuad(img *image.RGBA, tol int) ([4]Point, error) {
	b := img.Bounds()
	if b.Empty() {
		return [4]Point{}, ErrNoRegion
	}
	bgR, bgG, bgB := cornerBackground(img)

	var tl, tr, br, bl Point
	var minSum, maxSum, minDiff, maxDiff float64
	found := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			d := abs(float64(int(img.Pix[o])-bgR)) +
				abs(float64(int(img.Pix[o+1])-bgG)) +
				abs(float64(int(img.Pix[o+2])-bgB))
			if d <= float64(tol) {
				continue
			}
			p := Point{X: float64(x), Y: float64(y)}
			sum, diff := p.X+p.Y, p.X-p.Y
			if !found {
				found = true
				tl, tr, br, bl = p, p, p, p
				minSum, maxSum, minDiff, maxDiff = sum, sum, diff, diff
				continue
			}
			if sum < minSum {
				minSum, tl = sum, p
			}
			if sum > maxSum {
				maxSum, br = sum, p
			}
			if diff > maxDiff {
				maxDiff, tr = diff, p
			}
			if diff < minDiff {
				minDiff, bl = diff, p
			}
		}
	}
	if !found {
		return [4]Point{}, ErrNoRegion
	}
	// Refine the rough extreme-pixel corners (accurate to a pixel or two) to
	// sub-pixel by fitting a line to each of the four region edges and
	// intersecting them — this is what lets dense/long text auto-decode exactly.
	return refineQuad(img, bgR, bgG, bgB, tol, [4]Point{tl, tr, br, bl}), nil
}

// edgeLine is a fitted line in point-direction form (a point on the line and a
// unit direction vector), the result of total-least-squares fitting an edge.
type edgeLine struct {
	px, py, dx, dy float64
}

// refineQuad sharpens rough extreme-pixel corners. It collects boundary
// foreground pixels lying within edgeBand of one of the four rough edges, fits a
// line to each edge (total least squares), and intersects adjacent lines for
// sub-pixel corners. It returns the rough corners unchanged if any edge has too
// few points, a fit is degenerate, or a refined corner moves implausibly far —
// so refinement can only help, never corrupt, the result.
func refineQuad(img *image.RGBA, bgR, bgG, bgB, tol int, rough [4]Point) [4]Point {
	b := img.Bounds()
	fg := func(x, y int) bool {
		if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
			return false
		}
		o := img.PixOffset(x, y)
		d := abs(float64(int(img.Pix[o])-bgR)) +
			abs(float64(int(img.Pix[o+1])-bgG)) +
			abs(float64(int(img.Pix[o+2])-bgB))
		return d > float64(tol)
	}

	const edgeBand = 5.0 // px: keep boundary points near a rough edge, drop interior noise
	var sides [4][]Point // edge i runs rough[i] → rough[(i+1)%4]
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if !fg(x, y) {
				continue
			}
			if fg(x-1, y) && fg(x+1, y) && fg(x, y-1) && fg(x, y+1) {
				continue // interior pixel, not a boundary
			}
			p := Point{X: float64(x), Y: float64(y)}
			best, bestD := -1, edgeBand
			for i := range 4 {
				if d := segDist(p, rough[i], rough[(i+1)%4]); d < bestD {
					bestD, best = d, i
				}
			}
			if best >= 0 {
				// best is -1 or 0..3 (set only from `range 4`); the guard makes the
				// index provably in range.
				sides[best] = append(sides[best], p) // #nosec G602 -- best ∈ [0,3] here
			}
		}
	}

	var lines [4]edgeLine
	for i := range 4 {
		if len(sides[i]) < 2 {
			return rough
		}
		lines[i] = fitLine(sides[i])
	}

	var out [4]Point
	for i := range 4 {
		p, ok := lineIntersect(lines[(i+3)%4], lines[i])
		if !ok || dist(p, rough[i]) > 2*edgeBand {
			return rough
		}
		out[i] = p
	}
	return out
}

// fitLine returns the total-least-squares line through pts: the centroid and the
// principal-axis direction (angle ½·atan2(2·Sxy, Sxx−Syy)).
func fitLine(pts []Point) edgeLine {
	n := float64(len(pts))
	var mx, my float64
	for _, p := range pts {
		mx += p.X
		my += p.Y
	}
	mx, my = mx/n, my/n
	var sxx, sxy, syy float64
	for _, p := range pts {
		ex, ey := p.X-mx, p.Y-my
		sxx += ex * ex
		sxy += ex * ey
		syy += ey * ey
	}
	theta := 0.5 * math.Atan2(2*sxy, sxx-syy)
	return edgeLine{px: mx, py: my, dx: math.Cos(theta), dy: math.Sin(theta)}
}

// lineIntersect returns the intersection of two point-direction lines, or
// ok=false when they are (near) parallel.
func lineIntersect(a, e edgeLine) (Point, bool) {
	det := a.dy*e.dx - a.dx*e.dy
	if abs(det) < 1e-9 {
		return Point{}, false
	}
	rx, ry := e.px-a.px, e.py-a.py
	s := (e.dx*ry - e.dy*rx) / det
	return Point{X: a.px + s*a.dx, Y: a.py + s*a.dy}, true
}

// segDist returns the distance from p to the segment a–b.
func segDist(p, a, e Point) float64 {
	vx, vy := e.X-a.X, e.Y-a.Y
	wx, wy := p.X-a.X, p.Y-a.Y
	vv := vx*vx + vy*vy
	if vv == 0 {
		return dist(p, a)
	}
	t := (wx*vx + wy*vy) / vv
	t = max(0, min(1, t))
	return dist(p, Point{X: a.X + t*vx, Y: a.Y + t*vy})
}

// dist returns the Euclidean distance between a and b.
func dist(a, e Point) float64 {
	dx, dy := e.X-a.X, e.Y-a.Y
	return math.Sqrt(dx*dx + dy*dy)
}

// cornerBackground returns the mean R,G,B of img's four corner pixels — a robust
// estimate of a uniform background that avoids sampling the central redaction.
func cornerBackground(img *image.RGBA) (r, g, b int) {
	bb := img.Bounds()
	corners := [4]image.Point{
		{X: bb.Min.X, Y: bb.Min.Y},
		{X: bb.Max.X - 1, Y: bb.Min.Y},
		{X: bb.Min.X, Y: bb.Max.Y - 1},
		{X: bb.Max.X - 1, Y: bb.Max.Y - 1},
	}
	for _, c := range corners {
		o := img.PixOffset(c.X, c.Y)
		r += int(img.Pix[o])
		g += int(img.Pix[o+1])
		b += int(img.Pix[o+2])
	}
	return r / 4, g / 4, b / 4
}

// Apply maps p through the homography, performing the projective division.
// The returned point is in the destination plane. When the homogeneous w
// collapses to zero (a point mapped to infinity) the raw scaled coordinates are
// returned; callers warping a bounded region never hit this in practice.
func (m Matrix3) Apply(p Point) Point {
	w := m[6]*p.X + m[7]*p.Y + m[8]
	x := m[0]*p.X + m[1]*p.Y + m[2]
	y := m[3]*p.X + m[4]*p.Y + m[5]
	if w != 0 {
		return Point{X: x / w, Y: y / w}
	}
	return Point{X: x, Y: y}
}

// Mul returns the composition m·o (apply o first, then m).
func (m Matrix3) Mul(o Matrix3) Matrix3 {
	var r Matrix3
	for row := range 3 {
		for col := range 3 {
			var sum float64
			for k := range 3 {
				sum += m[row*3+k] * o[k*3+col]
			}
			r[row*3+col] = sum
		}
	}
	return r
}

// SolveDLT computes the homography mapping the four src points to the four dst
// points, using the Direct Linear Transform with m[8] fixed to 1. The four
// points in each set must be in the same order (e.g. clockwise from top-left)
// and no three may be collinear; otherwise it returns [ErrSingular].
func SolveDLT(src, dst [4]Point) (Matrix3, error) {
	// Each correspondence (x,y)->(u,v) contributes two rows, with unknowns
	// h0..h7 (h8 ≡ 1):
	//   h0·x + h1·y + h2            − h6·x·u − h7·y·u = u
	//                    h3·x + h4·y + h5 − h6·x·v − h7·y·v = v
	var a [8][8]float64
	var b [8]float64
	for i := range 4 {
		x, y := src[i].X, src[i].Y
		u, v := dst[i].X, dst[i].Y
		r := i * 2
		a[r] = [8]float64{x, y, 1, 0, 0, 0, -x * u, -y * u}
		b[r] = u
		a[r+1] = [8]float64{0, 0, 0, x, y, 1, -x * v, -y * v}
		b[r+1] = v
	}
	h, err := solve8(a, b)
	if err != nil {
		return Matrix3{}, err
	}
	return Matrix3{h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], 1}, nil
}

// RectToQuad returns the homography mapping the axis-aligned rectangle
// [0,w]×[0,h] to the destination quadrilateral dst (top-left, top-right,
// bottom-right, bottom-left). This is the inverse-warp map for rectification:
// for each pixel of the rectified output it yields the source photo coordinate
// to sample. dst must be supplied in that corner order.
func RectToQuad(w, h float64, dst [4]Point) (Matrix3, error) {
	src := [4]Point{{0, 0}, {w, 0}, {w, h}, {0, h}}
	return SolveDLT(src, dst)
}

// Inverse returns the matrix inverse, or [ErrSingular] when the determinant is
// zero. The inverse of a photo→rectified homography maps rectified coordinates
// back to the photo for sampling.
func (m Matrix3) Inverse() (Matrix3, error) {
	a, b, c := m[0], m[1], m[2]
	d, e, f := m[3], m[4], m[5]
	g, h, i := m[6], m[7], m[8]

	co0 := e*i - f*h
	co1 := f*g - d*i
	co2 := d*h - e*g
	det := a*co0 + b*co1 + c*co2
	if det == 0 {
		return Matrix3{}, ErrSingular
	}
	inv := 1.0 / det
	return Matrix3{
		co0 * inv, (c*h - b*i) * inv, (b*f - c*e) * inv,
		co1 * inv, (a*i - c*g) * inv, (c*d - a*f) * inv,
		co2 * inv, (b*g - a*h) * inv, (a*e - b*d) * inv,
	}, nil
}

// Warp produces a w×h RGBA image by inverse-mapping: for each destination pixel
// (dx,dy) it applies dstToSrc to find the source coordinate and samples src with
// bilinear interpolation. Coordinates outside src contribute opaque white, the
// pixelation/blur background, so the rectified crop matches the white-padding
// the decode pipeline expects. dstToSrc maps destination→source (e.g. the result
// of [RectToQuad], or a photo→rectified homography's Inverse).
func Warp(src *image.RGBA, dstToSrc Matrix3, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	b := src.Bounds()
	maxX, maxY := b.Dx()-1, b.Dy()-1
	for dy := range h {
		for dx := range w {
			s := dstToSrc.Apply(Point{X: float64(dx) + 0.5, Y: float64(dy) + 0.5})
			r, g, bl, a := sampleBilinear(src, s.X-0.5, s.Y-0.5, maxX, maxY)
			o := dst.PixOffset(dx, dy)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = r, g, bl, a
		}
	}
	return dst
}

// sampleBilinear samples src at fractional (fx,fy) in src-local coordinates,
// returning opaque white for samples whose four taps all fall outside the image.
func sampleBilinear(src *image.RGBA, fx, fy float64, maxX, maxY int) (r, g, b, a uint8) {
	x0 := int(floor(fx))
	y0 := int(floor(fy))
	if x0 < -1 || y0 < -1 || x0 > maxX || y0 > maxY {
		return 255, 255, 255, 255
	}
	tx := fx - float64(x0)
	ty := fy - float64(y0)
	var rr, gg, bb, aa float64
	for j := range 2 {
		for i := range 2 {
			wx := 1 - tx
			if i == 1 {
				wx = tx
			}
			wy := 1 - ty
			if j == 1 {
				wy = ty
			}
			wt := wx * wy
			if wt == 0 {
				continue
			}
			pr, pg, pb, pa := tap(src, x0+i, y0+j, maxX, maxY)
			rr += wt * float64(pr)
			gg += wt * float64(pg)
			bb += wt * float64(pb)
			aa += wt * float64(pa)
		}
	}
	return round8(rr), round8(gg), round8(bb), round8(aa)
}

// tap reads one clamped pixel; out-of-bounds taps read opaque white so edges do
// not bleed dark borders into the rectified crop.
func tap(src *image.RGBA, x, y, maxX, maxY int) (r, g, b, a uint8) {
	if x < 0 || y < 0 || x > maxX || y > maxY {
		return 255, 255, 255, 255
	}
	o := src.PixOffset(x+src.Bounds().Min.X, y+src.Bounds().Min.Y)
	return src.Pix[o], src.Pix[o+1], src.Pix[o+2], src.Pix[o+3]
}

// solve8 solves the 8×8 linear system a·x = b by Gaussian elimination with
// partial pivoting. Returns [ErrSingular] if a pivot is effectively zero.
func solve8(a [8][8]float64, b [8]float64) ([8]float64, error) {
	const eps = 1e-12
	for col := range 8 {
		// Partial pivot: pick the row with the largest |a[row][col]|.
		piv := col
		best := abs(a[col][col])
		for row := col + 1; row < 8; row++ {
			if v := abs(a[row][col]); v > best {
				best, piv = v, row
			}
		}
		if best < eps {
			return [8]float64{}, ErrSingular
		}
		if piv != col {
			a[col], a[piv] = a[piv], a[col]
			b[col], b[piv] = b[piv], b[col]
		}
		// Eliminate below.
		for row := col + 1; row < 8; row++ {
			factor := a[row][col] / a[col][col]
			if factor == 0 {
				continue
			}
			for k := col; k < 8; k++ {
				a[row][k] -= factor * a[col][k]
			}
			b[row] -= factor * b[col]
		}
	}
	// Back-substitute.
	var x [8]float64
	for row := 7; row >= 0; row-- {
		sum := b[row]
		for k := row + 1; k < 8; k++ {
			sum -= a[row][k] * x[k]
		}
		x[row] = sum / a[row][row]
	}
	return x, nil
}

// Projector scores a candidate against a perspective photo in the photo's own
// pixel space — the forward-model decode. Rather than warping (and resampling)
// the photo to rectify it, it keeps the photo's native pixels and projects each
// axis-aligned candidate through the homography to compare against them. This
// avoids the interpolation loss of rectify-then-decode: the homography becomes
// part of the forward model (render → pixelate → project → compare).
//
// A Projector is built once per (photo, quad) and reused across every candidate
// in the search, so its per-candidate cost is one [Projector.Distance] call.
type Projector struct {
	photo        *image.RGBA
	photoToRect  Matrix3
	rectW, rectH int
	// Bounding box of the quad in photo pixels, clipped to the photo, over which
	// Distance iterates; pixels whose preimage falls outside [0,rectW)×[0,rectH)
	// are skipped (they are outside the redaction quad).
	minX, minY, maxX, maxY int
}

// NewProjector builds a forward-model scorer for the redaction quad (top-left,
// top-right, bottom-right, bottom-left, in photo pixel coordinates). rectW×rectH
// is the axis-aligned candidate size — typically the quad's average edge lengths
// rounded to whole blocks. It returns [ErrSingular] if the quad is degenerate.
func NewProjector(photo *image.RGBA, quad [4]Point, rectW, rectH int) (*Projector, error) {
	if rectW <= 0 || rectH <= 0 {
		return nil, errors.New("rectify: NewProjector needs positive rect dimensions")
	}
	rectToPhoto, err := RectToQuad(float64(rectW), float64(rectH), quad)
	if err != nil {
		return nil, err
	}
	photoToRect, err := rectToPhoto.Inverse()
	if err != nil {
		return nil, err
	}
	b := photo.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for _, c := range quad {
		minX = min(minX, int(floor(c.X)))
		minY = min(minY, int(floor(c.Y)))
		maxX = max(maxX, int(floor(c.X))+1)
		maxY = max(maxY, int(floor(c.Y))+1)
	}
	return &Projector{
		photo:       photo,
		photoToRect: photoToRect,
		rectW:       rectW,
		rectH:       rectH,
		minX:        max(minX, b.Min.X),
		minY:        max(minY, b.Min.Y),
		maxX:        min(maxX, b.Max.X),
		maxY:        min(maxY, b.Max.Y),
	}, nil
}

// Distance returns the mean per-channel RGB difference, normalised to [0,1],
// between the photo and the candidate projected into the photo frame, averaged
// over the photo pixels that fall inside the redaction quad.
//
// The candidate is sampled at a normalised position: a photo pixel whose
// preimage in rect space lands at (r.X, r.Y) ∈ [0,rectW)×[0,rectH) is mapped
// to (r.X/rectW × candW, r.Y/rectH × candH) in the candidate's own pixel
// coordinates, where candW×candH are the candidate bounds. This makes Distance
// candidate-size-independent: a candidate rendered at any size is stretched to
// fill the quad and compared correctly — the caller does not need to pre-scale
// the candidate to exactly rectW×rectH.
//
// Only the true text reproduces the photo's projected blocks, so the true
// candidate scores near zero and wrong candidates score higher — the same
// generate-and-test signal as the flat pipeline, but perspective-correct.
//
// It returns 1 (maximally different) when no photo pixel falls inside the quad.
func (p *Projector) Distance(candRect *image.RGBA) float64 {
	cb := candRect.Bounds()
	candW := float64(cb.Dx())
	candH := float64(cb.Dy())
	cmaxX, cmaxY := cb.Dx()-1, cb.Dy()-1
	rW := float64(p.rectW)
	rH := float64(p.rectH)
	var sum float64
	var n int
	for y := p.minY; y < p.maxY; y++ {
		for x := p.minX; x < p.maxX; x++ {
			r := p.photoToRect.Apply(Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
			if r.X < 0 || r.Y < 0 || r.X >= rW || r.Y >= rH {
				continue // outside the redaction quad
			}
			// Normalise the rect-space coordinate to the candidate's own bounds
			// so a candidate of any size is stretched to fill the quad.
			cx := r.X / rW * candW
			cy := r.Y / rH * candH
			cr, cg, cbl, _ := sampleBilinear(candRect, cx-0.5, cy-0.5, cmaxX, cmaxY)
			o := p.photo.PixOffset(x, y)
			sum += absDiff(p.photo.Pix[o], cr) + absDiff(p.photo.Pix[o+1], cg) + absDiff(p.photo.Pix[o+2], cbl)
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return sum / (float64(n) * 3 * 255)
}

// PartialDistance is like [Distance] but compares only the left xFrac fraction
// of the quad in rect space (xFrac ∈ (0,1]). Photo pixels whose rect-space
// preimage has r.X ≥ xFrac*rectW are skipped.
//
// This is used during beam search to score a prefix candidate of width
// candW against only the portion of the quad the prefix fills, avoiding
// the right-edge white-padding penalty that would otherwise rank a correct
// short prefix lower than wrong candidates of the same length.
func (p *Projector) PartialDistance(candRect *image.RGBA, xFrac float64) float64 {
	if xFrac <= 0 {
		return 1
	}
	if xFrac >= 1 {
		return p.Distance(candRect)
	}
	cb := candRect.Bounds()
	candW := float64(cb.Dx())
	candH := float64(cb.Dy())
	cmaxX, cmaxY := cb.Dx()-1, cb.Dy()-1
	rW := float64(p.rectW)
	rH := float64(p.rectH)
	xLimit := xFrac * rW
	var sum float64
	var n int
	for y := p.minY; y < p.maxY; y++ {
		for x := p.minX; x < p.maxX; x++ {
			r := p.photoToRect.Apply(Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
			if r.X < 0 || r.Y < 0 || r.X >= xLimit || r.Y >= rH {
				continue
			}
			cx := r.X / rW * candW
			cy := r.Y / rH * candH
			cr, cg, cbl, _ := sampleBilinear(candRect, cx-0.5, cy-0.5, cmaxX, cmaxY)
			o := p.photo.PixOffset(x, y)
			sum += absDiff(p.photo.Pix[o], cr) + absDiff(p.photo.Pix[o+1], cg) + absDiff(p.photo.Pix[o+2], cbl)
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return sum / (float64(n) * 3 * 255)
}

func absDiff(a, b uint8) float64 {
	if a > b {
		return float64(a - b)
	}
	return float64(b - a)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func floor(v float64) float64 {
	f := float64(int(v))
	if v < 0 && f != v {
		f--
	}
	return f
}

func round8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
