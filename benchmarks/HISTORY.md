# Performance history

Per-commit snapshots of the hot-path benchmarks so gains are comparable over time.

- **Regenerate:** `mise run bench:record` → writes `benchmarks/latest.txt` (commit it).
- **Compare two commits:** check out each, `mise run bench:record`, then
  `benchstat old.txt new.txt` (benchstat is provided by mise).
- Machine of record: 13th Gen Intel i7-1370P, linux/amd64, Go 1.26.4. Absolute
  numbers vary by machine; the **deltas between commits** are what matter.

Headline metric: `BenchmarkDiscoverOffsets/workers_1` (full render→pixelate→metric
per candidate over the 64-offset discovery sweep).

| Commit | Phase | DiscoverOffsets/workers_1 | Render/default_32pt | FillWhite | Note |
|--------|-------|---------------------------|---------------------|-----------|------|
| pre-4.1 | baseline | ~98.6 ms | ~55 µs | ~6334 ns | before Phase 4 |
| `6a9e1ab` | P4.1 | **~28.6 ms** (−71%) | ~55 µs | ~6334 ns | drop display-only totalScore (2nd full pixelmatch/candidate) |
| `598304d` | P4.2 | ~28.6 ms (≈) | ~55 µs | ~6334 ns | struct alignment (memory hygiene; CPU neutral) |
| `18749c3` | P4.7 | ~25 ms | **~37 µs** (−30%) | **~170 ns** (−97%) | FillWhite exponential-copy (memmove) |
| `1984b52` | bench | ~24.7 ms | ~37 µs | ~170 ns | perf-stats baseline (this file) |
| `bdca2f0` | P4.6 | **~21.0 ms** (−15%) | ~37 µs | ~170 ns | render cache (text-keyed): drop 63/64 redundant renders in discovery + prevGuess re-renders; −16% B/op |
| `9557cab` | P4.x | **~19.3 ms** (−8%) | ~37 µs | ~170 ns | pixelate blockMean+fill via direct dst.Pix indexing + row-copy (micro −58%) |
| `427a141` | P4.x | ~19 ms | ~37 µs | ~170 ns | marginColumn replaces diffRed+Margins (no full diff image): **GuidedSearch DFS −16%** (1.50→1.25 ms) |
| `d5136b5` | P4.x | ~19 ms | ~37 µs | ~170 ns | step-9 single composed Crop (band+trim in one): GuidedSearch −4% sec, **−8% allocs**, −6.5% B/op |
| `177cc2f` | perf | ~19 ms | ~37 µs | **~186 ns** (−97%) | FillWhite exponential-copy fill (memmove vs byte loop) |
| (challenge) | feat | ~19 ms | ~37 µs | ~186 ns | custom fonts + letter-spacing + whole-image TotalScore final ranking: GuidedSearch **neutral** (−2%, p=0.001, allocs identical) |
| `d15e68a` | P4.8 | **~17.4 ms** (−8.1%) | ~37 µs | ~186 ns | buffer pool (sync.Pool): SSIM −18% allocs, FastBlur −8.7% (−67% B/op), GaussianBlur −5.6% (−87% B/op) |
| `23dbb7e` | P3.11 + P4.11 | ~17.4 ms (≈) | ~37 µs | ~186 ns | **wide-charset/code path only** — auto Top-K pruning (P3.11): wide-charset GuidedDFS **~10.8× faster**, −17× B/op; intra-node parallel eval (P4.11): wide single-offset **~1.5× faster**. Default small-charset path **neutral** (GuidedSearch/DiscoverOffsets unchanged, allocs identical) |
| `P4.10-1` | P4.10 step 1 | **~16.3 ms** (−4.4%) | ~37 µs | ~186 ns | in-repo pixelmatch on `*image.RGBA.Pix` (bit-identical, matrix 315/315): **Compare −16/−27 %, 0 alloc**; end-to-end **−47 % allocs, −11 % mem**, GuidedSearch −2.3 % (p=0.04). External `orisano/pixelmatch` removed from runtime (test-only). Profile: MatchPixel was 57.7 % CPU, ~17.6 % of it pure reader-abstraction overhead now gone |

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

Raw latest run: see `benchmarks/latest.txt`.

All changes above keep recovery output identical (faithful path unchanged); see the
recovery matrix (`matrix_test.go`) and round-trip tests for the quality guard.
