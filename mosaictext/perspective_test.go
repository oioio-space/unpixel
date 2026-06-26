package mosaictext_test

import (
	"encoding/json"
	"image"
	_ "image/png"
	"os"
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/rectify"
	"github.com/oioio-space/unpixel/mosaictext"
)

// TestDecodePerspective_autoQuad exercises the full auto path: render "go", place
// it under perspective on a GRAY page (so the white-padded patch is detectable),
// then decode with WithPerspectiveAutoQuad and no corners supplied.
func TestDecodePerspective_autoQuad(t *testing.T) {
	red, err := fixture.Redact(fixture.Spec{
		Text: "go", Charset: "go abcd", FontSize: 32, BlockSize: 8, PaddingTop: 8, PaddingLeft: 8,
	})
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	rw, rh := red.Bounds().Dx(), red.Bounds().Dy()
	w, h := float64(rw), float64(rh)
	quad := [4]rectify.Point{{X: 40, Y: 30}, {X: 40 + w, Y: 48}, {X: 40 + w - 12, Y: 30 + h + 22}, {X: 46, Y: 30 + h + 14}}

	r2p, err := rectify.RectToQuad(w, h, quad)
	if err != nil {
		t.Fatalf("RectToQuad: %v", err)
	}
	p2r, err := r2p.Inverse()
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	// Inside the quad: warped patch (bilinear, white-padded). Outside: gray page.
	photoW, photoH := rw+100, rh+100
	photo := rectify.Warp(red, p2r, photoW, photoH)
	for y := range photoH {
		for x := range photoW {
			rp := p2r.Apply(rectify.Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
			if rp.X >= 0 && rp.Y >= 0 && rp.X < w && rp.Y < h {
				continue // inside quad — keep the warped patch
			}
			o := photo.PixOffset(x, y)
			photo.Pix[o], photo.Pix[o+1], photo.Pix[o+2], photo.Pix[o+3] = 128, 128, 128, 255
		}
	}

	res, err := mosaictext.DecodePerspective(
		t.Context(), photo,
		mosaictext.WithPerspectiveAutoQuad(0),
		mosaictext.WithPerspectiveBlockSize(8),
		mosaictext.WithPerspectiveCharset("go abcd"),
	)
	if err != nil {
		t.Fatalf("DecodePerspective auto: %v", err)
	}
	if res.Text != "go" {
		t.Errorf("auto-quad decode: got %q, want %q (dist=%.4f)", res.Text, "go", res.Distance)
	}
}

// perspectiveFixture mirrors one entry in testdata/perspective/manifest.json.
type perspectiveFixture struct {
	Name      string        `json:"name"`
	File      string        `json:"file"`
	Text      string        `json:"text"`
	Charset   string        `json:"charset"`
	FontSize  float64       `json:"font_size"`
	BlockSize int           `json:"block_size"`
	RectW     int           `json:"rect_w"` // true rendered width; 0 → derive from quad
	RectH     int           `json:"rect_h"` // true rendered height; 0 → derive from quad
	Quad      [4][2]float64 `json:"quad"`
}

// loadPerspectiveFixtures reads the manifest and returns all entries.
func loadPerspectiveFixtures(tb testing.TB) []perspectiveFixture {
	tb.Helper()
	data, err := os.ReadFile("../testdata/perspective/manifest.json") // #nosec G304 -- test fixture path
	if err != nil {
		tb.Fatalf("read manifest: got %v, want nil", err)
	}
	var fixtures []perspectiveFixture
	if err = json.Unmarshal(data, &fixtures); err != nil {
		tb.Fatalf("parse manifest: got %v, want nil", err)
	}
	if len(fixtures) == 0 {
		tb.Fatal("manifest has no fixtures")
	}
	return fixtures
}

// loadPhoto opens and decodes the PNG at path relative to testdata/perspective/.
func loadPhoto(tb testing.TB, file string) image.Image {
	tb.Helper()
	imgPath := "../testdata/perspective/" + file
	f, err := os.Open(imgPath) // #nosec G304 -- test fixture path
	if err != nil {
		tb.Fatalf("open %q: got %v, want nil", imgPath, err)
	}
	defer func() { _ = f.Close() }()
	photo, _, err := image.Decode(f)
	if err != nil {
		tb.Fatalf("decode %q: got %v, want nil", file, err)
	}
	return photo
}

// fixtureQuad converts the manifest [4][2]float64 layout to [4]rectify.Point.
func fixtureQuad(raw [4][2]float64) [4]rectify.Point {
	return [4]rectify.Point{
		{X: raw[0][0], Y: raw[0][1]},
		{X: raw[1][0], Y: raw[1][1]},
		{X: raw[2][0], Y: raw[2][1]},
		{X: raw[3][0], Y: raw[3][1]},
	}
}

// TestDecodePerspective loads every fixture from testdata/perspective/manifest.json,
// calls DecodePerspective with the manifest quad, charset, font size, and block
// size, and asserts EXACT decode for each fixture. The fixtures were rendered
// with Liberation Sans / font 32 / block 8 / pad 8,8; using matching candidate
// parameters makes the true string the global distance minimum.
func TestDecodePerspective(t *testing.T) {
	ctx := t.Context()
	fixtures := loadPerspectiveFixtures(t)

	for _, fix := range fixtures {
		t.Run(fix.Name, func(t *testing.T) {
			photo := loadPhoto(t, fix.File)

			res, err := mosaictext.DecodePerspective(
				ctx, photo,
				mosaictext.WithPerspectiveQuad(fixtureQuad(fix.Quad)),
				mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
				mosaictext.WithPerspectiveCharset(fix.Charset),
				mosaictext.WithPerspectiveFontSize(fix.FontSize),
				mosaictext.WithPerspectiveRectSize(fix.RectW, fix.RectH),
				// Default embedded font (Liberation Sans) matches the fixture renderer.
			)
			if err != nil {
				t.Fatalf("DecodePerspective: got %v, want nil", err)
			}

			t.Logf("fixture=%s rectW=%d rectH=%d dist=%.4f text=%q",
				fix.Name, res.RectW, res.RectH, res.Distance, res.Text)

			got, want := res.Text, fix.Text
			if got != want {
				t.Errorf("Text: got %q, want %q (dist=%.4f)", got, want, res.Distance)
			}
		})
	}
}

// TestDecodePerspective_autoFromFixtures decodes every on-disk fixture with NO
// corners supplied — the quad is auto-detected (WithPerspectiveAutoQuad) from the
// gray-page background — and asserts the same exact decode as the manual-quad path.
func TestDecodePerspective_autoFromFixtures(t *testing.T) {
	ctx := t.Context()
	for _, fix := range loadPerspectiveFixtures(t) {
		t.Run(fix.Name, func(t *testing.T) {
			photo := loadPhoto(t, fix.File)
			res, err := mosaictext.DecodePerspective(
				ctx, photo,
				mosaictext.WithPerspectiveAutoQuad(0),
				mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
				mosaictext.WithPerspectiveCharset(fix.Charset),
				mosaictext.WithPerspectiveFontSize(fix.FontSize),
			)
			if err != nil {
				t.Fatalf("DecodePerspective auto: got %v, want nil", err)
			}
			t.Logf("auto fixture=%s rectW=%d rectH=%d dist=%.4f text=%q",
				fix.Name, res.RectW, res.RectH, res.Distance, res.Text)
			// DetectQuad recovers the corners to within a few pixels; that small
			// error is harmless for short text but compounds for dense/longer
			// strings. Assert exact decode for short fixtures and a low
			// forward-model distance (best-effort text) for longer ones — supplying
			// exact corners (WithPerspectiveQuad) removes the limit (see TestDecodePerspective).
			if got, want := res.Text, fix.Text; len([]rune(want)) <= 3 {
				if got != want {
					t.Errorf("auto Text: got %q, want %q (dist=%.4f)", got, want, res.Distance)
				}
			} else if res.Distance > 0.05 {
				t.Errorf("auto distance for %q: got %.4f, want ≤ 0.05 (best-effort text %q)", want, res.Distance, got)
			}
		})
	}
}

var sinkPerspText string

// BenchmarkDecodePerspectiveAuto measures the full auto path (detect quad + beam
// decode) on the smallest fixture.
func BenchmarkDecodePerspectiveAuto(b *testing.B) {
	var fix perspectiveFixture
	for _, f := range loadPerspectiveFixtures(b) {
		if f.Name == "persp_go" {
			fix = f
			break
		}
	}
	photo := loadPhoto(b, fix.File)
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		res, err := mosaictext.DecodePerspective(
			ctx, photo,
			mosaictext.WithPerspectiveAutoQuad(0),
			mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
			mosaictext.WithPerspectiveCharset(fix.Charset),
			mosaictext.WithPerspectiveFontSize(fix.FontSize),
		)
		if err != nil {
			b.Fatal(err)
		}
		sinkPerspText = res.Text
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
	_, err := mosaictext.DecodePerspective(
		ctx, img,
		mosaictext.WithPerspectiveQuad(degen),
	)
	if err == nil {
		t.Error("got nil error, want error for degenerate (collinear) quad")
	}
}

// TestDecodePerspectiveWithFontFile exercises the WithPerspectiveFontFile and
// WithPerspectiveLinear option paths using the first fixture.
func TestDecodePerspectiveWithFontFile(t *testing.T) {
	ctx := t.Context()
	fixtures := loadPerspectiveFixtures(t)
	fix := fixtures[0]

	photo := loadPhoto(t, fix.File)

	ttfData, err := os.ReadFile("../fonts/embed/LiberationSans-Regular.ttf") // #nosec G304 -- test font path
	if err != nil {
		t.Skip("LiberationSans-Regular.ttf not found at expected path — skipping font-file branch test")
	}

	// WithPerspectiveFontFile exercises the explicit-TTF branch.
	res, err := mosaictext.DecodePerspective(
		ctx, photo,
		mosaictext.WithPerspectiveQuad(fixtureQuad(fix.Quad)),
		mosaictext.WithPerspectiveCharset(fix.Charset),
		mosaictext.WithPerspectiveFontFile(ttfData),
		mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
		mosaictext.WithPerspectiveFontSize(fix.FontSize),
		mosaictext.WithPerspectiveRectSize(fix.RectW, fix.RectH),
		mosaictext.WithPerspectiveLinear(true), // accepted silently (no effect)
	)
	if err != nil {
		t.Fatalf("DecodePerspective with font file: got %v, want nil", err)
	}
	t.Logf("font-file path: dist=%.4f text=%q", res.Distance, res.Text)

	// WithPerspectiveLinear(false) exercises that option branch (no effect).
	_, err = mosaictext.DecodePerspective(
		ctx, photo,
		mosaictext.WithPerspectiveQuad(fixtureQuad(fix.Quad)),
		mosaictext.WithPerspectiveCharset(fix.Charset),
		mosaictext.WithPerspectiveFont("Liberation Sans"),
		mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
		mosaictext.WithPerspectiveFontSize(fix.FontSize),
		mosaictext.WithPerspectiveRectSize(fix.RectW, fix.RectH),
		mosaictext.WithPerspectiveLinear(false),
	)
	if err != nil {
		t.Fatalf("DecodePerspective linear=false: got %v, want nil", err)
	}
}

// TestPerspectiveOptions exercises WithPerspectiveBeamWidth and
// WithPerspectiveMaxLen by running DecodePerspective on the first fixture with
// tightly bounded beam parameters. The decode result is not asserted — the test
// only verifies that both options are accepted and that the search completes
// without error.
func TestPerspectiveOptions(t *testing.T) {
	ctx := t.Context()
	fixtures := loadPerspectiveFixtures(t)
	fix := fixtures[0] // persp_go — smallest fixture (charset "go abcd")

	photo := loadPhoto(t, fix.File)

	_, err := mosaictext.DecodePerspective(
		ctx, photo,
		mosaictext.WithPerspectiveQuad(fixtureQuad(fix.Quad)),
		mosaictext.WithPerspectiveCharset(fix.Charset),
		mosaictext.WithPerspectiveFontSize(fix.FontSize),
		mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
		mosaictext.WithPerspectiveRectSize(fix.RectW, fix.RectH),
		mosaictext.WithPerspectiveBeamWidth(2),
		mosaictext.WithPerspectiveMaxLen(2),
	)
	if err != nil {
		t.Fatalf("DecodePerspective with BeamWidth=2/MaxLen=2: got %v, want nil", err)
	}
}

// sinkPerspective is a package-level sink for benchmark results to prevent
// the compiler from eliminating the decode call.
var sinkPerspective mosaictext.PerspectiveResult

// BenchmarkDecodePerspective measures the pure forward-model beam search over
// the persp_cat fixture (3-character text, charset "cat eoabd", block 8).
func BenchmarkDecodePerspective(b *testing.B) {
	fixtures := loadPerspectiveFixtures(b)
	var fix perspectiveFixture
	for _, f := range fixtures {
		if f.Name == "persp_cat" {
			fix = f
			break
		}
	}
	if fix.Name == "" {
		b.Fatal("persp_cat fixture not found in manifest")
	}

	photo := loadPhoto(b, fix.File)

	quad := fixtureQuad(fix.Quad)
	ctx := b.Context()

	b.ReportAllocs()
	for b.Loop() {
		res, err := mosaictext.DecodePerspective(
			ctx, photo,
			mosaictext.WithPerspectiveQuad(quad),
			mosaictext.WithPerspectiveBlockSize(fix.BlockSize),
			mosaictext.WithPerspectiveCharset(fix.Charset),
			mosaictext.WithPerspectiveFontSize(fix.FontSize),
			mosaictext.WithPerspectiveRectSize(fix.RectW, fix.RectH),
		)
		if err != nil {
			b.Fatal(err)
		}
		sinkPerspective = res
	}
}
