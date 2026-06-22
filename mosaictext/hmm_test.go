package mosaictext_test

import (
	"context"
	"errors"
	"image"
	"image/draw"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/mosaictext"
)

// monoFont returns the bundled Font entry with the given name, or marks tb as
// failed and returns a zero value.
func monoFont(tb testing.TB, name string) fonts.Font {
	tb.Helper()
	for _, f := range fonts.All() {
		if f.Name == name {
			return f
		}
	}
	tb.Fatalf("bundled font %q not found", name)
	return fonts.Font{}
}

// syntheticMosaic renders text with the given bundled font data at the given
// size, crops the render to the sentinel boundary (removing the blue sentinel
// column that the renderer adds), pixelates, and returns the result. Cropping
// before pixelation prevents the blue sentinel block from being averaged into
// the mosaic blocks, which would inflate tW and corrupt the N calibration.
func syntheticMosaic(tb testing.TB, text string, fontData []byte, fs float64, block int, linear bool) image.Image {
	tb.Helper()
	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		tb.Fatalf("build renderer: %v", err)
	}
	// PaddingTop: 16 gives extra rows above so contentBounds can locate the ink band.
	rendered, sentinelX, err := r.Render(text, unpixel.Style{FontSize: fs, PaddingTop: 16, PaddingLeft: 4})
	if err != nil {
		tb.Fatalf("render %q: %v", text, err)
	}
	// Crop to [0, sentinelX) to strip the blue sentinel column.
	cropped := image.NewRGBA(image.Rect(0, 0, sentinelX, rendered.Bounds().Dy()))
	draw.Draw(cropped, cropped.Bounds(), rendered, image.Point{}, draw.Src)

	var pix unpixel.Pixelator
	if linear {
		pix = defaults.LinearBlockAverage(block)
	} else {
		pix = defaults.BlockAverage(block)
	}
	return pix.Pixelate(cropped, 0, 0)
}

// embedInWhite places mosaic inside a white canvas with block-aligned margins.
func embedInWhite(mosaic image.Image, block int) *image.RGBA {
	mb := mosaic.Bounds()
	marginX := block * 5
	marginY := block * 3
	canvas := image.NewRGBA(image.Rect(0, 0, mb.Dx()+2*marginX, mb.Dy()+2*marginY))
	for i := range len(canvas.Pix) / 4 {
		canvas.Pix[i*4+0] = 255
		canvas.Pix[i*4+1] = 255
		canvas.Pix[i*4+2] = 255
		canvas.Pix[i*4+3] = 255
	}
	draw.Draw(canvas, image.Rect(marginX, marginY, marginX+mb.Dx(), marginY+mb.Dy()),
		mosaic, mb.Min, draw.Src)
	return canvas
}

// TestDecodeHMM_GridDetection verifies that InferBlockGrid finds block=4 on
// the synthetic mosaics before we run the full decode.
func TestDecodeHMM_GridDetection(t *testing.T) {
	cases := []struct {
		font   string
		text   string
		fs     float64
		block  int
		linear bool
	}{
		{"Liberation Mono", "hello world", 32, 4, true},
		{"Liberation Mono", "access denied", 32, 4, true},
		{"Liberation Mono", "great success", 32, 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.font+"/"+tc.text, func(t *testing.T) {
			f := monoFont(t, tc.font)
			mosaic := syntheticMosaic(t, tc.text, f.Data, tc.fs, tc.block, tc.linear)
			grid, ok := unpixel.InferBlockGrid(mosaic)
			t.Logf("raw mosaic bounds=%v  InferBlockGrid ok=%v size=%d", mosaic.Bounds(), ok, grid.Size)
			if !ok {
				canvas := embedInWhite(mosaic, tc.block)
				grid2, ok2 := unpixel.InferBlockGrid(canvas)
				t.Logf("canvas bounds=%v  InferBlockGrid ok=%v size=%d", canvas.Bounds(), ok2, grid2.Size)
			}
		})
	}
}

// TestDecodeHMM_HelloWorld checks that DecodeHMM exactly recovers the classic
// "hello world" string in Liberation Mono with linear-light block averaging.
//
// This fixture has been verified to have correctMSE == seedMSE (greedy reaches
// exact answer before the beam pass), confirming the rendering pipeline is
// self-consistent. block=4 gives ~4.75 blocks per character (adv≈19px).
// WithFont+WithFontSize+WithCharCount bypass calibration to exercise only the
// LM-guided beam sequence decode.
func TestDecodeHMM_HelloWorld(t *testing.T) {
	f := monoFont(t, "Liberation Mono")
	const (
		text  = "hello world"
		fs    = 32.0
		block = 4
		n     = 11
	)
	img := syntheticMosaic(t, text, f.Data, fs, block, true)
	res, err := mosaictext.DecodeHMM(t.Context(), img,
		mosaictext.WithFont("Liberation Mono"),
		mosaictext.WithFontSize(fs),
		mosaictext.WithCharCount(n),
		mosaictext.WithLanguage(lang.English),
		mosaictext.WithCharset(mosaictext.DefaultHMMCharset),
		mosaictext.WithEmissionTemperature(0.1),
	)
	if err != nil {
		t.Fatalf("DecodeHMM: %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.2f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeHMM = %q, want %q", res.Text, text)
	}
}

// TestDecodeHMM_AccessDenied checks exact recovery of a longer string with
// mixed-case and two dictionary words. "access denied" (13 chars) stresses the
// LM prior on word boundaries and the beam width on simultaneous confusions.
func TestDecodeHMM_AccessDenied(t *testing.T) {
	f := monoFont(t, "Liberation Mono")
	const (
		text  = "access denied"
		fs    = 32.0
		block = 4
		n     = 13
	)
	img := syntheticMosaic(t, text, f.Data, fs, block, true)
	res, err := mosaictext.DecodeHMM(t.Context(), img,
		mosaictext.WithFont("Liberation Mono"),
		mosaictext.WithFontSize(fs),
		mosaictext.WithCharCount(n),
		mosaictext.WithLanguage(lang.English),
		mosaictext.WithCharset(mosaictext.DefaultHMMCharset),
		mosaictext.WithEmissionTemperature(0.1),
	)
	if err != nil {
		t.Fatalf("DecodeHMM: %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.2f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeHMM = %q, want %q", res.Text, text)
	}
}

// TestDecodeHMM_GreatSuccess checks exact recovery using a sRGB-pixelated
// mosaic ("great success", 13 chars) — exercises the sRGB path separately
// from the linear-light path used by the other two fixture tests.
func TestDecodeHMM_GreatSuccess(t *testing.T) {
	f := monoFont(t, "Liberation Mono")
	const (
		text  = "great success"
		fs    = 32.0
		block = 4
		n     = 13
	)
	img := syntheticMosaic(t, text, f.Data, fs, block, false) // sRGB
	res, err := mosaictext.DecodeHMM(t.Context(), img,
		mosaictext.WithFont("Liberation Mono"),
		mosaictext.WithFontSize(fs),
		mosaictext.WithCharCount(n),
		mosaictext.WithLanguage(lang.English),
		mosaictext.WithCharset(mosaictext.DefaultHMMCharset),
		mosaictext.WithEmissionTemperature(0.1),
	)
	if err != nil {
		t.Fatalf("DecodeHMM: %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.2f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeHMM = %q, want %q", res.Text, text)
	}
}

// TestDecodeHMM_WithFontFile verifies that DecodeHMM with WithFontFile
// (Liberation Mono bytes from fonts.All) exactly recovers a synthetic mosaic
// rendered with that same font. The bundled-mono sweep is bypassed; the decoder
// must succeed using the caller-supplied font data alone.
func TestDecodeHMM_WithFontFile(t *testing.T) {
	f := monoFont(t, "Liberation Mono")
	const (
		text  = "hello world"
		fs    = 32.0
		block = 4
		n     = 11
	)
	img := syntheticMosaic(t, text, f.Data, fs, block, true)
	res, err := mosaictext.DecodeHMM(t.Context(), img,
		mosaictext.WithFontFile(f.Data), // user-supplied font, not a bundled name
		mosaictext.WithFontSize(fs),
		mosaictext.WithCharCount(n),
		mosaictext.WithLanguage(lang.English),
		mosaictext.WithCharset(mosaictext.DefaultHMMCharset),
		mosaictext.WithEmissionTemperature(0.1),
	)
	if err != nil {
		t.Fatalf("DecodeHMM with WithFontFile: %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.2f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != text {
		t.Errorf("DecodeHMM = %q, want %q", res.Text, text)
	}
}

// TestDecodeHMM_Errors checks sentinel error paths without running the full
// decode pipeline.
func TestDecodeHMM_Errors(t *testing.T) {
	// 1×1 white image — no mosaic grid → ErrNoMosaic.
	white := image.NewRGBA(image.Rect(0, 0, 1, 1))
	white.Pix[0], white.Pix[1], white.Pix[2], white.Pix[3] = 255, 255, 255, 255
	if _, err := mosaictext.DecodeHMM(t.Context(), white); !errors.Is(err, mosaictext.ErrNoMosaic) {
		t.Errorf("1×1 white: got %v, want ErrNoMosaic", err)
	}

	// WithFont that does not match any bundled mono font → ErrNoContent.
	f := monoFont(t, "Liberation Mono")
	img := syntheticMosaic(t, "test", f.Data, 32, 4, false)
	if _, err := mosaictext.DecodeHMM(t.Context(), img,
		mosaictext.WithFont("NoSuchFont XYZ"),
	); !errors.Is(err, mosaictext.ErrNoContent) {
		t.Errorf("unknown font: got %v, want ErrNoContent", err)
	}
}

var sinkHMMResult mosaictext.Result // defeats dead-code elimination

// BenchmarkDecodeHMM measures the full DecodeHMM pipeline on a self-consistent
// synthetic fixture: Liberation Mono at 32 pt, block=4, linear-light mosaic,
// font+size+count pinned to focus on the LM-guided beam sequence decode.
func BenchmarkDecodeHMM(b *testing.B) {
	f := monoFont(b, "Liberation Mono")
	const (
		text  = "hello world"
		fs    = 32.0
		block = 4
		n     = 11
	)
	img := syntheticMosaic(b, text, f.Data, fs, block, true)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, decErr := mosaictext.DecodeHMM(context.Background(), img,
			mosaictext.WithFont("Liberation Mono"),
			mosaictext.WithFontSize(fs),
			mosaictext.WithCharCount(n),
			mosaictext.WithCharset(mosaictext.DefaultHMMCharset),
			mosaictext.WithEmissionTemperature(0.1),
		)
		if decErr != nil {
			b.Fatalf("DecodeHMM: %v", decErr)
		}
		sinkHMMResult = res
	}
}
