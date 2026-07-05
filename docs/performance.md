# Performance

Two **absolute project rules** govern this area (see [`CLAUDE.md`](../CLAUDE.md)):

1. **Pure Go, no CGO** — ever. No `import "C"`, no cgo-requiring dependency.
   `CGO_ENABLED=0` is pinned and enforced by `cgo:check`.
2. **The hot path is benchmarked, and every perf-affecting change is proven with
   benchstat** — never optimized by feel. The per-candidate render → re-pixelate →
   image-distance → search loop carries `Benchmark…` tests; changes are kept only on a
   statistically significant gain with no alloc/throughput regression.

Recovery quality and speed are tracked release-over-release in
[`benchmarks/quality-history.md`](../benchmarks/quality-history.md) and the per-run
[`JOURNAL.md`](JOURNAL.md).

The rest of this page is the grounded study of the one acceleration idea people ask
about most: **can a no-CGO GPU speed up the hot path?**

## Accelerating the hot path — SIMD vs GPU (no-CGO)

The question: *can we speed up the per-candidate compute with a no-CGO GPU package?*
The answer is grounded in the actual CPU profile, the project's constraints, and the
2026 state of no-CGO GPU in Go.

### 1. What the profile says (measured, not assumed)

`go test ./internal/search -bench BenchmarkDiscoverOffsets/workers_1 -cpuprofile`:

| Function                           | flat  | cum   |
|------------------------------------|-------|-------|
| `colorDelta` (YIQ per-pixel delta) | 12.8% | 24.4% |
| `isAntiAliased` (3×3 neighborhood) | 12.2% | 27.6% |
| `pixelate.BlockAverage.Pixelate`   | 6.7%  | 9.3%  |
| YIQ conversions (`rgbaToY/I/Q`)    | ~5%   | —     |
| GC / runtime                       | ~8%   | —     |

The per-candidate **compare** dominates. It splits into a **vectorizable** part
(`colorDelta`: pure arithmetic) and a **branchy** part (`isAntiAliased`: data-dependent
3×3 neighborhood walk with early-outs and a second-order `hasManySiblings` check).

### 2. Why this workload is a poor fit for GPU offload

GPUs win when the kernel is large, arithmetically dense, uniformly parallel, with a high
compute:transfer ratio. This workload is the opposite:

1. **Tiny work units.** Each `Compare` runs over the changed band — often ≈1 block wide
   × the redaction height (a few KB). A CPU compare is microseconds.
2. **Per-dispatch overhead dominates.** Submitting one kernel and reading the result
   back costs ~10–100 µs — *more than the entire CPU compare it would replace.*
3. **Control flow is sequential.** The guided DFS chooses the next character from the
   previous result. It is thousands of small, data-dependent steps, not one big batch.
   (Offsets and a node's children are already parallelized on CPU.)
4. **Branch divergence.** `isAntiAliased` is exactly the per-pixel data-dependent
   branching that wrecks GPU occupancy. Only `colorDelta` maps cleanly to SIMD/GPU lanes.
5. **Rendering is on the critical path too.** To avoid shuttling rendered images to the
   GPU per candidate, rendering + pixelation would also have to move on-GPU — a project
   in itself.

**To make a GPU pay off you must change the algorithm**, not just the metric: batch
thousands of candidate evaluations into one dispatch — a major rearchitecture that only
helps the wide-charset/beam regime, not the common small-charset DFS.

### 3. No-CGO GPU in Go, 2026

No-CGO GPU is genuinely possible via `purego` (call native shared libraries with no C
compiler, using assembly trampolines): `gogpu/wgpu` (pure-Go WebGPU with compute
shaders), `wgpu-native` via `purego`/`goffi`, or direct Vulkan/Metal/OpenCL. **The catch
for this project:** "no CGO at build" ≠ "pure Go at runtime." Every GPU path introduces
a **runtime native dependency** (a GPU driver and/or `wgpu-native` library) — at odds
with UnPixel's ethos of embedding its own fonts and shipping self-contained
cross-compiled binaries. The pure-Go `gogpu` *software* backend keeps the ethos but
offers no speedup. Links in [references](reference/references.md).

### 4. The SIMD attempt — measured and rejected

Vectorizing `colorDelta` with Go assembly (AVX2) was the recommended near-term path
(Go asm is not CGO, no runtime dependency, cross-compiles with a pure-Go fallback). The
prerequisite SoA layout was implemented and measured (bit-exact: ~570 differential cases
+ the full recovery matrix unchanged), and benchstat (`-count 12`) showed a **net
regression everywhere**: Pixelmatch `/10pct_different` **+38%**, `/gradient` **+20%**,
GuidedSearch **+10%**, DiscoverOffsets **+3.5%**.

Root cause: scalar `colorDelta`'s `if pa==pb return 0` fast-path **skips all float work
for identical pixels**, which dominate real crops (margins/background). SIMD — like any
SoA pre-compute — must process **all** lanes, redoing that skipped work; it cannot beat
the data-dependent scalar skip on this workload. Per the prove-it rule, it was reverted
and no AVX2 asm was written on speculation. One avenue remains *open but unproven*: a
4-pixel-block AVX2 kernel that **skips all-identical blocks** could preserve the
fast-path — to be pursued only on benchstat evidence.

> Note: `simd/archsimd` (Go 1.26) is not suitable as a library default — it requires
> `GOEXPERIMENT=simd` at build, is AMD64-only, and is outside the Go 1 compatibility
> promise.

### 5. Recommendation

1. **SIMD `colorDelta`:** tried and reverted (net regression — the scalar fast-path
   already skips the dominant identical-pixel work). The naïve vectorization is a dead
   end; only a block-skipping AVX2 kernel might help, and only with benchstat proof.
2. **GPU batch backend:** prototype only if a real large/batch use case appears, as an
   opt-in build-tagged backend with a CPU fallback — never the default.
3. **Full on-GPU pipeline:** documented research direction only; conflicts most with the
   pure-Go/zero-config ethos.

**Conclusion:** a no-CGO GPU path exists in 2026, but for UnPixel's
many-small-comparisons, sequential, branch-heavy workload it is not a straightforward
gain — it requires an algorithmic rearchitecture and a runtime native dependency, whereas
the CPU path remains fastest today with neither cost.

## Profile-Guided Optimization (PGO)

`cmd/unpixel/default.pgo` is a CPU profile committed to the main package directory.
Go 1.21+ auto-detects it and applies PGO when building the binary (`go build ./cmd/unpixel`)
— no flags needed. The profile was captured by running the hot-path benchmarks
(BenchmarkGuidedSearch + BenchmarkDiscoverOffsets) for 20 s each on a representative
machine, generating a pprof-format file that the compiler uses to guide inlining and
other decisions.

### Measured gain (benchstat, -count 10, i7-1370P)

```
pkg: github.com/oioio-space/unpixel/internal/search
BenchmarkGuidedSearch              -3.83%  (p=0.002)
BenchmarkDiscoverOffsets/workers_1 -4.79%  (p=0.011)
BenchmarkDiscoverOffsets/workers_max -11.25% (p=0.000)
geomean (search)                   -5.69%

pkg: github.com/oioio-space/unpixel/internal/metric
Pixelmatch_Distance/10pct_different -10.80% (p=0.000)
Pixelmatch_Distance/gradient        -6.37%  (p=0.000)
geomean (metric)                    -8.15%

pkg: github.com/oioio-space/unpixel/internal/pixelate
BlockAverage_Pixelate               -5.16%  (p=0.035)
BlockAverage_Pixelate_Padded        -3.85%  (p=0.043)
geomean (pixelate)                  -4.51%
```

No allocation regressions. Output is byte-identical (PGO is a pure compiler optimization).

### Regenerating the profile

Run `mise run pgo:update` to re-capture the profile from the current codebase and overwrite
`cmd/unpixel/default.pgo`. Do this after a significant hot-path refactor — PGO profiles
age gracefully (a stale profile rarely regresses; it just applies less benefit), but
re-profiling after major structural changes keeps the gain current. Commit the updated file.

The command is:

```bash
scripts/gotest-caged.sh go test -run '^$' \
  -bench 'BenchmarkGuidedSearch$|BenchmarkGuidedSearch_bounded|BenchmarkDiscoverOffsets' \
  -benchmem -benchtime=20s \
  -cpuprofile=cmd/unpixel/default.pgo \
  ./internal/search/
```

## Where the wins actually came from

The shipped speedups are algorithmic and structural, not SIMD: render LRU + prefix
memoization, pooled font faces, metric early-exit, parallel offset discovery and
intra-node search with a deterministic merge, FastBlur, and the monospace fast-path,
and now PGO on the production binary (~5–11% on the hot loop).
See [`PROGRESS.md`](../PROGRESS.md) and [`benchmarks/quality-history.md`](../benchmarks/quality-history.md)
for the version-by-version record.
