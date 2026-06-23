// Package multiframe implements multi-frame sub-pixel fusion for mosaic-pixelated
// images. When the same hidden content is pixelated at several DIFFERENT grid
// phases (e.g. consecutive video frames where the content scrolls sub-block, or
// multiple crops of the same redaction), each frame's block-means sample the
// underlying image at a different sub-block offset. Stacking phase-diverse
// block-means lets us solve for a higher-resolution reconstruction than any
// single frame — this adds real information via deconvolution of the known
// box-average operator, not hallucination.
//
// Method — iterative back-projection (IBP):
//
//  1. Initialise the estimate H from the first frame (nearest-neighbour upsample).
//  2. For each iteration, for each frame f:
//     a. Re-pixelate H at frame f's phase offset → simulated mosaic S_f.
//     b. Compute the residual R_f = observed_f − S_f (per block mean).
//     c. Distribute R_f back to every pixel that contributed to each block
//     (additive back-projection, uniform weight 1/blockPixels).
//  3. Clamp the estimate to [0, 255] after each iteration.
//
// Convergence: the residual shrinks per iteration as the estimate's block
// means approach the observed ones. With F phase-diverse frames each
// contributing independent constraints, the system is over-determined and
// IBP converges to the least-squares solution. With F=1 it trivially
// converges in one iteration to a constant-block image matching the single
// frame (no invented detail). With more frames, sub-block structure is
// recovered that no single frame can reveal.
//
// Usage:
//
//	frames := []multiframe.Frame{
//	    {Img: mosaic0, OffsetX: 0, OffsetY: 0},
//	    {Img: mosaic1, OffsetX: 3, OffsetY: 0},
//	}
//	fused, err := multiframe.Fuse(frames, blockSize)
package multiframe

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// Frame is one observed mosaic of the shared hidden content at a known
// sub-block grid phase offset. OffsetX and OffsetY are the pixel offsets at
// which the pixelation grid was aligned when this frame was produced — they
// are passed directly to [pixelate.BlockAverage.Pixelate] as originX/originY.
type Frame struct {
	// Img is the mosaic-pixelated observation. It must be non-nil and have
	// the same bounds as all other frames in the Fuse call.
	Img image.Image
	// OffsetX is the horizontal sub-block grid phase in pixels.
	OffsetX int
	// OffsetY is the vertical sub-block grid phase in pixels.
	OffsetY int
}

// defaultIterations is the number of IBP iterations used when the caller does
// not specify (via [FuseN]). Three passes are enough for the residual to drop
// below the 8-bit quantisation floor in most practical cases.
const defaultIterations = 3

// Fuse reconstructs a higher-resolution estimate of the hidden image from
// multiple phase-diverse mosaics, given the block size. It returns an
// *image.RGBA at the original pixel resolution whose block-mean structure is
// consistent with all input frames.
//
// frames must be non-empty; every frame's Img must be non-nil and must cover
// at least the same top-left w×h region (where w and h come from the first
// frame). block must be ≥ 1. When len(frames)==1, Fuse returns that frame's
// content without any invented sub-block detail. Fuse is deterministic: the
// same inputs always produce the same output.
func Fuse(frames []Frame, block int) (*image.RGBA, error) {
	return FuseN(frames, block, defaultIterations)
}

// FuseN is like [Fuse] but lets the caller control the number of IBP
// iterations. iterations=0 returns a plain nearest-neighbour upsample of the
// first frame (equivalent to a single-frame pass with no back-projection).
func FuseN(frames []Frame, block, iterations int) (*image.RGBA, error) {
	if len(frames) == 0 {
		return nil, errors.New("multiframe: no frames provided")
	}
	if block < 1 {
		return nil, errors.New("multiframe: block size must be ≥ 1")
	}
	for i, f := range frames {
		if f.Img == nil {
			return nil, fmt.Errorf("multiframe: frame %d has nil image", i)
		}
	}

	// Determine output dimensions from the first frame.
	b0 := frames[0].Img.Bounds()
	imgW, imgH := b0.Dx(), b0.Dy()

	// Initialise the floating-point estimate from the first frame (bilinear
	// upsample is unnecessary since all frames are already at full resolution —
	// the mosaic itself has the same width/height as the source; each block of
	// block×block pixels carries the same colour).
	// estR/G/B hold float64 per-pixel accumulators in [0, 255].
	estR := make([]float64, imgW*imgH)
	estG := make([]float64, imgW*imgH)
	estB := make([]float64, imgW*imgH)

	f0rgba := toRGBAView(frames[0].Img, imgW, imgH)
	for y := range imgH {
		for x := range imgW {
			c := f0rgba.RGBAAt(x, y)
			i := y*imgW + x
			estR[i] = float64(c.R)
			estG[i] = float64(c.G)
			estB[i] = float64(c.B)
		}
	}

	// Pre-convert all frames to *image.RGBA for fast pixel access.
	frameRGBA := make([]*image.RGBA, len(frames))
	for i, f := range frames {
		frameRGBA[i] = toRGBAView(f.Img, imgW, imgH)
	}

	pixer := pixelate.NewBlockAverage(block)
	scratch := image.NewRGBA(image.Rect(0, 0, imgW, imgH))

	for range iterations {
		// One back-projection sweep over all frames.
		for fi, f := range frames {
			// Build current estimate as *image.RGBA.
			fillRGBA(scratch, estR, estG, estB, imgW, imgH)

			// Re-pixelate the estimate at this frame's phase.
			simulated := pixer.Pixelate(scratch, f.OffsetX, f.OffsetY)
			observed := frameRGBA[fi]

			// For each block: compute residual (observed − simulated) and add
			// it uniformly back to every pixel in the block.
			projectResidual(estR, estG, estB, observed, simulated, imgW, imgH, block, f.OffsetX, f.OffsetY)
		}

		// Clamp to [0, 255] after each full sweep.
		for i := range imgW * imgH {
			estR[i] = min(max(estR[i], 0), 255)
			estG[i] = min(max(estG[i], 0), 255)
			estB[i] = min(max(estB[i], 0), 255)
		}
	}

	// Render final estimate.
	out := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	for y := range imgH {
		for x := range imgW {
			i := y*imgW + x
			out.SetRGBA(x, y, rgbaFrom(estR[i], estG[i], estB[i]))
		}
	}
	return out, nil
}

// projectResidual distributes the per-block residual (observed − simulated)
// back to every pixel in that block within the estimate buffers.
//
// For block b at grid position (bx, by):
//
//	residual_c = mean(observed_c in b) − mean(simulated_c in b)
//	             = observed_block_mean_c − simulated_block_mean_c
//
// Because the simulated mosaic is block-constant at the simulated mean, and
// the observed mosaic is block-constant at the observed mean, the per-pixel
// residual is just the block-mean residual, added uniformly to every pixel.
func projectResidual(
	estR, estG, estB []float64,
	observed, simulated *image.RGBA,
	w, h, block, offX, offY int,
) {
	// Iterate over the same block grid that pixelate.BlockAverage uses.
	startX := gridStart(offX, block)
	startY := gridStart(offY, block)

	for by := startY; by < h; by += block {
		y0, y1 := max(by, 0), min(by+block, h)
		if y0 >= y1 {
			continue
		}
		for bx := startX; bx < w; bx += block {
			x0, x1 := max(bx, 0), min(bx+block, w)
			if x0 >= x1 {
				continue
			}

			// Sample the block mean from observed and simulated at the top-left
			// pixel of the block (they are both block-constant, so any pixel
			// in the block gives the same value).
			oc := observed.RGBAAt(x0, y0)
			sc := simulated.RGBAAt(x0, y0)

			dR := float64(oc.R) - float64(sc.R)
			dG := float64(oc.G) - float64(sc.G)
			dB := float64(oc.B) - float64(sc.B)

			// Add the residual to every pixel in the block.
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					i := y*w + x
					estR[i] += dR
					estG[i] += dG
					estB[i] += dB
				}
			}
		}
	}
}

// gridStart returns the first block origin ≤ 0 in the same grid as offset,
// mirroring the logic in pixelate.BlockAverage.Pixelate.
func gridStart(offset, block int) int {
	s := offset - (offset/block)*block
	if s > 0 {
		s -= block
	}
	return s
}

// fillRGBA writes estR/G/B (float64 in [0,255]) into dst as uint8 RGBA pixels
// with alpha=255.
func fillRGBA(dst *image.RGBA, estR, estG, estB []float64, w, h int) {
	for y := range h {
		for x := range w {
			i := y*w + x
			c := rgbaFrom(estR[i], estG[i], estB[i])
			dst.SetRGBA(x, y, c)
		}
	}
}

// rgbaFrom converts float64 channel values to color.RGBA, clamping to [0,255].
func rgbaFrom(r, g, b float64) color.RGBA {
	return color.RGBA{
		R: uint8(math.Round(min(max(r, 0), 255))),
		G: uint8(math.Round(min(max(g, 0), 255))),
		B: uint8(math.Round(min(max(b, 0), 255))),
		A: 255,
	}
}

// toRGBAView returns the image as *image.RGBA cropped/copied to exactly w×h.
// If the image already is *image.RGBA with bounds (0,0,w,h) it is returned
// as-is (zero alloc). Otherwise a copy is made.
func toRGBAView(img image.Image, w, h int) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		b := r.Bounds()
		if b.Min.X == 0 && b.Min.Y == 0 && b.Dx() == w && b.Dy() == h {
			return r
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			dst.Set(x, y, img.At(x, y))
		}
	}
	return dst
}
