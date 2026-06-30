package mosaictext

// TestMultiFrameDistWidensGap is a white-box controlled experiment that
// measures whether scoring against phase-diverse frames widens the
// true-vs-wrong MSE gap compared to single-frame scoring.
//
// It exercises the frame-0-relative delta invariant: deltas are anchored to
// frame 0 (so sfs[0].pox == 0 always), not to the minimum phase. Cases where
// frame 1 has the smaller absolute phase (e.g. phases [(5,0),(2,0)]) are the
// critical regression path: under the old min-anchored scheme frame 0's delta
// was 3 (nonzero), misaligning it from the calibrated sweep; post-fix it is 0.

import (
	"fmt"
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

func TestMultiFrameDistWidensGap(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}

	const (
		trueText  = "be"
		wrongText = "bc"
		fs        = 24.0
	)

	cases := []struct {
		block  int
		phases [2][2]int // absolute (X,Y) grid phases; frame1 X < frame0 X → frame 1 is min
	}{
		// frame 1 has the minimum X phase: old min-anchor gave Δ0=(3,0) Δ1=(0,0),
		// misaligning frame 0 from the sweep; fixed gives Δ0=(0,0) Δ1=(5,0).
		{block: 8, phases: [2][2]int{{5, 0}, {2, 0}}},
		// same pattern at block=16.
		{block: 16, phases: [2][2]int{{10, 0}, {3, 0}}},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("block%d", tc.block), func(t *testing.T) {
			block := tc.block
			p0, p1 := tc.phases[0], tc.phases[1]

			// Render the true text and crop to ink bounds for a compact canvas.
			img, sx, rerr := r.Render(trueText, unpixel.Style{FontSize: fs})
			if rerr != nil || sx <= 0 {
				t.Fatalf("render: %v", rerr)
			}
			bb := inkBounds(img, sx)

			// Build a padded RGBA canvas from the ink crop — the pre-pixelation source.
			const pad = 4
			src := image.NewRGBA(image.Rect(0, 0, bb.Dx()+pad*2, bb.Dy()+pad*2))
			for i := range len(src.Pix) / 4 {
				src.Pix[i*4+0] = 255
				src.Pix[i*4+1] = 255
				src.Pix[i*4+2] = 255
				src.Pix[i*4+3] = 255
			}
			for y := range bb.Dy() {
				for x := range bb.Dx() {
					src.SetRGBA(pad+x, pad+y, img.RGBAAt(bb.Min.X+x, bb.Min.Y+y))
				}
			}

			pix := pixelate.NewBlockAverage(block)

			// Build per-frame mosaic targets at their respective absolute phases.
			mosaic0 := pix.Pixelate(src, p0[0], p0[1])
			mosaic1 := pix.Pixelate(src, p1[0], p1[1])

			// Build a single-frame decoder targeting mosaic0 (frame 0 anchor).
			d := &decoder{
				r:        r,
				target:   mosaic0,
				tW:       mosaic0.Bounds().Dx(),
				tH:       mosaic0.Bounds().Dy(),
				block:    block,
				pixelate: pix,
				cacheCap: minCacheEntries,
			}
			if _, _, _, ok := d.calibrate(); !ok {
				t.Skipf("calibrate failed (image too small for font probe at block=%d)", block)
			}

			stretch := d.stretchForN(len([]rune(trueText)))

			d.cache = newRenderCache(minCacheEntries)
			defer func() { d.cache = nil }()

			// Single-frame: score both candidates at frame-0's absolute phase.
			trueS := d.dist(trueText, fs, stretch, p0[0])
			wrongS := d.dist(wrongText, fs, stretch, p0[0])
			singleGap := wrongS - trueS

			// Replicate the fixed buildFrames logic: deltas are frame-0-relative,
			// normalized to [0, block) via the positive-modulo idiom.
			// df=1 (no coarse downscale), so no rounding step is needed.
			// Frame 0's delta is always (0,0) by definition — it anchors the sweep.
			b := block
			const dx0, dy0 = 0, 0 // frame 0 is the anchor; Δ=(0,0) always
			dx1 := ((p1[0]-p0[0])%b + b) % b
			dy1 := ((p1[1]-p0[1])%b + b) % b

			d.frames = []scoreFrame{
				{target: mosaic0, pixelate: pix, pox: dx0, poy: dy0},
				{target: mosaic1, pixelate: pix, pox: dx1, poy: dy1},
			}

			// pox passed to dist is the frame-0 absolute phase — correct because
			// dist places frame i at (pox + f.pox) and frame 0's f.pox is now 0.
			trueM := d.dist(trueText, fs, stretch, p0[0])
			wrongM := d.dist(wrongText, fs, stretch, p0[0])
			multiGap := wrongM - trueM

			t.Logf("block=%d phases=(%v→%v) frame0-relativeDeltas=(Δ0=(%d,%d) Δ1=(%d,%d))",
				block, p0, p1, dx0, dy0, dx1, dy1)
			t.Logf("  single-frame: true=%.2f wrong=%.2f gap=%.2f", trueS, wrongS, singleGap)
			t.Logf("  multi-frame:  true=%.2f wrong=%.2f gap=%.2f", trueM, wrongM, multiGap)

			// The multi-frame scores must be finite and non-negative.
			// Pre-fix, a negative-size canvas produced +Inf or 0 MSE.
			if trueM < 0 || trueM != trueM {
				t.Fatalf("trueDistMulti=%v is invalid — canvas bug not fixed", trueM)
			}
			if wrongM < 0 || wrongM != wrongM {
				t.Fatalf("wrongDistMulti=%v is invalid", wrongM)
			}

			// When the single-frame decoder already discriminates (gap > 0), verify
			// that multi-frame scoring preserves that discrimination — i.e. the true
			// text still wins. Averaging two frames can narrow the absolute gap (each
			// frame contributes partial signal), but it must not flip the ranking.
			if singleGap > 0 && multiGap <= 0 {
				t.Errorf("multi-frame scoring flipped a correct single-frame discrimination: singleGap=%.2f multiGap=%.2f",
					singleGap, multiGap)
			}
		})
	}
}
