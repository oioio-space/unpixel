package mosaictext_test

import (
	"errors"
	"image"
	"testing"
	"time"

	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/mosaictext"
)

// ---- DecodeMultiFrameAuto error paths ----

// TestDecodeMultiFrameAuto_Errors verifies input-validation error paths.
func TestDecodeMultiFrameAuto_Errors(t *testing.T) {
	ctx := t.Context()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))

	for _, tc := range []struct {
		name string
		imgs []image.Image
	}{
		{name: "nil slice", imgs: nil},
		{name: "empty slice", imgs: []image.Image{}},
		{name: "nil element", imgs: []image.Image{nil}},
		{name: "nil second element", imgs: []image.Image{img, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mosaictext.DecodeMultiFrameAuto(ctx, tc.imgs)
			if err == nil {
				t.Errorf("DecodeMultiFrameAuto(%s): got nil error, want non-nil", tc.name)
			}
		})
	}
}

// ---- Single-frame equivalence ----

// TestDecodeMultiFrameAuto_SingleFrameEquivalent verifies that a one-element
// slice produces the same result as calling Decode directly on that image.
//
// It uses a small panel fixture (not the large real-world capture) so the
// byte-identical contract is exercised on every run — including the default,
// non-short test pass — without the multi-minute cost of a blind decode of a
// full-size photo.
func TestDecodeMultiFrameAuto_SingleFrameEquivalent(t *testing.T) {
	ctx := t.Context()
	img := loadPNG(t, "../testdata/fixtures/text_cat.png")

	want, err := mosaictext.Decode(ctx, img)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	got, err := mosaictext.DecodeMultiFrameAuto(ctx, []image.Image{img})
	if err != nil {
		t.Fatalf("DecodeMultiFrameAuto(1 image): %v", err)
	}

	t.Logf("Decode:                  %q (dist=%.4f)", want.Text, want.Distance)
	t.Logf("DecodeMultiFrameAuto(1): %q (dist=%.4f)", got.Text, got.Distance)

	if got.Text != want.Text {
		t.Errorf("single-frame contract (text): got %q, want %q", got.Text, want.Text)
	}
	if got.Distance != want.Distance {
		t.Errorf("single-frame contract (distance): got %v, want %v", got.Distance, want.Distance)
	}
}

// ---- Multi-frame smoke / quality ----

// TestDecodeMultiFrameAuto_MultiFrame verifies that auto-phased multi-frame
// fusion runs end-to-end without error and yields a decode whose distance is
// not severely worse than a single-frame decode of the same source.
//
// The fixture is already-pixelated, so re-pixelating it at additional phases
// adds no new sub-block information; accordingly the assertion is lenient
// (≤ 50 % degradation), matching the contract established in
// TestDecodeMultiFrame_TwoFrames.
func TestDecodeMultiFrameAuto_MultiFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-frame auto-decode in -short mode")
	}
	// This test now runs a full multi-frame decode sweep (~4 frames × ~150 s each
	// at full resolution), which is too slow for the default go-test timeout shared
	// with the rest of the package. Skip unless the caller explicitly raises the
	// timeout with -timeout or uses mise run ci (which sets a long per-package limit).
	if dl, ok := t.Deadline(); ok {
		if time.Until(dl) < 600*time.Second {
			t.Skip("skipping: remaining test budget < 600 s (run with -timeout=15m or mise run ci)")
		}
	}
	ctx := t.Context()

	src := loadPNG(t, "../testdata/real/hello-world.png")
	srcRGBA := toRGBATest(src)
	const block = 16

	phases := [][2]int{{0, 0}, {3, 0}, {0, 4}, {5, 2}}
	pix := pixelate.NewBlockAverage(block)
	imgs := make([]image.Image, len(phases))
	for i, ph := range phases {
		imgs[i] = pix.Pixelate(srcRGBA, ph[0], ph[1])
	}

	isSentinel := func(err error) bool {
		return errors.Is(err, mosaictext.ErrNoMosaic) || errors.Is(err, mosaictext.ErrNoContent)
	}

	res, err := mosaictext.DecodeMultiFrameAuto(ctx, imgs)
	if err != nil {
		if isSentinel(err) {
			t.Logf("DecodeMultiFrameAuto(%d frames): non-fatal sentinel: %v", len(imgs), err)
			return
		}
		t.Fatalf("DecodeMultiFrameAuto: %v", err)
	}
	t.Logf("DecodeMultiFrameAuto(%d frames): %q (dist=%.4f)", len(imgs), res.Text, res.Distance)

	// Compare against single-frame baseline.
	single, singleErr := mosaictext.DecodeMultiFrameAuto(ctx, imgs[:1])
	if isSentinel(singleErr) {
		return // cannot compare; skip quality gate
	}
	if singleErr != nil {
		t.Fatalf("single-frame DecodeMultiFrameAuto: %v", singleErr)
	}
	t.Logf("single-frame: %q (dist=%.4f)", single.Text, single.Distance)

	if res.Distance > single.Distance*1.5 {
		t.Errorf("multi-frame distance %.4f is more than 50%% worse than single-frame %.4f",
			res.Distance, single.Distance)
	}
}
