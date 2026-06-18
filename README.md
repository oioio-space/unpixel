# UnPixel

A faithful pure-Go port of [Bishop Fox's **unredacter**](https://github.com/bishopfox/unredacter) — reconstructs text hidden behind **pixelation/mosaic** redaction. Background: [*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).

[![CI](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml/badge.svg)](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/oioio-space/unpixel.svg)](https://pkg.go.dev/github.com/oioio-space/unpixel) [![Go Report Card](https://goreportcard.com/badge/github.com/oioio-space/unpixel)](https://goreportcard.com/report/github.com/oioio-space/unpixel) [![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat)](https://go.dev/dl/) [![License GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

> **Status:** the **library and CLI are usable** (~92% test coverage, all gates green).
> See [`PROGRESS.md`](PROGRESS.md) for the roadmap.

## Table of contents

- [How it works](#how-it-works)
- [Features](#features)
- [Install](#install)
- [Command-line tool](#command-line-tool)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Architecture](#architecture)
- [Contributing](#contributing)
- [Credits](#credits)
- [License](#license)

## How it works

Pixelation is a **deterministic function** of its input, so UnPixel doesn't "un-blur" anything —
it runs **generate-and-test**. It renders a candidate string in the target font, re-pixelates it
with the same block grid as the redacted region, scores the image distance against the target,
and drives a **guided depth-first search** over candidate strings: discover the grid offset, then
extend the plaintext character by character, pruning branches as soon as the re-pixelated output
stops matching. Because pixelation averages each block, only the true text reproduces the
target's blocks exactly.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the algorithm and the library choices behind it.

## Features

- **Pure Go, no CGO.** Deterministic, statically linked, cross-compilable — no C toolchain.
- **Library-agnostic progress API.** `Run` streams typed `Progress` events on a buffered channel
  (best guess, current candidate, score, depth, offsets probed/total, evaluated count, elapsed),
  so any UI — web/SSE, TUI, desktop — can subscribe via the channel or the `OnProgress` callback.
- **Pluggable everything.** Swap the `Renderer`, `Pixelator`, `Metric`, or search `Strategy`
  through `Config`; the faithful defaults are wired by importing the `defaults` package. Built-in
  choices: guided-DFS or beam search; pixelmatch or SSIM (structural) distance.
- **Concurrent by default.** Grid-offset discovery and per-offset search fan out across
  `Config.Workers` goroutines (default: all CPUs) with a **deterministic merge** — same output
  regardless of scheduling. ~4× faster offset discovery on a typical laptop.
- **Auto-detects the block size.** Leave `Config.BlockSize` unset and `New` infers the mosaic
  grid from the image (`InferBlockSize`), so callers don't have to measure it.
- **Ranked results, not just one guess.** Each `Result` carries the top-N candidates per grid
  offset (sorted by score, ties broken deterministically) plus `Confidence` and `Ambiguity`
  scores, so callers can surface alternatives instead of a single best guess.
- **Self-consistent correctness.** Fidelity is judged by a redaction round-trip (redact a known
  plaintext, then recover it). Matching a *Chromium*-rendered redaction is a documented Phase-2
  goal (needs a `chromedp` renderer).
- **~92% test coverage** across rendering, pixelation, metrics, search, CLI, and end-to-end.

## Install

As a library:

```bash
go get github.com/oioio-space/unpixel
```

As a command-line tool:

```bash
go install github.com/oioio-space/unpixel/cmd/unpixel@latest
```

From source (development) — uses [mise](https://mise.jdx.dev) for the toolchain and tasks:

```bash
git clone https://github.com/oioio-space/unpixel.git
cd unpixel
mise run setup     # install pinned tools + wire git hooks
mise run test      # verify the build
mise run           # list all tasks
```

Requires Go 1.26+.

## Command-line tool

```bash
unpixel redacted.png                       # recover; best guess to stdout
cat redacted.png | unpixel -               # read PNG from stdin
unpixel --format json --top 10 redacted.png
unpixel --strategy beam --metric ssim --workers 8 redacted.png
```

Key flags (`unpixel --help` for the full list):

| Flag | Default | Purpose |
|------|---------|---------|
| `--charset` | `a–z` + space | Candidate characters to try |
| `--block-size`, `-b` | `0` (auto) | Pixelation block size; `0` auto-detects from the image |
| `--strategy` | `guided` | `guided` (full DFS) or `beam` (bounded, faster) |
| `--beam-width` | `0` (16) | Candidates kept per depth level under `--strategy beam` |
| `--metric` | `pixelmatch` | `pixelmatch` (faithful) or `ssim` (structural) |
| `--workers` | `0` (all CPUs) | Grid offsets searched concurrently |
| `--top`, `-n` | `5` | Ranked candidates to report |
| `--format`, `-f` | `text` | `text` or machine-readable `json` |
| `--timeout` | `0` (none) | Max recovery time |

The best guess prints to stdout (so it pipes cleanly); the ranked table and live progress go to
stderr. `--format json` emits a stable schema (`best_guess`, `confidence`, `top`, …).

## Quick start

One call — give it an image (or a path), get the text back:

```go
package main

import (
	"context"
	"fmt"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wires the default renderer/pixelator/metric/strategy
)

func main() {
	res, err := unpixel.RecoverFile(context.Background(), "redacted.png")
	if err != nil {
		panic(err)
	}
	fmt.Println("recovered:", res.BestGuess)
}
```

`Recover` / `RecoverReader` / `RecoverFile` take functional options for the common knobs while
auto-detecting the rest (block size, …):

```go
res, err := unpixel.Recover(ctx, img,
	unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz0123456789 "),
	unpixel.WithWorkers(8),
)
```

For streaming progress or full control, drop to the low-level `Engine` (the helpers wrap exactly
this):

<details><summary>Low-level <code>Engine</code> API</summary>

```go
eng, err := unpixel.New(img, unpixel.Config{}) // zero Config = faithful defaults
if err != nil {
	panic(err)
}
progress, results := eng.Run(context.Background())
go unpixel.OnProgress(progress, func(p unpixel.Progress) {
	fmt.Printf("\rbest: %-20q (%.3f)", p.BestGuess, p.BestScore)
})
fmt.Println("\nrecovered:", (<-results).BestGuess)
```

</details>

Public API (root package `unpixel`):

| Symbol | Purpose |
|--------|---------|
| `Recover(ctx, image.Image, ...Option) (Result, error)` | One call: search and return the best result |
| `RecoverReader(ctx, io.Reader, ...Option)` / `RecoverFile(ctx, path, ...Option)` | Decode then `Recover` |
| `With*` options (`WithCharset`, `WithWorkers`, `WithStrategy`, …) | Tweak the common knobs; `WithConfig` seeds a full `Config` |
| `New(redacted image.Image, cfg Config) (*Engine, error)` | Build an engine; zero `Config` = faithful defaults |
| `(*Engine).Run(ctx) (<-chan Progress, <-chan Result)` | Run the search; stream progress, deliver the result |
| `(*Engine).Config() Config` | Resolved config (e.g. the inferred block size) |
| `OnProgress(ch <-chan Progress, fn func(Progress))` | Drain progress events into a callback (any UI) |
| `InferBlockSize(image.Image) int` | Detect the mosaic block size |
| `Renderer`, `Pixelator`, `Metric`, `Strategy` | Pluggable pipeline interfaces |
| `Config`, `Style`, `Result`, `Eval`, `Offset`, `Progress`, `EventKind` | Configuration and result/event types |

## Configuration

Pass a `Config` to `unpixel.New`. Every zero value falls back to a documented default.

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `Charset` | `string` | `"abcdefghijklmnopqrstuvwxyz "` | Candidate characters to search |
| `MaxLength` | `int` | `20` | Maximum plaintext length |
| `BlockSize` | `int` | `0` → auto / `8` | Pixelation block size; `≤0` auto-detects via `InferBlockSize`, else falls back to `8` |
| `Threshold` | `float64` | `0.25` | Max image-distance score (0–1) to keep a candidate |
| `SpaceThreshold` | `float64` | `0.5` | Looser threshold for extending with a space (whitespace blur) |
| `ThresholdFor` | `func(rune) float64` | space→`SpaceThreshold`, else `Threshold` | Per-character threshold; override for new char classes |
| `TopN` | `int` | `5` | Ranked candidates kept per offset in `Result.TopN` |
| `Style` | `Style` | Liberation Sans, 32 px, white bg | Font family/size/weight/padding for rendering |
| `Renderer` | `Renderer` | `x/image/font` (pure Go) | Text → raster |
| `Pixelator` | `Pixelator` | block-average | Raster → pixelated |
| `Metric` | `Metric` | `orisano/pixelmatch` | Image-distance score |
| `Strategy` | `Strategy` | guided DFS | Candidate-space search (`defaults.GuidedStrategy()` / `defaults.BeamStrategy(width)`) |
| `BeamWidth` | `int` | `16` | Candidates kept per depth level — beam strategy only |
| `CacheSize` | `int` | `4096` | LRU size for prefix-render memoization — beam strategy only (`0` disables) |
| `Workers` | `int` | `0` → all CPUs | Grid offsets probed/searched concurrently; `1` forces sequential. Never changes output |

Selecting beam search (bounded branching + prefix-render caching) instead of the default DFS:

```go
cfg := unpixel.Config{Strategy: defaults.BeamStrategy(0)} // 0 = use BeamWidth (default 16)
```

## Architecture

<details><summary>Package layout</summary>

```
github.com/oioio-space/unpixel
├── unpixel.go              # Engine, Config, Result, Eval, Offset, Progress; the 4 interfaces
├── defaults/               # wires the default components (breaks the root↔internal import cycle)
├── internal/
│   ├── imutil/             # crop / pad / compose; blueMargin & leftEdge detection
│   ├── pixelate/           # block-average pixelator; grid-origin crop; white padding
│   ├── metric/             # pixelmatch (faithful default) + simple RGB metric
│   ├── render/             # pure-Go x/image/font renderer
│   │   └── fonts/          #   embedded Liberation Sans (Regular/Bold) + OFL license
│   └── search/             # offset discovery + marginal cropping + guided DFS
└── cmd/unpixel/            # CLI (placeholder)
```

The four interfaces (`Renderer`, `Pixelator`, `Metric`, `Strategy`) live in the root package so
they can be implemented and injected from outside; the concrete implementations stay under
`internal/`.

</details>

<details><summary>Rendering fidelity &amp; Phase-2 roadmap</summary>

The original `unredacter` rasterizes via Chromium; UnPixel uses pure-Go
`golang.org/x/image/font`. Byte-identical glyphs across engines aren't possible (different
hinting/anti-aliasing), so **correctness is judged by self-consistency**: redact a known
plaintext with UnPixel's own renderer, then recover it. Recovering a Chromium-produced redaction
(e.g. the original `secret.png`) is a Phase-2 goal requiring a `chromedp` renderer.

**Landed:** top-N confidence/ambiguity reporting; a **beam-search strategy** with
**prefix-render memoization** (`defaults.BeamStrategy`); an **SSIM metric** (`defaults.SSIMMetric`);
**automatic block-size inference** (`InferBlockSize`); and **goroutine fan-out** over offset
discovery and per-offset search (`Config.Workers`, deterministic merge). **Still ahead** (behind
the interfaces; the faithful default stays put): edge-aware metrics and the `chromedp` fidelity
renderer (deferred — it would require a Chrome binary at runtime/CI). Details in
[`docs/DESIGN.md`](docs/DESIGN.md) § Phase-2.

</details>

## Contributing

```bash
mise run test:watch   # TDD loop
mise run lint         # gofmt + go vet + golangci-lint
mise run test         # tests
mise run cover        # coverage report (floor: 85%, currently ~94%)
mise run bench        # hot-path benchmarks (benchstat-proven)
mise run ci           # everything CI runs
```

Commits go through git hooks — **artifacts → secrets (gitleaks) → vulns (gosec + govulncheck) →
style (golangci-lint) → `cgo:check`** — plus a `/simplify` review. CI re-runs all of it and adds
a CycloneDX SBOM scanned by grype, a full-history secret scan, and the coverage floor.

Two **absolute project rules**: the project stays **pure Go (no CGO)**, and the hot path is
**benchmarked with benchstat-proven** changes. See [`CLAUDE.md`](CLAUDE.md) for tooling and
[`PROGRESS.md`](PROGRESS.md) for the roadmap.

## Credits

- **Original work:** [Bishop Fox's unredacter](https://github.com/bishopfox/unredacter) and the
  write-up [*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).
- **Font:** bundled [Liberation Sans](https://github.com/liberationfonts/liberation-fonts)
  (SIL OFL 1.1) — metrically Arial-compatible, for deterministic rendering.
- **Libraries:** [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image) (pure-Go font
  rasterizer) and [`github.com/orisano/pixelmatch`](https://github.com/orisano/pixelmatch)
  (faithful port of mapbox/pixelmatch).

## License

**GPL-3.0-or-later** — see [`LICENSE`](LICENSE). UnPixel is a derivative work of Bishop Fox's
unredacter (GPL-3.0); the copyleft license is preserved. © the UnPixel authors; original
© Bishop Fox.
