package unpixel_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire DefaultComponents
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
