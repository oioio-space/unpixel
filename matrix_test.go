package unpixel_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/fixture"
)

// fixtureDir holds the committed reference images + manifest, produced by
// `go generate` (see internal/fixture/gen). The manifest is the persisted link
// between each image file and the parameters it was generated from.
const fixtureDir = "testdata/fixtures"

// loadManifest reads the committed fixture specs.
func loadManifest(t *testing.T) []fixture.Spec {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest (run `go generate ./...`): %v", err)
	}
	var specs []fixture.Spec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("manifest is empty")
	}
	return specs
}

// loadFixtureImage decodes a committed reference PNG.
func loadFixtureImage(t *testing.T, file string) image.Image {
	t.Helper()
	f, err := os.Open(filepath.Join(fixtureDir, file))
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", file, err)
	}
	return img
}

// recovers reports whether the plaintext appears among the recovered candidates.
func recovers(t *testing.T, img image.Image, s fixture.Spec) bool {
	t.Helper()
	res, err := unpixel.Recover(t.Context(), img,
		unpixel.WithStyle(s.Style()),
		unpixel.WithBlockSize(s.BlockSize),
		unpixel.WithCharset(s.Charset),
		unpixel.WithMaxLength(utf8.RuneCountInString(s.Text)+1),
		unpixel.WithWorkers(2), // bound CPU use under parallel subtests
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	guesses := []string{res.BestGuess}
	for _, e := range res.Candidates {
		guesses = append(guesses, e.Guess)
	}
	return slices.Contains(guesses, s.Text)
}

// TestMatrix_Recovery is the headline matrix: every committed reference image is
// recovered to its known plaintext. The cases span block sizes, font sizes and
// weights, charsets (incl. digits/uppercase/symbols), padding (grid offset) and
// text shapes — see internal/fixture.Matrix.
func TestMatrix_Recovery(t *testing.T) {
	t.Parallel()
	for _, s := range loadManifest(t) {
		t.Run(s.Name, func(t *testing.T) {
			t.Parallel()
			img := loadFixtureImage(t, s.File())
			if !recovers(t, img, s) {
				t.Errorf("did not recover %q from %s (block=%d size=%.0f bold=%v charset=%dchars)",
					s.Text, s.File(), s.BlockSize, s.FontSize, s.Bold, utf8.RuneCountInString(s.Charset))
			}
		})
	}
}

// handContributedFixtures are PNGs committed under testdata/fixtures that are NOT
// produced by the fixture generator (they are real third-party redactions, e.g.
// a GIMP export), so the manifest does not — and should not — describe them.
// They are exempt from the manifest ↔ file cross-check below; their own decode
// lives in real_mosaic_test.go.
var handContributedFixtures = map[string]bool{
	"text_hello-world.png": true,
}

// TestMatrix_manifestMatchesFiles checks every manifest entry has a file and
// vice-versa, so the image ↔ parameters link cannot silently drift.
func TestMatrix_manifestMatchesFiles(t *testing.T) {
	specs := loadManifest(t)
	named := make(map[string]bool, len(specs))
	for _, s := range specs {
		named[s.File()] = true
		if _, err := os.Stat(filepath.Join(fixtureDir, s.File())); err != nil {
			t.Errorf("manifest references missing image %s", s.File())
		}
	}
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("read fixture dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".png" && !named[e.Name()] && !handContributedFixtures[e.Name()] {
			t.Errorf("orphan image %s not in manifest", e.Name())
		}
	}
}

// TestMatrix_darkMode inverts a reference image (simulating a dark-mode capture)
// and confirms auto-contrast still recovers it.
func TestMatrix_darkMode(t *testing.T) {
	t.Parallel()
	s := specByName(t, "block08_go")
	src := loadFixtureImage(t, s.File())

	b := src.Bounds()
	dark := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			dark.SetRGBA(x, y, color.RGBA{R: 255 - uint8(r>>8), G: 255 - uint8(g>>8), B: 255 - uint8(bl>>8), A: uint8(a >> 8)})
		}
	}
	if !recovers(t, dark, s) {
		t.Errorf("auto-contrast failed to recover inverted %q", s.Text)
	}
}

// TestMatrix_imageFormats confirms the reference image decodes through several
// input formats: PNG round-trips losslessly and recovers; GIF and JPEG are
// accepted by image.Decode once their decoders are imported.
func TestMatrix_imageFormats(t *testing.T) {
	t.Parallel()
	s := specByName(t, "block08_go")
	img := loadFixtureImage(t, s.File())

	// PNG: end-to-end via RecoverReader (lossless).
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	res, err := unpixel.RecoverReader(t.Context(), &pngBuf,
		unpixel.WithStyle(s.Style()), unpixel.WithBlockSize(s.BlockSize),
		unpixel.WithCharset(s.Charset), unpixel.WithMaxLength(utf8.RuneCountInString(s.Text)+1),
		unpixel.WithWorkers(2))
	if err != nil {
		t.Fatalf("RecoverReader(png): %v", err)
	}
	guesses := []string{res.BestGuess}
	for _, e := range res.Candidates {
		guesses = append(guesses, e.Guess)
	}
	if !slices.Contains(guesses, s.Text) {
		t.Errorf("RecoverReader(png) did not recover %q", s.Text)
	}

	// GIF and JPEG: confirm the formats decode (lossy → recovery not asserted).
	for _, tc := range []struct {
		name   string
		encode func(*bytes.Buffer, image.Image) error
	}{
		{"gif", func(b *bytes.Buffer, im image.Image) error { return gif.Encode(b, im, nil) }},
		{"jpeg", func(b *bytes.Buffer, im image.Image) error { return jpeg.Encode(b, im, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.encode(&buf, img); err != nil {
				t.Fatalf("%s encode: %v", tc.name, err)
			}
			if _, _, err := image.Decode(&buf); err != nil {
				t.Errorf("image.Decode(%s): %v", tc.name, err)
			}
		})
	}
}

// specByName returns the manifest spec with the given name.
func specByName(t *testing.T, name string) fixture.Spec {
	t.Helper()
	for _, s := range loadManifest(t) {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no fixture named %q", name)
	return fixture.Spec{}
}
