package multiframe_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/internal/multiframe"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// TestDiscoverPhases_RoundTrip pixelates a source image at known (ox, oy)
// offsets and verifies that DiscoverPhases recovers each offset exactly.
func TestDiscoverPhases_RoundTrip(t *testing.T) {
	const (
		W     = 64
		H     = 64
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	cases := []struct {
		name string
		ox   int
		oy   int
	}{
		{name: "origin", ox: 0, oy: 0},
		{name: "x_only", ox: 3, oy: 0},
		{name: "y_only", ox: 0, oy: 4},
		{name: "both", ox: 5, oy: 2},
	}

	frames := make([]multiframe.Frame, len(cases))
	for i, tc := range cases {
		mosaic := pix.Pixelate(src, tc.ox, tc.oy)
		frames[i] = multiframe.Frame{Img: mosaic, OffsetX: 0, OffsetY: 0} // phases zeroed intentionally
	}

	got := multiframe.DiscoverPhases(frames, block)

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantX := tc.ox % block
			wantY := tc.oy % block
			if got[i].OffsetX != wantX {
				t.Errorf("OffsetX: got %d, want %d", got[i].OffsetX, wantX)
			}
			if got[i].OffsetY != wantY {
				t.Errorf("OffsetY: got %d, want %d", got[i].OffsetY, wantY)
			}
		})
	}
}

// TestDiscoverPhases_Deterministic verifies that two calls with the same input
// return identical output.
func TestDiscoverPhases_Deterministic(t *testing.T) {
	const (
		W     = 48
		H     = 48
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	frames := []multiframe.Frame{
		{Img: pix.Pixelate(src, 0, 0)},
		{Img: pix.Pixelate(src, 3, 0)},
		{Img: pix.Pixelate(src, 0, 4)},
		{Img: pix.Pixelate(src, 5, 2)},
	}

	got1 := multiframe.DiscoverPhases(frames, block)
	got2 := multiframe.DiscoverPhases(frames, block)

	for i := range frames {
		if got1[i].OffsetX != got2[i].OffsetX || got1[i].OffsetY != got2[i].OffsetY {
			t.Errorf("frame %d: run 1 = (%d,%d), run 2 = (%d,%d): non-deterministic",
				i, got1[i].OffsetX, got1[i].OffsetY, got2[i].OffsetX, got2[i].OffsetY)
		}
	}
}

// TestDiscoverPhases_NoMutate checks that DiscoverPhases does not mutate the
// input frames slice.
func TestDiscoverPhases_NoMutate(t *testing.T) {
	const (
		W     = 32
		H     = 32
		block = 8
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	frames := []multiframe.Frame{
		{Img: pix.Pixelate(src, 3, 5), OffsetX: 99, OffsetY: 77},
	}
	before := multiframe.Frame{
		Img:     frames[0].Img,
		OffsetX: frames[0].OffsetX,
		OffsetY: frames[0].OffsetY,
	}

	_ = multiframe.DiscoverPhases(frames, block)

	if frames[0].OffsetX != before.OffsetX || frames[0].OffsetY != before.OffsetY || frames[0].Img != before.Img {
		t.Errorf("input mutated: got (%d,%d), want (%d,%d)",
			frames[0].OffsetX, frames[0].OffsetY, before.OffsetX, before.OffsetY)
	}
}

// TestDiscoverPhases_BlockLessThan1 checks the no-op edge case: block < 1.
func TestDiscoverPhases_BlockLessThan1(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 128, A: 255}) //nolint:gosec
		}
	}

	frames := []multiframe.Frame{
		{Img: src, OffsetX: 3, OffsetY: 7},
	}

	for _, block := range []int{0, -1} {
		got := multiframe.DiscoverPhases(frames, block)
		// Must not panic and must return a copy with the original offsets preserved.
		if len(got) != len(frames) {
			t.Errorf("block=%d: got len %d, want %d", block, len(got), len(frames))
			continue
		}
		if got[0].OffsetX != frames[0].OffsetX || got[0].OffsetY != frames[0].OffsetY {
			t.Errorf("block=%d: got (%d,%d), want (%d,%d)",
				block, got[0].OffsetX, got[0].OffsetY, frames[0].OffsetX, frames[0].OffsetY)
		}
	}
}

// TestDiscoverPhases_PhaseInRange asserts the detected phases are in [0, block).
func TestDiscoverPhases_PhaseInRange(t *testing.T) {
	const (
		W     = 64
		H     = 32
		block = 10
	)
	src := syntheticSource(W, H)
	pix := pixelate.NewBlockAverage(block)

	frames := make([]multiframe.Frame, block)
	for p := range block {
		frames[p] = multiframe.Frame{Img: pix.Pixelate(src, p, p)}
	}

	got := multiframe.DiscoverPhases(frames, block)
	for i, f := range got {
		if f.OffsetX < 0 || f.OffsetX >= block {
			t.Errorf("frame %d: OffsetX %d not in [0,%d)", i, f.OffsetX, block)
		}
		if f.OffsetY < 0 || f.OffsetY >= block {
			t.Errorf("frame %d: OffsetY %d not in [0,%d)", i, f.OffsetY, block)
		}
	}
}
