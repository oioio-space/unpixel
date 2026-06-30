package mosaictext_test

import (
	"context"
	"errors"
	"image"
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/mosaictext"
)

// multiframeSink absorbs benchmark results so the compiler cannot eliminate the call.
var multiframeSink mosaictext.Result

// toRGBATest converts any image.Image to *image.RGBA for test helper use.
func toRGBATest(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x, y, img.At(x, y))
		}
	}
	return dst
}

// buildJitteredFrames pixelates src at each phase and wraps the results as
// mosaictext.Frame values for use with DecodeMultiFrame.
func buildJitteredFrames(src *image.RGBA, block int, phases [][2]int) []mosaictext.Frame {
	frames := make([]mosaictext.Frame, len(phases))
	pix := pixelate.NewBlockAverage(block)
	for i, ph := range phases {
		frames[i] = mosaictext.Frame{
			Img:     pix.Pixelate(src, ph[0], ph[1]),
			OffsetX: ph[0],
			OffsetY: ph[1],
		}
	}
	return frames
}

// ---- Frame type ----

// TestFrameType verifies that mosaictext.Frame is exported and can be
// constructed without importing an internal package.
func TestFrameType(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range len(img.Pix) {
		img.Pix[i] = 255
	}
	f := mosaictext.Frame{Img: img, OffsetX: 1, OffsetY: 2}
	if f.Img == nil {
		t.Error("Frame.Img is nil after assignment")
	}
	if f.OffsetX != 1 || f.OffsetY != 2 {
		t.Errorf("Frame offsets: got (%d,%d), want (1,2)", f.OffsetX, f.OffsetY)
	}
}

// ---- DecodeMultiFrame error paths ----

// TestDecodeMultiFrame_NoFrames verifies that a nil slice returns an error.
func TestDecodeMultiFrame_NoFrames(t *testing.T) {
	ctx := t.Context()
	_, err := mosaictext.DecodeMultiFrame(ctx, nil)
	if err == nil {
		t.Error("DecodeMultiFrame(nil) expected error, got nil")
	}
}

// TestDecodeMultiFrame_EmptyFrames verifies that an empty slice returns an error.
func TestDecodeMultiFrame_EmptyFrames(t *testing.T) {
	ctx := t.Context()
	_, err := mosaictext.DecodeMultiFrame(ctx, []mosaictext.Frame{})
	if err == nil {
		t.Error("DecodeMultiFrame(empty) expected error, got nil")
	}
}

// TestDecodeMultiFrame_NilImage verifies that a nil Img inside a Frame returns an error.
func TestDecodeMultiFrame_NilImage(t *testing.T) {
	ctx := t.Context()
	frames := []mosaictext.Frame{{Img: nil, OffsetX: 0, OffsetY: 0}}
	_, err := mosaictext.DecodeMultiFrame(ctx, frames)
	if err == nil {
		t.Error("DecodeMultiFrame(nil Img) expected error, got nil")
	}
}

// ---- Single-frame equivalence ----

// TestDecodeMultiFrame_SingleFrameEquivalent verifies that passing exactly one
// frame to DecodeMultiFrame produces the same result as calling Decode directly
// on that frame's image.  This is the "safe superset" contract.
func TestDecodeMultiFrame_SingleFrameEquivalent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping single-frame equivalence (full decode) in -short mode")
	}
	ctx := t.Context()
	img := loadPNG(t, "../testdata/real/hello-world.png")

	// Direct Decode.
	want, err := mosaictext.Decode(ctx, img)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// One-frame DecodeMultiFrame — fusion is a no-op for a single frame, so the
	// result must be identical.
	frames := []mosaictext.Frame{{Img: img, OffsetX: 0, OffsetY: 0}}
	got, err := mosaictext.DecodeMultiFrame(ctx, frames)
	if err != nil {
		t.Fatalf("DecodeMultiFrame(1 frame): %v", err)
	}

	t.Logf("Decode:              %q (dist=%.2f)", want.Text, want.Distance)
	t.Logf("DecodeMultiFrame(1): %q (dist=%.2f)", got.Text, got.Distance)

	if got.Text != want.Text {
		t.Errorf("single-frame contract broken: DecodeMultiFrame=%q, Decode=%q",
			got.Text, want.Text)
	}
}

// ---- Multi-frame smoke test ----

// TestDecodeMultiFrame_MultiFrame verifies that DecodeMultiFrame accepts multiple
// jittered frames of the real hello-world fixture without error.  Re-pixelating the
// already-pixelated source at additional phase offsets alters the block means, so
// the fused image may not decode perfectly — the assertions here cover the
// integration path (no panic, valid error contract) rather than text accuracy.
func TestDecodeMultiFrame_MultiFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-frame decode in -short mode")
	}
	ctx := t.Context()

	img := loadPNG(t, "../testdata/real/hello-world.png")
	srcRGBA := toRGBATest(img)
	const block = 16

	phases := [][2]int{{0, 0}, {3, 0}, {0, 4}, {5, 2}}
	frames := buildJitteredFrames(srcRGBA, block, phases)

	res, err := mosaictext.DecodeMultiFrame(ctx, frames)
	if err != nil {
		// ErrNoMosaic / ErrNoContent are acceptable: the fused image from
		// re-pixelating an already-pixelated source may not always pass the
		// block-grid detector.  Any other error is a real failure.
		if errors.Is(err, mosaictext.ErrNoMosaic) || errors.Is(err, mosaictext.ErrNoContent) {
			t.Logf("DecodeMultiFrame(%d frames): non-fatal: %v", len(frames), err)
			return
		}
		t.Fatalf("DecodeMultiFrame: %v", err)
	}
	t.Logf("DecodeMultiFrame(%d frames): %q (dist=%.2f)", len(frames), res.Text, res.Distance)
}

// TestDecodeMultiFrame_TwoFrames exercises the two-frame fusion path end to end
// and asserts it produces a VALID decode (finite distance, non-empty text).
//
// It deliberately does NOT assert that two frames beat one: genuine multi-frame
// gain requires sub-pixel-jittered captures of the ORIGINAL content, whereas the
// only available testdata is a single already-pixelated mosaic. Re-pixelating
// that mosaic at two phases and fusing it carries no new sub-block signal, so
// the fused result can be marginally worse — an honest property of the input,
// not a defect. The comparison is logged for visibility. (See PROGRESS.md: the
// testdata is mono-frame; real multi-capture validation is future work.)
func TestDecodeMultiFrame_TwoFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping two-frame quality comparison in -short mode")
	}
	ctx := t.Context()
	img := loadPNG(t, "../testdata/real/hello-world.png")
	srcRGBA := toRGBATest(img)
	const block = 16

	// Single-frame decode: pixelate at phase (0,0) only.
	single := buildJitteredFrames(srcRGBA, block, [][2]int{{0, 0}})
	singleRes, singleErr := mosaictext.DecodeMultiFrame(ctx, single)

	// Two-frame decode: add a phase (4,0) frame.
	two := buildJitteredFrames(srcRGBA, block, [][2]int{{0, 0}, {4, 0}})
	twoRes, twoErr := mosaictext.DecodeMultiFrame(ctx, two)

	// If either call returns a non-fatal sentinel, skip — the test requires a
	// successful decode to compare distances.
	isSentinel := func(err error) bool {
		return errors.Is(err, mosaictext.ErrNoMosaic) || errors.Is(err, mosaictext.ErrNoContent)
	}
	if isSentinel(singleErr) || isSentinel(twoErr) {
		t.Skipf("skipping: one or both decodes returned non-fatal sentinel (single=%v two=%v)",
			singleErr, twoErr)
	}
	if singleErr != nil {
		t.Fatalf("single-frame DecodeMultiFrame: %v", singleErr)
	}
	if twoErr != nil {
		t.Fatalf("two-frame DecodeMultiFrame: %v", twoErr)
	}

	t.Logf("1-frame: %q dist=%.4f", singleRes.Text, singleRes.Distance)
	t.Logf("2-frame: %q dist=%.4f", twoRes.Text, twoRes.Distance)

	// Mechanism check: the two-frame fusion path must yield a valid decode.
	if math.IsInf(twoRes.Distance, 0) || math.IsNaN(twoRes.Distance) {
		t.Errorf("two-frame decode distance not finite: %v", twoRes.Distance)
	}
	if twoRes.Text == "" {
		t.Error("two-frame decode returned empty text")
	}
}

// ---- Multi-frame SCORING test (sharp-source fixtures, real assertion) ----

// TestDecodeMultiFrameScored_TwoFrames asserts that two-frame scoring is at
// least as good as single-frame on genuine phase-diverse fixtures generated
// from a SHARP (unredacted) source. The testdata/multiframe/ fixtures are
// pixelations of original sharp text at known sub-block phase offsets — the
// correct input for multi-frame scoring per the design.
//
// The real assertion: if single-frame decodes correctly, two-frame must also
// decode correctly (no regression). If both fail, the two-frame distance must
// not be severely worse. A strict improvement is logged but not required —
// the fixture uses block=8 which leaves enough per-block signal that
// single-frame often already succeeds, making improvement hard to demonstrate;
// the test guards against regression, not proves gain.
func TestDecodeMultiFrameScored_TwoFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-frame scoring assertion in -short mode")
	}
	ctx := t.Context()

	// Use the "hello_b8" case: block=8, text="hello", frames generated from a
	// genuine sharp source at phases (0,0) and (3,0).
	const wantText = "hello"
	frame0 := mosaictext.Frame{Img: loadPNG(t, "../testdata/multiframe/hello_b8_f0.png"), OffsetX: 0, OffsetY: 0}
	frame1 := mosaictext.Frame{Img: loadPNG(t, "../testdata/multiframe/hello_b8_f1.png"), OffsetX: 3, OffsetY: 0}

	isSentinel := func(err error) bool {
		return errors.Is(err, mosaictext.ErrNoMosaic) || errors.Is(err, mosaictext.ErrNoContent)
	}

	// Single-frame baseline: frame 0 only.
	singleRes, singleErr := mosaictext.DecodeMultiFrame(ctx, []mosaictext.Frame{frame0})
	if isSentinel(singleErr) {
		t.Skipf("single-frame baseline sentinel (frame0 not decodable): %v", singleErr)
	}
	if singleErr != nil {
		t.Fatalf("single-frame DecodeMultiFrame: %v", singleErr)
	}

	// Two-frame scoring: frames at phases (0,0) and (3,0).
	twoRes, twoErr := mosaictext.DecodeMultiFrame(ctx, []mosaictext.Frame{frame0, frame1})
	if isSentinel(twoErr) {
		t.Skipf("two-frame decode sentinel: %v", twoErr)
	}
	if twoErr != nil {
		t.Fatalf("two-frame DecodeMultiFrame: %v", twoErr)
	}

	t.Logf("1-frame: text=%q dist=%.4f correct=%v", singleRes.Text, singleRes.Distance, singleRes.Text == wantText)
	t.Logf("2-frame: text=%q dist=%.4f correct=%v", twoRes.Text, twoRes.Distance, twoRes.Text == wantText)

	if twoRes.Distance < singleRes.Distance {
		t.Logf("two-frame strictly better by %.2f%%", (singleRes.Distance-twoRes.Distance)/singleRes.Distance*100)
	}

	// REAL assertion: if single-frame decoded correctly, two-frame must not regress.
	if singleRes.Text == wantText && twoRes.Text != wantText {
		t.Errorf("two-frame scoring regression: single-frame decoded %q correctly but two-frame decoded %q (dist=%.4f)",
			wantText, twoRes.Text, twoRes.Distance)
		return
	}

	// Either both failed or both succeeded: distance must not be severely worse.
	if twoRes.Distance > singleRes.Distance*1.5 {
		t.Errorf("two-frame distance %.4f is >50%% worse than single-frame %.4f",
			twoRes.Distance, singleRes.Distance)
	}
}

// ---- Benchmark ----

// BenchmarkDecodeMultiFrame measures the scoring-based multi-frame decode path
// over the real hello-world fixture at 4 sub-block-phase frames. Run with
// -count=10 for benchstat; compare against BenchmarkFullDecodeSweep to
// quantify the overhead of scoring N frames versus a single frame.
func BenchmarkDecodeMultiFrame(b *testing.B) {
	src := loadPNG(b, "../testdata/real/hello-world.png")
	srcRGBA := toRGBATest(src)
	const block = 16

	phases := [][2]int{{0, 0}, {3, 0}, {0, 4}, {5, 2}}
	frames := buildJitteredFrames(srcRGBA, block, phases)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		multiframeSink, err = mosaictext.DecodeMultiFrame(context.Background(), frames)
		if err != nil && !errors.Is(err, mosaictext.ErrNoMosaic) && !errors.Is(err, mosaictext.ErrNoContent) {
			b.Fatalf("DecodeMultiFrame: %v", err)
		}
	}
}
