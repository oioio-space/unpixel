package unpixel_test

import (
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire DefaultComponents
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

func TestVerify_decisive(t *testing.T) {
	img := loadFixtureImage(t, "block08_go.png")
	vs, err := unpixel.Verify(t.Context(), img, []string{"go", "xy"},
		unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz "),
		unpixel.WithBlockSize(8),
		unpixel.WithMaxLength(3))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	byText := map[string]unpixel.Verdict{}
	for _, v := range vs {
		byText[v.Text] = v
	}
	if got := byText["go"]; !got.Match || got.Distance > 0.2 {
		t.Errorf("Verify(go) = {dist %.3f, match %v}, want match with dist≈0", got.Distance, got.Match)
	}
	if got := byText["xy"]; got.Match {
		t.Errorf("Verify(xy) = match (dist %.3f), want no-match for a wrong string", got.Distance)
	}
}

// TestVerify_calibration asserts that VerifyMatchThreshold (τ = 0.10) cleanly
// separates true strings (dist ≈ 0, scored < τ → Match=true) from wrong
// same-length strings (dist ≈ 0.44–0.49, scored ≥ τ → Match=false) across
// three committed fixtures from testdata/fixtures/manifest.json:
//
//   - block08_go  → "go"    (charset "abcdefghijklmnopqrstuvwxyz ", block 8)
//   - text_hello  → "hello" (charset "helo abcd",                   block 8)
//   - alnum_Go2   → "Go2"   (charset "Go2 abc019",                  block 8)
//
// Observed spread: true dist = 0.0000, wrong dist = 0.44–0.49 (τ = 0.10
// leaves a gap of > 0.34 — well above measurement noise).
func TestVerify_calibration(t *testing.T) {
	cases := []struct {
		fixture string
		truth   string
		wrong   string // same length, clearly different
		charset string
	}{
		{"block08_go.png", "go", "xy", "abcdefghijklmnopqrstuvwxyz "},
		{"text_hello.png", "hello", "world", "helo abcd"},
		{"alnum_Go2.png", "Go2", "Ab3", "Go2 abc019"},
	}
	const τ = unpixel.VerifyMatchThreshold
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			img := loadFixtureImage(t, tc.fixture)
			vs, err := unpixel.Verify(t.Context(), img,
				[]string{tc.truth, tc.wrong},
				unpixel.WithCharset(tc.charset),
				unpixel.WithBlockSize(8),
			)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			byText := make(map[string]unpixel.Verdict, len(vs))
			for _, v := range vs {
				byText[v.Text] = v
			}

			trueV := byText[tc.truth]
			if trueV.Distance >= τ {
				t.Errorf("true %q: dist %.4f >= τ %.2f (want < τ, Match=true)", tc.truth, trueV.Distance, τ)
			}
			if !trueV.Match {
				t.Errorf("true %q: Match=false (dist %.4f), want true", tc.truth, trueV.Distance)
			}

			wrongV := byText[tc.wrong]
			if wrongV.Distance < τ {
				t.Errorf("wrong %q: dist %.4f < τ %.2f (want >= τ, Match=false)", tc.wrong, wrongV.Distance, τ)
			}
			if wrongV.Match {
				t.Errorf("wrong %q: Match=true (dist %.4f), want false", tc.wrong, wrongV.Distance)
			}
		})
	}
}

// TestVerify_nilImage verifies that Verify returns ErrNilImage for a nil image.
func TestVerify_nilImage(t *testing.T) {
	_, err := unpixel.Verify(t.Context(), nil, []string{"go"})
	if !errors.Is(err, unpixel.ErrNilImage) {
		t.Errorf("Verify(nil image) = %v, want ErrNilImage", err)
	}
}

// TestVerify_emptyCandidates verifies that Verify returns an empty (non-nil)
// slice and no error when the candidates list is empty.
func TestVerify_emptyCandidates(t *testing.T) {
	img := loadFixtureImage(t, "block08_go.png")
	vs, err := unpixel.Verify(t.Context(), img, nil,
		unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz "),
		unpixel.WithBlockSize(8),
	)
	if err != nil {
		t.Fatalf("Verify(empty): %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("Verify(empty candidates) = %d verdicts, want 0", len(vs))
	}
}

// TestVerify_maxCandidatesCap verifies that Verify silently ignores candidates
// beyond maxVerifyCandidates (256) and returns at most 256 verdicts.
func TestVerify_maxCandidatesCap(t *testing.T) {
	img := loadFixtureImage(t, "block08_go.png")
	// Build 300 candidates — all unique but recognisably wrong.
	cands := make([]string, 300)
	for i := range 300 {
		cands[i] = strings.Repeat("a", (i%5)+1)
	}
	vs, err := unpixel.Verify(t.Context(), img, cands,
		unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz "),
		unpixel.WithBlockSize(8),
	)
	if err != nil {
		t.Fatalf("Verify(300 cands): %v", err)
	}
	if len(vs) > 256 {
		t.Errorf("Verify(300 cands) = %d verdicts, want ≤ 256", len(vs))
	}
}

// TestVerify_autoPath exercises Verify's auto-detection prologue (no explicit
// block/charset): it must run dark/invert + deskew + autocrop + fingerprint +
// calibrate through Verify without error or panic and return one verdict per
// candidate. (Auto block-size inference is weak on tiny crops, so this does not
// assert a match — only that the auto path is wired and safe.)
func TestVerify_autoPath(t *testing.T) {
	img := loadFixtureImage(t, "block08_go.png")
	vs, err := unpixel.Verify(t.Context(), img, []string{"go", "xy"})
	if err != nil {
		t.Fatalf("Verify(auto): %v", err)
	}
	if len(vs) != 2 {
		t.Errorf("Verify(auto) = %d verdicts, want 2", len(vs))
	}
}

// mosaicWord builds a self-consistent synthetic mosaic of text using the same
// pipeline as PipelineScorer (render → BlueMargin → crop → pad → pixelate →
// LeftEdge → vertical crop) with the default style (32 pt, 8 px padding) so
// that Verify's re-render of the true text produces a distance ≈ 0. It returns
// the mosaic image and the block size used.
func mosaicWord(t *testing.T, text string) (image.Image, int) {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil) // Liberation Sans
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	// Use the same style that applyDefaults fills in so Verify's re-render matches.
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	const block = 6
	px := pixelate.NewBlockAverage(block)

	img, sentinelX, err := r.Render(text, style)
	if err != nil || sentinelX <= 0 {
		t.Fatalf("render: %v sentinelX=%d", err, sentinelX)
	}

	// Mirror PipelineScorer's stageImage steps 2–7 with offset (0,0).
	bm, imageCenter := imutil.BlueMargin(img)
	if bm == 0 {
		bm = sentinelX
	}
	img = imutil.Crop(img, 0, 0, bm, img.Bounds().Dy())
	w := img.Bounds().Dx()
	if rem := block - (w % block); rem < block {
		img = imutil.PadWhite(img, w+rem, img.Bounds().Dy())
	}
	pixelated := px.Pixelate(img, 0, 0)
	le := imutil.LeftEdge(pixelated)
	adjustedCenter := imageCenter - (imageCenter % block) + 4
	redactedH := 2 * adjustedCenter
	mosaic := imutil.Crop(pixelated, le, 0, pixelated.Bounds().Dx()-le, pixelated.Bounds().Dy())
	if mosaic.Bounds().Dy() < redactedH {
		mosaic = imutil.PadWhite(mosaic, mosaic.Bounds().Dx(), redactedH)
	}
	return mosaic, block
}

// TestVerifyImage_nilImages verifies that VerifyImage returns ErrNilImage when
// either the redacted or restored image is nil.
func TestVerifyImage_nilImages(t *testing.T) {
	img, _ := mosaicWord(t, "the")
	if _, err := unpixel.VerifyImage(t.Context(), nil, img); !errors.Is(err, unpixel.ErrNilImage) {
		t.Errorf("nil redacted err = %v; want ErrNilImage", err)
	}
	if _, err := unpixel.VerifyImage(t.Context(), img, nil); !errors.Is(err, unpixel.ErrNilImage) {
		t.Errorf("nil restored err = %v; want ErrNilImage", err)
	}
}

// TestVerify_unchangedAfterRefactor confirms that extracting prepareVerify did
// not alter Verify's observable behaviour: the true text matches and a clearly
// wrong candidate does not.
func TestVerify_unchangedAfterRefactor(t *testing.T) {
	// Build an in-memory mosaic of "the" and confirm Verify still ranks the true
	// text below the match threshold and a wrong candidate above it.
	img, block := mosaicWord(t, "the")
	verdicts, err := unpixel.Verify(t.Context(), img, []string{"the", "xyz"},
		unpixel.WithBlockSize(block), unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(verdicts) != 2 {
		t.Fatalf("verdicts len = %d; want 2", len(verdicts))
	}
	if !verdicts[0].Match {
		t.Errorf("true text %q not a Match (distance %.3f)", verdicts[0].Text, verdicts[0].Distance)
	}
	if verdicts[1].Match {
		t.Errorf("wrong text %q unexpectedly Match (distance %.3f)", verdicts[1].Text, verdicts[1].Distance)
	}
}
