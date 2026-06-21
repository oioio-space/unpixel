package imutil

import (
	"image"
)

// Median applies a 2D median filter to src and returns a new *image.RGBA.
//
// The filter replaces each pixel with the median of all pixels in the
// (2·radius+1)² neighbourhood centred on it. Channels R, G, B, and A are
// filtered independently. Out-of-bounds samples are clamped to the nearest
// border pixel (clamp-to-border padding), which preserves edges at image
// boundaries without introducing a dark or light halo.
//
// A median filter removes isolated salt-and-pepper impulse noise and JPEG
// speckle while preserving edges far better than a box or Gaussian blur —
// a sharp step remains sharp because the median of a neighbourhood that
// straddles the edge equals one of the two values on either side, not an
// average.
//
// radius controls the kernel size:
//   - radius=1 → 3×3 kernel (removes single-pixel spikes)
//   - radius=2 → 5×5 kernel (removes larger speckle clusters)
//   - radius≤0 → returns a pixel-identical copy with no filtering
//   - radius>7  → clamped to 7 (max effective radius; the fixed [225]byte
//     window supports up to (2·7+1)²=225 samples)
//
// Intended use: run Median on a noisy capture before calling
// InferBlockSize, LocateRedaction, or InferBlurSigma to improve detection
// on images with JPEG compression artefacts or capture noise.
func Median(src *image.RGBA, radius int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	// srcBase is the offset from src.Pix[0] to the pixel at b.Min.
	// For a normal *image.RGBA, b.Min == src.Rect.Min == (0,0), so srcBase == 0.
	// For a sub-image (e.g. from SubImage), src.Pix already starts at the
	// sub-image's first pixel in memory, so PixOffset(b.Min.X, b.Min.Y) == 0
	// as well. Using src.PixOffset handles both cases correctly.
	srcBase := src.PixOffset(b.Min.X, b.Min.Y)

	if radius <= 0 || w == 0 || h == 0 {
		// Per-row copy honours a non-zero bounds origin (sub-image/Crop).
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		rowBytes := w * 4
		for y := range h {
			srcOff := srcBase + y*src.Stride
			dstOff := y * dst.Stride
			copy(dst.Pix[dstOff:dstOff+rowBytes], src.Pix[srcOff:srcOff+rowBytes])
		}
		return dst
	}
	if radius > 7 {
		radius = 7
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	// Four fixed-size windows, one per channel. The array cap 225 supports up
	// to radius=7 ((2·7+1)²=225). Stack-allocated; reused across every pixel
	// — zero per-pixel allocation.
	var rw, gw, bw, aw [225]byte

	srcStride := src.Stride
	srcPix := src.Pix
	dstPix := dst.Pix

	for y := range h {
		for x := range w {
			n := 0
			for ky := -radius; ky <= radius; ky++ {
				sy := max(0, min(y+ky, h-1))
				rowOff := srcBase + sy*srcStride
				for kx := -radius; kx <= radius; kx++ {
					sx := max(0, min(x+kx, w-1))
					off := rowOff + sx*4
					rw[n] = srcPix[off]
					gw[n] = srcPix[off+1]
					bw[n] = srcPix[off+2]
					aw[n] = srcPix[off+3]
					n++
				}
			}
			mid := n / 2
			insertionSortBytes(rw[:n])
			insertionSortBytes(gw[:n])
			insertionSortBytes(bw[:n])
			insertionSortBytes(aw[:n])

			doff := y*dst.Stride + x*4
			dstPix[doff] = rw[mid]
			dstPix[doff+1] = gw[mid]
			dstPix[doff+2] = bw[mid]
			dstPix[doff+3] = aw[mid]
		}
	}

	return dst
}

// insertionSortBytes sorts a small byte slice in-place using insertion sort.
// For the kernel sizes used here (≤225 elements, typically 9 or 25) this is
// faster than sort.Slice because it avoids interface calls and function-literal
// overhead, and the data fits in a few cache lines.
func insertionSortBytes(s []byte) {
	for i := 1; i < len(s); i++ {
		v := s[i]
		j := i - 1
		for j >= 0 && s[j] > v {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = v
	}
}
