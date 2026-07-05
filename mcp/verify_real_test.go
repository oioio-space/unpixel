package mcpserver_test

import (
	"image"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// realHelloWorld is the hand-contributed real GIMP mosaic of "Hello World !"
// (Noto Sans Mono, GEGL Pixelize at a 16 px block scaled ~2× → 32 px blocks),
// committed under testdata/real. It is a genuine third-party redaction, not
// engine output.
const (
	realHelloWorldPath = "../testdata/real/hello-world.png"
	realHelloWorldText = "Hello World !"
)

// TestVerifyWithHints_RealHelloWorld is the production wall-break: the LLM
// propose → physics-verify loop recovers a REAL redaction end-to-end through the
// MCP verify core. Per-character search is information-starved at this coarse
// block; whole-string verification confirms the truth, but only when the
// candidate is rendered with the right font/geometry/colourspace and compared
// against a tight crop of the redaction band — all hints an LLM client discovers
// from unpixel_analyze (band, block, colourspace) and unpixel_rank_fonts (font).
//
// Passing those hints to VerifyWithHints yields a decisive physical Pick of the
// truth (distance ≈ 0), while a wrong-shape decoy is rejected. This is the same
// recovery TestVerify_RealHelloWorld proves at the library level, now reachable
// by an MCP client via the tool's schema — closing the strategic differentiator.
func TestVerifyWithHints_RealHelloWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-image propose/verify recovery in -short mode")
	}

	img, err := loadImageFile(realHelloWorldPath)
	if err != nil {
		t.Fatalf("load %s: %v", realHelloWorldPath, err)
	}

	// The redaction band, as unpixel_analyze would report it: the non-white
	// content bounding box of the screenshot.
	band := contentBox(img)

	const decoy = "HELLO WORLD !"
	report, err := mcpserver.VerifyWithHints(t.Context(), img,
		[]string{realHelloWorldText, decoy},
		mcpserver.VerifyHints{
			Font:        "Noto Sans Mono",
			BlockSize:   32,
			LinearLight: true,
			FontSize:    124,
			XScale:      1.06,
			Crop:        band,
		})
	if err != nil {
		t.Fatalf("VerifyWithHints: %v", err)
	}

	for _, r := range report.Ranked {
		t.Logf("candidate %-16q distance=%.4f match=%v", r.Text, r.Distance, r.Match)
	}

	if report.Pick != realHelloWorldText {
		t.Errorf("Pick = %q, want %q — the loop should physically pick the truth", report.Pick, realHelloWorldText)
	}

	byText := make(map[string]mcpserver.RankedCandidate, len(report.Ranked))
	for _, r := range report.Ranked {
		byText[r.Text] = r
	}
	if truth := byText[realHelloWorldText]; !truth.Match || truth.Distance > 0.05 {
		t.Errorf("truth %q: distance=%.4f match=%v, want match with distance ≤ 0.05",
			realHelloWorldText, truth.Distance, truth.Match)
	}
	if d := byText[decoy]; d.Match {
		t.Errorf("decoy %q: match=true (distance %.4f), want rejected", decoy, d.Distance)
	}
}

// contentBox returns the bounding box of non-white (luminance < 244) pixels in
// img — the same content-band heuristic the root real-mosaic tests use to locate
// the redaction, and what unpixel_analyze reports as the mosaic band.
func contentBox(img image.Image) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			lum := (299*int(r>>8) + 587*int(g>>8) + 114*int(bl>>8)) / 1000
			if lum < 244 {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x+1), max(y1, y+1)
			}
		}
	}
	if x1 <= x0 || y1 <= y0 {
		return b
	}
	return image.Rect(x0, y0, x1, y1)
}
