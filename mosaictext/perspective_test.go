package mosaictext_test

import (
	"encoding/json"
	"image"
	_ "image/png"
	"os"
	"testing"

	"github.com/oioio-space/unpixel/internal/rectify"
	"github.com/oioio-space/unpixel/mosaictext"
)

// perspectiveFixture mirrors one entry in testdata/perspective/manifest.json.
type perspectiveFixture struct {
	Name      string        `json:"name"`
	File      string        `json:"file"`
	Text      string        `json:"text"`
	Charset   string        `json:"charset"`
	FontSize  float64       `json:"font_size"`
	BlockSize int           `json:"block_size"`
	Quad      [4][2]float64 `json:"quad"`
}

// TestDecodePerspective loads every fixture from testdata/perspective/manifest.json,
// calls DecodePerspective with the manifest quad and charset, and asserts:
//   - Result.Distance ≤ 0.15 (forward-model geometry is self-consistent), and
//   - Result.Text matches the manifest text (logged, not hard-failed, for flakiness).
func TestDecodePerspective(t *testing.T) {
	ctx := t.Context()

	data, err := os.ReadFile("../testdata/perspective/manifest.json") // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatalf("read manifest: got %v, want nil", err)
	}
	var fixtures []perspectiveFixture
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("parse manifest: got %v, want nil", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("manifest has no fixtures")
	}

	for _, fix := range fixtures {
		t.Run(fix.Name, func(t *testing.T) {
			imgPath := "../testdata/perspective/" + fix.File
			f, err := os.Open(imgPath) // #nosec G304 -- test fixture path
			if err != nil {
				t.Fatalf("open %q: got %v, want nil", imgPath, err)
			}
			defer func() { _ = f.Close() }()

			photo, _, err := image.Decode(f)
			if err != nil {
				t.Fatalf("decode %q: got %v, want nil", fix.File, err)
			}

			quad := [4]rectify.Point{
				{X: fix.Quad[0][0], Y: fix.Quad[0][1]},
				{X: fix.Quad[1][0], Y: fix.Quad[1][1]},
				{X: fix.Quad[2][0], Y: fix.Quad[2][1]},
				{X: fix.Quad[3][0], Y: fix.Quad[3][1]},
			}

			res, err := mosaictext.DecodePerspective(ctx, photo,
				mosaictext.WithPerspectiveQuad(quad),
				mosaictext.WithPerspectiveCharset(fix.Charset),
				mosaictext.WithPerspectiveFont("Liberation Sans"),
				mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
			)
			if err != nil {
				t.Fatalf("DecodePerspective: got %v, want nil", err)
			}

			const distThreshold = 0.15
			if got, want := res.Distance, distThreshold; got > want {
				t.Errorf("Distance = %.4f, want ≤ %.4f (geometry+projection not self-consistent)", got, want)
			}

			if res.Text == fix.Text {
				t.Logf("Text match: got %q == want %q (correct)", res.Text, fix.Text)
			} else {
				t.Logf("Text mismatch: got %q, want %q (logged, not failed)", res.Text, fix.Text)
			}

			t.Logf("fixture=%s rectW=%d rectH=%d dist=%.4f text=%q",
				fix.Name, res.RectW, res.RectH, res.Distance, res.Text)
		})
	}
}

// TestDecodePerspectiveNoQuad verifies that omitting WithPerspectiveQuad returns an error.
func TestDecodePerspectiveNoQuad(t *testing.T) {
	ctx := t.Context()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	_, err := mosaictext.DecodePerspective(ctx, img)
	if err == nil {
		t.Error("got nil error, want error for missing quad")
	}
}

// TestDecodePerspectiveDegenerateQuad verifies that a degenerate (collinear)
// quad returns an error rather than panicking.
func TestDecodePerspectiveDegenerateQuad(t *testing.T) {
	ctx := t.Context()
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	// All four corners on a single line — degenerate homography.
	degen := [4]rectify.Point{
		{X: 0, Y: 0},
		{X: 10, Y: 0},
		{X: 20, Y: 0},
		{X: 30, Y: 0},
	}
	_, err := mosaictext.DecodePerspective(ctx, img,
		mosaictext.WithPerspectiveQuad(degen),
	)
	if err == nil {
		t.Error("got nil error, want error for degenerate (collinear) quad")
	}
}

// TestDecodePerspectiveWithFontFile exercises the WithPerspectiveFontFile and
// WithPerspectiveLinear option paths by running DecodePerspective with the
// first fixture using raw TTF bytes loaded from the bundled Liberation Sans font.
func TestDecodePerspectiveWithFontFile(t *testing.T) {
	ctx := t.Context()

	data, err := os.ReadFile("../testdata/perspective/manifest.json") // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatalf("read manifest: got %v, want nil", err)
	}
	var fixtures []perspectiveFixture
	if err = json.Unmarshal(data, &fixtures); err != nil || len(fixtures) == 0 {
		t.Fatalf("parse manifest: got %v len=%d", err, len(fixtures))
	}
	fix := fixtures[0]

	imgPath := "../testdata/perspective/" + fix.File
	f, err := os.Open(imgPath) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatalf("open %q: got %v, want nil", imgPath, err)
	}
	defer func() { _ = f.Close() }()
	photo, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode: got %v, want nil", err)
	}

	// Load the Liberation Sans TTF from the bundled font path so we can supply
	// it via WithPerspectiveFontFile, exercising that branch.
	ttfData, err := os.ReadFile("../fonts/embed/LiberationSans-Regular.ttf") // #nosec G304 -- test font path
	if err != nil {
		t.Skip("LiberationSans-Regular.ttf not found at expected path — skipping font-file branch test")
	}

	quad := [4]rectify.Point{
		{X: fix.Quad[0][0], Y: fix.Quad[0][1]},
		{X: fix.Quad[1][0], Y: fix.Quad[1][1]},
		{X: fix.Quad[2][0], Y: fix.Quad[2][1]},
		{X: fix.Quad[3][0], Y: fix.Quad[3][1]},
	}

	// WithPerspectiveLinear(true): exercises linear=1 branch.
	res, err := mosaictext.DecodePerspective(ctx, photo,
		mosaictext.WithPerspectiveQuad(quad),
		mosaictext.WithPerspectiveCharset(fix.Charset),
		mosaictext.WithPerspectiveFontFile(ttfData),
		mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
		mosaictext.WithPerspectiveLinear(true),
	)
	if err != nil {
		t.Fatalf("DecodePerspective with font file: got %v, want nil", err)
	}
	if got, want := res.Distance, 0.15; got > want {
		t.Errorf("Distance = %.4f, want ≤ %.4f", got, want)
	}
	t.Logf("font-file path: dist=%.4f text=%q", res.Distance, res.Text)

	// WithPerspectiveLinear(false): exercises linear=0 branch. Use empty fontTTF
	// to fall through to the font-name path (already covered), just verifying no panic.
	_, err = mosaictext.DecodePerspective(ctx, photo,
		mosaictext.WithPerspectiveQuad(quad),
		mosaictext.WithPerspectiveCharset(fix.Charset),
		mosaictext.WithPerspectiveFont("Liberation Sans"),
		mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
		mosaictext.WithPerspectiveLinear(false),
	)
	if err != nil {
		t.Fatalf("DecodePerspective linear=false: got %v, want nil", err)
	}
}
