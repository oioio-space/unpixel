package mosaictext

// White-box tests and benchmarks for multi-frame scoring. Kept in the mosaictext
// package so they can access unexported symbols: decoder, scoreFrame, placed2,
// dist, stretched, inkBounds, newRenderCache, minCacheEntries, pixelate.

import (
	"fmt"
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// distSink absorbs BenchmarkDist results to prevent dead-code elimination.
var distSink float64

// TestPlaced2_DelegatesPlaced verifies that placed2 with the decoder's own
// pixelator and target produces identical output to placed.
func TestPlaced2_DelegatesPlaced(t *testing.T) {
	d := newTestDecoder(t)
	if _, _, _, ok := d.calibrate(); !ok {
		t.Skip("calibrate failed")
	}
	st := d.stretched("go", d.fs, 1.0)

	got1 := d.placed(st, 2, 0, 0, 0)
	got2 := d.placed2(st, d.pixelate, d.target, 2, 0, 0, 0)

	if len(got1.Pix) != len(got2.Pix) {
		t.Fatalf("placed vs placed2 Pix length: %d vs %d", len(got1.Pix), len(got2.Pix))
	}
	for i, v := range got1.Pix {
		if got2.Pix[i] != v {
			t.Fatalf("placed vs placed2 differ at byte %d: got %d want %d", i, got2.Pix[i], v)
		}
	}
}

// TestDist_SingleFrameNilFrames verifies that with frames==nil dist returns a
// finite non-negative value (the original single-frame expression is intact).
func TestDist_SingleFrameNilFrames(t *testing.T) {
	d := newTestDecoder(t)
	if _, _, _, ok := d.calibrate(); !ok {
		t.Skip("calibrate failed")
	}
	d.cache = newRenderCache(d.cacheCap)
	defer func() { d.cache = nil }()

	if d.frames != nil {
		t.Fatal("newTestDecoder must not set frames")
	}
	got := d.dist("go", d.fs, 1.0, 0)
	if got != got {
		t.Errorf("dist single-frame returned NaN")
	}
	if got < 0 {
		t.Errorf("dist single-frame = %v, want ≥ 0", got)
	}
}

// TestDist_MultiFrameAverages verifies that with frames set dist returns a
// finite non-negative value (the averaging loop runs without error).
func TestDist_MultiFrameAverages(t *testing.T) {
	d := newTestDecoder(t)
	if _, _, _, ok := d.calibrate(); !ok {
		t.Skip("calibrate failed")
	}
	d.cache = newRenderCache(d.cacheCap)
	defer func() { d.cache = nil }()

	pix2 := pixelate.NewBlockAverage(d.block)
	d.frames = []scoreFrame{
		{target: d.target, pixelate: d.pixelate, pox: 0, poy: 0},
		{target: d.target, pixelate: pix2, pox: 2, poy: 0},
	}

	got := d.dist("go", d.fs, 1.0, 0)
	if got != got {
		t.Errorf("dist multi-frame returned NaN")
	}
	if got < 0 {
		t.Errorf("dist multi-frame = %v, want ≥ 0", got)
	}
}

// newBenchDecoder builds a minimal decoder for BenchmarkDist using a real
// rendered target so the dist cost is representative.
func newBenchDecoder(b *testing.B) *decoder {
	b.Helper()
	r, err := render.NewXImage()
	if err != nil {
		b.Fatalf("render.NewXImage: %v", err)
	}
	const block = 8
	img, sx, err := r.Render("Hello", unpixel.Style{FontSize: 24})
	if err != nil {
		b.Fatalf("render: %v", err)
	}
	bb := inkBounds(img, sx)
	const pad = 8
	target := image.NewRGBA(image.Rect(0, 0, bb.Dx()+pad, bb.Dy()+pad))
	for i := range len(target.Pix) {
		target.Pix[i] = 255
	}
	for y := range bb.Dy() {
		for x := range bb.Dx() {
			target.SetRGBA(x, y, img.RGBAAt(bb.Min.X+x, bb.Min.Y+y))
		}
	}
	pix := pixelate.NewBlockAverage(block)
	d := &decoder{
		r:        r,
		target:   target,
		tW:       target.Bounds().Dx(),
		tH:       target.Bounds().Dy(),
		block:    block,
		pixelate: pix,
		cacheCap: minCacheEntries,
	}
	if _, _, _, ok := d.calibrate(); !ok {
		b.Skip("calibrate failed on bench decoder")
	}
	return d
}

// BenchmarkDist measures dist() for N∈{1,2,4} frames.
//
//   - N=1: frames==nil — the exact original single-frame expression.
//   - N>1: multi-frame averaging with per-frame placed2+mseRGB.
//
// The render is shared across all N frames in one dist call, so N>1 should
// scale sub-linearly (the expensive render+resample is paid once; only
// placed+mseRGB scales with N). Compare with benchstat after -count=10.
func BenchmarkDist(b *testing.B) {
	d := newBenchDecoder(b)
	pix := pixelate.NewBlockAverage(d.block)

	for _, n := range []int{1, 2, 4} {
		n := n
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			nd := *d
			if n == 1 {
				nd.frames = nil // single-frame: exact original expression
			} else {
				frames := make([]scoreFrame, n)
				for i := range n {
					frames[i] = scoreFrame{
						target:   d.target,
						pixelate: pix,
						pox:      i * 2,
						poy:      0,
					}
				}
				nd.frames = frames
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				nd.cache = newRenderCache(nd.cacheCap)
				distSink = nd.dist("Hello", nd.fs, 1.0, 0)
			}
		})
	}
}
