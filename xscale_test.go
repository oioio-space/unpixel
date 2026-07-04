package unpixel_test

import (
	"image"
	"os"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/metric"
)

// TestXScale_HelloWorld_directModel is a fast direct-model check: rendering
// "Hello World !" via Style.XScale=1.06 should reproduce the hello-world.png
// GIMP redaction near-exactly, AND score strictly lower (better) than XScale=1.0.
//
// This proves that Style.XScale closes the anisotropy gap that LetterSpacing
// cannot model (intra-glyph ink redistribution vs. inter-glyph spacing).
// The test avoids the multi-minute guided DFS entirely — it calls only
// Render → Pixelate → Metric.Compare in the bestDistance slide from
// real_mosaic_test.go.
func TestXScale_HelloWorld_directModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-mosaic XScale direct-model check in -short mode")
	}

	f, err := os.Open(realMosaicSample)
	if err != nil {
		t.Fatalf("open %s: %v", realMosaicSample, err)
	}
	defer func() { _ = f.Close() }()
	src, err := decodePNG(f)
	if err != nil {
		t.Fatalf("decode %s: %v", realMosaicSample, err)
	}

	// Crop to mosaic content with a white margin, matching TestRealMosaic_HelloWorld.
	rect := contentBounds(src)
	target := image.NewRGBA(image.Rect(0, 0, rect.Dx()+128, rect.Dy()+32))
	xdraw.Draw(target, target.Bounds(), image.White, image.Point{}, xdraw.Src)
	xdraw.Draw(target, image.Rect(0, 0, rect.Dx(), rect.Dy()), src, rect.Min, xdraw.Src)

	r := notoMonoRenderer(t)
	m := metric.NewPixelmatch(0.1)
	const block = 32
	linear := defaults.LinearBlockAverage(block)

	// Render via Style.XScale. Apply the same two-step crop as renderStretched
	// in real_mosaic_test.go: strip the sentinel at sentinelX, then tight-crop
	// to the inked rows via inkBounds so the candidate fits within target height.
	// (bestDistance expects a pre-cropped input — no sentinel, no blank rows.)
	const fontSize = 124.0
	cropToInk := func(style unpixel.Style) *image.RGBA {
		t.Helper()
		img, sx, err := r.Render("Hello World !", style)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		bb := inkBounds(img, sx)
		out := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
		xdraw.Draw(out, out.Bounds(), img, bb.Min, xdraw.Src)
		return out
	}

	img106 := cropToInk(unpixel.Style{FontSize: fontSize, XScale: 1.06})
	img100 := cropToInk(unpixel.Style{FontSize: fontSize, XScale: 1.0})

	d106 := bestDistance(img106, target, linear, m, block)
	d100 := bestDistance(img100, target, linear, m, block)

	t.Logf("Style.XScale=1.06 distance=%.4f, XScale=1.0 distance=%.4f", d106, d100)

	// XScale=1.06 must reproduce the redaction near-exactly (same oracle tolerance
	// as TestRealMosaic_HelloWorld).
	if d106 > 0.05 {
		t.Errorf("XScale=1.06 distance=%.4f, want <=0.05 — anisotropy blocker not closed", d106)
	}

	// XScale=1.06 must strictly beat XScale=1.0, proving the stretch matters.
	if d106 >= d100 {
		t.Errorf("XScale=1.06 (%.4f) not better than XScale=1.0 (%.4f) — stretch not helping", d106, d100)
	}
}
