# Comparison vs. Bishop Fox unredacter

[bishopfox/unredacter](https://github.com/bishopfox/unredacter) is the proof-of-concept
that demonstrated pixelation is reversible. UnPixel is a faithful Go port that
generalizes it. (For the project's own version-over-version evolution, see
[`PROGRESS.md`](../PROGRESS.md).)

## Feature comparison

| Dimension | Bishop Fox unredacter | UnPixel |
|-----------|----------------------|---------|
| Language / runtime | TypeScript on Node.js (Electron) | **Pure Go, no CGO** (static, cross-compilable) |
| Candidate rendering | Chromium / Electron (a browser at runtime) | `golang.org/x/image/font` (in-process, deterministic) |
| Image diff | Jimp diff (Node) | `orisano/pixelmatch` (faithful YIQ) + optional SSIM |
| Redaction handled | **Mosaic only** | **Mosaic + Gaussian blur** |
| Font | Single, hand-matched in `test.html` CSS | **Bundle sweep** (redistributable faces) or any `--font`; letter-spacing/size |
| Block size | Manual `blockSize` in code | **Auto-detected** (`InferBlockSize`) |
| Blur σ / region | n/a | **Auto-estimated σ** + **region localization** |
| Search | Recursive DFS over charset | Guided DFS, **beam**, **monospace fast-path**, plus the `mosaictext` decoders; concurrent, deterministic merge |
| Ranking | Best image diff | `BestTotal` whole-image + **confidence/ambiguity** + optional **language prior** |
| Result shape | Single best string | Ranked Top-N, per-font ranking, JSON schema |
| Config effort | "The hardest part" — hand-tune CSS, font-weight, blockSize, charset per image | **Zero-config** path: detect type, block/σ, region, size; flags only to override |
| Use as a library | App / POC | Importable module (`Recover`, `RecoverMultiFont`, pluggable `Renderer/Pixelator/Metric/Strategy`) + CLI |
| Quality bars | Manual | Recovery matrices (mosaic + blur), ≥85% coverage, benchstat-proven hot path, security/secret/SBOM gates, CI release |

## Performance

The per-candidate inner loop is **render → re-apply operator → image-diff**, run over a
large candidate space, so per-candidate cost and concurrency dominate. UnPixel's numbers
are benchstat measurements; unredacter cannot be run here (Electron), so its figures are
architectural estimates, labelled ~.

| Metric | Bishop Fox unredacter | UnPixel | Delta |
|--------|----------------------|---------|-------|
| Candidate render | Chromium/Electron page + screenshot, ~**5–50 ms** (browser layout + IPC) | in-process `x/image`, **~37 µs** | ~**100–1000×** per render |
| Per-candidate (render+diff) | ~milliseconds | **~0.1 ms** (mosaic) | orders of magnitude |
| Concurrency | single page, sequential | fan-out across CPUs, deterministic merge (**~4×** offset discovery) | parallel vs serial |
| Startup | launch a browser | static binary, no runtime deps | seconds → ms |
| Blur operator | n/a (no blur) | FastBlur **~3×** vs exact Gaussian at σ6 (1.59 ms → 0.54 ms) | — |
| Search shape | recursive DFS | guided DFS / beam / **monospace fast-path (~8–16×** on mono) | fewer evals |
| Render reuse | re-render every candidate | per-text render LRU cache, prefix memoization (beam) | fewer renders |

The headline is the **render**: unredacter pays a Chromium screenshot per candidate
(cross-process, browser layout), while UnPixel rasterizes in-process with
`golang.org/x/image` — roughly 2–3 orders of magnitude cheaper per candidate, before
counting fan-out, caching, and the strategy fast-paths. The trade-off unredacter buys
with Chromium is pixel-exact browser fidelity; UnPixel trades that for speed +
portability and recovers fidelity via the font bundle and self-consistent matching (see
[limits](concepts/limits.md)).

## In one sentence

**Same core idea** (pixelation/blur is a deterministic function → render a candidate,
re-apply the same operator, compare, search). **What UnPixel adds:** pure-Go
portability, a second redaction type (blur), automatic parameter inference (block size,
σ, region, font size), a font bundle + sweep, alternative metrics/strategies/decoders,
ranked/confidence output, a language prior, and a zero-config CLI — versus the
original's single-font, mosaic-only, manually-tuned proof of concept.

For UnPixel's ongoing hot-path optimization work and the no-CGO GPU/SIMD study, see
[performance.md](performance.md).
