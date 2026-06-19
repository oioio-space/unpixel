# Accelerating the hot path — SIMD vs GPU (no-CGO)

Study requested: *can we speed up the per-candidate compute with a **no-CGO GPU**
package, and what do we propose?* This document grounds the answer in the actual
CPU profile, the project's constraints, and the 2026 state of no-CGO GPU in Go,
then makes concrete proposals.

## 1. What the profile says (measured, not assumed)

`go test ./internal/search -bench BenchmarkDiscoverOffsets/workers_1 -cpuprofile`
(commit after P4.10 step 1):

| Function | flat | cum | Owned? |
|----------|------|-----|--------|
| `colorDelta` (YIQ per-pixel delta) | 12.8% | 24.4% | now in-repo (P4.10 step 1) |
| `isAntiAliased` (3×3 neighborhood) | 12.2% | 27.6% | now in-repo |
| `pixelate.BlockAverage.Pixelate` | 6.7% | 9.3% | in-repo |
| YIQ conversions (`rgbaToY/I/Q`) | ~5% | — | in-repo |
| GC / runtime | ~8% | — | — |

The per-candidate **compare** dominates (~58% before P4.10 step 1, which removed
the external reader-abstraction overhead). The remaining compare cost splits into
a **vectorizable** part (`colorDelta`: pure arithmetic) and a **branchy** part
(`isAntiAliased`: data-dependent 3×3 neighborhood walk with early-outs and a
second-order `hasManySiblings` check).

## 2. Why this workload is a poor fit for GPU offload

GPUs win when the kernel is **large, arithmetically dense, uniformly parallel**,
and the **compute:transfer ratio is high**. Our workload is the opposite:

1. **Tiny work units.** Each `Compare` runs over the changed band — often ≈1
   block wide × the redaction height (tens × tens of pixels, a few KB). A single
   CPU compare is microseconds.
2. **Per-dispatch overhead dominates.** Submitting one kernel to the GPU and
   reading the result back costs ~10–100 µs (queue submit + sync + readback) —
   *more than the entire CPU compare it would replace*. Offloading one compare at
   a time is a guaranteed net loss.
3. **Control flow is sequential.** The guided DFS chooses the next character from
   the result of the previous one (`prevGuess`). It is not one big parallel
   batch; it is thousands of small, data-dependent steps. (Offsets and a node's
   children are already parallelized on CPU — P4.11.)
4. **Branch divergence.** `isAntiAliased` (the larger half of the compare) is
   exactly the kind of per-pixel, data-dependent branching that wrecks GPU
   occupancy. Only `colorDelta` maps cleanly to SIMD/GPU lanes.
5. **Rendering is on the critical path too.** Text glyph rasterization
   (`golang.org/x/image`) feeds every candidate. To avoid shuttling rendered
   images to the GPU per candidate, rendering + pixelation would also have to
   move on-GPU — a project in itself.

**To make a GPU pay off you must change the algorithm**, not just the metric:
batch thousands of candidate evaluations (render → pixelate → diff) into one
dispatch — e.g. evaluate the whole charset × offset grid for the next position,
or a wide beam, entirely on-GPU. That is a major rearchitecture and only helps
the wide-charset/beam regime, not the common small-charset DFS.

## 3. No-CGO GPU in Go, 2026 (state of the art)

No-CGO GPU is genuinely possible now, via `purego` (call native shared libraries
with no C compiler at build, using assembly trampolines):

- **gogpu/wgpu** — a pure-Go WebGPU implementation with **compute-shader
  support** (WGSL, storage buffers, workgroup dispatch), backends Vulkan / DX12 /
  Metal / GLES / **software**, zero-CGO. Pre-1.0 (v0.42.0, ~309★) → experimental,
  APIs still moving. [gogpu/wgpu](https://github.com/gogpu/wgpu),
  [gogpu/gogpu](https://github.com/gogpu/gogpu)
- **wgpu-native via purego/goffi** — call the Rust `wgpu-native` `.so/.dylib`
  from Go with no C compiler. Mature FFI layer, but a **runtime native-library
  dependency**. [go-webgpu/goffi](https://github.com/go-webgpu/goffi)
- **Direct Vulkan / Metal / OpenCL via purego** — maximum control, maximum work,
  runtime driver dependency.

**The catch for *this* project.** "No CGO at build" ≠ "pure Go at runtime". Every
GPU path above introduces a **runtime native dependency** — a GPU driver and/or
`wgpu-native` library that must be present, detected, and fallen back from. That
is a real stretch against UnPixel's ethos (embeds its own fonts, ships 6
self-contained cross-compiled binaries via goreleaser, zero-config). The pure-Go
`gogpu` *software* backend keeps the ethos but offers no speedup (it's a CPU
rasterizer); its real backends `dlopen` native drivers.

## 4. Proposals

### Proposal A — CPU SIMD on `colorDelta` (recommended near-term) — "P4.10 step 2"

Vectorize the per-pixel YIQ delta (the 24% `colorDelta`) with **Go assembly
(AVX2)** + runtime CPU feature detection + a **pure-Go fallback**; leave the
branchy `isAntiAliased` scalar. Go assembly is **not CGO**, adds **no runtime
dependency**, needs **no rearchitecture**, cross-compiles cleanly (fallback on
non-AMD64), and is benchstat-provable. Bit-exactness: avoid FMA so rounding
matches the scalar path (recovery output unchanged). *This is the lowest-risk,
best-aligned acceleration and the natural continuation of P4.10 step 1.*
Expected: a fraction of the 24% colorDelta; modest but real, default-path,
zero-dependency.

> Note `simd/archsimd` (Go 1.26) is **not** suitable as a library default: it
> requires `GOEXPERIMENT=simd` at build, is AMD64-only, and is outside the Go 1
> compatibility promise. Hand-written AVX2 asm + fallback is the production path.

> **Measured (P4.10 step 2) — Proposal A did NOT pay off; not adopted.** Any
> vectorization needs an SoA layout, so we first implemented the prerequisite:
> per-pixel Y/I/Q pre-computed once into a sliding window (bit-exact — differential
> ~570 cases + recovery matrix 315/315 unchanged). benchstat (`-count 12`) showed a
> **net regression everywhere**: Pixelmatch `/10pct_different` **+38%**, `/gradient`
> **+20%**, GuidedSearch **+10%**, DiscoverOffsets **+3.5%**. Root cause: scalar
> `colorDelta`'s `if pa==pb return 0` fast-path **skips all float work for identical
> pixels**, which dominate real crops (margins/background). SIMD — like any SoA
> pre-compute — must process **all** lanes, redoing that skipped work; it cannot beat
> the data-dependent scalar skip on this workload. Per the project's prove-it rule we
> reverted it and did **not** write AVX2 asm on speculation (the plan9-asm +
> fallback + CPU-detection cost is unjustified for a measured-negative gain). One
> avenue remains *open but unproven*: a 4-pixel-block AVX2 kernel that **skips
> all-identical blocks** could preserve the fast-path — pursue only on benchstat
> evidence. The representative `Pixelmatch_Distance/gradient` benchmark (every pixel
> differs) was kept.

### Proposal B — GPU batch backend for the parallel regimes (opt-in, build-tagged)

Add an **optional** GPU backend (via gogpu/wgpu compute or wgpu-native+purego)
that accelerates the **embarrassingly-parallel** parts only: the 64-offset
discovery sweep and/or wide-charset / beam evaluation, by batching many
candidate diffs (and ideally pixelation) into one compute dispatch. Gated behind
a build tag + runtime capability detection, **off by default**, pure-Go/CPU
remains the default. Only worth it for large/batch workloads; needs the search
restructured to hand the GPU a big batch. High effort, real runtime dependency,
benefits a subset of cases — measure against Proposal A before committing.

### Proposal C — Full on-GPU pipeline (research / out of scope)

Move render → pixelate → diff entirely on-GPU and reformulate the search as
mass-parallel candidate evaluation. Largest potential speedup for wide search
spaces, but it is effectively a second engine: GPU glyph rasterization, a WGSL
faithful-pixelmatch (incl. the AA logic), and a batch search driver. Conflicts
most with the pure-Go/zero-config ethos. Document as a future direction.

## 5. Recommendation

1. ~~**Do Proposal A now** (SIMD `colorDelta`, P4.10 step 2).~~ **Done and reverted:**
   measured a net regression (see the P4.10-step-2 note in §4) because the scalar
   fast-path already skips the dominant identical-pixel work that SIMD must process.
   The naïve vectorization is a dead end; only a block-skipping AVX2 kernel might
   help, and only with benchstat proof.
2. **Prototype Proposal B** only if a real large/batch use case appears, as an
   opt-in build-tagged backend with a CPU fallback — never the default. Measure
   the per-dispatch overhead against the CPU SIMD path on representative sizes
   before investing.
3. **Keep Proposal C** as a documented research direction.

The honest headline: a no-CGO GPU path **exists** in 2026, but for UnPixel's
many-tiny-compares, sequential, branch-heavy workload it is **not a free win** —
it needs an algorithmic rearchitecture and a runtime native dependency, whereas
CPU SIMD delivers a default-path gain today with none of those costs.

### Sources

- [gogpu/wgpu — pure-Go WebGPU (compute), zero-CGO](https://github.com/gogpu/wgpu)
- [gogpu/gogpu — pure-Go GPU framework](https://github.com/gogpu/gogpu)
- [go-webgpu/goffi — pure-Go FFI for wgpu-native (purego)](https://github.com/go-webgpu/goffi)
- [Go 1.26 release notes — `simd/archsimd` under `GOEXPERIMENT=simd`](https://go.dev/doc/go1.26)
- [purego — call C libraries without CGO](https://github.com/ebitengine/purego)
