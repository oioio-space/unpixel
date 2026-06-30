# Wall-breakers v0.18 Implementation Plan

> **For agentic workers:** execute task-by-task via subagent-driven-development (fresh go-dev per task, go-reviewer between, Opus whole-branch review at end). Steps use `- [ ]`.

**Goal:** Break the breakable depixelation walls — automatic multi-frame phase discovery (A), joint font-size+x-stretch forward-model calibration (B), measured DID-trellis routing for long digits (C) — plus the missing fixtures, journal, benchmarks, docs.

**Architecture:** All additive/opt-in. A extends `internal/multiframe` + `mosaictext`; B extends `internal/varfont`; C reuses `mosaictext.DecodeDID`. New fixtures via `internal/fixture` generators.

**Tech Stack:** Go 1.26, pure-Go, CGO forbidden. `golang.org/x/image`. Existing Nelder-Mead (`internal/varfont/optimizer.go`), IBP fusion (`internal/multiframe`), DID trellis (`internal/did`).

## Global Constraints

- Pure Go, `CGO_ENABLED=0`, no C/video deps.
- Default decode path byte-identical; **panel 17/17 invariant**; coverage ≥ 85.
- Caged tests: `scripts/gotest-caged.sh go test …` (never bare `go test`).
- Hot-path code carries `Benchmark…`; perf changes proven with benchstat (`-count ≥ 10 -benchmem`).
- In-memory / committed fixtures only.
- Per-commit `/simplify` marker in a SEPARATE bash call after `git restore --staged PROGRESS.md`.
- Commit trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## Part A — Automatic per-frame grid-phase discovery

### Task A1: `DiscoverPhases` + `AlignFrames` in `internal/multiframe`

**Files:** Create/modify `internal/multiframe/phase.go`, `internal/multiframe/phase_test.go`, `internal/multiframe/bench_test.go`.

**Interfaces produced:**
- `func DiscoverPhases(frames []Frame, block int) []Frame` — copy of frames with each `OffsetX/OffsetY` detected (the grid phase at which that frame's mosaic boundaries fall), each in `[0, block)`. Deterministic.
- `func AlignFrames(frames []Frame) []Frame` — integer-pixel content registration: cross-correlate each frame against frame 0 over a small search window (±block px), shift to best match. Pure-Go, no FFT needed (small window, spatial NCC). Returns aligned copies.

Detection method: a mosaic block-mean image is block-constant; the grid phase is the offset `p ∈ [0, block)` that best explains where horizontal/vertical constant-color boundaries fall. Implement a local detector (sum of absolute inter-column differences peaks at block boundaries) rather than importing `unpixel` (avoid cycle). Validate against known offsets from generated frames.

Steps: failing test (frames pixelated at known offsets → DiscoverPhases recovers them) → implement → pass → benchmark `BenchmarkDiscoverPhases` → commit.

### Task A2: `mosaictext.DecodeMultiFrameAuto`

**Files:** Create `mosaictext/multiframe_auto.go`, `mosaictext/multiframe_auto_test.go`.

**Interfaces:**
- Consumes: `multiframe.DiscoverPhases`, `multiframe.AlignFrames`, `multiframe.Fuse`, `unpixel.InferBlockGrid`, `Decode`.
- Produces: `func DecodeMultiFrameAuto(ctx context.Context, imgs []image.Image, opts ...Option) (Result, error)` — infers block, aligns, discovers phases, fuses, decodes. `len(imgs)==1` ⇒ byte-identical to `Decode`.

Test: ≥2 phase-diverse frames decode the hidden text where the single frame fails or is worse (assert auto ≥ single by distance, and recovers known text on a generated case).

### Task A3: Wire CLI `--frame` + MCP auto path

**Files:** Modify `cmd/unpixel/main.go` (runMultiFrame → DecodeMultiFrameAuto), `mcp/decode.go` (when all frame offsets are 0, run discovery).

Test: CLI integration test (or existing multi-frame test) still passes; single `--frame` ≡ Decode.

### Task A4: Multi-frame fixtures

**Files:** Create `internal/fixture/genmultiframe/main.go`, extend `internal/fixture` as needed, generate `testdata/multiframe/*.png` + `manifest.json`. Add a test that loads them and runs `DecodeMultiFrameAuto`.

Each case: one text, 4 frames pixelated at distinct offsets, content-aligned. Commit PNGs + manifest.

---

## Part B — Joint font-size + x-stretch calibration

### Task B1: `CalibrateGeometry` in `internal/varfont`

**Files:** Create `internal/varfont/geometry.go`, `internal/varfont/geometry_test.go`, bench.

**Interfaces:**
- `type GeometryConfig struct { VisibleCrop *image.RGBA; VisibleText string; Font *...; StartSizePx float64; ... }` (mirror `CalibrateConfig` fields actually present — read `calibrate.go` first).
- `func CalibrateGeometry(cfg GeometryConfig) (GeometryResult, error)` with `GeometryResult{ FontSizePx, XStretch, Distance float64 }`. Optimize size + stretch via the existing `nelderMead` simplex against the sharp visible crop (render→compare; no pixelation).

Test: round-trip — render visible text at known size/stretch, recover within tolerance; degenerate inputs error cleanly.

### Task B2: Wire `WithCalibrateGeometry` + CLI

**Files:** Modify `unpixel.go` (option) or `mosaictext/varfont.go` (whichever owns the visible-calibration wiring — read first), `cmd/unpixel/main.go` (`--calibrate-geometry`, requires `--visible-region`/`--visible-text`).

Test: opt-in path feeds fitted size/stretch into Style; absent ⇒ byte-identical.

### Task B3: x-stretch / size fixtures

**Files:** Extend `internal/fixture/fixture.go` `Spec` (+ `LetterSpacing`/`Stretch`, `FontSize` already may exist) and `Matrix()` with additional (non-panel) cases; regenerate `testdata/fixtures`. Test geometry calibration against them.

**Panel guard:** the 17/17 panel assertion set must NOT change — new specs are additional fixtures referenced only by the geometry test. Verify panel test count unchanged.

---

## Part C — Measured DID-trellis routing for long digits

### Task C1: Observational measurement

**Files:** Extend `mcp/campaign_test.go` (or new `internal/.../*_test.go` behind a build tag) — compare `DecodeDID` (+ digits constraint if reachable) vs engine on `testdata/sick/digits_*`. Observational, never-fail. Run, record numbers.

### Task C2 (conditional on C1 showing gain): route digit/format decodes through DID

**Files:** thin routing in `mosaictext`/`mcp` so `expected_format=digits` long strings use the trellis. Tests. If C1 shows no gain, SKIP and document the confirmed wall in the journal.

---

## Finalization

### Task F1: Journal + quality history
Append `docs/JOURNAL.md` "Wall-breakers v0.18" (experiment table A/B/C + benchstat deltas + honest lessons). Run `mise run bench:panel:record` (append a quality-history row). Confirm panel 17/17.

### Task F2: Docs + PROGRESS.md
Update README.md (multi-frame-auto, calibrate-geometry), `docs/reference/cli.md`, `docs/reference/api.md`, `docs/concepts/*` as relevant, and **PROGRESS.md** (record the program + outcomes). Run `mise run ci` green.

### Task F3: Whole-branch Opus review → finishing-a-development-branch.
