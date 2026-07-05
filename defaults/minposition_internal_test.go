package defaults

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/metric"
)

// TestMinPositionDist_RefinesOddOffset guards alignedDist's coarse-to-fine position
// search. The candidate matches the target at x=2 — an offset the coarse sweep
// (step alignPosStep=4, sampling x=0,4,8,…) never visits. A coarse-only search
// would bottom out at a positive distance; the ±(alignPosStep-1) refinement around
// the coarse optimum must reach x=2 and return ≈0. This is exactly the sub-coarse
// alignment that recovers the block-10 "r00t" context redaction.
func TestMinPositionDist_RefinesOddOffset(t *testing.T) {
	const (
		w, h    = 48, 16
		bw, bh  = 8, 8
		trueOff = 2 // between the coarse grid samples 0 and 4
	)
	if trueOff%alignPosStep == 0 {
		t.Fatalf("test offset %d is on the coarse grid (step %d); it must be off-grid", trueOff, alignPosStep)
	}

	// A solid dark block, already "pixelated" (constant colour), to slide.
	block := image.NewRGBA(image.Rect(0, 0, bw, bh))
	for i := range block.Pix {
		block.Pix[i] = 0x20 // dark, opaque enough for a clear mismatch off-position
	}
	for p := 3; p < len(block.Pix); p += 4 {
		block.Pix[p] = 0xFF // alpha
	}

	// Target: white canvas with the block composed at the off-grid offset.
	target := image.NewRGBA(image.Rect(0, 0, w, h))
	imutil.FillWhite(target)
	imutil.Compose(target, block, trueOff, 0)

	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	m := metric.NewPixelmatch(alignTolerance)

	got := minPositionDist(t.Context(), canvas, block, target, m)
	if got > 0.001 {
		t.Errorf("minPositionDist = %.4f, want ≈0 — the ±%d refinement should reach the off-grid optimum at x=%d",
			got, alignPosStep-1, trueOff)
	}
}
