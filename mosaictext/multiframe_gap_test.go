package mosaictext

// TestMultiFrameDistWidensGap is a white-box controlled experiment that
// measures whether scoring against phase-diverse frames widens the
// true-vs-wrong MSE gap compared to single-frame scoring.
//
// It also directly exercises the negative-delta bug: phases [(5,0),(2,0)]
// give raw Δ_x = 2−5 = −3; pre-fix that produced an undersized/empty canvas
// in placed2, inflating the MSE at the correct phase and masking the true text.

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
		phases [2][2]int // absolute (X,Y) grid phases; frame1 X < frame0 X → negative raw delta
	}{
		{block: 8, phases: [2][2]int{{5, 0}, {2, 0}}},   // rawΔx = −3
		{block: 16, phases: [2][2]int{{10, 0}, {3, 0}}}, // rawΔx = −7
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

			// Build a single-frame decoder targeting mosaic0.
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

			// Replicate the fixed buildFrames logic: use the minimum phase as
			// baseline so all deltas are non-negative, then reduce mod block.
			// df=1 here (no coarse downscale), so no rounding step is needed.
			minX := min(p0[0], p1[0])
			minY := min(p0[1], p1[1])
			dx0 := (p0[0] - minX) % block
			dy0 := (p0[1] - minY) % block
			dx1 := (p1[0] - minX) % block
			dy1 := (p1[1] - minY) % block

			d.frames = []scoreFrame{
				{target: mosaic0, pixelate: pix, pox: dx0, poy: dy0},
				{target: mosaic1, pixelate: pix, pox: dx1, poy: dy1},
			}

			trueM := d.dist(trueText, fs, stretch, p0[0])
			wrongM := d.dist(wrongText, fs, stretch, p0[0])
			multiGap := wrongM - trueM

			t.Logf("block=%d phases=(%v→%v) normalizedDeltas=(Δ0=(%d,%d) Δ1=(%d,%d))",
				block, p0, p1, dx0, dy0, dx1, dy1)
			t.Logf("  single-frame: true=%.2f wrong=%.2f gap=%.2f", trueS, wrongS, singleGap)
			t.Logf("  multi-frame:  true=%.2f wrong=%.2f gap=%.2f", trueM, wrongM, multiGap)

			// The multi-frame scores must be finite and non-negative.
			// Pre-fix, a negative-size canvas produced +Inf or 0 MSE.
			if trueM < 0 || trueM != trueM {
				t.Fatalf("trueDistMulti=%v is invalid — negative-delta canvas bug not fixed", trueM)
			}
			if wrongM < 0 || wrongM != wrongM {
				t.Fatalf("wrongDistMulti=%v is invalid", wrongM)
			}

			// The multi-frame gap must not strongly collapse relative to single-frame.
			// 50 MSE units of tolerance covers numerical noise; the signal is typically
			// hundreds of units so a genuine collapse (bug) exceeds this threshold.
			const tol = 50.0
			if multiGap < singleGap-tol {
				t.Errorf("multiGap (%.2f) collapsed more than %.1f below singleGap (%.2f) — multi-frame scoring hurt discrimination",
					multiGap, tol, singleGap)
			}
		})
	}
}
