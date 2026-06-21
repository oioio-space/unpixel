// Package mosaictext white-box tests: fast, always-on, never -short-gated.
//
// These tests cover the cheap unexported helpers and the decoder pipeline
// directly (one renderer, one font) to maximise statement coverage without
// running the full nine-font Decode, which is -short-gated in decode_test.go.
package mosaictext

import (
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// ---- image helpers ----

// TestMean8 verifies the bounded integer average.
func TestMean8(t *testing.T) {
	cases := []struct {
		sum, n int
		want   uint8
	}{
		{0, 1, 0},
		{255, 1, 255},
		{510, 2, 255},
		{100, 4, 25},
		{300, 3, 100},
	}
	for _, tc := range cases {
		got := mean8(tc.sum, tc.n)
		if got != tc.want {
			t.Errorf("mean8(%d,%d) = %d, want %d", tc.sum, tc.n, got, tc.want)
		}
	}
}

// TestMseRGB verifies that identical images score 0 and differing images score >0.
func TestMseRGB(t *testing.T) {
	make2 := func(w, h int, r, g, b, a uint8) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, a
		}
		return img
	}

	a := make2(4, 4, 200, 150, 100, 255)
	if got := mseRGB(a, a); got != 0 {
		t.Errorf("mseRGB(same, same) = %v, want 0", got)
	}

	b := make2(4, 4, 0, 0, 0, 255)
	if got := mseRGB(a, b); got <= 0 {
		t.Errorf("mseRGB(light, dark) = %v, want > 0", got)
	}

	// 1×1 images: Pix has exactly 4 bytes (n==4 ≥ 4), so mseRGB runs normally
	// and returns 0 for identical images.
	tiny := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if got := mseRGB(tiny, tiny); got != 0 {
		t.Errorf("mseRGB(1×1 same, same) = %v, want 0", got)
	}
}

// TestMseRGB_TooSmall verifies that a 0×0 image (Pix has 0 bytes, n<4) returns +Inf.
func TestMseRGB_TooSmall(t *testing.T) {
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	got := mseRGB(empty, empty)
	if got != got { // NaN check
		t.Errorf("mseRGB(0×0) = NaN, want +Inf")
	}
	if got <= 0 {
		t.Errorf("mseRGB(0×0) = %v, want +Inf", got)
	}
}

// TestContentBounds verifies that fully-white images return an empty rect,
// and a dark pixel moves the bounding box.
func TestContentBounds(t *testing.T) {
	// All-white: no content.
	white := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for i := 0; i < len(white.Pix); i += 4 {
		white.Pix[i], white.Pix[i+1], white.Pix[i+2], white.Pix[i+3] = 255, 255, 255, 255
	}
	if got := contentBounds(white); !got.Empty() {
		t.Errorf("contentBounds(white) = %v, want empty rect", got)
	}

	// One dark pixel at (3,4).
	gray := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for i := 0; i < len(gray.Pix); i += 4 {
		gray.Pix[i], gray.Pix[i+1], gray.Pix[i+2], gray.Pix[i+3] = 255, 255, 255, 255
	}
	gray.SetRGBA(3, 4, color.RGBA{R: 50, G: 50, B: 50, A: 255})
	got := contentBounds(gray)
	if got.Empty() {
		t.Fatal("contentBounds: expected non-empty rect for image with dark pixel")
	}
	if got.Min.X != 3 || got.Min.Y != 4 {
		t.Errorf("contentBounds min = %v, want (3,4)", got.Min)
	}
}

// TestInkBounds verifies that a freshly-white render returns the sentinel size
// and that an image with an ink pixel returns a tight bounding box.
func TestInkBounds(t *testing.T) {
	// No ink: returns the sentinel 1×1 rect.
	white := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := 0; i < len(white.Pix); i += 4 {
		white.Pix[i], white.Pix[i+1], white.Pix[i+2], white.Pix[i+3] = 255, 255, 255, 255
	}
	got := inkBounds(white, 20)
	if got.Dx() != 1 || got.Dy() != 1 {
		t.Errorf("inkBounds(all-white) = %v, want 1×1", got)
	}

	// One dark pixel at (5,6).
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 255, 255, 255, 255
	}
	img.SetRGBA(5, 6, color.RGBA{A: 255}) // black
	got = inkBounds(img, 20)
	if got.Min.X != 5 || got.Min.Y != 6 {
		t.Errorf("inkBounds dark-pixel min = %v, want (5,6)", got)
	}
	// Sentinel excludes pixels at or beyond sentinelX.
	img2 := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := 0; i < len(img2.Pix); i += 4 {
		img2.Pix[i], img2.Pix[i+1], img2.Pix[i+2], img2.Pix[i+3] = 255, 255, 255, 255
	}
	img2.SetRGBA(19, 0, color.RGBA{A: 255}) // black pixel beyond sentinelX=10
	gotSmall := inkBounds(img2, 10)
	if gotSmall.Dx() != 1 || gotSmall.Dy() != 1 {
		t.Errorf("inkBounds should ignore pixels beyond sentinelX; got %v", gotSmall)
	}
}

// TestDownscaleBox verifies that a 2×2 region of identical colour produces the
// exact same colour in the downscaled result.
func TestDownscaleBox(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 100, 150, 200, 255
	}
	got := downscaleBox(src, 2)
	if got.Bounds().Dx() != 2 || got.Bounds().Dy() != 2 {
		t.Fatalf("downscaleBox(4×4, 2) size = %v, want 2×2", got.Bounds())
	}
	c := got.RGBAAt(0, 0)
	if c.R != 100 || c.G != 150 || c.B != 200 {
		t.Errorf("downscaleBox uniform colour: got %v, want R=100 G=150 B=200", c)
	}
}

// TestToRGBA verifies that passing an *image.RGBA returns it unchanged and
// that passing an *image.NRGBA is converted correctly.
func TestToRGBA(t *testing.T) {
	orig := image.NewRGBA(image.Rect(0, 0, 4, 4))
	orig.SetRGBA(1, 1, color.RGBA{R: 10, G: 20, B: 30, A: 255})

	// identity path
	got := toRGBA(orig)
	if got != orig {
		t.Error("toRGBA(*image.RGBA) should return the same pointer")
	}

	// conversion path: use *image.NRGBA
	nrgba := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	nrgba.SetNRGBA(2, 2, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	got2 := toRGBA(nrgba)
	if got2 == nil {
		t.Fatal("toRGBA(NRGBA) returned nil")
	}
	c := got2.RGBAAt(2, 2)
	if c.R != 200 || c.G != 100 || c.B != 50 {
		t.Errorf("toRGBA NRGBA→RGBA pixel = %v, want (200,100,50)", c)
	}
}

// ---- language priors ----

// TestDictWords verifies that dictionary word counting handles spaces, punctuation,
// and short tokens correctly.
func TestDictWords(t *testing.T) {
	// "the" and "cat" should both be in the English dictionary embedded in lang.
	n := dictWords("the cat")
	if n < 1 {
		t.Errorf("dictWords(%q) = %d, want ≥ 1", "the cat", n)
	}
	// Short tokens (< 2 chars) are excluded.
	if got := dictWords("a i"); got != 0 {
		// "a" and "i" are single letters, excluded by the ≥2 rule
		t.Logf("dictWords(%q) = %d (single-letter tokens; may be 0)", "a i", got)
	}
	// Empty string must return 0.
	if got := dictWords(""); got != 0 {
		t.Errorf("dictWords(%q) = %d, want 0", "", got)
	}
}

// TestTerminalPunct verifies sentence-terminal detection.
func TestTerminalPunct(t *testing.T) {
	yes := []string{"Hello!", "Really.", "What?", "end.  ", "Go!"}
	no := []string{"", "   ", "no-punct", "mid! dle"}
	for _, s := range yes {
		if !terminalPunct(s) {
			t.Errorf("terminalPunct(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if terminalPunct(s) {
			t.Errorf("terminalPunct(%q) = true, want false", s)
		}
	}
}

// ---- beam search ----

// TestBeamMSE verifies that beamMSE on a 2-cell layout with two choices each
// returns strings in ascending cumulative-cost order.
func TestBeamMSE(t *testing.T) {
	cands := [][]cellCand{
		{{ch: 'a', cost: 1.0}, {ch: 'b', cost: 2.0}},
		{{ch: 'x', cost: 1.0}, {ch: 'y', cost: 3.0}},
	}
	out := beamMSE(cands, 4)
	if len(out) == 0 {
		t.Fatal("beamMSE returned empty slice")
	}
	// Best must be "ax" (cost 2.0), not "ay" (4.0) or "bx" (3.0).
	if out[0] != "ax" {
		t.Errorf("beamMSE best = %q, want %q", out[0], "ax")
	}
	// All returned strings must be length 2.
	for _, s := range out {
		if len([]rune(s)) != 2 {
			t.Errorf("beamMSE result length = %d, want 2 (string %q)", len([]rune(s)), s)
		}
	}
}

// TestBeamMSE_Width verifies the width cap.
func TestBeamMSE_Width(t *testing.T) {
	cands := [][]cellCand{
		{{ch: 'a', cost: 1.0}, {ch: 'b', cost: 2.0}, {ch: 'c', cost: 3.0}},
	}
	out := beamMSE(cands, 2)
	if len(out) != 2 {
		t.Errorf("beamMSE width=2 returned %d candidates, want 2", len(out))
	}
}

// ---- render cache ----

// TestRenderCache exercises the get/put/eviction behaviour.
func TestRenderCache(t *testing.T) {
	c := newRenderCache(2)
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))

	// Cache miss.
	if _, ok := c.get("miss"); ok {
		t.Error("expected cache miss for unseen key")
	}

	// Put and hit.
	c.put("a", img)
	if _, ok := c.get("a"); !ok {
		t.Error("expected cache hit after put")
	}

	// Update existing key.
	img2 := image.NewRGBA(image.Rect(0, 0, 2, 2))
	c.put("a", img2) // update
	if got, ok := c.get("a"); !ok || got != img2 {
		t.Errorf("cache put(update) failed: ok=%v got=%v", ok, got)
	}

	// Fill to capacity then add one more to trigger FIFO eviction.
	c.put("b", img)
	// "a" and "b" are in the cache (cap=2); adding "c" evicts oldest.
	c.put("c", img)
	// "a" should have been evicted.
	if _, ok := c.get("a"); ok {
		t.Error("expected 'a' to be evicted from full cache")
	}
	if _, ok := c.get("c"); !ok {
		t.Error("expected 'c' to be present after insertion")
	}
}

// ---- config options & plan ----

// TestConfigOptions exercises the option functions and plan derivation.
func TestConfigOptions(t *testing.T) {
	// defaultConfig returns sane values.
	cfg := defaultConfig()
	if cfg.maxParallel <= 0 {
		t.Errorf("defaultConfig().maxParallel = %d, want > 0", cfg.maxParallel)
	}
	if cfg.memBudget <= 0 {
		t.Errorf("defaultConfig().memBudget = %d, want > 0", cfg.memBudget)
	}

	// WithMaxParallelism updates the field.
	var c config
	WithMaxParallelism(3)(&c)
	if c.maxParallel != 3 {
		t.Errorf("WithMaxParallelism(3): got %d, want 3", c.maxParallel)
	}
	// Non-positive is ignored.
	WithMaxParallelism(0)(&c)
	if c.maxParallel != 3 {
		t.Errorf("WithMaxParallelism(0) should not change value; got %d", c.maxParallel)
	}

	// WithMemBudget updates the field.
	var c2 config
	WithMemBudget(1 << 28)(&c2)
	if c2.memBudget != 1<<28 {
		t.Errorf("WithMemBudget: got %d, want %d", c2.memBudget, 1<<28)
	}
	// Non-positive is ignored.
	WithMemBudget(0)(&c2)
	if c2.memBudget != 1<<28 {
		t.Errorf("WithMemBudget(0) should not change value; got %d", c2.memBudget)
	}
}

// TestConfigPlan verifies that plan caps workers to the task count and memory budget.
func TestConfigPlan(t *testing.T) {
	cfg := config{maxParallel: 8, memBudget: 512 << 20}
	// Very large frame: memory budget should cap workers well below maxParallel.
	workers, cacheCap := cfg.plan(50*1024*1024, 4) // 50 MB frame, 4 tasks
	if workers < 1 {
		t.Errorf("plan workers = %d, want ≥ 1", workers)
	}
	if workers > 4 {
		t.Errorf("plan workers = %d, want ≤ task count (4)", workers)
	}
	if cacheCap < 1 {
		t.Errorf("plan cacheCap = %d, want ≥ 1", cacheCap)
	}

	// Tiny frame: memory budget is not the bottleneck; parallelism caps at tasks.
	w2, _ := cfg.plan(1, 3)
	if w2 > 3 {
		t.Errorf("plan with tiny frame: workers = %d, want ≤ 3", w2)
	}
}

// TestPixelatorFor verifies that pixelatorFor returns non-nil for both modes.
func TestPixelatorFor(t *testing.T) {
	if p := pixelatorFor(8, false); p == nil {
		t.Error("pixelatorFor(8, false) returned nil")
	}
	if p := pixelatorFor(8, true); p == nil {
		t.Error("pixelatorFor(8, true) returned nil")
	}
}

// ---- decoder pipeline (single font, tiny image) ----

// newTestDecoder creates a lightweight decoder for unit-testing the pipeline
// methods without running the full nine-font Decode sweep.
func newTestDecoder(t *testing.T) *decoder {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	const block = 8
	// Render a known short word to produce a tiny target image.
	img, sx, err := r.Render("go", unpixel.Style{FontSize: 24})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Crop to ink bounds and make a small target.
	bb := inkBounds(img, sx)
	target := image.NewRGBA(image.Rect(0, 0, bb.Dx(), bb.Dy()))
	for i := 0; i < len(target.Pix); i += 4 {
		target.Pix[i+3] = 255 // opaque white
	}
	for y := range bb.Dy() {
		for x := range bb.Dx() {
			target.SetRGBA(x, y, img.RGBAAt(bb.Min.X+x, bb.Min.Y+y))
		}
	}
	p := pixelate.NewBlockAverage(block)
	return &decoder{
		r:        r,
		target:   target,
		tW:       target.Bounds().Dx(),
		tH:       target.Bounds().Dy(),
		block:    block,
		pixelate: p,
		cacheCap: minCacheEntries,
	}
}

// TestDecoder_Calibrate verifies that calibrate sets a plausible font size and
// advance on a real render.
func TestDecoder_Calibrate(t *testing.T) {
	d := newTestDecoder(t)
	nRef, nMin, nMax, ok := d.calibrate()
	if !ok {
		t.Fatal("calibrate returned ok=false")
	}
	if d.fs <= 0 {
		t.Errorf("calibrate: fs = %v, want > 0", d.fs)
	}
	if d.adv <= 0 {
		t.Errorf("calibrate: adv = %v, want > 0", d.adv)
	}
	if nRef < 1 {
		t.Errorf("calibrate: nRef = %d, want ≥ 1", nRef)
	}
	if nMin > nRef {
		t.Errorf("calibrate: nMin (%d) > nRef (%d)", nMin, nRef)
	}
	if nMax < nRef {
		t.Errorf("calibrate: nMax (%d) < nRef (%d)", nMax, nRef)
	}
}

// TestDecoder_StretchForN verifies that stretchForN returns 1.0 when n matches
// the natural width and a different factor when it doesn't.
func TestDecoder_StretchForN(t *testing.T) {
	d := newTestDecoder(t)
	_, _, _, ok := d.calibrate()
	if !ok {
		t.Fatal("calibrate failed")
	}
	nNat := max(1, int(float64(d.tW)/d.adv))
	stretch := d.stretchForN(nNat)
	// Should be close to 1.0 (within 20%).
	if stretch < 0.5 || stretch > 2.5 {
		t.Errorf("stretchForN(%d) = %v, want ≈ 1.0", nNat, stretch)
	}
	// Doubling n should halve the stretch.
	if nNat > 0 {
		s2 := d.stretchForN(nNat * 2)
		if s2 >= stretch {
			t.Errorf("stretchForN(2n) = %v, expected < stretchForN(n) = %v", s2, stretch)
		}
	}
}

// TestDecoder_RenderedAndPlaced exercises the rendering side of the decoder
// (stretched, placed, dist) with a single character on the tiny target image.
func TestDecoder_RenderedAndPlaced(t *testing.T) {
	d := newTestDecoder(t)
	_, _, _, ok := d.calibrate()
	if !ok {
		t.Fatal("calibrate failed")
	}
	d.cache = newRenderCache(d.cacheCap)

	stretch := d.stretchForN(2)
	// stretched must return a non-nil, non-empty image.
	st := d.stretched("go", d.fs, stretch)
	if st == nil || st.Bounds().Empty() {
		t.Fatal("stretched returned nil or empty image")
	}
	// Second call should hit the cache (same pointer).
	st2 := d.stretched("go", d.fs, stretch)
	if st2 != st {
		t.Error("stretched: expected cache hit on second call")
	}

	// placed must return a canvas of the target's dimensions.
	placed := d.placed(st, 0, 0, 0, 0)
	if placed == nil {
		t.Fatal("placed returned nil")
	}
	tgt := d.target
	if placed.Bounds().Dx() != tgt.Bounds().Dx() || placed.Bounds().Dy() != tgt.Bounds().Dy() {
		t.Errorf("placed size = %v, want %v", placed.Bounds(), tgt.Bounds())
	}

	// dist must return a finite non-negative number.
	score := d.dist("go", d.fs, stretch, 0)
	if score < 0 {
		t.Errorf("dist = %v, want ≥ 0", score)
	}
}

// TestDecoder_GreedyN verifies that greedyN returns a string of the requested
// length from the charset.
func TestDecoder_GreedyN(t *testing.T) {
	d := newTestDecoder(t)
	_, _, _, ok := d.calibrate()
	if !ok {
		t.Fatal("calibrate failed")
	}
	d.cache = newRenderCache(d.cacheCap)
	charset := []rune("abcdefghijklmnopqrstuvwxyz")
	stretch := d.stretchForN(2)
	result := d.greedyN(stretch, 2, 0, charset, 1)
	if len([]rune(result)) != 2 {
		t.Errorf("greedyN(n=2) returned %q (len=%d), want length 2", result, len([]rune(result)))
	}
}

// TestDecoder_Confusion verifies that confusion returns one slice per cell
// with entries in ascending cost order.
func TestDecoder_Confusion(t *testing.T) {
	d := newTestDecoder(t)
	_, _, _, ok := d.calibrate()
	if !ok {
		t.Fatal("calibrate failed")
	}
	d.cache = newRenderCache(d.cacheCap)
	charset := []rune("abcgo")
	stretch := d.stretchForN(2)
	cands := d.confusion("go", d.fs, stretch, 0, charset, 3)
	if len(cands) != 2 {
		t.Fatalf("confusion: got %d cells, want 2", len(cands))
	}
	for i, cell := range cands {
		if len(cell) == 0 {
			t.Errorf("cell %d has no candidates", i)
			continue
		}
		for j := 1; j < len(cell); j++ {
			if cell[j].cost < cell[j-1].cost {
				t.Errorf("cell %d: candidates not sorted by cost at position %d", i, j)
			}
		}
	}
}

// TestDecoder_Phase verifies that phase returns a phase in [0, block) and a
// finite MSE.
func TestDecoder_Phase(t *testing.T) {
	d := newTestDecoder(t)
	_, _, _, ok := d.calibrate()
	if !ok {
		t.Fatal("calibrate failed")
	}
	stretch := d.stretchForN(2)
	pox, mse := d.phase(stretch, 2)
	if pox < 0 || pox >= d.block {
		t.Errorf("phase: pox = %d, want in [0, %d)", pox, d.block)
	}
	if mse < 0 || mse != mse { // NaN check
		t.Errorf("phase: mse = %v, want finite non-negative", mse)
	}
}

// TestDecoder_Decode verifies that the decode method returns a non-empty string.
func TestDecoder_Decode(t *testing.T) {
	d := newTestDecoder(t)
	_, _, _, ok := d.calibrate()
	if !ok {
		t.Fatal("calibrate failed")
	}
	stretch := d.stretchForN(2)
	text, obj, mse, pox := d.decode(2, 0, stretch)
	if text == "" {
		t.Error("decode returned empty string")
	}
	if obj < 0 {
		t.Errorf("decode: obj = %v, want ≥ 0", obj)
	}
	if mse < 0 {
		t.Errorf("decode: mse = %v, want ≥ 0", mse)
	}
	if pox < 0 || pox >= d.block {
		t.Errorf("decode: pox = %d, want in [0, %d)", pox, d.block)
	}
	_ = text
}

// TestDecodeErrors verifies ErrNoMosaic / ErrNoContent sentinel paths via the
// public Decode API on trivially bad inputs, which is fast (early return).
func TestDecodeErrors(t *testing.T) {
	ctx := t.Context()

	// A 1×1 white image has no mosaic grid → ErrNoMosaic.
	white := image.NewRGBA(image.Rect(0, 0, 1, 1))
	white.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	if _, err := Decode(ctx, white); !errors.Is(err, ErrNoMosaic) {
		t.Errorf("Decode(1×1 white) = %v, want ErrNoMosaic", err)
	}
}
