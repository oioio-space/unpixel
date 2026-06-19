# Deltas

Two comparisons: what the blur / zero-config / performance work added **over the
previous UnPixel** (≈ v0.3.0), and how UnPixel compares to the **original Bishop
Fox unredacter**.

## 1. Delta vs the previous UnPixel (v0.3.0 → now)

Before this work UnPixel reversed **mosaic** redaction, zero-config for the
*font* (bundle sweep) but mosaic-only. This batch extends it to **blur**, makes
blur recovery zero-config, and adds search-quality/perf levers.

| Area | Before (v0.3.0) | Now | Where |
|------|-----------------|-----|-------|
| Redaction types | Mosaic only | Mosaic **+ Gaussian blur** | `pixelate.GaussianBlur`, `WithPixelator`, CLI `--redaction` |
| Blur operator | — | Separable exact Gaussian **+ FastBlur** (3-box, O(1)/px, ~3× cheaper at σ6) | `pixelate.NewGaussianBlur` / `NewFastBlur`, `defaults.{GaussianBlur,FastBlur}` (#3) |
| Blur params (σ) | — | **Auto-estimated** (σ ≈ contrast/(gPeak·√2π)); CLI `--blur-sigma`, fast box approx auto at σ≥6 (`--blur-exact` to force) | `unpixel.InferBlurSigma` |
| Region (screenshots) | Whole image | **Locate the blurred band**, then estimate σ + recover on it (fixes whole-image σ skew: 0.6 → 4.8 on bf_challenge) | `unpixel.LocateRedaction` (#1) |
| Font size | Fixed default (32) | **Calibrated** from content height when unset | `unpixel.InferFontSize` (#2) |
| Tie-breaking | Whole-image `BestTotal` only | + optional **char-bigram language prior** (plausible text wins equal-image ties) | `internal/lang`, `Config.LanguageModel`, CLI `--language` (#5) |
| Strategies | guided, beam | + **monospace fast-path** (greedy narrow-beam, parallel per-position; ~8–16× faster on mono) | `search.MonospaceStrategy`, CLI `--strategy mono` (#6) |
| Blur quality guard | — | **Blur recovery matrix** (texts × σ, true-Gaussian redaction) | `recover_blur_test.go` (#7) |

**Zero-config blur, demonstrated:** `unpixel --redaction blur secret.png` locates
the region, estimates σ, calibrates font size, sweeps the bundled fonts, and
recovers a short blurred secret with no font/σ/size supplied.

**Honest non-result — #4 reduced-resolution compare:** implemented as a metric
decorator (box-downsample then compare) and **benchmarked → rejected**: the
downsample pass costs ~as much per pixel as the compare it saves, so it was a net
time loss (pixelmatch 84µs → 101µs at ×2; SSIM 77µs → 87µs). The genuine
reduced-resolution lever is **pipeline-level** (downscale *before* the expensive
blur, compounding with FastBlur), which needs geometry scaling + fidelity
validation and is proposed, not shipped. (Same lesson as a reverted FastBlur
micro-opt: blur is FLOP-bound; only algorithmic changes move it.)

**Limits (measured):** the real Bishop Fox challenge *line* (≈560 px, arbitrary
content/charset, σ≈5.6) is not cracked — it is a research-grade combinatorial
problem. The pieces that make it tractable are now in place (locate → σ → size →
font sweep → language prior → mono fast-path); closing it further needs charset
inference from the visible text and a stronger LM, both proposed in `PROGRESS.md`.

## 2. Delta vs Bishop Fox unredacter (the original)

[bishopfox/unredacter](https://github.com/bishopfox/unredacter) is the
proof-of-concept that proved pixelation is reversible. UnPixel is a faithful Go
port that generalises it.

| Dimension | Bishop Fox unredacter | UnPixel |
|-----------|----------------------|---------|
| Language / runtime | TypeScript on Node.js (Electron) | **Pure Go, no CGO** (static, cross-compilable) |
| Candidate rendering | Chromium / Electron (a browser at runtime) | `golang.org/x/image/font` (in-process, deterministic) |
| Image diff | Jimp diff (Node) | `orisano/pixelmatch` (faithful YIQ) + optional SSIM |
| Redaction handled | **Mosaic only** | **Mosaic + Gaussian blur** |
| Font | Single, hand-matched in `test.html` CSS | **Bundle sweep** (7 redistributable faces) or any `--font`; letter-spacing/size |
| Block size | Manual `blockSize` in code | **Auto-detected** (`InferBlockSize`) |
| Blur σ / region | n/a | **Auto-estimated σ** + **region localization** |
| Search | Recursive DFS over charset | Guided DFS, **beam**, **monospace fast-path**; concurrent, deterministic merge |
| Ranking | Best image diff | `BestTotal` whole-image + **confidence/ambiguity** + optional **language prior** |
| Result shape | Single best string | Ranked Top-N, per-font ranking, JSON schema |
| Config effort | "The hardest part" — hand-tune CSS, font-weight, blockSize, charset per image | **Zero-config** path: detect type, block/σ, region, size; flags only to override |
| Use as a library | App / POC | Importable module (`Recover`, `RecoverMultiFont`, pluggable `Renderer/Pixelator/Metric/Strategy`) + CLI |
| Quality bars | Manual | Recovery matrices (mosaic + blur), ~89% coverage, benchstat-proven hot path, security/secret/SBOM gates, CI release |

### Performance (UnPixel measured; unredacter reasoned from its architecture)

The per-candidate inner loop is **render → re-apply operator → image-diff**, run
over a large candidate space, so per-candidate cost and concurrency dominate.
UnPixel's numbers are benchstat measurements on this machine; unredacter cannot
be run here (Electron), so its figures are architectural estimates, labelled ~.

| Metric | Bishop Fox unredacter | UnPixel | Delta |
|--------|----------------------|---------|-------|
| Candidate render | Chromium/Electron page + screenshot, ~**5–50 ms** (browser layout + IPC) | in-process `x/image`, **~37 µs** | ~**100–1000×** per render |
| Per-candidate (render+diff) | ~milliseconds | **~0.1 ms** (mosaic) | orders of magnitude |
| Concurrency | single page, sequential | fan-out across CPUs, deterministic merge (**~4×** offset discovery) | parallel vs serial |
| Startup | launch a browser | static binary, no runtime deps | seconds → ms |
| Mosaic discovery (this repo) | n/a (different impl) | **98.6 ms → 19.3 ms ≈ 5.1×** over the Phase-4 work | — |
| Blur operator | n/a (no blur) | FastBlur **~3×** vs exact Gaussian at σ6 (1.59 ms → 0.54 ms) | — |
| Search shape | recursive DFS | guided DFS / beam / **monospace fast-path (~8–16×** on mono) | fewer evals |
| Render reuse | re-render every candidate | per-text render LRU cache (**discovery −15%**), prefix memoization (beam) | fewer renders |

The headline is the **render**: unredacter pays a Chromium screenshot per
candidate (cross-process, browser layout), while UnPixel rasterises in-process
with `golang.org/x/image` — roughly 2–3 orders of magnitude cheaper per
candidate, before counting fan-out, caching, and the strategy fast-paths. The
trade-off unredacter buys with Chromium is pixel-exact browser fidelity; UnPixel
trades that for speed + portability and recovers fidelity via the font bundle and
self-consistent matching.

**Same core idea** (pixelation/blur is a deterministic function → render a
candidate, re-apply the same operator, compare, search). **What UnPixel adds:**
pure-Go portability, a second redaction type (blur), automatic parameter
inference (block size, σ, region, font size), a font bundle + sweep, alternative
metrics/strategies, ranked/confidence output, a language prior, and a
zero-config CLI — versus the original's single-font, mosaic-only, manually-tuned
proof of concept.
