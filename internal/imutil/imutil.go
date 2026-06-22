// Package imutil provides image manipulation helpers used by the unpixel pipeline.
package imutil

import (
	"image"
	"image/draw"
)

// FillWhite fills every pixel of img with opaque white (all channels = 0xFF).
// It operates as a memset over img.Pix and is faster than per-pixel SetRGBA.
func FillWhite(img *image.RGBA) {
	// Exponential copy (memmove) instead of a per-byte loop: the compiler only
	// lowers all-zero fills to memclr, so 0xFF needs this to avoid a byte loop.
	p := img.Pix
	if len(p) == 0 {
		return
	}
	p[0] = 0xFF
	for n := 1; n < len(p); n *= 2 {
		copy(p[n:], p[:n])
	}
}

// Crop returns a new *image.RGBA containing the sub-rectangle of src starting
// at (x, y) with the given width and height. The result is clamped to src's
// bounds; pixels outside are not included.
func Crop(src *image.RGBA, x, y, w, h int) *image.RGBA {
	b := src.Bounds()
	x0 := max(b.Min.X, b.Min.X+x)
	y0 := max(b.Min.Y, b.Min.Y+y)
	x1 := min(b.Max.X, x0+w)
	y1 := min(b.Max.Y, y0+h)
	dw := max(0, x1-x0)
	dh := max(0, y1-y0)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	if dw == 0 || dh == 0 {
		return dst
	}
	rowBytes := dw * 4
	for row := range dh {
		srcOff := src.PixOffset(x0, y0+row)
		copy(dst.Pix[row*dst.Stride:row*dst.Stride+rowBytes], src.Pix[srcOff:srcOff+rowBytes])
	}
	return dst
}

// PadWhite returns a new *image.RGBA of size newW×newH with src composited at
// the top-left. Any area beyond src is filled with opaque white.
func PadWhite(src *image.RGBA, newW, newH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	FillWhite(dst)
	Compose(dst, src, 0, 0)
	return dst
}

// CropInto writes the sub-rectangle of src starting at (x, y) with the given
// width and height into dst, resizing dst in place as needed. The result is
// clamped to src's bounds. It is the zero-allocation equivalent of Crop for
// callers that own a reusable scratch buffer. dst may be nil; a new image is
// allocated in that case.
func CropInto(dst *image.RGBA, src *image.RGBA, x, y, w, h int) *image.RGBA {
	b := src.Bounds()
	x0 := max(b.Min.X, b.Min.X+x)
	y0 := max(b.Min.Y, b.Min.Y+y)
	x1 := min(b.Max.X, x0+w)
	y1 := min(b.Max.Y, y0+h)
	dw := max(0, x1-x0)
	dh := max(0, y1-y0)
	dst = resizeRGBA(dst, dw, dh)
	if dw == 0 || dh == 0 {
		return dst
	}
	rowBytes := dw * 4
	for row := range dh {
		srcOff := src.PixOffset(x0, y0+row)
		copy(dst.Pix[row*dst.Stride:row*dst.Stride+rowBytes], src.Pix[srcOff:srcOff+rowBytes])
	}
	return dst
}

// PadWhiteInto writes src composited at the top-left of a newW×newH white
// canvas into dst, resizing dst in place as needed. It is the zero-allocation
// equivalent of PadWhite for callers that own a reusable scratch buffer.
// dst may be nil; a new image is allocated in that case.
func PadWhiteInto(dst *image.RGBA, src *image.RGBA, newW, newH int) *image.RGBA {
	dst = resizeRGBA(dst, newW, newH)
	FillWhite(dst)
	Compose(dst, src, 0, 0)
	return dst
}

// resizeRGBA returns dst if it already has the requested size and origin (0,0);
// otherwise it allocates a new *image.RGBA. Callers that want in-place reuse
// should replace their pointer: dst = resizeRGBA(dst, w, h).
func resizeRGBA(dst *image.RGBA, w, h int) *image.RGBA {
	want := image.Rect(0, 0, w, h)
	if dst != nil && dst.Bounds() == want {
		return dst
	}
	return image.NewRGBA(want)
}

// Compose blits src onto dst at offset (offX, offY), clipped to dst's bounds.
// It delegates to the stdlib image/draw package which has an RGBA fast-path.
func Compose(dst, src *image.RGBA, offX, offY int) {
	sb := src.Bounds()
	r := image.Rect(offX, offY, offX+sb.Dx(), offY+sb.Dy())
	draw.Draw(dst, r, src, sb.Min, draw.Src)
}

// BlueMargin scans the middle row of img to find the first pure-blue pixel
// (B=255, R≠255, G≠255) and returns its x-coordinate as margin. It then scans
// that column to find the vertical extent of the blue block and returns its
// integer center. Both are 0 if no blue pixel is found.
//
// faithful: main.ts getBlueMargin — middle-row scan then column scan at margin+5.
func BlueMargin(img *image.RGBA) (margin, center int) {
	b := img.Bounds()
	midY := b.Min.Y + b.Dy()/2

	// Scan middle row for first blue pixel.
	found := false
	for x := b.Min.X; x < b.Max.X; x++ {
		c := img.RGBAAt(x, midY)
		if c.B == 255 && c.R != 255 && c.G != 255 {
			margin = x
			found = true
			break
		}
	}
	if !found {
		return 0, 0
	}

	// Scan the column at margin+5 for vertical extent of the blue block.
	// faithful: main.ts scans at margin+5 to be safely inside the blue box.
	scanX := min(margin+5, b.Max.X-1)
	topBlue, botBlue := 0, 0
	inBlue := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		c := img.RGBAAt(scanX, y)
		isBlue := c.B == 255 && c.R != 255 && c.G != 255
		if !inBlue && isBlue {
			inBlue = true
			topBlue = y
		}
		if inBlue && !isBlue {
			inBlue = false
			botBlue = y
		}
	}
	// Handle blue extending to the bottom edge.
	if inBlue {
		botBlue = b.Max.Y
	}
	center = (topBlue + botBlue) / 2
	return margin, center
}

// LeftEdge returns the x-coordinate of the first column that contains any
// non-white pixel (R≠255 or G≠255 or B≠255) scanning the full image height.
// Returns 0 if the image is entirely white.
//
// faithful: main.ts getLeftEdge — full-image scan, minimum x of non-white pixel.
func LeftEdge(img *image.RGBA) int {
	b := img.Bounds()
	leftEdge := b.Max.X
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < leftEdge; x++ {
			c := img.RGBAAt(x, y)
			if c.R != 255 || c.G != 255 || c.B != 255 {
				leftEdge = x
			}
		}
	}
	if leftEdge == b.Max.X {
		return 0
	}
	return leftEdge
}

// Margins scans the middle row of img for the first red pixel (R=255, G≠255,
// B≠255) and returns its x-coordinate. Returns 0 if none is found.
//
// faithful: main.ts getMargins — the diff image is red where pixels differ.
func Margins(img *image.RGBA) int {
	b := img.Bounds()
	midY := b.Min.Y + b.Dy()/2
	for x := b.Min.X; x < b.Max.X; x++ {
		c := img.RGBAAt(x, midY)
		if c.R == 255 && c.G != 255 && c.B != 255 {
			return x
		}
	}
	return 0
}
