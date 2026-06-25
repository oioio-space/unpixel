package pixelate

import (
	"image"
	"math"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// DetectColorspace inspects a mosaic-pixelated image and reports whether its
// blocks were averaged in linear light (true) or in the (gamma-encoded) sRGB
// space (false), together with a confidence in [0, 1].
//
// # How it works
//
// For each mosaic block with mean luminance v, two predictions are computed:
//
//   - delta_to_linear  = linearToSRGB8(v/255) − v
//     (how much lighter v would become if treated as a linear fill fraction
//     and re-encoded to sRGB)
//
//   - delta_from_linear = v − round(srgbToLinear[v]·255)
//     (how much darker v would become if treated as a linear-space value
//     and projected back to sRGB)
//
// The ratio R = mean(delta_from_linear) / mean(delta_to_linear) discriminates
// the two modes across typical text-on-white mosaics:
//
//   - sRGB-averaged mosaic: R ≈ 1.0–1.3 (both deltas are similar in magnitude)
//   - Linear-averaged mosaic: R ≈ 1.4–1.8 (delta_from_linear dominates)
//
// Decision boundary: R > 1.4 → linear.
//
// Confidence is derived from the Jensen gap — the non-negative quantity
//
//	jg = linearToSRGB8(mean(srgbToLinear[v])) − mean(v)  [8-bit units]
//
// which equals zero for uniform or near-white images (no discriminating signal)
// and grows with the block-value variance:
//
//	confidence = tanh(jg / 5)
//
// # Caveats
//
//   - Reliable for text-on-white and similar content where blocks span a
//     substantial brightness range (jg > 3 → confidence > 0.5). Uniform,
//     near-white, or heavily dark images return confidence ≈ 0.
//   - Does not require a candidate render; no font or charset needed.
//   - JPEG noise is reduced by averaging pixels within each block.
//   - The block grid is assumed to start at (0, 0) with no grid phase, matching
//     the standard unpixel pipeline where the redacted region is a tight crop.
//
// block must be ≥ 1. Panics if block < 1.
func DetectColorspace(redacted *image.RGBA, block int) (linear bool, confidence float64) {
	if block < 1 {
		panic("pixelate.DetectColorspace: block must be ≥ 1")
	}
	b := redacted.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return false, 0
	}

	stride := redacted.Stride
	pix := redacted.Pix
	ox := b.Min.X
	oy := b.Min.Y

	var sumToLin, sumFromLin float64 // delta accumulators
	var sumSRGB, sumLin float64      // for Jensen gap
	nBlocks := 0

	for by := 0; by < h; by += block {
		y0, y1 := by, min(by+block, h)
		for bx := 0; bx < w; bx += block {
			x0, x1 := bx, min(bx+block, w)
			n := (x1 - x0) * (y1 - y0)
			if n == 0 {
				continue
			}

			// Average RGBA of this block (int accumulation to avoid float per-pixel).
			var rSum, gSum, bSum int
			for y := oy + y0; y < oy+y1; y++ {
				off := y*stride + (ox+x0)*4
				for range x1 - x0 {
					rSum += int(pix[off])
					gSum += int(pix[off+1])
					bSum += int(pix[off+2])
					off += 4
				}
			}
			// Block means and their BT.601 luminance. Each mean is an average of
			// byte channels, so it is provably in [0,255] — the uint8 conversions
			// cannot overflow.
			mr, mg, mb := rSum/n, gSum/n, bSum/n
			lum := uint8(imutil.Lum601(uint8(mr), uint8(mg), uint8(mb))) // #nosec G115 -- means of bytes, always in [0,255]
			lumF := float64(lum)

			// delta_to_linear: how much lighter if lum were a linear fill fraction.
			predLin := float64(linearToSrgb8(lumF / 255))
			sumToLin += predLin - lumF

			// delta_from_linear: how much darker if lum were a linear-encoded value.
			predSRGB := math.Round(srgbToLinear[lum] * 255)
			sumFromLin += lumF - predSRGB

			// Jensen gap accumulators.
			sumSRGB += lumF
			sumLin += srgbToLinear[lum]
			nBlocks++
		}
	}

	if nBlocks == 0 {
		return false, 0
	}

	// Jensen gap: measures the block-value variance that drives confidence.
	// jg ≥ 0 always; jg = 0 for uniform or near-white images.
	meanSRGB := sumSRGB / float64(nBlocks)
	meanLin := sumLin / float64(nBlocks)
	jg := float64(linearToSrgb8(meanLin)) - meanSRGB
	jg = max(jg, 0)

	// Confidence: sigmoid of jg. Zero when no signal; near 1 for rich content.
	// tanh(jg/5): tanh(0.6)=0.54 at jg=3, tanh(1.8)=0.97 at jg=9, ~1 at jg≥15.
	confidence = math.Tanh(jg / 5.0)

	// Decision: ratio R = mean_dfl / mean_dtl.
	// R > 1.4 → linear. Threshold calibrated on text-on-white mosaics:
	//   sRGB mode produces R ≈ 1.0–1.3; linear mode produces R ≈ 1.4–1.8.
	meanDTL := sumToLin / float64(nBlocks)
	meanDFL := sumFromLin / float64(nBlocks)
	if meanDTL <= 0 {
		// No upward delta: image is at or near linear-max. Treat as low signal.
		return false, 0
	}
	ratio := meanDFL / meanDTL
	const ratioThreshold = 1.4
	linear = ratio > ratioThreshold
	return linear, confidence
}
