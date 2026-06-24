# UnPixel documentation

> **Current release: v0.13.0** — pure-Go mosaic and Gaussian-blur text recovery,
> zero-config auto-detection, seven opt-in decoders, bilingual (FR/EN) blind recovery.
> Quality and speed are tracked release-over-release in
> [`benchmarks/quality-history.md`](../benchmarks/quality-history.md) and the
> per-run [`JOURNAL.md`](JOURNAL.md).

Start at the [README](../README.md) if you just want to run the tool. This index maps
the rest of the documentation.

## I want to…

| Goal | Page |
|------|------|
| Install and run the common cases | [getting-started.md](getting-started.md) |
| Understand the core idea (generate-and-test) | [concepts/how-it-works.md](concepts/how-it-works.md) |
| Recover blur instead of mosaic | [concepts/mosaic-vs-blur.md](concepts/mosaic-vs-blur.md) |
| Match or reconstruct the font | [concepts/fonts-and-calibration.md](concepts/fonts-and-calibration.md) |
| Choose the right decoder for my content | [concepts/decoders.md](concepts/decoders.md) |
| Know honestly what UnPixel can and can't do | [concepts/limits.md](concepts/limits.md) |
| Look up every CLI flag and example | [reference/cli.md](reference/cli.md) |
| Use the Go library API | [reference/api.md](reference/api.md) |
| Find the papers, prior art, and libraries | [reference/references.md](reference/references.md) |
| See the package layout and pipeline | [architecture.md](architecture.md) |
| Compare to the original Bishop Fox tool | [comparison.md](comparison.md) |
| Understand the performance work and GPU/SIMD study | [performance.md](performance.md) |

## Project records (kept for history, not a how-to)

- [`PROGRESS.md`](../PROGRESS.md) — canonical roadmap, decision log, and full
  evolution (French).
- [`JOURNAL.md`](JOURNAL.md) — machine-generated recovery quality+speed journal over
  all test corpora, tracked version-over-version.
- [`benchmarks/quality-history.md`](../benchmarks/quality-history.md) — one row per
  release: panel score and timing.
- [`CLAUDE.md`](../CLAUDE.md) — contributor/tooling guide and the two absolute project
  rules (no CGO; benchstat-proven hot path).
