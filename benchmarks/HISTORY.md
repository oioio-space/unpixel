# Performance history

Per-commit snapshots of the hot-path benchmarks so gains are comparable over time.

- **Regenerate:** `mise run bench:record` → writes `benchmarks/latest.txt` (commit it).
- **Compare two commits:** check out each, `mise run bench:record`, then
  `benchstat old.txt new.txt` (benchstat is provided by mise).
- Machine of record: 13th Gen Intel i7-1370P, linux/amd64, Go 1.26.4. Absolute
  numbers vary by machine; the **deltas between commits** are what matter.

Headline metric: `BenchmarkDiscoverOffsets/workers_1` (full render→pixelate→metric
per candidate over the 64-offset discovery sweep).

| Commit      | Phase         | DiscoverOffsets/workers_1 | Render/default_32pt | FillWhite          | Note                                                                                                                                                                                                                                                                                                                                       |
|-------------|---------------|---------------------------|---------------------|--------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| pre-4.1     | baseline      | ~98.6 ms                  | ~55 µs              | ~6334 ns           | before Phase 4                                                                                                                                                                                                                                                                                                                             |
| `6a9e1ab`   | P4.1          | **~28.6 ms** (−71%)       | ~55 µs              | ~6334 ns           | drop display-only totalScore (2nd full pixelmatch/candidate)                                                                                                                                                                                                                                                                               |
| `598304d`   | P4.2          | ~28.6 ms (≈)              | ~55 µs              | ~6334 ns           | struct alignment (memory hygiene; CPU neutral)                                                                                                                                                                                                                                                                                             |
| `18749c3`   | P4.7          | ~25 ms                    | **~37 µs** (−30%)   | **~170 ns** (−97%) | FillWhite exponential-copy (memmove)                                                                                                                                                                                                                                                                                                       |
| `1984b52`   | bench         | ~24.7 ms                  | ~37 µs              | ~170 ns            | perf-stats baseline (this file)                                                                                                                                                                                                                                                                                                            |
| `bdca2f0`   | P4.6          | **~21.0 ms** (−15%)       | ~37 µs              | ~170 ns            | render cache (text-keyed): drop 63/64 redundant renders in discovery + prevGuess re-renders; −16% B/op                                                                                                                                                                                                                                     |
| `9557cab`   | P4.x          | **~19.3 ms** (−8%)        | ~37 µs              | ~170 ns            | pixelate blockMean+fill via direct dst.Pix indexing + row-copy (micro −58%)                                                                                                                                                                                                                                                                |
| `427a141`   | P4.x          | ~19 ms                    | ~37 µs              | ~170 ns            | marginColumn replaces diffRed+Margins (no full diff image): **GuidedSearch DFS −16%** (1.50→1.25 ms)                                                                                                                                                                                                                                       |
| `d5136b5`   | P4.x          | ~19 ms                    | ~37 µs              | ~170 ns            | step-9 single composed Crop (band+trim in one): GuidedSearch −4% sec, **−8% allocs**, −6.5% B/op                                                                                                                                                                                                                                           |
| `177cc2f`   | perf          | ~19 ms                    | ~37 µs              | **~186 ns** (−97%) | FillWhite exponential-copy fill (memmove vs byte loop)                                                                                                                                                                                                                                                                                     |
| (challenge) | feat          | ~19 ms                    | ~37 µs              | ~186 ns            | custom fonts + letter-spacing + whole-image TotalScore final ranking: GuidedSearch **neutral** (−2%, p=0.001, allocs identical)                                                                                                                                                                                                            |
| `d15e68a`   | P4.8          | **~17.4 ms** (−8.1%)      | ~37 µs              | ~186 ns            | buffer pool (sync.Pool): SSIM −18% allocs, FastBlur −8.7% (−67% B/op), GaussianBlur −5.6% (−87% B/op)                                                                                                                                                                                                                                      |
| `23dbb7e`   | P3.11 + P4.11 | ~17.4 ms (≈)              | ~37 µs              | ~186 ns            | **wide-charset/code path only** — auto Top-K pruning (P3.11): wide-charset GuidedDFS **~10.8× faster**, −17× B/op; intra-node parallel eval (P4.11): wide single-offset **~1.5× faster**. Default small-charset path **neutral** (GuidedSearch/DiscoverOffsets unchanged, allocs identical)                                                |
| `P4.10-1`   | P4.10 step 1  | **~16.3 ms** (−4.4%)      | ~37 µs              | ~186 ns            | in-repo pixelmatch on `*image.RGBA.Pix` (bit-identical, matrix 315/315): **Compare −16/−27 %, 0 alloc**; end-to-end **−47 % allocs, −11 % mem**, GuidedSearch −2.3 % (p=0.04). External `orisano/pixelmatch` removed from runtime (test-only). Profile: MatchPixel was 57.7 % CPU, ~17.6 % of it pure reader-abstraction overhead now gone |

Cumulative discovery: **~98.6 ms → ~16.3 ms ≈ 6.0× faster** on the default path, all
changes exact (recovery output identical). P3.11 + P4.11 do not touch the default small-charset
discovery metric — they target **wide-charset/code recovery** with a language prior (where a full
ASCII charset is ~10.8× faster), leaving the common path byte-identical. PGO (P4.9) evaluated: no
measurable gain here (hot path is in external pixelmatch/x-image), so not adopted.

P4.4 (disable pixelmatch AA detection) **measured** but NOT adopted: −44% Compare / −12%
GuidedSearch, matrix 155/155 identical, **but** it diverges from faithful Jimp.diff semantics →
fidelity decision reserved for the user (see PROGRESS P4.4).

Perspective beam (`BenchmarkDecodePerspective`, approach-B forward-model decode):
scoring the full-quad `rectify.Projector.Distance` for **only the pruned beam survivors**
instead of every extension — pruning with the cheaper `PartialDistance` — gave
**−34.3%** sec/op (5.83 s → 3.83 s, p=0.002, n=6) with no alloc/memory regression
(B/op flat, allocs −0.7%) and **byte-identical decode** (fixtures still recover
go/cat/hello at distance 0.0000). Justified by the cpu profile: `Distance` was ~66% of
runtime. See `mosaictext/perspective.go`.

Perspective beam — parallel candidate evaluation: each beam level's render →
re-pixelate → `PartialDistance` of independent candidates now runs across
GOMAXPROCS goroutines (chunked, results written by index → byte-identical decode,
goleak-clean via the package `TestMain`). On a 20-core host: **−58.9%** sec/op
(3.83 s → 1.57 s, p=0.002, n=6), B/op +0.1% / allocs +0.2% (noise). Combined with
the survivor-only `Distance` change above, the beam is ~3.7× faster than the
original 5.83 s. `WithPerspectiveWorkers` overrides the worker count.

Perspective beam — reuse one renderer across candidates: a memprofile showed
`fixture.Redact` re-parsed the font on every candidate (~21% of bytes, via
`NewXImageFromFonts`). A new reusable `fixture.Redactor` parses the font once and
is shared across the workers (Render is concurrency-safe). benchstat -count=6
caged: **allocs −78.7%** (959k → 205k/op) and **B/op −23.4%** (2.83 → 2.17 GiB),
both p=0.002; sec/op neutral (p=0.24 — parse was not CPU-bound, matching the cpu
profile). Decode byte-identical. (The remaining 76% of bytes is per-candidate
`image.NewRGBA` deep in render/pixelate — a future buffer-pool target.)

rectify.DetectQuad — edge-fit corner refinement: after the extreme-pixel pass it
fits a line (total least squares) to each of the four region edges and intersects
them for sub-pixel corners (~1.4px max error vs a filled quad, sub-pixel on the
unforeshortened corner). `BenchmarkDetectQuad` 218µs → 479µs (the boundary scan +
fits) — a one-time cost per decode, negligible beside the ~600ms beam; bought for
tighter auto-detect corners (lower forward-model distances; no decode regression —
manual-quad fixtures still 0.0000). Refinement only ever helps: it bails back to
the rough corners on a degenerate fit or an implausibly large corner move.

Perf batch (post-v0.14.0, PROGRESS.md "Optimisations de performance") — candidates
attempted, benchstat-gated, decode byte-identical (panel 17/17, matrix 310/310). 4 adopted:
- **`imutil.LeftEdge` direct `Pix[]` + per-row early break** (was full-image RGBAAt, missing
  break): **−42% sec/op** (22.7µs → 13.1µs, p=0.000, n=8), 0 allocs. Per-candidate hot path.
  New `BenchmarkLeftEdge`/`BenchmarkMargins`.
- **`search.marginColumn` direct `Pix[]` middle-row scan** (was per-pixel RGBAAt): **−59% sec/op**
  (646n → 265n, p=0.000, n=10), 0 allocs. Per-candidate marginal-region scan. New `BenchmarkMarginColumn`.
- **`trainHMM` single render pass** (record window spans in pass 1, drop the 2nd corpus
  re-render): **−24% allocs/op** (19.4k → 14.8k, p=0.000); wall-clock noise-dominated at the
  50-string bench, linear win on the 2000-string real corpus. New `BenchmarkTrainHMM`.
- **`unpixel.toRGBA` → `imutil.ToRGBA`** (8 call sites): dedup, replaces a per-pixel `Set`
  loop with `draw.Draw`; perf-neutral on the cold path, code-quality win.
2 measured and REJECTED (no-regression rule):
- **DID advance-strip pixelate**: −22% B/op but **+13% sec/op** (per-call pixelator overhead
  dominates the smaller canvas) — reverted.
- **`bestSeenTracker` atomic**: −15–17% at 8/20 cores but **+35% at workers_1** (atomic
  pair costlier than a plain lock in the sequential case) — reverted. `BenchmarkSearchOffsets`
  kept as infra for a future re-attempt.

Perf batch 2 (post-v0.14.0, PROGRESS.md Tier-1/Tier-2) — benchstat-gated, decode byte-identical
(panel 17/17 fidelity 1.000, matrix 310/310). First filled the three hot-path benchmark gaps the
RULE flagged: `internal/windowhmm` (`BenchmarkViterbi`/`ViterbiLM`/`KMeans`), `internal/did`
(`BenchmarkTrellisDP`), `internal/varfont` (`BenchmarkFitAxes`/`VarRenderer_Render`). Then:
**2 newly adopted this batch**
- **`windowhmm` Viterbi sparse O(T·E) + tuple-split hoist + per-Model memo** (`model.go`): sparse
  predecessor lists sorted by `prev` (identical tie-break to the dense loop), tuple table parsed once
  O(S²)→O(S). Sparse alone: **−90.9% sec/op geomean** (up to −97%, p=0.000, n≥10) vs the dense
  baseline. The /simplify efficiency pass then caught that `buildPredLists` was being rebuilt on every
  `ViterbiLM` call; memoising it per `Model` (`sync.Once`, pure function of States+LogTrans) — the real
  search pattern (many calls, one model) — cut S50/sparse05 a further ~8× (≈1029µs→≈133µs).
  `TestViterbiSparseIdentity` proves the path is identical even on a fully-tied uniform model.
- **`search` intra-node worker budget = min(Workers, surviving offsets)** (`beam.go` `searchOffsets`):
  feeds otherwise-idle cores when few offsets survive. **−69/−77/−80% at -cpu=4/8/20** on
  `BenchmarkSearchOffsets/workers_max`, -cpu=1 neutral, goleak-clean.
**4 confirmed already-done in prior commits** (were unticked in PROGRESS.md):
glyphMu→face pool `0cf2493` (~3–4× parallel), CachingScorer in GuidedStrategy `8e09cb6` (2.4× warm),
metric no-AA early-exit `11cbe81` (3.7× on rejected), varfont Face reuse `73c9206` (FitAxes −8.7%/−25% B/op).
**3 measured and REJECTED** (no-regression / no-quality-loss rule):
- **DID true ICP**: block-mean bound is NOT exact for `phaseX>0` (candidate sub-block straddles two
  canvas blocks) → pruning would change decode (`TestFastEmissionDID_MatchesSlow` caught the bad 0.0
  emissions). Reverted; `BenchmarkTrellisDP` kept.
- **blinddecode per-word tile cache**: composition IS byte-identical but **+58% sec/op** — the bottleneck
  is SSIM+Pixelate, not render (face pool already made render cheap). Reverted.
- **fontrank pre-prune**: quality regression — at block=32 the true font (Liberation Sans) ranks #8/9,
  any top-k<9 changes `Result.Font`. Reverted; `BenchmarkFullDecodeSweep` kept.
Also evaluated: **pixelate** partial-FillWhite (byte-identical but noise-dominated — engine pre-pads so
`paddedW==w` on the hot path) + dst `sync.Pool` (unsafe: caller owns the returned buffer). Reverted;
`BenchmarkBlockAverage_Pixelate_Padded` kept.

Perf batch 3 (post-v0.14.0, PROGRESS.md Tier-2/Tier-3) — benchstat-gated, decode byte-identical
(panel 17/17 fidelity 1.000). Concurrency changes proven race-free with `CGO_ENABLED=1 go test -race`
(CGO permitted for the race detector ONLY — never in build/test/release; the shipped binary stays pure
Go): `internal/search` ok 4.9s, `internal/blinddecode` ok 560s (caged).
**2 adopted**
- **`search.evalChildren` prealloc + unbox** (`search.go`): child slices preallocated to `cap=len(charset)`
  with `slices.Clip`; the parallel path writes into a `[]node` value slice by index instead of `[]*node`
  (no per-child heap boxing). **−58% sec/op geomean, −9% allocs/op** (p=0.000), decode byte-identical.
- **`deblur` FFT twiddle tables + scratch reuse** (`l0text.go`): precompute `e^{-2πik/n}` once per size
  (`fftTwiddles`/`fftPlan`) + `…Into` variants writing into buffers preallocated once and reused across
  the 20 HQS iterations. **−41% sec/op geomean, −85% B/op, ~−90% allocs/op** (306→31 allocs, p=0.000),
  decode byte-identical. (Kernel FFT was already precomputed; this targets twiddles + per-iter scratch.
  The /simplify pass also routed the one-shot kHat/bHat FFTs through the prebuilt plan and deleted the
  now-dead fft1D/ifft1D/realFFT2D/realIFFT2D — 39→31 allocs.)
**1 confirmed already-correct**: blinddecode phase-2 is already a per-slot fan-out and the feared
`widthCache` race does not exist (populated serially in phase-1, never touched in phase-2) — now
*proven* by `-race`. A chunk-rewrite attempt regressed +54% and was reverted (comment-only change kept).
**2 measured and REJECTED**
- **multiframe direct `Pix[]` writes**: mixed (large image −31% but small image +58%) → **geomean +4.7%
  sec/op**; the compiler already inlines `SetRGBA` well on small loops. Reverted.
- **PGO re-measure** (now the metric core is in-tree, unlike the pre-P4.10 null result): real but modest
  ~3.5% geomean (~5–7% on DiscoverOffsets/metric-compare), not worth the `default.pgo` maintenance burden
  (regenerate after every hot-path refactor). Revisit after the next major algorithmic step.

Raw latest run: see `benchmarks/latest.txt`.

All changes above keep recovery output identical (faithful path unchanged); see the
recovery matrix (`matrix_test.go`) and round-trip tests for the quality guard.
