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
| _P4.6_ | P4.6 | **~21.0 ms** (−15%) | ~37 µs | ~170 ns | render cache (text-keyed): drop 63/64 redundant renders in discovery + prevGuess re-renders; −16% B/op |

Raw latest run: see `benchmarks/latest.txt`.

All changes above keep recovery output identical (faithful path unchanged); see the
recovery matrix (`matrix_test.go`) and round-trip tests for the quality guard.
