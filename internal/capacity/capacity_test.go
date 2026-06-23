package capacity_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/capacity"
	"github.com/oioio-space/unpixel/internal/render"
)

// newRenderer builds a fresh XImage renderer or fatals the test.
func newRenderer(t *testing.T) *render.XImage {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage() error = %v", err)
	}
	return r
}

// TestCoarseBlocksCollapseMore asserts the information-theoretic claim:
// a finer block size preserves more distinct glyph signatures and therefore
// yields more equivalence classes (higher BitsPerGlyph).
func TestCoarseBlocksCollapseMore(t *testing.T) {
	r := newRenderer(t)
	ctx := t.Context()

	const charset = "abcdefghijklmnopqrstuvwxyz"
	const fontSize = 16.0

	fine, err := capacity.Analyze(ctx, r, charset, fontSize, 2, image.Point{})
	if err != nil {
		t.Fatalf("Analyze(block=2) error = %v", err)
	}
	coarse, err := capacity.Analyze(ctx, r, charset, fontSize, 16, image.Point{})
	if err != nil {
		t.Fatalf("Analyze(block=16) error = %v", err)
	}

	t.Logf("block=2:  classes=%d BitsPerGlyph=%.3f", len(fine.Classes), fine.BitsPerGlyph)
	t.Logf("block=16: classes=%d BitsPerGlyph=%.3f", len(coarse.Classes), coarse.BitsPerGlyph)

	got := fine.BitsPerGlyph
	want := coarse.BitsPerGlyph
	if got <= want {
		t.Errorf("expected bits(block=2) > bits(block=16), got %.3f <= %.3f", got, want)
	}
}

// TestKnownConfusionsAppear checks that visually similar glyphs end up in the
// same equivalence class at a coarse block size. At block=16 with fontSize=16
// the entire glyph fits in roughly one block, so circular letters ('o', 'c',
// 'e') and tall narrow letters ('l', 'i') tend to collapse together.
// The test asserts membership, not the exact partition.
func TestKnownConfusionsAppear(t *testing.T) {
	r := newRenderer(t)
	ctx := t.Context()

	// Observed partition at block=16 font=16 (Liberation Sans, sRGB averaging):
	//   class 0: "abcdeghknopqsuvxyz"  — medium-weight rounded shapes
	//   class 1: "fijlrt"              — thin/tall narrow strokes (l and i together)
	//   class 2: "mw"                  — wide glyphs
	// This confirms 'l' and 'i' collapse, as do 'o'/'c'/'e' with many others.
	const charset = "abcdefghijklmnopqrstuvwxyz"
	result, err := capacity.Analyze(ctx, r, charset, 16.0, 16, image.Point{})
	if err != nil {
		t.Fatalf("Analyze error = %v", err)
	}

	t.Logf("classes at block=16, font=16:")
	for i, cls := range result.Classes {
		t.Logf("  class %d: %q", i, string(cls.Members))
	}

	// Confusable map must be non-empty when any class has multiple members.
	if len(result.Confusable) == 0 {
		t.Error("expected Confusable to be non-empty: at block=16 many glyphs collapse")
	}

	// At coarse block the 26-letter alphabet must produce fewer than 26 classes.
	nClasses := len(result.Classes)
	if nClasses >= len([]rune(charset)) {
		t.Errorf("expected classes < charset size at block=16, got %d classes for %d runes", nClasses, len([]rune(charset)))
	}

	// Assert 'l' and 'i' are in the same class (thin tall strokes collapse).
	lConf := result.Confusable['l']
	found := false
	for _, ch := range lConf {
		if ch == 'i' {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'l' and 'i' to be confusable at block=16 font=16; Confusable['l']=%q", string(lConf))
	}
}

// TestDeterminism verifies that the same inputs always produce an identical Capacity.
func TestDeterminism(t *testing.T) {
	r := newRenderer(t)
	ctx := t.Context()

	const charset = "abcdefghijklmnopqrstuvwxyz"
	phase := image.Point{X: 3, Y: 5}

	a, err := capacity.Analyze(ctx, r, charset, 32.0, 8, phase)
	if err != nil {
		t.Fatalf("first Analyze error = %v", err)
	}
	b, err := capacity.Analyze(ctx, r, charset, 32.0, 8, phase)
	if err != nil {
		t.Fatalf("second Analyze error = %v", err)
	}

	if a.BitsPerGlyph != b.BitsPerGlyph {
		t.Errorf("BitsPerGlyph: got %v vs %v (non-deterministic)", a.BitsPerGlyph, b.BitsPerGlyph)
	}
	if len(a.Classes) != len(b.Classes) {
		t.Errorf("len(Classes): got %d vs %d (non-deterministic)", len(a.Classes), len(b.Classes))
	}
	for i := range min(len(a.Classes), len(b.Classes)) {
		if string(a.Classes[i].Members) != string(b.Classes[i].Members) {
			t.Errorf("Classes[%d]: got %q vs %q", i, string(a.Classes[i].Members), string(b.Classes[i].Members))
		}
	}
}

// TestDigitsSeparation checks that digits 0-9 are meaningfully separated at a
// geometry typical of real terminal/IDE redactions (font=32, block=8).
//
// Observed at this geometry (zero phase, sRGB averaging, Liberation Sans):
//   - "0689" collapse to one class (circular/closed-top rounded shapes)
//   - "13"   collapse to one class (tall thin strokes)
//   - "2", "4", "5", "7" are each their own class
//
// This yields 6 classes / 2.585 bits — meaningfully above 1 bit but well
// below the theoretical maximum of log₂(10)=3.32 bits, confirming that
// digit recovery at block=8 is feasible yet not trivial.
//
// The threshold of 6 is set to the actually-observed count so the test is
// honest: if rendering changes cause further collapse (fewer classes) the test
// will catch the regression.
func TestDigitsSeparation(t *testing.T) {
	r := newRenderer(t)
	ctx := t.Context()

	result, err := capacity.Analyze(ctx, r, "0123456789", 32.0, 8, image.Point{})
	if err != nil {
		t.Fatalf("Analyze(digits) error = %v", err)
	}

	t.Logf("digit geometry (font=32, block=8): classes=%d BitsPerGlyph=%.3f",
		len(result.Classes), result.BitsPerGlyph)
	for _, cls := range result.Classes {
		t.Logf("  class: %q", string(cls.Members))
	}

	// Require at least 6 distinct classes: this is the observed minimum at this
	// geometry. A drop below 6 would indicate increased collapse (rendering
	// regression or wrong τ). The theoretical max is 10 (all distinct).
	const minClasses = 6
	nClasses := len(result.Classes)
	if nClasses < minClasses {
		t.Errorf("digit separation: got %d classes, want >= %d (regression: too much collapse at font=32 block=8)", nClasses, minClasses)
	}
	// Also assert BitsPerGlyph is meaningfully above 1 bit.
	if result.BitsPerGlyph < 2.0 {
		t.Errorf("digit BitsPerGlyph: got %.3f, want >= 2.0", result.BitsPerGlyph)
	}
}

// BenchmarkAnalyze measures throughput for a realistic full-alphabet charset.
// This is a one-shot analysis call (off the hot recovery path) but it should
// complete in well under a millisecond for the default alphabet.
func BenchmarkAnalyze(b *testing.B) {
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage() error = %v", err)
	}
	ctx := b.Context()
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789 "

	b.ResetTimer()
	for b.Loop() {
		_, err := capacity.Analyze(ctx, r, charset, 32.0, 8, image.Point{})
		if err != nil {
			b.Fatalf("Analyze error = %v", err)
		}
	}
}
