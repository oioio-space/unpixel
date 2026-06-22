# UnPixel

A faithful pure-Go port of [Bishop Fox's **unredacter**](https://github.com/bishopfox/unredacter) — reconstructs text hidden behind **mosaic pixelation _or_ Gaussian blur** redaction. Background: [*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).

[![CI](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml/badge.svg)](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/oioio-space/unpixel.svg)](https://pkg.go.dev/github.com/oioio-space/unpixel) [![Go Report Card](https://goreportcard.com/badge/github.com/oioio-space/unpixel)](https://goreportcard.com/report/github.com/oioio-space/unpixel) [![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat)](https://go.dev/dl/) [![License GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

> **Status:** **v0.8.0** published — mosaic and Gaussian-blur recovery, zero-config
> auto-detection (block size / blur σ / region / font), **blind bilingual (FR/EN) text recovery**,
> **zero-config blur recovery** (`RecoverBlurred`: auto σ-search + language-prior beam),
> **noise-robust recovery (auto median denoise) and a frequency-weighted French language prior**,
> **monospace mosaic decoder** (`mosaictext`), and real GIMP sample corpus; ~86% test coverage, all gates green.
> See [`PROGRESS.md`](PROGRESS.md) for the roadmap and [`docs/DELTA.md`](docs/DELTA.md) for the
> delta vs the original Bishop Fox unredacter.

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
target's blocks exactly. When image distance alone is ambiguous, optional **plausibility priors**
break ties: a dictionary of real words, common passwords (including French ones), and recognized
secret formats (UUIDs, API tokens, Luhn checksums) all add a small bonus, making natural
language and structured secrets rank higher than random noise.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the algorithm and the library choices behind it, and
[`docs/DELTA.md`](docs/DELTA.md) for how UnPixel compares to the original Bishop Fox unredacter
and what the blur / zero-config work added.

## Features

- **Pure Go, no CGO.** Deterministic, statically linked, cross-compilable — no C toolchain.
- **Library-agnostic progress API.** `Run` streams typed `Progress` events on a buffered channel
  (best guess, current candidate, score, depth, offsets probed/total, evaluated count, elapsed),
  so any UI — web/SSE, TUI, desktop — can subscribe via the channel or the `OnProgress` callback.
- **Pluggable everything.** Swap the `Renderer`, `Pixelator`, `Metric`, or search `Strategy`
  through `Config`; the faithful defaults are wired by importing the `defaults` package. Built-in
  choices: guided-DFS, beam, or monospace fast-path; mosaic or Gaussian-blur operator; optional
  Richardson-Lucy deconvolution for exploratory preprocessing; pixelmatch or SSIM distance;
  optional priors to break equal-image ties: char-bigram language model, dictionary words, and
  structured-secret formats (UUID, API tokens, Luhn checksums, common French/English passwords).
- **Concurrent by default.** Grid-offset discovery and per-offset search fan out across
  `Config.Workers` goroutines (default: all CPUs) with a **deterministic merge** — same output
  regardless of scheduling. Intra-node parallelism of the DFS tree further accelerates wide-charset
  recovery. ~4–7× faster offset discovery on a typical laptop.
- **Auto-detects the block size.** Leave `Config.BlockSize` unset and `New` infers the mosaic
  grid from the image (`InferBlockSize`), so callers don't have to measure it.
- **Blur, not just mosaic.** Blur is also a deterministic function of its input, so the same
  attack applies: `pixelate.NewGaussianBlur(σ)` / `WithPixelator` reproduce a Gaussian blur. **Blur
  recovery is now zero-config on σ:** `unpixel.RecoverBlurred(ctx, img, opts...)` auto-estimates σ
  via `InferBlurSigma`, then searches σ adaptively as a dimension of the search (like block size in
  mosaic), defaulting to **beam search with language prior** to recover longer words where per-character
  image signal is weak. The CLI `--redaction blur` auto-searches σ when `--blur-sigma` is unset.
  `Result.BlurSigma` records the recovered σ. Optional exploratory Richardson-Lucy deconvolution
  (`--deblur`) for known-PSF cases.
- **Zero-config font matching.** Recovery needs the redaction's typeface — so with **no `--font`,
  UnPixel sweeps a built-in set of redistributable fonts** (Liberation Sans/Serif/Mono ≈
  Arial/Times/Courier, Carlito ≈ Calibri, Caladea ≈ Cambria, Adwaita Mono, Noto Sans Mono, Source
  Code Pro & JetBrains Mono for code) **in parallel** and keeps the **best fit by whole-image score**.
  Or match it yourself with `--font`/`--font-size`/`--letter-spacing` (and sweep your own via repeated
  `--font`/`--font-dir`). Library: the `fonts` bundle + `RecoverMultiFont`; `render.NewXImageFromFonts`
  for a custom face.
- **Automatic Top-K pruning for code.** When a language model is set and the charset is wide (≥40
  runes), the search automatically narrows candidates to the most-likely next characters per
  language, keeping the search tractable (~10.8× speedup for wide charsets) while maintaining full
  recall on the default small-charset path.
- **Ranked results, not just one guess.** Each `Result` carries the top-N candidates per grid
  offset (sorted by score, ties broken deterministically) plus `Confidence`/`Ambiguity` and a
  whole-image `BestTotal` distance — comparable across runs, so it can rank fonts or styles —
  letting callers surface alternatives instead of a single best guess.
- **Self-consistent correctness.** Fidelity is judged by a redaction round-trip (redact a known
  plaintext, then recover it). Matching a *Chromium*-rendered redaction is a documented Phase-2
  goal (needs a `chromedp` renderer).
- **~89% test coverage** across rendering, pixelation, metrics, search, CLI, and end-to-end.

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
unpixel redacted.png                       # zero-config: sweeps the built-in fonts; best guess to stdout
cat redacted.png | unpixel -               # read PNG from stdin
unpixel --format json --top 10 redacted.png
unpixel --strategy beam --metric ssim --workers 8 redacted.png

# Match a known typeface yourself (skips the sweep) — e.g. a Consolas code screenshot:
unpixel --font Consolas.ttf --font-size 24 --letter-spacing -0.2 -b 5 redacted.png

# Sweep your own candidate fonts (or a whole directory) instead of the built-ins:
unpixel --font Arial.ttf --font Consolas.ttf --font Courier.ttf -b 5 redacted.png
unpixel --font-dir /usr/share/fonts/truetype -b 5 redacted.png
```

Key flags (`unpixel --help` for the full list):

| Flag | Default | Purpose |
|------|---------|---------|
| `--charset` | `a–z` + space | Candidate characters to try |
| `--charset-preset` | — | Named charset when `--charset` is unset: `lower`, `alnum`, `ascii`/`code` |
| `--block-size`, `-b` | `0` (auto) | Pixelation block size; `0` auto-detects from the image |
| `--font` | embedded (Liberation Sans) | TTF/OTF font to render candidates; **repeat to sweep** and keep the best fit |
| `--font-dir` | — | Directory of TTF/OTF fonts to sweep (each tried; best whole-image fit wins) |
| `--font-size` | `0` (32) | Font size in points to match the redaction |
| `--letter-spacing` | `0` | Extra px after each glyph, like CSS `letter-spacing` (may be negative) |
| `--redaction` | `auto` | `auto`, `mosaic`, or `blur` (blur auto-detected when there's no mosaic grid) |
| `--blur-sigma` | `0` (auto) | Gaussian blur radius for `--redaction blur`; `0` estimates it from the image |
| `--blur-exact` | off | Force the exact Gaussian (default uses the ~3× faster box approx at large σ) |
| `--deblur` | `0` (off) | Optional Richardson-Lucy deconvolution iterations (exploratory preprocessing) |
| `--denoise` | `-1` (auto) | Median denoise for `--blind` mode: `-1` auto-detects impulse noise, `0` disables, `N` forces N×N window |
| `--strategy` | `guided` | `guided` (full DFS), `beam` (bounded), or `mono` (monospace fast-path) |
| `--beam-width` | `0` (16) | Candidates kept per depth level under `--strategy beam` |
| `--metric` | `pixelmatch` | `pixelmatch` (faithful) or `ssim` (structural) |
| `--language` | off | Break ties between equal-image candidates toward plausible text (char-bigram prior) |
| `--secrets` | off | Boost plausibility of structured formats (UUID, API token, Luhn checksums, common passwords) and dictionary words |
| `--workers` | `0` (all CPUs) | Grid offsets searched concurrently; also the sweep's core budget |
| `--top`, `-n` | `5` | Ranked candidates to report |
| `--format`, `-f` | `text` | `text` or machine-readable `json` |
| `--timeout` | `0` (none) | Max recovery time |

The best guess prints to stdout (so it pipes cleanly); the ranked table and live progress go to
stderr. `--format json` emits a stable schema (`best_guess`, `confidence`, `total_score`, `top`,
and a ranked `fonts` array when sweeping).

> **Tip:** lower block sizes carry less information per glyph, so a tighter `--threshold`
> (e.g. `0.1`) prunes coincidental matches and lets the whole-image score pick the complete answer.

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

Unknown typeface? Build a renderer per candidate font (you supply the `.ttf`) and let
`RecoverMultiFont` recover with each in parallel, ranked best-fit first:

```go
import "github.com/oioio-space/unpixel/defaults"

var rs []unpixel.Renderer
for _, data := range fontFiles { // each is TTF/OTF bytes you read
	r, err := defaults.RendererFromFonts(data, nil)
	if err != nil {
		panic(err)
	}
	rs = append(rs, r)
}
ranked, err := unpixel.RecoverMultiFont(ctx, img, rs, unpixel.WithBlockSize(5))
best := ranked[0] // lowest BestTotal — the font that fit best
fmt.Printf("%q via font #%d\n", best.Result.BestGuess, best.Index)
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

### Blind & bilingual recovery (experimental)

UnPixel can recover text without knowing the font, block size, or language in advance. The **blind** package auto-detects the redaction region, calibrates the block size and font size, sweeps built-in fonts, uses a frequency-weighted bilingual prior (French or English) to score candidates, and automatically denoises salt-pepper/impulse noise by default:

```bash
unpixel --blind --lang fr testdata/real/marx.png
unpixel --blind --lang en --block-size 8 image.png
unpixel --blind --lang fr --denoise 0 image.png    # disable auto-denoise
unpixel --blind --denoise 3 image.png              # force 3×3 median window
```

In Go, use `blind.Recover` with language selection and optional denoise control:

```go
import "github.com/oioio-space/unpixel/blind"

res, err := blind.Recover(ctx, img,
	blind.WithLanguage(blind.French), // or blind.English (the default)
	blind.WithDenoise(-1),             // auto-detect (default); 0 = off, N = force N×N window
)
if err != nil {
	panic(err)
}
fmt.Println(res.Text)
fmt.Println("Font:", res.Font, "Block:", res.Block, "Distance:", res.Dist)
fmt.Println("Denoise applied:", res.Denoise)  // radius used, or 0 if none
```

The `blind` package re-exports `English`/`French` (and `ParseLanguage` for a string flag), so no internal import is needed. **Status: experimental.** Blind recovery is proven end-to-end on synthetic mosaics rendered in the bundled fonts (sans/serif/mono); it is most reliable there. Real captures in a font outside the bundle, or containing punctuation/apostrophes outside the dictionary, recover only partially. It is also compute-heavy: a large multi-line screenshot with the font sweep can take many minutes — pin `--block-size`/`--font-size` and a single language to keep it tractable.

Public API (root package `unpixel` and sub-packages `blind` / `mosaictext`):

| Symbol | Purpose |
|--------|---------|
| `Recover(ctx, image.Image, ...Option) (Result, error)` | One call: search and return the best result |
| `RecoverReader(ctx, io.Reader, ...Option)` / `RecoverFile(ctx, path, ...Option)` | Decode then `Recover` |
| `RecoverMultiFont(ctx, image.Image, []Renderer, ...Option) ([]FontResult, error)` | Sweep candidate fonts in parallel; results ranked best-fit first by `BestTotal` |
| `RecoverBlurred(ctx, image.Image, ...Option) (Result, error)` | **Zero-config Gaussian-blur recovery: auto-estimates σ, searches σ as a dimension (adaptive or bounded sweep), defaults to beam+language-prior for longer words** |
| `With*` options (`WithCharset`, `WithWorkers`, `WithRenderer`, `WithStrategy`, `WithPriors`, …) | Tweak the common knobs; `WithConfig` seeds a full `Config` |
| `New(redacted image.Image, cfg Config) (*Engine, error)` | Build an engine; zero `Config` = faithful defaults |
| `(*Engine).Run(ctx) (<-chan Progress, <-chan Result)` | Run the search; stream progress, deliver the result |
| `(*Engine).Config() Config` | Resolved config (e.g. the inferred block size) |
| `OnProgress(ch <-chan Progress, fn func(Progress))` | Drain progress events into a callback (any UI) |
| `InferBlockSize(image.Image) int` | Detect the mosaic block size |
| `InferBlurSigma(image.Image) float64` | Estimate Gaussian blur radius σ from image contrast |
| `InferImpulseNoise(image.Image) float64` | Detect impulse (salt-pepper) noise; returned value used by `blind.Recover` to auto-denoise |
| `Renderer`, `Pixelator`, `Metric`, `Strategy` | Pluggable pipeline interfaces |
| `Config`, `Style`, `Result`, `FontResult`, `Eval`, `Offset`, `Progress`, `EventKind` | Configuration and result/event types |
| **`blind.Recover(ctx, image.Image, ...Option) (*Recovery, error)`** | **Blind bilingual recovery (FR/EN) without knowing font/block/offset; re-exports `English`/`French`/`ParseLanguage`** |
| **`blind.With*` options (`WithLanguage`, `WithBlock`, `WithOffset`, `WithFontSize`, `WithLinear`, `WithFonts`, `WithMetric`)** | **Fine-tune blind recovery or override auto-detection** |
| **`mosaictext.Decode(ctx, image.Image, ...Option) (string, error)`** | **Zero-config monospace mosaic decoder (auto grid inference + character recognition)** |

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
| `Style` | `Style` | Liberation Sans, 32 px, white bg | Font size/weight/padding and `LetterSpacing` for rendering (the font itself comes from the `Renderer`) |
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
├── fonts/                  # bundled redistributable fonts (OFL/Apache) for the zero-config sweep
├── internal/
│   ├── imutil/             # crop / pad / compose; blueMargin & leftEdge detection
│   ├── pixelate/           # block-average pixelator; grid-origin crop; white padding
│   ├── metric/             # pixelmatch (faithful default) + simple RGB metric
│   ├── render/             # pure-Go x/image/font renderer (embedded or custom fonts; letter-spacing)
│   │   └── fonts/          #   embedded Liberation Sans (Regular/Bold) + OFL license
│   └── search/             # offset discovery + marginal cropping + guided DFS + whole-image ranking
└── cmd/unpixel/            # CLI (urfave/cli/v3): recovery, font sweep, text/JSON output
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
mise run cover        # coverage report (floor: 85%)
mise run bench        # hot-path benchmarks (benchstat-proven)
mise run gen          # regenerate test fixtures (mise run gen:check verifies no drift)
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
- **Fonts:** the default renderer uses bundled
  [Liberation Sans](https://github.com/liberationfonts/liberation-fonts) (SIL OFL 1.1, ≈ Arial).
  The zero-config sweep also bundles Liberation Serif/Mono, Carlito (≈ Calibri) and Source Code Pro
  & JetBrains Mono (all SIL OFL 1.1), and Caladea (≈ Cambria, Apache 2.0) — unmodified, with
  attribution and license texts in [`fonts/`](fonts) (`NOTICE.md` + `licenses/`).
- **Libraries:** [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image) (pure-Go font
  rasterizer) and [`github.com/orisano/pixelmatch`](https://github.com/orisano/pixelmatch)
  (faithful port of mapbox/pixelmatch).

## License

**GPL-3.0-or-later** — see [`LICENSE`](LICENSE). UnPixel is a derivative work of Bishop Fox's
unredacter (GPL-3.0); the copyleft license is preserved. © the UnPixel authors; original
© Bishop Fox.
