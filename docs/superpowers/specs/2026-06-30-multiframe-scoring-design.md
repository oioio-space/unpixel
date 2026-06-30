# Multi-frame generate-and-test scoring — design (algo-architect, validated)

**Supersedes** the fuse-then-decode multi-frame path for the DECODE use case.
`internal/multiframe.Fuse` stays as a standalone super-resolution image utility but
no longer feeds `Decode`.

## Finding (CONFIRMED, empirically + by code)

`mosaictext.DecodeMultiFrame`/`DecodeMultiFrameAuto` fuse N phase-diverse mosaics via
IBP, then call `Decode` on the fused image. `Decode` hard-gates on
`unpixel.InferBlockGrid` (decode.go:127-131) which requires block-constant structure
(`grid.go:51-57` GCD-of-gaps). A *successful* fusion recovers sub-block detail → not
block-constant → `ErrNoMosaic`. `mise run mfmeasure` on genuine sharp-source fixtures:
all 3 cases return `ErrNoMosaic`. Fuse-then-decode only "works" when fusion is a no-op
(single frame).

## Fix: keep phase-diversity in the OBJECTIVE, not the image

Score each candidate against ALL frames: render once, then for each frame pixelate the
render at that frame's grid phase and sum the per-frame distances. The true text matches
every phase; wrong candidates that collide under one phase generally won't under all →
strictly more disambiguating. No fusion, no grid-inference on reconstructed detail. This
matches the Positive Security mechanism (information from correlating the same content
under multiple grid offsets).

## The seam: `decoder.dist` (mosaictext/recover.go:256-258)

Every candidate→distance funnels through the private `dist(text, fs, stretch, pox)`:
`mseRGB(d.placed(d.stretched(text,fs,stretch), pox,0,0,0), d.target)`.
- `stretched` renders ONCE (expensive, cached) — shared across frames unchanged.
- `placed` pixelates the render at phase `pox` and composites onto a target-sized canvas.
- `mseRGB` distances to the single target.

Multi-frame = run `placed`+`mseRGB` per frame, reuse the one `stretched` render, sum.

## Implementation (ordered; additive, opt-in, panel 17/17 invariant)

**Step 1 — generalize `placed` (recover.go:240-258).** Add
`placed2(st *image.RGBA, pix unpixel.Pixelator, target *image.RGBA, pox, poy, ox, oy int) *image.RGBA`
= current `placed` body but using `pix`/`target` params. Reimplement `placed` as a
one-line delegate (`d.placed2(st, d.pixelate, d.target, …)`). No behaviour change.

**Step 2 — frame set on `decoder` (decode.go:327-341).**
`type scoreFrame struct { target *image.RGBA; pixelate unpixel.Pixelator; pox, poy int }`
and field `frames []scoreFrame`. Doc: nil ⇒ single-frame (uses d.target/d.pixelate/sweep pox).

**Step 3 — multi-frame `dist` (recover.go:256-258).** Render once via `stretched`.
If `d.frames == nil` → return the EXACT current expression (byte-identical single-frame
guarantee). Else sum over frames `mseRGB(placed2(st, f.pixelate, f.target, pox+f.pox, f.poy,0,0), f.target)`
/ N. Each `scoreFrame.pox` is the phase DELTA Δ_i = phase_i − phase_0 (frame 0 included
with Δ=0 for uniformity, OR kept as the base — pick whichever keeps N=1 → nil).

**Step 4 — one code path via `decodeFrames`.** Refactor so `Decode` and the multi-frame
entry share:
`decodeFrames(ctx, targets []*image.RGBA, phases [][2]int, block int, opts...) (Result, error)`.
- `targets[0]` drives ALL calibration/contentBounds/coarse/hi exactly as `Decode` today.
- After choosing the winning combo, populate `.frames` on both coarse (`bc.d`) and hi
  (`hi`) decoders: per frame build coarse target (`downscaleBox(crop_i, f)`) + hi target
  (`crop_i`), pixelator (`pixelatorFor(block, bc.linear)` / `pixelatorFor(blockHi, bc.linear)`),
  and Δ_i. **Crop each frame to frame-0's `rect`** (identical bounds — see risks).
- `Decode(ctx,img,opts)` ⇒ `decodeFrames(ctx, []{target0}, {{0,0}}, block, opts)`, frames nil.
- `DecodeMultiFrameAuto(ctx,imgs,opts)` ⇒ infer block from imgs[0]; `DiscoverPhases`;
  crop each to rect; deltas = phase_i − phase_0; `decodeFrames`. DELETE the Fuse call.
- `DecodeMultiFrame(ctx,frames,opts)` ⇒ use caller OffsetX/OffsetY as phases (skip discovery).

**Step 5 — phase scaling.** Δ_i are full-res pixels. Coarse decoder uses `block`-scaled
coords: store coarse `scoreFrame.pox = Δ_i / f` (mirror `pox*scale` at decode.go:251,297),
hi `scoreFrame.pox = Δ_i`. Verify against existing `scale` usage.

**Step 6 — guards + tests.**
- Precondition in `decodeFrames`: all targets identical bounds; error otherwise (the
  metric.Compare/mseRGB overlap-truncation trap — see image-compare-bounds-trap memory).
- `TestDecodeMultiFrameScored_TwoFrames`: build true phase-diverse frames (pixelate the
  ORIGINAL sharp source at two phases; `buildJitteredFrames` exists) and assert
  **two frames ≥ one frame** in correctness — a REAL, non-skipped assertion.
- Keep `*_SingleFrameEquivalent` (pass via frames==nil).
- Hot-path rule: `BenchmarkDist` for N∈{1,2,4}; N=1 no regression vs current; N>1 scales
  sub-N (render reuse). Run `bench:panel` → 17/17 unchanged.

**Step 7 — retire Fuse from decode.** Keep `Fuse`/`FuseN` as a standalone utility; update
`multiframe.go`/`multiframe_auto.go` package docs to describe scoring. PROGRESS/JOURNAL.

## Risks (validated)
- (a) Calibration from frame 0, reused across frames — SAFE and required (all frames share
  content+block, differ only in phase). Per-frame quantity = phase only.
- (b) Build every frame's target by cropping to frame-0's `rect` (identical bounds), NOT by
  re-running contentBounds per frame — else mseRGB compares misaligned/truncated overlap.
- (c) linear/sRGB is per-decode (winning combo), not per-frame: use the same `bc.linear`.
- (d) Phase error: the `pox` sweep (full block, ±4 refine) absorbs systematic frame-0 error;
  only relative Δ_i must be accurate (robust). Near-constant → Δ=0 → degrades to single-frame.
- (e) Cost: N× placed+mseRGB per `dist`; render reused. Benchmark.

Source: [Positive Security – video depixelation](https://positive.security/blog/video-depixelation).
