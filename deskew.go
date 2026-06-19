package unpixel

import (
	"image"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"github.com/oioio-space/unpixel/internal/imutil"
)

const (
	// deskewGridMinConfidence is the minimum InferBlockGrid confidence on the
	// raw image below which the deskew search is triggered. An axis-aligned
	// mosaic has confidence ≈ 1.0 and will never reach this threshold.
	deskewGridMinConfidence = 0.60

	// deskewHomogMinGain is the minimum absolute improvement in block-homogeneity
	// score required before a rotation is applied. Empirically, random-noise
	// images produce gains ≤ 0.01, while rotated mosaics produce gains ≥ 0.05,
	// so 0.03 cleanly separates the two populations with a comfortable margin.
	deskewHomogMinGain = 0.03

	// deskewHomogMinScore is the minimum post-rotation block-homogeneity score
	// required before the deskew is applied. A genuine mosaic corrected to
	// axis-alignment scores ≥ 0.90; this floor rejects improvements that are
	// merely less-noisy noise.
	deskewHomogMinScore = 0.50

	// deskewMaxAngleDeg is the search radius in degrees (inclusive, both signs).
	deskewMaxAngleDeg = 12.0

	// deskewStepDeg is the angular step used during the candidate search.
	deskewStepDeg = 0.5

	// deskewProbeBlockSize is the block size assumed when computing the
	// homogeneity score during the angle search. It is a fixed probe size
	// independent of the actual InferBlockSize result — an accurate S is not
	// needed here, only a consistent one across candidates.
	deskewProbeBlockSize = 8
)

// SkewInfo carries the outcome of the automatic skew detection performed by New.
// It is available via Engine.SkewInfo for introspection and CLI warnings.
type SkewInfo struct {
	// Detected is true when the image appeared to have a non-axis-aligned mosaic
	// grid (InferBlockGrid confidence was low on the raw image).
	Detected bool
	// Applied is true when a deskew rotation was actually applied to the image
	// inside New — meaning the block-homogeneity gain exceeded deskewHomogMinGain
	// and the post-rotation score exceeded deskewHomogMinScore.
	Applied bool
	// AngleDeg is the rotation angle (in degrees, counter-clockwise) that best
	// corrected the skew. It is 0 when no skew was detected or applied.
	AngleDeg float64
	// BaselineConfidence is the InferBlockGrid confidence of the raw (unrotated)
	// image. Values below deskewGridMinConfidence trigger the search.
	BaselineConfidence float64
	// BestConfidence is the InferBlockGrid confidence after applying AngleDeg.
	// It is populated only when Applied is true.
	BestConfidence float64
}

// SkewInfo returns the skew-detection outcome recorded when the Engine was
// created by New. The zero SkewInfo (Detected=false, Applied=false) is returned
// when the image was axis-aligned or too small to analyse.
func (e *Engine) SkewInfo() SkewInfo { return e.skewInfo }

// DeskewedImage returns the internal RGBA image used by the Engine — after any
// dark-background inversion and deskew rotation performed by New. When no
// deskew was applied it is the same pixel data that was passed to New (after
// optional inversion); when a deskew was applied it is the rotated version.
// The returned pointer is the live internal buffer; callers must not modify it.
func (e *Engine) DeskewedImage() *image.RGBA { return e.redacted }

// detectAndDeskew analyses img for skew. If InferBlockGrid confidence is low
// (below deskewGridMinConfidence) it searches for the rotation angle that
// maximises block-homogeneity — a signal that works even when the GCD-based
// grid detector fails on rotated images. The rotation is applied only when the
// homogeneity gain clearly exceeds deskewHomogMinGain and the post-rotation
// score exceeds deskewHomogMinScore. Axis-aligned images are returned unchanged.
//
// It also returns the BlockGrid of the returned image, so callers (New) can
// reuse the already-computed grid for block-size inference instead of scanning
// the image a second time.
func detectAndDeskew(img *image.RGBA) (*image.RGBA, SkewInfo, BlockGrid) {
	baseGrid, baseOK := InferBlockGrid(img)
	baseConf := 0.0
	if baseOK {
		baseConf = baseGrid.Confidence
	}

	info := SkewInfo{BaselineConfidence: baseConf}

	// Axis-aligned: confidence is high, skip the expensive search entirely so
	// clean inputs are returned byte-identical.
	if baseConf >= deskewGridMinConfidence {
		return img, info, baseGrid
	}

	// Low confidence — the mosaic may be rotated. Use block-homogeneity to
	// find the correcting angle; the GCD approach doesn't work on rotated images.
	info.Detected = true

	baseHomog := blockHomogeneity(img, deskewProbeBlockSize)
	bestAngle, bestHomog := searchBestAngle(img)
	info.AngleDeg = bestAngle

	gain := bestHomog - baseHomog
	if gain <= deskewHomogMinGain || bestHomog < deskewHomogMinScore {
		// Improvement insufficient — report detection but do NOT rotate.
		return img, info, baseGrid
	}

	rectified := rotateRGBA(img, bestAngle)

	// Re-detect the grid on the deskewed image; reuse it for BestConfidence and
	// for the caller's block-size inference.
	rectGrid, _ := InferBlockGrid(rectified)
	info.BestConfidence = rectGrid.Confidence
	info.Applied = true
	return rectified, info, rectGrid
}

// searchBestAngle scans angles from -deskewMaxAngleDeg to +deskewMaxAngleDeg
// in deskewStepDeg increments and returns the angle and block-homogeneity score
// that scored highest. It skips angle=0 (the caller already measured the baseline).
func searchBestAngle(img *image.RGBA) (bestAngle, bestScore float64) {
	steps := int(2*deskewMaxAngleDeg/deskewStepDeg) + 1
	bestScore = -1
	for i := range steps {
		angle := -deskewMaxAngleDeg + float64(i)*deskewStepDeg
		if math.Abs(angle) < deskewStepDeg/2 { // skip the baseline (already measured)
			continue
		}
		rotated := rotateRGBA(img, angle)
		score := blockHomogeneity(rotated, deskewProbeBlockSize)
		if score > bestScore {
			bestScore = score
			bestAngle = angle
		}
	}
	if bestScore < 0 {
		bestScore = 0
	}
	return bestAngle, bestScore
}

// blockHomogeneity measures how uniform an image is when partitioned into
// blockSize×blockSize tiles, returning a score in [0, 1] where 1.0 means every
// tile is perfectly constant-colour. It is the primary signal used during the
// deskew angle search: a clean axis-aligned mosaic scores near 1.0; a rotated
// mosaic scores low because the rotation mixes colours across tile boundaries.
//
// The score is 1 − (mean intra-block variance / maxVariance), where maxVariance
// is 255²/4 (the variance of a bimodal distribution with values at 0 and 255).
func blockHomogeneity(img *image.RGBA, blockSize int) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < blockSize || h < blockSize {
		return 0
	}

	const maxVar = 255.0 * 255.0 / 4 // bimodal 0/255 variance

	var totalVar, n float64
	for by := 0; by+blockSize <= h; by += blockSize {
		for bx := 0; bx+blockSize <= w; bx += blockSize {
			// Single-pass per-channel variance: accumulate Σx and Σx² over the
			// block, then var = Σx²/n − (Σx/n)². 8-bit values over a small block
			// keep the sums well within float64 precision.
			var sumR, sumG, sumB, sqR, sqG, sqB float64
			count := float64(blockSize * blockSize)
			for dy := range blockSize {
				for dx := range blockSize {
					c := img.RGBAAt(b.Min.X+bx+dx, b.Min.Y+by+dy)
					r, g, bl := float64(c.R), float64(c.G), float64(c.B)
					sumR, sumG, sumB = sumR+r, sumG+g, sumB+bl
					sqR, sqG, sqB = sqR+r*r, sqG+g*g, sqB+bl*bl
				}
			}
			mR, mG, mB := sumR/count, sumG/count, sumB/count
			vR := sqR/count - mR*mR
			vG := sqG/count - mG*mG
			vB := sqB/count - mB*mB
			// Mean variance over channels for this block.
			blockVar := (vR + vG + vB) / 3
			totalVar += blockVar
			n++
		}
	}
	if n == 0 {
		return 0
	}
	meanVar := totalVar / n
	score := 1 - meanVar/maxVar
	return max(0, min(1, score))
}

// rotateRGBA rotates src by angleDeg degrees counter-clockwise around its centre
// using nearest-neighbour interpolation and returns a new image of the same size.
// Pixels outside the source bounds are filled with white (0xFF), which matches
// the light background assumed by the rendering pipeline.
func rotateRGBA(src *image.RGBA, angleDeg float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	// Fill with white so unsampled border pixels look like background.
	imutil.FillWhite(dst)

	rad := angleDeg * math.Pi / 180
	cosA, sinA := math.Cos(rad), math.Sin(rad)
	cx, cy := float64(w)/2, float64(h)/2

	// Transform takes a dst→src affine matrix (inverse of the desired rotation).
	// To rotate dst by +angleDeg, we map each dst pixel back to src by rotating
	// by -angleDeg:
	//
	//   x_src = cos(θ)·x_dst + sin(θ)·y_dst + cx·(1−cos(θ)) − cy·sin(θ)
	//   y_src = −sin(θ)·x_dst + cos(θ)·y_dst + cx·sin(θ)   + cy·(1−cos(θ))
	m := f64.Aff3{
		cosA, sinA, cx*(1-cosA) - cy*sinA,
		-sinA, cosA, cx*sinA + cy*(1-cosA),
	}
	xdraw.NearestNeighbor.Transform(dst, m, src, b, xdraw.Src, nil)
	return dst
}
