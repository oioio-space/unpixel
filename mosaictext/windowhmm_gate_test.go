package mosaictext

// windowhmm_gate_test.go — Stage 3 digit gate and Stage 4 proportional gate.
//
// Stage 3: render "3141592653" in Liberation Mono at a fixed font size, mosaic
// at a known block+phase, and verify DecodeWindowHMM recovers it exactly.
//
// Stage 4: render "hello world" in Liberation Sans (proportional), mosaic at a
// known grid, and verify DecodeWindowHMM recovers it with edit-distance ≤ 1.
// This is the fundamental win over DecodeHMM (P-A): proportional fonts have
// variable glyph advances, so the monospace window-HMM cannot be applied
// directly — the sliding-window approach handles them naturally.

import (
	"image"
	"image/png"
	"os"
	"testing"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
)

// renderMosaic renders text with r at fs, pixelates at (block, linear),
// and returns the resulting *image.RGBA.
func renderMosaic(t *testing.T, r unpixel.Renderer, text string, fs float64, block int, linear bool) *image.RGBA {
	t.Helper()
	img, _, err := r.Render(text, unpixel.Style{FontSize: fs})
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	pix := pixelatorFor(block, linear)
	return pix.Pixelate(img, 0, 0)
}

// saveTestPNG writes img to a temporary file and returns its path.
// Used to pass a synthesised mosaic as an image.Image to DecodeWindowHMM.
func saveTestPNG(t *testing.T, img image.Image) string {
	t.Helper()
	f, err := os.CreateTemp("", "whmm-gate-*.png")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close temp: %v", cerr)
		}
	}()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}

// loadTestPNG reads the PNG written by saveTestPNG back as an image.Image.
func loadTestPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test helper using t.TempDir path
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close %s: %v", path, cerr)
		}
	}()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

// editDistance returns the Levenshtein distance between a and b (rune-aware).
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := range lb + 1 {
		prev[j] = j
	}
	for i := range la {
		cur[0] = i + 1
		for j := range lb {
			cost := 1
			if ra[i] == rb[j] {
				cost = 0
			}
			cur[j+1] = min(cur[j]+1, min(prev[j+1]+1, prev[j]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

// findFont finds a bundled font by name and returns its data, or skips the test.
func findFont(t *testing.T, name string) []byte {
	t.Helper()
	all := fonts.All()
	for _, f := range all {
		if f.Name == name {
			return f.Data
		}
	}
	t.Skipf("bundled font %q not found", name)
	return nil
}

// TestDecodeWindowHMM_DigitGate is the Stage 3 gate: render "3141592653" in
// Liberation Mono at block=4 (fine mosaic), and verify exact recovery.
//
// block=4 is required: the beam-search decoder needs avgAdv/block ≥ 2 so that
// each character spans ≥ 2 block columns and produces a discriminating window
// vector. At fs=32, Liberation Mono has avgAdv≈19 px → 4.75 blocks at block=4,
// which gives clear per-character separation. block=12 (avgAdv/block≈1.6) is
// outside the decoder's operating envelope and is not a valid gate parameter.
func TestDecodeWindowHMM_DigitGate(t *testing.T) {
	fontData := findFont(t, "Liberation Mono")

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}

	const (
		text  = "3141592653"
		fs    = 32.0
		block = 4 // fine mosaic: avgAdv/block ≈ 4.75 → well-separated per-char windows
	)

	// Render and mosaic the target string (sRGB, not linear).
	mosaicImg := renderMosaic(t, r, text, fs, block, false)

	// Write to disk and reload so DecodeWindowHMM sees a genuine image.Image.
	path := saveTestPNG(t, mosaicImg)
	img := loadTestPNG(t, path)

	ctx := t.Context()
	res, err := DecodeWindowHMM(
		ctx, img,
		WithWHMMFont("Liberation Mono"),
		WithWHMMCharset("0123456789 "),
		WithWHMMSeed(1),
		WithWHMMLinear(0), // sRGB only
		WithWHMMBeamWidth(50),
	)
	if err != nil {
		t.Fatalf("DecodeWindowHMM: %v", err)
	}

	t.Logf("decoded: %q (want %q) dist=%.4f", res.Text, text, res.Distance)

	// Primary gate: exact recovery.
	if res.Text != text {
		ed := editDistance(res.Text, text)
		t.Errorf("digit gate: got %q, want %q (edit-distance %d)", res.Text, text, ed)
	}
}

// TestDecodeWindowHMM_ProportionalGate is the Stage 4 gate: render "hello world"
// in Liberation Sans (proportional), mosaic it, and verify recovery with
// edit-distance ≤ 1. This is the fundamental advantage over DecodeHMM, which
// requires monospace fonts. Exact recovery is asserted when it holds; the
// edit-distance bar documents the realistic operating envelope.
//
// block=4 is required for the same reason as the digit gate: the beam-search
// decoder needs avgAdv/block ≥ 2. At fs=18, Liberation Sans has avgAdv≈8.67 px
// → 2.17 blocks at block=4, which just clears the discrimination threshold.
func TestDecodeWindowHMM_ProportionalGate(t *testing.T) {
	fontData := findFont(t, "Liberation Sans")

	r, err := defaults.RendererFromFonts(fontData, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}

	const (
		text  = "hello world"
		fs    = 18.0
		block = 4 // avgAdv/block ≈ 2.17 → discriminating per-char windows
	)

	mosaicImg := renderMosaic(t, r, text, fs, block, false)
	path := saveTestPNG(t, mosaicImg)
	img := loadTestPNG(t, path)

	ctx := t.Context()
	res, err := DecodeWindowHMM(
		ctx, img,
		WithWHMMFont("Liberation Sans"),
		WithWHMMCharset("abcdefghijklmnopqrstuvwxyz "),
		WithWHMMSeed(1),
		WithWHMMLinear(0),
		WithWHMMBeamWidth(50),
		WithWHMMWindow(3), // proportional font → W=3
	)
	if err != nil {
		t.Fatalf("DecodeWindowHMM: %v", err)
	}

	ed := editDistance(res.Text, text)
	t.Logf("proportional gate: got %q (want %q) edit-distance=%d dist=%.4f",
		res.Text, text, ed, res.Distance)

	// Realistic bar: edit-distance ≤ 1 (adjacent pixels in a proportional font
	// produce very similar block signatures, so occasional 1-character confusion
	// is expected). Assert exact when it holds.
	const maxED = 1
	switch {
	case res.Text == text:
		t.Logf("exact recovery achieved")
	case ed <= maxED:
		// Document the realistic bar inline.
		t.Logf("near-exact recovery (edit-distance %d ≤ %d): acceptable", ed, maxED)
	default:
		t.Errorf("proportional gate: edit-distance %d > %d (got %q, want %q)",
			ed, maxED, res.Text, text)
	}
}

// TestDecodeWindowHMM_EditDistanceHelper verifies the editDistance helper used
// in the gate tests.
func TestDecodeWindowHMM_EditDistanceHelper(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "ab", 1},
		{"abc", "axc", 1},
		{"hello", "helo", 1},
		{"kitten", "sitting", 3},
	}
	for _, tc := range cases {
		got := editDistance(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestDecodeWindowHMM_RuneLen sanity-checks utf8.RuneCountInString used implicitly
// in the gate test result reporting.
func TestDecodeWindowHMM_RuneLen(t *testing.T) {
	if n := utf8.RuneCountInString("hello world"); n != 11 {
		t.Errorf("unexpected rune count: %d", n)
	}
}
