# UnPixel documentation

> **Current release: v0.14.0** — pure-Go recovery of mosaic- and Gaussian-blur-redacted
> text, with automatic parameter detection, the specialized decoders, bilingual
> (French/English) blind recovery, and **perspective decode** of redactions photographed
> at an angle (`--rectify`, manual or auto-detected corners). Quality and performance are
> tracked across releases in [`benchmarks/quality-history.md`](../benchmarks/quality-history.md)
> and in the per-run [`JOURNAL.md`](JOURNAL.md).

Readers seeking only to run the tool should begin with the [README](../README.md). This
index maps the remainder of the documentation.

## By objective

| Objective | Page |
|-----------|------|
| Install and run the common cases | [getting-started.md](getting-started.md) |
| Understand the core method (generate-and-test) | [concepts/how-it-works.md](concepts/how-it-works.md) |
| Recover blur rather than mosaic | [concepts/mosaic-vs-blur.md](concepts/mosaic-vs-blur.md) |
| Match or reconstruct the font | [concepts/fonts-and-calibration.md](concepts/fonts-and-calibration.md) |
| Select the appropriate decoder | [concepts/decoders.md](concepts/decoders.md) |
| Review the operating envelope | [concepts/limits.md](concepts/limits.md) |
| Consult every CLI flag and example | [reference/cli.md](reference/cli.md) |
| Use the Go library API | [reference/api.md](reference/api.md) |
| Locate the papers, prior art, and libraries | [reference/references.md](reference/references.md) |
| Review the package layout and pipeline | [architecture.md](architecture.md) |
| Compare with the original Bishop Fox tool | [comparison.md](comparison.md) |
| Examine the performance work and the GPU/SIMD study | [performance.md](performance.md) |
| Wire an external image restorer behind the physics-verify gate | [sidecar-protocol.md](sidecar-protocol.md) |

## Project records (maintained for history)

- [`PROGRESS.md`](../PROGRESS.md) — the canonical roadmap, decision log, and complete
  project history (French).
- [`JOURNAL.md`](JOURNAL.md) — the machine-generated record of recovery quality and
  performance across all test corpora, tracked release by release.
- [`benchmarks/quality-history.md`](../benchmarks/quality-history.md) — one row per
  release: panel score and timing.
- [`CLAUDE.md`](../CLAUDE.md) — the contributor and tooling guide, including the two
  inviolable project rules (no CGO; a benchstat-verified hot path).
