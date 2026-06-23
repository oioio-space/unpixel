// Package pixelate (blurmosaic.go) adds the BlurMosaic composite operator
// implementing the Hill–Zhou–Saul–Shacham PETS-2016 recommendation (§4):
// "mosaicing as error-correction for blur".
//
// The insight: Gaussian blur at σ and a slightly-different σ' produce different
// pixel values, but re-mosaicing both at block size b ≈ σ collapses them to
// near-identical block images. The forward operator
//
//	render(candidate) → GaussianBlur(σ) → BlockAverage(b)
//
// is therefore robust to σ-mismatch and JPEG noise, because the mosaic stage
// equalises small pixel-level deviations.
package pixelate

import "image"

// BlurMosaic implements unpixel.Pixelator as a composite of GaussianBlur
// followed by BlockAverage. It is the forward operator recommended by Hill et al.
// PETS-2016 §4 for recover-with-remosaic: render a candidate, blur it at σ,
// then block-average at grid size b — the resulting block image is compared
// against a pre-mosaiced version of the target, making the comparison robust to
// σ-mismatch and JPEG compression artefacts.
//
// Use [NewBlurMosaic] to construct one; zero value is not usable.
type BlurMosaic struct {
	blur  *GaussianBlur
	block *BlockAverage
}

// NewBlurMosaic returns a BlurMosaic that blurs at sigma then block-averages at
// the given block size. When linear is true the block-average step uses linear-
// light colour space (matching GEGL/GIMP pixelation); false uses sRGB means.
// sigma is clamped to ≥ 0.1 by [NewGaussianBlur]; block must be ≥ 1.
func NewBlurMosaic(sigma float64, block int, linear bool) *BlurMosaic {
	if block < 1 {
		block = 1
	}
	var ba *BlockAverage
	if linear {
		ba = NewLinearBlockAverage(block)
	} else {
		ba = NewBlockAverage(block)
	}
	return &BlurMosaic{
		blur:  NewGaussianBlur(sigma),
		block: ba,
	}
}

// Pixelate applies GaussianBlur(σ) then BlockAverage(b) to src. The origin
// arguments are forwarded to the BlockAverage stage (the blur stage ignores
// them, as Gaussian blur has no block grid).
func (bm *BlurMosaic) Pixelate(src *image.RGBA, originX, originY int) *image.RGBA {
	blurred := bm.blur.Pixelate(src, 0, 0)
	return bm.block.Pixelate(blurred, originX, originY)
}

// Sigma returns the Gaussian standard deviation used by this operator.
func (bm *BlurMosaic) Sigma() float64 { return bm.blur.Sigma() }

// BlockSize returns the block-average grid size used by this operator.
func (bm *BlurMosaic) BlockSize() int { return bm.block.blockSize }
