# UnPixel

A faithful pure-Go port of [Bishop Fox's **unredacter**](https://github.com/bishopfox/unredacter) — reconstructs text hidden behind **pixelation/mosaic** redaction. Background: [*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).

[![CI](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml/badge.svg)](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml) [![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat)](https://go.dev/dl/) [![License GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

> **Status:** the core **library is usable** (~94% test coverage, all gates green). The
> **CLI** (`cmd/unpixel`) is still a placeholder. See [`PROGRESS.md`](PROGRESS.md) for the roadmap.

## Table of contents

- [How it works](#how-it-works)
- [Features](#features)
- [Install](#install)
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
  through `Config`; the faithful defaults are wired by importing the `defaults` package.
- **Self-consistent correctness.** Fidelity is judged by a redaction round-trip (redact a known
  plaintext, then recover it). Matching a *Chromium*-rendered redaction is a documented Phase-2
  goal (needs a `chromedp` renderer).
- **~94% test coverage** across rendering, pixelation, metrics, search, and end-to-end.

## Install

As a library:

```bash
go get github.com/oioio-space/unpixel
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

## Quick start

Recover the text behind a pixelated PNG:

```go
package main

import (
	"context"
	"fmt"
	"image/png"
	"os"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wires the default renderer/pixelator/metric/strategy
)

func main() {
	f, err := os.Open("redacted.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		panic(err)
	}

	eng, err := unpixel.New(img, unpixel.Config{}) // zero Config = faithful defaults
	if err != nil {
		panic(err)
	}

	progress, results := eng.Run(context.Background())
	go unpixel.OnProgress(progress, func(p unpixel.Progress) {
		fmt.Printf("\rbest: %-20q (%.3f)", p.BestGuess, p.BestScore)
	})

	fmt.Println("\nrecovered:", (<-results).BestGuess)
}
```

Public API (root package `unpixel`):

| Symbol | Purpose |
|--------|---------|
| `New(redacted image.Image, cfg Config) (*Engine, error)` | Build an engine; zero `Config` = faithful defaults |
| `(*Engine).Run(ctx) (<-chan Progress, <-chan Result)` | Run the search; stream progress, deliver the result |
| `OnProgress(ch <-chan Progress, fn func(Progress))` | Drain progress events into a callback (any UI) |
| `Renderer`, `Pixelator`, `Metric`, `Strategy` | Pluggable pipeline interfaces |
| `Config`, `Style`, `Result`, `Eval`, `Offset`, `Progress`, `EventKind` | Configuration and result/event types |

## Configuration

Pass a `Config` to `unpixel.New`. Every zero value falls back to a documented default.

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `Charset` | `string` | `"abcdefghijklmnopqrstuvwxyz "` | Candidate characters to search |
| `MaxLength` | `int` | `20` | Maximum plaintext length |
| `BlockSize` | `int` | `8` | Pixelation block size, in pixels |
| `Threshold` | `float64` | `0.25` | Max image-distance score (0–1) to keep a candidate |
| `SpaceThreshold` | `float64` | `0.5` | Looser threshold for extending with a space (whitespace blur) |
| `ThresholdFor` | `func(rune) float64` | space→`SpaceThreshold`, else `Threshold` | Per-character threshold; override for new char classes |
| `Style` | `Style` | Liberation Sans, 32 px, white bg | Font family/size/weight/padding for rendering |
| `Renderer` | `Renderer` | `x/image/font` (pure Go) | Text → raster |
| `Pixelator` | `Pixelator` | block-average | Raster → pixelated |
| `Metric` | `Metric` | `orisano/pixelmatch` | Image-distance score |
| `Strategy` | `Strategy` | guided DFS | Candidate-space search |

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

Phase-2 ideas (behind the interfaces; the faithful default stays put): beam search, goroutine
fan-out over candidates/offsets, SSIM / edge-aware metrics, automatic block-size & offset
inference, the `chromedp` fidelity renderer, and top-N confidence/ambiguity reporting. Details in
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
