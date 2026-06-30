package defaults_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// cleanAndMosaic renders text and returns (clean image padded to block-width,
// its mosaic). The clean image is pre-padded so its width is already a multiple
// of block — exactly matching the width that BlockAverage.Pixelate produces —
// so verifyImageCore's re-pixelation of the clean image produces distance ≈ 0
// against the mosaic (pipeline-faithful fixture).
func cleanAndMosaic(t *testing.T, text string, block int) (*image.RGBA, image.Image) {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	clean, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}

	// Pre-pad to block-multiple width so clean.Bounds() == mosaic.Bounds().
	// BlockAverage.Pixelate pads the source to the same multiple before averaging,
	// so this makes the clean image and the mosaic share the same rectangle and the
	// re-pixelation distance is exactly 0 (no CatmullRom resize noise).
	w := clean.Bounds().Dx()
	if rem := block - (w % block); rem < block {
		clean = imutil.PadWhite(clean, w+rem, clean.Bounds().Dy())
	}

	mosaic := pixelate.NewBlockAverage(block).Pixelate(clean, 0, 0)
	return clean, mosaic
}

func TestVerifyImage_acceptsTrueRejectsHallucination(t *testing.T) {
	const block = 6
	clean, mosaic := cleanAndMosaic(t, "the", block)

	// The true clean image re-pixelates back to the observed mosaic → Match.
	good, err := unpixel.VerifyImage(t.Context(), mosaic, clean, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(true): %v", err)
	}
	if !good.Match {
		t.Errorf("true restoration not a Match (distance %.4f)", good.Distance)
	}

	// A different clean image (wrong text) does not re-pixelate to the mosaic.
	wrongClean, _ := cleanAndMosaic(t, "xyz", block)
	bad, err := unpixel.VerifyImage(t.Context(), mosaic, wrongClean, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(wrong): %v", err)
	}
	if bad.Match {
		t.Errorf("hallucinated restoration unexpectedly Match (distance %.4f)", bad.Distance)
	}
	if !(bad.Distance > good.Distance) {
		t.Errorf("wrong distance %.4f should exceed true distance %.4f", bad.Distance, good.Distance)
	}
}

func TestVerifyImage_resizesRestored(t *testing.T) {
	const block = 6
	clean, mosaic := cleanAndMosaic(t, "the", block)
	// Upscale the clean image 2× so VerifyImage must resize it back.
	big := image.NewRGBA(image.Rect(0, 0, clean.Bounds().Dx()*2, clean.Bounds().Dy()*2))
	// nearest-neighbour blow-up is fine for the test; just exercise the resize path.
	for y := range big.Bounds().Dy() {
		for x := range big.Bounds().Dx() {
			big.Set(x, y, clean.At(x/2, y/2))
		}
	}
	v, err := unpixel.VerifyImage(t.Context(), mosaic, big, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(resized): %v", err)
	}
	// Observed distance after CatmullRom downscale of a 2× nearest-neighbour
	// blow-up is 0.0000; threshold of 0.05 catches a broken resize path while
	// leaving comfortable headroom for interpolation noise.
	if v.Distance > 0.05 {
		t.Errorf("resized true restoration distance %.4f too high (resize path broken?)", v.Distance)
	}
}

// solidRGBA returns a new *image.RGBA of the given dimensions filled with c.
func solidRGBA(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		switch i % 4 {
		case 0:
			img.Pix[i] = c.R
		case 1:
			img.Pix[i] = c.G
		case 2:
			img.Pix[i] = c.B
		case 3:
			img.Pix[i] = c.A
		}
	}
	return img
}

// TestVerifyImage_nonBlockMultipleWidthRejectsGarbage guards against the C1
// false-accept: when redacted's width is NOT a multiple of blockSize,
// BlockAverage.Pixelate pads reMosaic to the next block multiple, causing
// reMosaic.Bounds() != redacted.Bounds(). Before the fix, metric.Compare
// short-circuits on unequal bounds and returns 0.0 — so ANY restored image,
// including pure garbage, was accepted (Match=true). After the fix, reMosaic is
// cropped back to the real region before Compare, so garbage gets a high
// distance and is correctly rejected.
func TestVerifyImage_nonBlockMultipleWidthRejectsGarbage(t *testing.T) {
	const block = 6

	// Build a faithful mosaic from text, then shave 2 px from the right so the
	// redaction width is NOT a multiple of block (e.g. 100 → 98, not ÷ 6).
	clean, mosaicFull := cleanAndMosaic(t, "hello", block)
	mosaicRGBA, ok := mosaicFull.(*image.RGBA)
	if !ok {
		t.Fatal("cleanAndMosaic: expected *image.RGBA mosaic")
	}
	mb := mosaicRGBA.Bounds()
	// Shave until width is not a multiple of block.
	shaved := mb.Dx()
	for shaved%block == 0 {
		shaved--
	}
	redacted := imutil.Crop(mosaicRGBA, 0, 0, shaved, mb.Dy())
	if redacted.Bounds().Dx()%block == 0 {
		t.Fatalf("setup error: redacted width %d is still a multiple of block %d", redacted.Bounds().Dx(), block)
	}

	// Garbage restored: solid red, same crop size.
	garbage := solidRGBA(shaved, mb.Dy(), color.RGBA{R: 0xFF, A: 0xFF})

	got, err := unpixel.VerifyImage(t.Context(), redacted, garbage, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(garbage, non-multiple width): %v", err)
	}
	// Before the fix: Compare short-circuits on unequal bounds → distance 0.0,
	// Match=true. After the fix: garbage gets a real (high) distance → Match=false.
	if got.Match {
		t.Errorf("C1 false-accept: garbage restoration Match=true (distance %.4f) for non-block-multiple width %d (block %d)",
			got.Distance, shaved, block)
	}
	if got.Distance < 0.1 {
		t.Errorf("C1 false-accept: garbage distance %.4f suspiciously low for non-block-multiple width %d",
			got.Distance, shaved)
	}

	// The true clean (cropped to same shaved width) must still Match.
	cleanCropped := imutil.Crop(clean, 0, 0, shaved, clean.Bounds().Dy())
	good, err := unpixel.VerifyImage(t.Context(), redacted, cleanCropped, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(true, non-multiple width): %v", err)
	}
	if !good.Match {
		t.Errorf("true restoration not a Match (distance %.4f) for non-block-multiple width %d",
			good.Distance, shaved)
	}
}

// TestVerifyImage_nonZeroMinRejectsGarbage guards against the C1 false-accept
// when redacted is an *image.RGBA whose Bounds().Min is not (0,0) — e.g. a
// SubImage slice. Before the fix, reMosaic was compared against the original
// (non-zero-Min) redacted; the bounds mismatch (reMosaic is always zero-Min)
// caused metric.Compare to return 0.0, accepting any garbage. After the fix,
// verifyImageCore normalises redacted to zero-Min first.
func TestVerifyImage_nonZeroMinRejectsGarbage(t *testing.T) {
	const block = 6

	// Build a mosaic that is a clean block multiple, then take a SubImage whose
	// Min is (block, 0) — non-zero, and whose width is still a block multiple so
	// the only source of the bug is the non-zero Min.
	clean, mosaicFull := cleanAndMosaic(t, "world", block)
	mosaicRGBA, ok := mosaicFull.(*image.RGBA)
	if !ok {
		t.Fatal("cleanAndMosaic: expected *image.RGBA mosaic")
	}
	mb := mosaicRGBA.Bounds()
	if mb.Dx() < 2*block {
		t.Skip("mosaic too narrow for non-zero-Min SubImage test")
	}

	// SubImage from x=block onward, preserving block-multiple width.
	subW := ((mb.Dx() - block) / block) * block
	subRect := image.Rect(mb.Min.X+block, mb.Min.Y, mb.Min.X+block+subW, mb.Max.Y)
	// SubImage returns image.Image; assert to *image.RGBA to exercise the
	// imutil.ToRGBA fast-path that returns it unchanged (non-zero Min preserved).
	redacted := mosaicRGBA.SubImage(subRect).(*image.RGBA)
	if redacted.Bounds().Min == (image.Point{}) {
		t.Fatal("setup error: SubImage Min is (0,0); test would not exercise the non-zero-Min path")
	}

	dx := redacted.Bounds().Dx()
	dy := redacted.Bounds().Dy()
	garbage := solidRGBA(dx, dy, color.RGBA{R: 0xFF, A: 0xFF})

	got, err := unpixel.VerifyImage(t.Context(), redacted, garbage, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(garbage, non-zero Min): %v", err)
	}
	// Before the fix: reMosaic (zero-Min) vs redacted (non-zero-Min) → bounds
	// mismatch → distance 0.0, Match=true. After the fix: redZero normalises the
	// redacted, so garbage receives a real high distance → Match=false.
	if got.Match {
		t.Errorf("C1 false-accept: garbage restoration Match=true (distance %.4f) for non-zero-Min redacted %v",
			got.Distance, redacted.Bounds())
	}
	if got.Distance < 0.1 {
		t.Errorf("C1 false-accept: garbage distance %.4f suspiciously low for non-zero-Min redacted %v",
			got.Distance, redacted.Bounds())
	}

	// The true clean crop at the same sub-region must still Match.
	cleanSub := imutil.Crop(clean, block, 0, subW, clean.Bounds().Dy())
	good, err := unpixel.VerifyImage(t.Context(), redacted, cleanSub, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(true, non-zero Min): %v", err)
	}
	if !good.Match {
		t.Errorf("true restoration not a Match (distance %.4f) for non-zero-Min redacted %v",
			good.Distance, redacted.Bounds())
	}
}
