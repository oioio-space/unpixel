package fontrank

// internal_test.go exercises unexported functions that are not reachable via
// the exported API with the inputs required to cover specific branches.

import (
	"image"
	"testing"
)

// TestCropToSentinel_earlyReturn verifies that cropToSentinel returns the
// original image unchanged when sentinelX ≤ b.Min.X (nothing to crop).
func TestCropToSentinel_earlyReturn(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	// sentinelX=0 → endX = min(0, 8) = 0; 0 <= 0 (b.Min.X) → early return.
	got := cropToSentinel(img, 0)
	if got != img {
		t.Error("cropToSentinel(img, 0): expected the original image pointer, got a different one")
	}
}

// TestCropToSentinel_normal verifies that cropToSentinel crops to sentinelX
// when sentinelX is within bounds and SubImage returns *image.RGBA directly.
func TestCropToSentinel_normal(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 16, 8))
	got := cropToSentinel(img, 10)
	if got == nil {
		t.Fatal("cropToSentinel returned nil")
	}
	if got.Bounds().Dx() != 10 {
		t.Errorf("cropToSentinel(img, 10).Dx() = %d, want 10", got.Bounds().Dx())
	}
}
