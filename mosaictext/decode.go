// Package mosaictext recovers monospace text from a mosaic-pixelated redaction
// with zero configuration: given only the image, it locates the redaction,
// detects the block grid, calibrates the typography (font, size, tracking) from
// the image itself, and reconstructs the most plausible text.
//
// It complements the core generate-and-test engine ([github.com/oioio-space/unpixel]),
// which recovers short strings when the caller supplies the typography. Long,
// faint monospace redactions defeat that incremental search (per-character
// signal is too weak), so this package instead:
//
//   - calibrates font size and horizontal tracking from a probe render,
//   - scores candidates by raw block-value distance (MSE) rather than the
//     thresholded pixelmatch — the threshold makes many strings tie, MSE does not,
//   - reconstructs per character cell (monospace cells are independent under MSE),
//   - and disambiguates the residual confusions ('H'/'N', 'l'/'I', '!'/'-') with
//     a language prior (dictionary words + sentence-terminal punctuation).
//
// The redaction must be mosaic (block-average) pixelation of a single line of
// monospace text. GEGL/GIMP/CSS pixelate in linear light; this is auto-detected.
package mosaictext

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"math"
	"runtime"
	"slices"
	"sync"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"

	xdraw "golang.org/x/image/draw"
)

// defaultCharset is the zero-config alphabet: ASCII letters, space, and the
// common punctuation that survives mosaic pixelation as a distinct shape.
const defaultCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ !.,?'-:;"

// Result is a decoded mosaic-text recovery.
type Result struct {
	// Text is the most plausible decoded string.
	Text string
	// Font is the bundled font name whose calibration won.
	Font string
	// Distance is the block-value MSE of the winning reconstruction (lower is
	// better; ~0 means the candidate reproduces the redaction near-exactly).
	Distance float64
	// Linear reports whether linear-light block averaging matched (GEGL/GIMP),
	// versus sRGB averaging.
	Linear bool
	// BlockSize, CharCount, and GridPhaseX are the calibrated grid and layout.
	BlockSize  int
	CharCount  int
	GridPhaseX int
}

// Option configures Decode. Use WithMaxParallelism and WithMemBudget to rate-limit
// CPU and memory; the zero options give adaptive, machine-aware defaults.
type Option func(*config)

type config struct {
	maxParallel int   // cap on concurrent decoders (CPU rate limit)
	memBudget   int64 // cap on total live render-cache bytes (memory rate limit)
}

// WithMaxParallelism caps the number of decoders run concurrently — the CPU
// rate-limit. n<=0 restores the default (GOMAXPROCS). The effective worker count
// is the smaller of this and what the memory budget allows.
func WithMaxParallelism(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxParallel = n
		}
	}
}

// WithMemBudget sets the target peak memory footprint. It governs both how many
// decoders run at once and how large each render cache is, so a smaller budget
// yields a smaller footprint at the cost of speed (evicted renders are recomputed,
// never wrong). bytes<=0 restores the default (defaultMemBudget). For a hard cap,
// also run under a cgroup or GOMEMLIMIT matching this value.
func WithMemBudget(bytes int64) Option {
	return func(c *config) {
		if bytes > 0 {
			c.memBudget = bytes
		}
	}
}

func defaultConfig() config {
	return config{maxParallel: runtime.GOMAXPROCS(0), memBudget: defaultMemBudget}
}

// plan derives, from the config and the per-render frame size, both the worker
// count and the per-decoder cache cap so that the live render caches stay within
// the memory budget. memBudget is the target peak footprint; caches get half of it
// (liveFraction) and the rest is headroom for GC garbage, so peak RSS ≈ memBudget.
// This is the footprint knob: shrink memBudget and the caches — the dominant live
// memory — shrink with it (slower, since evicted renders are recomputed, never
// wrong). Coarse frames are tiny so the cap saturates and parallelism is free;
// full-resolution frames are large so the budget binds, capping concurrency.
func (c config) plan(frameBytes, tasks int) (workers, cacheCap int) {
	live := max(1, c.memBudget/liveFraction)
	fb := int64(max(1, frameBytes))
	maxW := max(1, int(live/(minCacheEntries*fb)))
	workers = min(c.maxParallel, max(1, tasks), maxW)
	capEntries := int(live / int64(workers) / fb)
	cacheCap = max(minCacheEntries, min(maxCacheEntries, capEntries))
	return workers, cacheCap
}

// Decode recovers monospace text from a mosaic redaction with zero
// configuration. It returns ErrNoMosaic if no block grid can be detected and
// ErrNoContent if the image has no redacted content. Options rate-limit CPU and
// memory (see WithMaxParallelism, WithMemBudget).
func Decode(ctx context.Context, img image.Image, opts ...Option) (Result, error) {
	return decodeFrames(ctx, []image.Image{img}, nil, opts...)
}

// decodeFrames is the shared entry point for single- and multi-frame recovery.
// imgs[0] drives all calibration (block inference, contentBounds, font/phase
// sweep). When phases is non-nil it must have the same length as imgs; each
// entry is the [2]int{offsetX, offsetY} grid phase at which that frame was
// pixelated. Pass phases==nil (or a single-element imgs with any phases) to
// use single-frame mode (frames==nil on each decoder → byte-identical to the
// original Decode path).
//
// All images in imgs must have identical bounds (same Dx/Dy as imgs[0] after
// the content crop to frame-0's rect); decodeFrames returns an error otherwise
// to avoid the mseRGB overlap-truncation trap.
func decodeFrames(ctx context.Context, imgs []image.Image, phases [][2]int, opts ...Option) (Result, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	rgba0 := toRGBA(imgs[0])

	grid, ok := unpixel.InferBlockGrid(imgs[0])
	if !ok || grid.Size < 2 {
		return Result{}, ErrNoMosaic
	}
	blockHiBase := grid.Size

	rect := contentBounds(rgba0)
	if rect.Empty() {
		return Result{}, ErrNoContent
	}

	// multiFrame is true when we have more than one frame with distinct phases.
	multiFrame := len(imgs) > 1 && len(phases) == len(imgs)

	// Guard: each additional frame must contain frame-0's content rect so the
	// xdraw.Draw crop below stays in-bounds. Frames may have different total
	// sizes (e.g. different padding); only the content region must be common.
	// This is the mseRGB overlap-truncation trap guard from the design: all
	// per-frame targets are cropped to the same rect, so mseRGB always sees
	// identical bounds regardless of the original frame sizes.
	if multiFrame {
		for i := 1; i < len(imgs); i++ {
			ib := imgs[i].Bounds()
			if rect.Max.X > ib.Max.X || rect.Max.Y > ib.Max.Y ||
				rect.Min.X < ib.Min.X || rect.Min.Y < ib.Min.Y {
				return Result{}, fmt.Errorf("mosaictext: frame %d bounds %v do not contain frame-0 content rect %v", i, ib, rect)
			}
		}
	}

	// Build frame-0's hi-res target (the anchor for all calibration).
	const pad = 24
	// Tight content crop, padded so a calibrated render can shift to align. This is
	// the full-resolution ("Hi") target used for the final rerank.
	targetHi0 := image.NewRGBA(image.Rect(0, 0, rect.Dx()+pad, rect.Dy()+pad))
	imutil.FillWhite(targetHi0)
	xdraw.Draw(targetHi0, image.Rect(0, 0, rect.Dx(), rect.Dy()), rgba0, rect.Min, xdraw.Src)
	blockHi, tWHi, tHHi := blockHiBase, rect.Dx(), rect.Dy()

	// Build hi-res targets for additional frames, cropped to frame-0's rect.
	hiTargets := make([]*image.RGBA, len(imgs))
	hiTargets[0] = targetHi0
	if multiFrame {
		for i := 1; i < len(imgs); i++ {
			rgbai := toRGBA(imgs[i])
			ti := image.NewRGBA(image.Rect(0, 0, rect.Dx()+pad, rect.Dy()+pad))
			imutil.FillWhite(ti)
			xdraw.Draw(ti, image.Rect(0, 0, rect.Dx(), rect.Dy()), rgbai, rect.Min, xdraw.Src)
			hiTargets[i] = ti
		}
	}

	// Coarse search at reduced resolution. The objective is a block-average
	// distance, so area-downscaling the target and the block together by f shrinks
	// every per-candidate render+pixelate+MSE by ~f² and (via the cache-budget
	// worker count) unlocks full parallelism. Coarse blocks blur near-confusions
	// ('e'/'n', 'l'/'I'), so the final shortlist is re-scored on targetHi (see the
	// fine decoder wired in phase 2) to recover the lost discrimination. Grid
	// inference already ran on the full-res input, so coarsening here is safe.
	f := max(1, blockHiBase/targetBlockPx)
	target, tW, tH := targetHi0, tWHi, tHHi
	block := blockHiBase
	if f > 1 {
		block = blockHiBase / f
		target = downscaleBox(targetHi0, f)
		tW, tH = tWHi/f, tHHi/f
	}

	// Build coarse targets for additional frames.
	coarseTargets := make([]*image.RGBA, len(imgs))
	coarseTargets[0] = target
	if multiFrame && f > 1 {
		for i := 1; i < len(imgs); i++ {
			coarseTargets[i] = downscaleBox(hiTargets[i], f)
		}
	} else if multiFrame {
		copy(coarseTargets[1:], hiTargets[1:])
	}

	rs, err := fonts.Renderers()
	if err != nil {
		return Result{}, err
	}
	all := fonts.All()

	// Phase 1 — rank calibrations cheaply. For every (font, linear-averaging?)
	// combination, calibrate typography from the image and find the grid phase at
	// the natural character count, scoring the fit by a one-pass reconstruction.
	// This concentrates the expensive search (phase 2) on the winning calibration
	// instead of running it for all 18 combinations.
	type combo struct {
		d      *decoder
		font   string
		linear bool
		nRef   int
		nMin   int
		nMax   int
		pox    int
		fitMSE float64
	}
	combos := make([]combo, len(rs)*2)
	// Bound concurrency AND per-decoder cache size by the memory budget, not core
	// count: each calibration builds a render cache, so launching all len(rs)*2 at
	// once — or even GOMAXPROCS at once on a 20-core host — multiplied the live
	// working set into the ~27 GB / OOM range. plan derives both so the live caches
	// stay within budget (coarse frames are tiny, so here the cap saturates and all
	// calibrations run in parallel).
	frameBytes := target.Bounds().Dx() * target.Bounds().Dy() * 4
	workers, coarseCap := cfg.plan(frameBytes, len(rs)*2)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for fi := range rs {
		for li := range 2 {
			wg.Go(func() {
				if ctx.Err() != nil {
					return
				}
				sem <- struct{}{}
				defer func() { <-sem }()
				linear := li == 1
				d := &decoder{r: rs[fi], target: target, tW: tW, tH: tH, block: block, pixelate: pixelatorFor(block, linear), cacheCap: coarseCap}
				nRef, nMin, nMax, ok := d.calibrate()
				if !ok {
					return
				}
				pox, fit := d.phase(d.stretchForN(nRef), nRef)
				combos[fi*2+li] = combo{d, all[fi].Name, linear, nRef, nMin, nMax, pox, fit}
			})
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}

	bestCombo := combo{fitMSE: math.Inf(1)}
	for _, c := range combos {
		if c.d != nil && c.fitMSE < bestCombo.fitMSE {
			bestCombo = c
		}
	}
	if bestCombo.d == nil {
		return Result{}, ErrNoContent
	}

	bc := bestCombo

	// buildFrames constructs the []scoreFrame for a decoder at a given block size
	// and downscale factor df (1 for hi-res, f for coarse). Phase 0 has Δ=0 and
	// is always included; subsequent frames get Δ_i = phase_i − phase_0, scaled
	// by 1/df for the coarse stage.
	buildFrames := func(pix unpixel.Pixelator, targets []*image.RGBA, df int) []scoreFrame {
		if !multiFrame {
			return nil
		}
		p0x, p0y := phases[0][0], phases[0][1]
		sfs := make([]scoreFrame, len(imgs))
		for i := range imgs {
			dx := (phases[i][0] - p0x) / df
			dy := (phases[i][1] - p0y) / df
			sfs[i] = scoreFrame{target: targets[i], pixelate: pix, pox: dx, poy: dy}
		}
		return sfs
	}

	// A dres is one character-count hypothesis decoded to a string with its score.
	type dres struct {
		n, pox   int
		obj, mse float64
		text     string
	}
	// parDecode decodes each character count concurrently, each on its own clone of
	// tmpl — decode resets the per-call cache, so clones must not share a decoder —
	// bounded to maxWorkers. That bound is the single CPU+memory rate-limit knob:
	// both the cores in flight and the live render caches scale with it. scale maps
	// a coarse grid phase up to tmpl's resolution.
	parDecode := func(tmpl *decoder, ns, pox []int, scale, maxWorkers int) []dres {
		out := make([]dres, len(ns))
		limiter := make(chan struct{}, max(1, maxWorkers))
		var wg sync.WaitGroup
		for i, n := range ns {
			wg.Go(func() {
				if ctx.Err() != nil {
					return
				}
				limiter <- struct{}{}
				defer func() { <-limiter }()
				nd := *tmpl
				txt, o, m, p := (&nd).decode(n, pox[i]*scale, nd.stretchForN(n))
				out[i] = dres{n, p, o, m, txt}
			})
		}
		wg.Wait()
		return out
	}

	// Attach multi-frame scoring to the coarse decoder and run phase 2a.
	bc.d.frames = buildFrames(pixelatorFor(block, bc.linear), coarseTargets, f)

	// Phase 2a — localize the character count cheaply, in parallel, on the coarse
	// winner. Per-cell glyphs may be confused at low resolution, but the *length*
	// that best fits the redaction is robust to it; keep the best few for a
	// full-resolution refit. This sweep used to be the dominant serial cost.
	allN := make([]int, 0, bc.nMax-bc.nMin+1)
	allPox := make([]int, 0, cap(allN))
	for n := bc.nMin; n <= bc.nMax; n++ {
		allN = append(allN, n)
		allPox = append(allPox, bc.pox)
	}
	coarse := parDecode(bc.d, allN, allPox, 1, workers)
	coarse = slices.DeleteFunc(coarse, func(r dres) bool { return r.text == "" })
	slices.SortFunc(coarse, func(a, b dres) int { return cmp.Compare(a.obj, b.obj) })
	if len(coarse) > nRefineTop {
		coarse = coarse[:nRefineTop]
	}

	// Phase 2b — full-resolution decode of only the surviving character counts, in
	// parallel. Candidate generation (confusion sets) and the final decision happen
	// here at native resolution to separate near-confusions ('e'/'n', 'o'/'v',
	// 'l'/'I') that coarse blocks blur — paid for ≤ nRefineTop counts of one font.
	// (Seeding the full-res search from the coarse text was ~5× faster but perturbed
	// the grid-phase pick and the confusion sets, regressing the decode; reverted.)
	fineN := make([]int, len(coarse))
	finePox := make([]int, len(coarse))
	for i, r := range coarse {
		fineN[i], finePox[i] = r.n, r.pox
	}
	hiFrameBytes := targetHi0.Bounds().Dx() * targetHi0.Bounds().Dy() * 4
	hiWorkers, hiCap := cfg.plan(hiFrameBytes, len(coarse))
	dec, scale := bc.d, 1
	if f > 1 {
		hi := &decoder{
			r:        bc.d.r,
			target:   targetHi0,
			tW:       tWHi,
			tH:       tHHi,
			block:    blockHi,
			pixelate: pixelatorFor(blockHi, bc.linear),
			cacheCap: hiCap,
			frames:   buildFrames(pixelatorFor(blockHi, bc.linear), hiTargets, 1),
		}
		if _, _, _, ok := hi.calibrate(); ok {
			dec, scale = hi, f
		}
	}

	bestObj, bestText, bestN, bestPox, bestMSE := math.Inf(1), "", bc.nRef, bc.pox*scale, math.Inf(1)
	for _, r := range parDecode(dec, fineN, finePox, scale, hiWorkers) {
		if r.text != "" && r.obj < bestObj {
			bestObj, bestText, bestN, bestPox, bestMSE = r.obj, r.text, r.n, r.pox, r.mse
		}
	}
	if bestText == "" {
		return Result{}, ErrNoContent
	}
	return Result{
		Text:       bestText,
		Font:       bc.font,
		Distance:   bestMSE,
		Linear:     bc.linear,
		BlockSize:  blockHi,
		CharCount:  bestN,
		GridPhaseX: bestPox,
	}, nil
}

// pixelatorFor returns the linear-light or sRGB block-average pixelator.
func pixelatorFor(block int, linear bool) unpixel.Pixelator {
	if linear {
		return defaults.LinearBlockAverage(block)
	}
	return defaults.BlockAverage(block)
}

// scoreFrame is one additional phase-diverse observation used during multi-frame
// scoring. pox and poy are the phase DELTAS relative to frame 0 (Δ_i = phase_i −
// phase_0), so frame 0 is represented by Δ=0 in the slice. When frames is nil on
// the parent decoder, dist falls back to the single-frame expression.
type scoreFrame struct {
	target   *image.RGBA
	pixelate unpixel.Pixelator
	pox, poy int // phase delta relative to frame 0 (full-res pixels)
}

// decoder holds the per-(font,pixelator) calibration and search state. A
// decoder runs on a single goroutine, so cache needs no synchronization.
type decoder struct {
	r        unpixel.Renderer
	target   *image.RGBA
	tW, tH   int
	block    int
	pixelate unpixel.Pixelator
	fs, adv  float64      // calibrated font size and natural monospace advance
	cacheCap int          // per-decoder render-cache entry cap (from the memory budget)
	frames   []scoreFrame // nil ⇒ single-frame; non-nil ⇒ multi-frame scoring
	// cache memoizes stretched renders by text within one decodeN (where fs and
	// the stretch factor are fixed), so the phase sweep and the per-cell search
	// pay the costly render+resample only once per distinct string. It is bounded
	// (see renderCache): an unbounded map let a single decoder retain thousands of
	// full-size renders (~3 GB), and 18 retained decoders summed to ~27 GB.
	cache *renderCache
}

// Cache-entry bounds for the per-decoder stretched-render memo. Each entry is a
// ~tW×tH RGBA, so a decoder's cache footprint is entries × frame bytes. The actual
// cap is computed per decoder from the memory budget (config.plan); these only clamp
// it: minCacheEntries keeps enough of one per-cell reconstruction's working set
// (n cells × charset) to avoid pathological re-render thrash, maxCacheEntries stops
// a generous budget from holding renders no search ever revisits. Eviction (FIFO)
// only ever costs a recompute, never correctness.
const (
	minCacheEntries = 96
	maxCacheEntries = 1024
)

// defaultMemBudget is the default target peak footprint (config.memBudget). Set at
// the knee of the measured footprint↔speed curve: the decode stays correct at any
// budget (the cache is pure memoization), and below this point speed degrades
// sharply for little further memory saved. Callers that want a smaller footprint
// pass a lower WithMemBudget (e.g. 128 MB ≈ ~240 MB peak, ~2× slower); callers that
// want speed pass a larger one.
const defaultMemBudget = 512 << 20 // 512 MB → ~600 MB peak

// liveFraction is the share of memBudget spent on live render caches; the remainder
// is headroom for GC garbage, so peak RSS lands near memBudget rather than 2×.
const liveFraction = 2

// targetBlockPx is the block size (px) the scoring pipeline downscales to. The
// objective is a per-block average, so a handful of pixels per block carry the
// full signal; coarsening native blocks (often 16–32 px) to this makes each
// candidate render+pixelate+MSE ~ (block/targetBlockPx)² cheaper.
const targetBlockPx = 8

// nRefineTop is how many of the best coarse character-count hypotheses are
// re-decoded at full resolution. >1 guards against the coarse length pick being
// off by one without paying full resolution across the whole plausible range.
const nRefineTop = 2

// renderCache is a fixed-capacity string→render memo with FIFO eviction. Capacity
// is set per decoder from the memory budget. It is not safe for concurrent use;
// each decoder owns its own and runs single-threaded.
type renderCache struct {
	cap  int
	m    map[string]*image.RGBA
	keys []string // insertion order, used as a ring for eviction
	next int      // ring write position once full
}

func newRenderCache(capEntries int) *renderCache {
	capEntries = max(1, capEntries)
	return &renderCache{cap: capEntries, m: make(map[string]*image.RGBA, capEntries)}
}

func (c *renderCache) get(text string) (*image.RGBA, bool) {
	img, ok := c.m[text]
	return img, ok
}

// put stores img under text; it must only be called with a key whose rendered
// value is stable — updating an existing key's value is not supported.
func (c *renderCache) put(text string, img *image.RGBA) {
	if _, ok := c.m[text]; ok {
		c.m[text] = img
		return
	}
	if len(c.keys) < c.cap {
		c.keys = append(c.keys, text)
		c.m[text] = img
		return
	}
	// Full: evict the oldest entry and reuse its ring slot.
	delete(c.m, c.keys[c.next])
	c.keys[c.next] = text
	c.next = (c.next + 1) % c.cap
	c.m[text] = img
}

// calibrate measures the font size and natural monospace advance from the image
// (storing them on the decoder) and returns the natural character count and the
// plausible character-count range (tracking 0.85×–1.5× of natural). ok is false
// if the font cannot be measured.
func (d *decoder) calibrate() (nRef, nMin, nMax int, ok bool) {
	// Font size from content height: rendered ink spans k×size, measured from a
	// probe with ascenders/descenders, so size = contentHeight / k.
	probe, psx, err := d.r.Render("Hhelo Wrd", unpixel.Style{FontSize: 100})
	if err != nil || psx <= 0 {
		return 0, 0, 0, false
	}
	k := float64(inkBounds(probe, psx).Dy()) / 100.0
	if k <= 0 {
		return 0, 0, 0, false
	}
	d.fs = float64(d.tH) / k
	// Natural monospace advance at fs (difference between "HH" and "H" ink width).
	w2, s2, _ := d.r.Render("HH", unpixel.Style{FontSize: d.fs})
	w1, s1, _ := d.r.Render("H", unpixel.Style{FontSize: d.fs})
	d.adv = float64(inkBounds(w2, s2).Dx() - inkBounds(w1, s1).Dx())
	if d.adv <= 1 {
		return 0, 0, 0, false
	}
	nRef = max(1, int(math.Round(float64(d.tW)/d.adv)))
	nMin = max(1, int(float64(d.tW)/(d.adv*1.5)))
	nMax = int(float64(d.tW)/(d.adv*0.85)) + 1
	return nRef, nMin, nMax, true
}

// stretchForN is the horizontal tracking factor that fits n monospace cells of
// the calibrated advance into the content width.
func (d *decoder) stretchForN(n int) float64 {
	return (float64(d.tW) / float64(n)) / d.adv
}
