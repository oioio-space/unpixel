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

Cumulative discovery: **~98.6 ms → ~19.3 ms ≈ 5.1× faster**, all changes exact (recovery
output identical). The realistic multi-char path (`BenchmarkGuidedSearch`) gained a further
**~22%** this round (marginColumn −16%, fused-crop −4%). PGO (P4.9) evaluated: no measurable
gain here (hot path is in external pixelmatch/x-image), so not adopted.

P4.4 (disable pixelmatch AA detection) **measured** but NOT adopted: −44% Compare / −12%
GuidedSearch, matrix 155/155 identical, **but** it diverges from faithful Jimp.diff semantics →
fidelity decision reserved for the user (see PROGRESS P4.4).

Raw latest run: see `benchmarks/latest.txt`.

All changes above keep recovery output identical (faithful path unchanged); see the
recovery matrix (`matrix_test.go`) and round-trip tests for the quality guard.
