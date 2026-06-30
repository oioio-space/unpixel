# Wall-breakers v0.18 — design

**Status:** approved (autonomous /goal execution, 2026-06-30)
**Branch:** `feat/wall-breakers-v0.18`

## Context

The #1–#8 program + the MCP decode campaign (2026-06-30) mapped four decoding
"walls" and which are breakable. Research grounding:
[Positive Security – video depixelation](https://positive.security/blog/video-depixelation)
(multi-frame super-resolution: a moving mosaic grid yields phase-diverse linear
measurements of the same content) and Hill et al.,
[*On the (In)effectiveness of Mosaicing and Blurring*](https://hovav.net/ucsd/dist/redaction.pdf).

A reconnaissance pass (4 explorers) established **what already exists** so we build
only the genuine gaps:

- **Multi-frame fusion EXISTS** (`internal/multiframe.Fuse`, IBP; `mosaictext.DecodeMultiFrame`)
  but each frame's grid phase `(OffsetX, OffsetY)` must be **supplied by the caller**;
  the CLI `--frame` path hardcodes `(0,0)` (`cmd/unpixel/main.go:1289`) and MCP passes
  caller offsets unmodified. There is **no automatic per-frame phase discovery**.
- **Forward-model calibration EXISTS** (`internal/varfont.CalibrateFromVisible`,
  Nelder-Mead + coordinate descent over OpenType axes) but **font size and x-stretch
  are only heuristically *seeded*** (`InferXStretch`, ±3%), never *optimized*.
- **A per-glyph-column trellis EXISTS** (`mosaictext.DecodeDID` over
  `internal/did.TrellisDP`, Viterbi + LM). Long digit strings (`sick/`) are decoded
  through the **combinatorial** engine search instead, and collapse.
- **No ML/training** path is built (the `//go:build ml` seam is designed but empty).

Testdata gaps that block validating the above: **no phase-diverse multi-frame
frame-sets**, **no x-stretch / non-default-size fixtures**.

## Goal

Break (or measurably probe) the breakable walls, pure-Go / no-CGO, additive and
opt-in (panel stays 17/17, default path byte-identical), with fixtures, benchmarks,
docs, PROGRESS.md, and a journal experiment + analysis.

## Scope — three parts + fixtures + journal

### Part A — Automatic per-frame grid-phase discovery (breaks the *hard* wall)

The single static-frame mosaic average is information-theoretically lossy; the only
real extra-information channel is **multiple phase-diverse frames** (proven attack).
Fusion is built; the missing piece is making it usable without hand-labeled offsets.

- **New** `internal/multiframe.DiscoverPhases(frames []Frame, block int) []Frame` —
  returns a copy of `frames` with each frame's `OffsetX/OffsetY` filled by detecting
  that frame's mosaic grid phase. Reuse the existing single-image grid-phase logic
  (`unpixel.InferGridPhase`, exposed for this; or a local block-boundary detector if
  that import would cycle). Phase is taken modulo `block`, range `[0, block)`.
- **New** `mosaictext.DecodeMultiFrameAuto(ctx, imgs []image.Image, opts...)` — infers
  block size from the first image, calls `DiscoverPhases`, then `Fuse` + `Decode`.
- **Wire** the CLI `--frame` path to `DecodeMultiFrameAuto` (replace the hardcoded
  `(0,0)`), and add an MCP `multi-frame-auto` behaviour (frames with all-zero offsets
  trigger discovery). Single-frame call stays byte-identical to `Decode`.
- **Scope boundary (documented, NOT built):** video codec ingestion (CGO) and
  sub-pixel content registration of a shaky camera (optical flow / RANSAC). We accept
  **already-extracted, content-aligned, integer-pixel frames**; sub-pixel content
  registration is future work. We DO add a cheap integer-pixel content cross-correlation
  alignment (`AlignFrames`) so minor whole-pixel content drift between extracted frames
  is corrected before phase discovery — pure-Go, no deps.

### Part B — Joint forward-model calibration: fit font size + x-stretch (breaks the real single-frame wall)

Make single-frame real-image render-match injective by recovering an exact-enough
forward model via optimization, not heuristic seeding.

- **Extend** `internal/varfont` calibration to optimize **font size** and **x-stretch
  (letter-spacing advance)** as additional continuous parameters, using the existing
  Nelder-Mead simplex (no new optimizer). Provide
  `CalibrateGeometry(cfg GeometryConfig) (GeometryResult, error)` returning fitted
  `{FontSizePx float64, XStretch float64, Distance float64}`.
- These geometry params are optimized against a **sharp visible region** (same contract
  as `CalibrateFromVisible`) where the signal survives — NOT against the pixelated
  redaction (opsz/slnt signal-loss caveat applies equally to fine size detail; document).
- **Wire** an opt-in `WithCalibrateGeometry()` / CLI `--calibrate-geometry` (requires a
  visible region) and feed fitted size+stretch into the decode `Style`.
- Keep `WithAutoCalibrate` (heuristic seed) intact; geometry calibration is the
  optimized superset used when a visible region is available.

### Part C — Measured DID-trellis routing for long structured strings (probes the length wall)

- **Measure first** (`mise run campaign` extension or a focused observational test):
  does routing the `sick/` digit fixtures through `DecodeDID` (length-native column
  trellis) + the #6 `digits` format constraint recover more than the combinatorial
  engine (which collapses)? 
- **If it helps:** add a thin `expected_format`-aware path that routes digit/format
  decodes through the DID trellis, and wire it (CLI/MCP). 
- **If it does not:** record it as a confirmed wall (the per-column emission model, not
  the trellis, is the limiter → ML, deferred D) in the journal. No speculative build.

### Fixtures (generate the missing ones, matching the existing mechanism)

Reuse `internal/fixture` + generators (`go generate ./...` for committed dirs;
`manifest.json` per dir). All synthetic, in-memory generation committed as PNGs.

- **`testdata/multiframe/`** — N (=4) phase-diverse, content-aligned frames per case
  (same text, pixelated at distinct grid offsets), plus a `manifest.json` recording
  text + true offsets. New generator `internal/fixture/genmultiframe`.
- **`testdata/fixtures/` additions** — x-stretch / letter-spacing-distorted cases and
  a non-default font-size case, to exercise Part B geometry calibration. Extend
  `fixture.Matrix()` + `Spec` minimally (a `LetterSpacing`/`Stretch` field) so the
  panel-invariant set is unchanged (new specs are *additional* fixtures, validated by
  the geometry path, not added to the 17/17 panel assertion).

### Journal + analysis

After A/B/C land, append a `docs/JOURNAL.md` section "Wall-breakers v0.18" with:
an experiment table (multi-frame auto-phase recovery vs single-frame; geometry-calibrated
decode vs seeded on the new stretch/size fixtures; DID-trellis-vs-engine on sick digits),
the benchstat deltas, and the honest lessons (what broke which wall, what remains ML-bound).
Update `benchmarks/quality-history.md` via `mise run bench:panel:record`.

## Constraints (Global)

- **Pure Go, CGO forbidden.** `CGO_ENABLED=0`. No C deps, no video codec bindings.
- **Additive / opt-in.** Default decode path byte-identical; panel **17/17** invariant;
  coverage ≥ 85 (`COVER_MIN`).
- **Caged tests only:** `scripts/gotest-caged.sh`. Never bare `go test`.
- **Benchmark the hot path:** new fusion/calibration/trellis code carries `Benchmark…`;
  any perf-affecting change proven with benchstat (`-count ≥ 10 -benchmem`).
- **In-memory / committed fixtures only**; never commit gitignored or network fixtures.
- **Per-commit `/simplify` marker** armed in a SEPARATE bash call after
  `git restore --staged PROGRESS.md`; commits end with
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **PROGRESS.md** updated to record this program.

## Non-goals

- Video decoding / sub-pixel camera registration (CGO / heavy CV) — documented frontier.
- Training an ML emission model (D) — `//go:build ml` seam stays the designed home; not built.
- Changing any default-path behaviour or the 17/17 panel assertion.
