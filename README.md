# UnPixel

A faithful pure-Go port of [Bishop Fox's **unredacter**](https://github.com/bishopfox/unredacter) — reconstructs text hidden behind **mosaic pixelation _or_ Gaussian blur** redaction. Background: [*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).

[![CI](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml/badge.svg)](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/oioio-space/unpixel.svg)](https://pkg.go.dev/github.com/oioio-space/unpixel) [![Go Report Card](https://goreportcard.com/badge/github.com/oioio-space/unpixel)](https://goreportcard.com/report/github.com/oioio-space/unpixel) [![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat)](https://go.dev/dl/) [![License GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

> **Status:** **v0.10.0** published — pure-Go mosaic and Gaussian-blur recovery with five decoders,
> input normalization, and a re-readable test journal.
> - **Core:** zero-config auto-detection (block size / blur σ / font / language), bilingual
>   blind recovery (FR/EN), **~86% test coverage**, all gates green.
> - **Decoders:** guided/beam default, LM-guided monospace (`--decoder mono-hmm`), Depix-style
>   reference-matching with optional LM disambiguation (`--decoder ref-match [--lang]`), grid-window
>   beam for proportional fonts (`--decoder window-hmm`), and a genuine blind learned-emission Viterbi
>   HMM (`--decoder trained-hmm`).
> - **Real captures:** input normalization (`--normalize`), re-mosaic blur error-correction
>   (`--remosaic`), improved blur-σ estimation, multi-format input (JPEG/GIF/WebP/BMP/TIFF), robust
>   mosaic-vs-blur auto-detect + best-effort surfacing (`Result.BelowThreshold`).
> - **Tracking:** `mise run journal` writes an evolving `docs/JOURNAL.md` over all corpora; a SICK /
>   check-number parity corpus benchmarks against Hill-2016.
> - **Honesty:** decoders recover known-font cases exactly (digits/short content) and extend the
>   envelope, but real-world recovery stays font-fidelity-bounded (supply the exact font via `--font`)
>   and proportional natural-language sentences at coarse blocks remain unsolved.
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
  via `InferBlurSigma` (density-adaptive gradient-percentile, accurate to ~±2% for σ∈{1,2,4,8}), then
  searches σ adaptively as a dimension of the search (like block size in mosaic), defaulting to **beam
  search with language prior** to recover longer words where per-character image signal is weak. The CLI
  `--redaction blur` auto-searches σ when `--blur-sigma` is unset. `Result.BlurSigma` records the
  recovered σ. Optional exploratory Richardson-Lucy deconvolution (`--deblur`) for known-PSF cases.
  **Re-mosaic error correction** (`--remosaic` / `WithRemosaic()` / `WithRemosaicGrid(b)` /
  `WithRemosaicLinear()`): apply Hill–Zhou–Saul–Shacham (PETS-2016 §4) composite Gaussian-blur →
  block-average operator to collapse σ-mismatch and JPEG noise; opt-in via CLI or API, auto-selects
  block grid as `max(2, round(σ))` and supports both sRGB and linear-light averaging (GEGL/GIMP
  targets). Honest note: on self-consistent synthetics the plain path already converges, so the benefit
  is for real-world σ-mismatch/JPEG; never regresses vs plain.
- **Zero-config font matching.** Recovery needs the redaction's typeface — so with **no `--font`,
  UnPixel sweeps a built-in set of redistributable fonts** (Liberation Sans/Serif/Mono ≈
  Arial/Times/Courier, Carlito ≈ Calibri, Caladea ≈ Cambria, Adwaita Mono, Noto Sans Mono, Source
  Code Pro & JetBrains Mono for code) **in parallel** and keeps the **best fit by whole-image score**.
  Or match it yourself with `--font`/`--font-size`/`--letter-spacing` (and sweep your own via repeated
  `--font`/`--font-dir`). Library: the `fonts` bundle + `RecoverMultiFont`; `render.NewXImageFromFonts`
  for a custom face. **For real-world images, supplying the exact font via `--font` or `--font-dir`
  significantly improves recovery**, since font fidelity dominates the score.
- **LM-guided monospace mosaic decoder** (`--decoder mono-hmm`): fuses a bigram language model
  with per-character image signal via left-to-right beam search, avoiding the charset^length
  exponential barrier. Recovers long monospace text (10–50+ characters) when character-by-character
  signals are weak. Options: `--lang en|fr`, `--font <bundled-or-path>`, `--charset`. Limitation:
  font fidelity dominates — exact font via `--font` is strongly recommended for real captures.
- **Reference-matching mosaic decoder** (`--decoder ref-match`): Depix-style per-glyph reference
  matching that recovers **arbitrary content** (passwords, code, random strings) and works on
  **proportional fonts** (not just monospace). Renders candidate glyphs, matches columns left-to-right
  with zero language assumption. Options: `--font <bundled-or-path>`, `--charset`. Limitation:
  works when font is known; bundled sweep is exploratory, exact font required for real reliability.
- **Input normalization for real-world captures** (`--normalize`): morphological background
  estimation and removal (additive/multiplicative), dark-theme auto-inversion, and optional JPEG
  deblocking. Extends blur recovery to textured/vignette/dark-background scenarios. Multi-format
  input decoding: PNG/JPEG/GIF/WebP/BMP/TIFF.
- **Robust mosaic-vs-blur auto-detection** (`InferBlockSizeRobust`): detects pixelated grids even
  when resampled, anti-aliased, or JPEG-compressed. Routes true pixelizations to mosaic pipeline
  and ambiguous cases to blur. Best-effort result surfacing (`Result.BelowThreshold`) returns the
  best candidate even when it falls below the acceptance threshold, suitable for exploratory analysis.
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
- **87% test coverage** across rendering, pixelation, metrics, search, CLI, and end-to-end.

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

# Decoders (v0.10.0):
unpixel --decoder mono-hmm --lang en image.png                              # LM-guided monospace decoder
unpixel --decoder mono-hmm --lang fr --font "JetBrains Mono" long-text.png  # with specific font
unpixel --decoder ref-match --font "Liberation Sans" passwords.png          # reference-matching for arbitrary content
unpixel --decoder window-hmm --lang en image.png                            # window-grid beam decoder (proportional fonts)
unpixel --decoder trained-hmm image.png                                     # learned-emission Viterbi HMM (digit/PIN codes)
unpixel --normalize --redaction blur real-blur.jpg                          # normalize input + blur recovery on JPEG

# Re-mosaic correction (Hill–Zhou–Saul–Shacham PETS-2016, §4):
unpixel --remosaic --redaction blur blurred.png                             # apply blur→remosaic error correction
unpixel --remosaic-grid 4 --redaction blur image.png                        # pin the remosaic block grid
unpixel --remosaic-linear --redaction blur gimp-output.png                  # use linear-light remosaic (GEGL/GIMP)
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
| `--decoder` | `default` | `default` (guided DFS/beam), `mono-hmm` (LM-guided monospace beam), `ref-match` (reference-matching for known fonts), `window-hmm` (grid-window beam for proportional fonts), or `trained-hmm` (learned-emission Viterbi HMM for constrained alphabets) |
| `--remosaic` | off | Enable Hill–Zhou–Saul–Shacham PETS-2016 §4 composite blur→remosaic error correction (scales σ-mismatch and JPEG noise) |
| `--remosaic-grid` | `0` (auto) | Block grid size for `--remosaic`; `0` auto-detects as `max(2, round(σ))` |
| `--remosaic-linear` | off | Use linear-light block averaging for `--remosaic` (GEGL/GIMP-rendered targets) |
| `--strategy` | `guided` | `guided` (full DFS), `beam` (bounded), or `mono` (monospace fast-path) |
| `--beam-width` | `0` (16) | Candidates kept per depth level under `--strategy beam` |
| `--metric` | `pixelmatch` | `pixelmatch` (faithful; auto `pixelmatch-fast` on block-average mosaic for identical results, zero-config) or `ssim` (structural) |
| `--language` | off | Break ties between equal-image candidates toward plausible text (char-bigram prior) |
| `--secrets` | off | Boost plausibility of structured formats (UUID, API token, Luhn checksums, common passwords) and dictionary words |
| `--workers` | `0` (all CPUs) | Grid offsets searched concurrently; also the sweep's core budget |
| `--top`, `-n` | `5` | Ranked candidates to report |
| `--normalize` | off | Enable input normalization for blur recovery: morphological background removal + dark-theme inversion |
| `--normalize-bg` | `divide` | Background removal mode: `divide` (multiplicative), `subtract` (additive), or `none` |
| `--deblock` | `0` (off) | Median deblocking radius for JPEG (0 = off, N = force (2N+1)×(2N+1) kernel) |
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

### LM-guided monospace mosaic decoder (language-prior beam search)

For **long monospace text** where character-by-character image signals are weak, the `mosaictext` package offers an opt-in **LM-guided beam decoder** that breaks the exponential barrier. Instead of generate-and-test per character (charset^length candidates), it decodes left-to-right with a bigram language model fused into the objective:

```go
text, err := mosaictext.DecodeHMM(ctx, img,
	mosaictext.WithLanguage(lang.French),  // or English
	mosaictext.WithCharset("abcdefg…"),    // optional; defaults to ASCII letters + common punctuation
	mosaictext.WithFont("JetBrains Mono"), // pins a bundled mono font; omit to sweep all mono fonts
)
if err != nil {
	panic(err)
}
fmt.Println(text)
```

On the command line, use `--decoder mono-hmm` to activate it:

```bash
unpixel --decoder mono-hmm --lang fr image.png                            # auto-detect font
unpixel --decoder mono-hmm --lang en --font "Source Code Pro" image.png   # pin the font
unpixel --decoder mono-hmm --lang en --font Consolas.ttf image.png   # supply a custom TTF/OTF
```

**API**: `mosaictext.DecodeHMM(ctx, img, opts...)` with options `WithLanguage`, `WithCharset`, `WithEmissionTemperature`, `WithFont` (bundled mono by name), `WithFontFile`/`WithFontFileBold` (caller-supplied TTF/OTF bytes).

**Why it matters:** The default generate-and-test scales as charset^(text length), so a 25-character line becomes intractable. The beam decoder is polynomial in length, scanning once left-to-right with a bounded beam (default width 8) and scoring each cell by fused image-MSE + log-probability transitions. This is the principled successor to post-hoc language reranking — the LM is now *part of the optimized objective*.

**Key limitation:** Recovery quality is bounded by **font fidelity**. On synthetic mosaics in bundled fonts (Liberation, JetBrains Mono, Noto Sans Mono, etc.), the decoder recovers full-length sequences. On real captures where the exact font is not bundled, the decode is partial or incorrect. **Mitigation**: supply the exact font via `--font` (path to a TTF/OTF). When the font is known, this is expected to recover most real monospace redactions. Note: an exact global-MAP Viterbi variant was attempted and rejected — the per-cell emissions are not independent (block-boundary averaging couples adjacent cells), so the bigram lookahead overwhelms the emission signal even at tuned temperatures; the beam search avoids this by scoring each cell against the already-committed context.

### Reference-matching mosaic decoder (arbitrary content + proportional fonts)

For **arbitrary text** (passwords, source code, random strings) and **proportional-width fonts**, the `mosaictext` package offers a **Depix-style reference-matching decoder** that does not assume language structure. It renders per-glyph references in candidate fonts, pixelates them with the target's block grid, and greedily matches block columns left-to-right:

```go
text, err := mosaictext.DecodeReference(ctx, img,
	mosaictext.WithRefCharset("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"),
	mosaictext.WithRefFont("Liberation Sans"), // pins a bundled font; omit to sweep all bundled fonts
)
if err != nil {
	panic(err)
}
fmt.Println(text)
```

On the command line, use `--decoder ref-match` to activate it:

```bash
unpixel --decoder ref-match image.png                                  # auto-detect font
unpixel --decoder ref-match --font "Liberation Sans" image.png         # pin the font
unpixel --decoder ref-match --font Arial.ttf image.png                 # supply a custom TTF/OTF
unpixel --decoder ref-match --charset "0-9A-Z" image.png               # narrow the charset
```

**API**: `mosaictext.DecodeReference(ctx, img, opts...)` with options `WithRefCharset` (default: all printable ASCII), `WithRefFont` (bundled font by name), `WithRefFontFile`/`WithRefFontFileBold` (caller-supplied TTF/OTF bytes), `WithRefLinear` (tri-state: auto/sRGB/linear-light block averaging).

**Why it matters:** Unlike the LM-guided decoder (which assumes character bigrams), reference-matching makes no language assumption, so it recovers **arbitrary secrets** (passwords, codes, random strings) exactly when the font is known. It also works on **proportional fonts** (not just monospace), expanding beyond code-like content. On self-consistent synthetic fixtures (e.g., "Pa55w0rd!" in Liberation Sans proportional, "X7kQ2mR9" in Liberation Mono), the decoder recovers the text exactly (distance near-zero).

**Font contract:** When you supply a font via `--font` (path to TTF/OTF) or `WithRefFontFile`, that font is used exclusively and the bundled sweep is skipped — this is the **primary mitigation** for real-world images. When no font is specified, the decoder sweeps all bundled fonts (Liberation, Carlito, Caladea, Source Code Pro, etc.) in both sRGB and linear-light modes, keeping the result with the lowest whole-image block distance.

**Key limitation:** Like all generate-and-test approaches, recovery is bounded by **font fidelity**. On real images where the exact font is not bundled (e.g., Notepad/Sublime screenshots), the bundled-sweep decode is incorrect. The exact-font path (`--font yourfont.ttf` or `WithRefFontFile`) is the technique's strength and is expected to recover redactions when the font is known.

### Grid-window beam decoder (proportional-font mosaic text recovery)

For **grid-aligned mosaic text** (not monospace-limited), the `mosaictext` package offers a **grid-window HMM beam decoder** that slides a window over pixelated grid cells and scores each candidate character by its per-window block MSE. This recovers **proportional-font mosaics** that the monospace `mono-hmm` cannot:

```go
text, err := mosaictext.DecodeWindowHMM(ctx, img,
	mosaictext.WithWHMMCharset("0123456789 "),  // candidate alphabet
	mosaictext.WithWHMMFont("Liberation Sans"), // or omit to sweep bundled fonts
)
if err != nil {
	panic(err)
}
fmt.Println(text)
```

On the command line, use `--decoder window-hmm` to activate it:

```bash
unpixel --decoder window-hmm image.png                          # auto-detect font
unpixel --decoder window-hmm --lang en --font Arial.ttf image.png  # supply a custom TTF/OTF
unpixel --decoder window-hmm --charset "0-9" image.png          # narrow the charset
```

**API**: `mosaictext.DecodeWindowHMM(ctx, img, opts...)` with options `WithWHMMCharset` (default: `"0123456789 "`), `WithWHMMFont` (bundled font by name), `WithWHMMFontFile`/`WithWHMMFontFileBold` (caller-supplied TTF/OTF bytes), `WithWHMMLinear` (tri-state: auto/sRGB/linear-light block averaging), `WithWHMMBeamWidth` (default: 16), `WithWHMMSeed` (optional RNG seed for reproducibility).

**Why it matters:** Proportional fonts have variable-width glyphs (unlike monospace), so the character-grid alignment changes per position. The window-HMM beam variant scores each grid cell window independently by MSE, allowing per-glyph recovery without assuming monospace structure. On synthetic grid-aligned fixtures (e.g., "hello world" rendered proportional then pixelated), the decoder recovers text exactly (distance near-zero).

**Design note:** This is the **grid-window beam variant**, not the learned-emission HMM (Hill et al. PETS-2016 full model with k-means + Viterbi); the latter requires blind column-anchored observations (a structural redesign). The beam search variant trades some optimality for robustness to font mismatch.

**Key limitation:** Like all approaches, recovery is bounded by **font fidelity**. On real images where the exact font is not bundled, the decode is inaccurate. The exact-font path (`--font yourfont.ttf` or `WithWHMMFontFile`) is the strength and is expected to recover proportional-font redactions when the font is known.

### Learned-emission Viterbi HMM decoder (constrained alphabets)

For **constrained character alphabets** (digits, PINs, check numbers), the `mosaictext` package offers a **learned-emission Viterbi HMM decoder** that trains on rendered examples to model per-window block observations and character-tuple state transitions. It then decodes the target via a single Viterbi pass over the block grid without assuming character boundaries — true column-anchored blind recovery:

```go
text, err := mosaictext.DecodeTrainedHMM(ctx, img,
	mosaictext.WithTHMMCharset("0123456789"),         // digits: PINs, credit cards, check numbers
	mosaictext.WithTHMMFont("Liberation Mono"),       // or omit to sweep bundled fonts
)
if err != nil {
	panic(err)
}
fmt.Println(text)
```

On the command line, use `--decoder trained-hmm` to activate it:

```bash
unpixel --decoder trained-hmm image.png                                  # auto-detect font
unpixel --decoder trained-hmm --charset "0-9" --font Arial.ttf image.png # with specific font
unpixel --decoder trained-hmm --charset "0-9A-Z" image.png               # custom charset (auto-font sweep)
```

**API**: `mosaictext.DecodeTrainedHMM(ctx, img, opts...)` with options `WithTHMMCharset` (default: digits), `WithTHMMFont` (bundled font by name), `WithTHMMFontFile`/`WithTHMMFontFileBold` (caller-supplied TTF/OTF bytes), `WithTHMMLinear` (tri-state: auto/sRGB/linear-light block averaging), `WithTHMMK` (KMeans clusters; default 128), `WithTHMMWindow` (window width in blocks; default auto), `WithTHMMCorpus` (training corpus size; default 2000), `WithTHMMSeed` (PRNG seed for reproducibility).

**Why it matters:** This is the genuine learned-emission HMM from Hill et al. (PETS-2016), with k-means quantized block observations and empirically-trained state transitions. Unlike beam search (which commits partial decisions), Viterbi finds the globally optimal state path, provided the model captures the true distribution.

**Honest scope & limitations:**
- **Recovers the constrained-alphabet case exactly** on self-consistent synthetic fixtures (digits/PINs rendered and re-pixelated at the same grid/offset). Achieves the paper's reference result (~100% on digit codes).
- **Per-window emission accuracy is modest (~55%)** — the k-means clustering of block windows loses fine structure — but global path optimization compensates when the true answer is structurally plausible.
- **Brittle to grid/render geometry mismatch**: The model is trained on a specific `(block size, font size, font face, block phase)` tuple. On independent test images with different geometry, even when the same font, accuracy drops sharply (paper's Fig-14 offset-sensitivity; observed <5% on paper-parity fixtures with different image sources).
- **Not a general decoder for real images.** Font fidelity and geometry drift dominate recovery success. On real captures, supply the exact font via `--font` and ensure block-size consistency. For out-of-sample geometry, `window-hmm` (beam) is currently more robust.
- **Best suited for:** digits, PINs, check numbers, or other highly constrained codes on self-consistent redactions. For proportional-font text or weak per-window signal, use `window-hmm` or the LM-guided `mono-hmm` instead.

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
| `InferBlockSize(image.Image) int` | Detect the mosaic block size (exact GCD of grid boundaries) |
| `InferBlockSizeRobust(image.Image) (blockSize int, support float64)` | Detect mosaic block size robustly (handles resampled/anti-aliased/JPEG'd grids); returns support confidence |
| `InferBlurSigma(image.Image) float64` | Estimate Gaussian blur radius σ from image contrast |
| `InferImpulseNoise(image.Image) float64` | Detect impulse (salt-pepper) noise; returned value used by `blind.Recover` to auto-denoise |
| `Renderer`, `Pixelator`, `Metric` (`PixelmatchFast` via `defaults.PixelmatchFastMetric()`), `Strategy` | Pluggable pipeline interfaces; `PixelmatchFast` skips anti-aliasing detection on block-average mosaic (identical results, ~35% faster), keeps faithful `Pixelmatch` on blur for cross-engine robustness |
| `Config`, `Style`, `Result`, `FontResult`, `Eval`, `Offset`, `Progress`, `EventKind` | Configuration and result/event types |
| **`blind.Recover(ctx, image.Image, ...Option) (*Recovery, error)`** | **Blind bilingual recovery (FR/EN) without knowing font/block/offset; re-exports `English`/`French`/`ParseLanguage`** |
| **`blind.With*` options (`WithLanguage`, `WithBlock`, `WithOffset`, `WithFontSize`, `WithLinear`, `WithFonts`, `WithMetric`)** | **Fine-tune blind recovery or override auto-detection** |
| **`mosaictext.Decode(ctx, image.Image, ...Option) (string, error)`** | **Zero-config monospace mosaic decoder (auto grid inference + character recognition)** |
| **`mosaictext.DecodeHMM(ctx, image.Image, ...Option) (string, error)`** | **LM-guided beam decoder for long monospace mosaic text; fuses bigram language model into search objective; polynomial in length, breaks charset^len barrier; font-limited on out-of-bundle typefaces** |
| **`mosaictext.With*` options (`WithLanguage`, `WithCharset`, `WithEmissionTemperature`, `WithFont`, `WithFontFile`, `WithFontFileBold`)** | **Configure DecodeHMM font/language/charset/tuning** |
| **`mosaictext.DecodeReference(ctx, image.Image, ...RefOption) (string, error)`** | **Reference-matching decoder: recovers arbitrary content (passwords, code, random strings) from mosaics via per-glyph reference matching; no language assumption; exact on self-consistent fixtures when font is known; works on proportional fonts** |
| **`mosaictext.WithRef*` options (`WithRefCharset`, `WithRefFont`, `WithRefFontFile`, `WithRefFontFileBold`, `WithRefLinear`)** | **Configure DecodeReference font/charset/color-space selection** |
| **`mosaictext.DecodeWindowHMM(ctx, image.Image, ...WHMMOption) (string, error)`** | **Grid-window beam decoder: recovers proportional-font mosaic text via per-cell window MSE scoring; no monospace assumption; exact on grid-aligned fixtures when font is known** |
| **`mosaictext.WithWHMM*` options (`WithWHMMCharset`, `WithWHMMFont`, `WithWHMMFontFile`, `WithWHMMFontFileBold`, `WithWHMMLinear`, `WithWHMMBeamWidth`, `WithWHMMSeed`)** | **Configure DecodeWindowHMM font/charset/color-space/beam tuning** |
| **`mosaictext.DecodeTrainedHMM(ctx, image.Image, ...THMMOption) (string, error)`** | **Learned-emission Viterbi HMM decoder: trains on rendered corpus, decodes via single column-anchored Viterbi pass; exact on self-consistent constrained alphabets (digits/PINs), brittle to geometry mismatch on real images** |
| **`mosaictext.WithTHMM*` options (`WithTHMMCharset`, `WithTHMMFont`, `WithTHMMFontFile`, `WithTHMMFontFileBold`, `WithTHMMLinear`, `WithTHMMK`, `WithTHMMWindow`, `WithTHMMCorpus`, `WithTHMMSeed`)** | **Configure DecodeTrainedHMM font/charset/KMeans-K/window-width/corpus-size/color-space** |

## Configuration

Pass a `Config` to `unpixel.New`. Every zero value falls back to a documented default.

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `Charset` | `string` | `"abcdefghijklmnopqrstuvwxyz "` | Candidate characters to search |
| `MaxLength` | `int` | `20` | Maximum plaintext length |
| `BlockSize` | `int` | `0` → auto / `8` | Pixelation block size; `≤0` auto-detects via `InferBlockSize` (or `InferBlockSizeRobust` for resampled/anti-aliased/JPEG'd mosaics), else falls back to `8` |
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

<details><summary>Real-world images: input normalization, known limitations and roadmap</summary>

UnPixel recovers synthetic mosaics and Gaussian blurs accurately with the right font and parameters.
Real internet images (screenshots, GIMP-rendered redactions, etc.) remain challenging. Zero-config
recovery succeeds on images where the font is in the bundled set and the redaction matches our
assumptions; it struggles when:
- **Font is out of bundle:** supplying `--font` / `--font-dir` with the exact typeface dramatically
  improves recovery (font fidelity dominates the score).
- **Long text with sparse signal:** text shorter than ~5 glyphs or very tall/thin glyphs (monospace,
  code) produce weak per-character image signals; generate-and-test explores an exponential space.
  A language model prior + beam search (enabled by `--language` or `unpixel --blind`) helps.
- **Blur vs mosaic ambiguity:** the auto-detector now correctly identifies real pixelated images
  (`InferBlockSizeRobust`), but miscalibrated parameters or unusual encodings (JPEG compression,
  resampling) can still confuse it.
- **Real blur is not Gaussian:** internet JPEG blur is often motion or defocus, not clean Gaussian,
  so the score stays above threshold and recovery returns no candidate.
- **Textured / vignette / dark backgrounds:** real captures often have a non-white background or
  dark-theme rendering that breaks the σ-search's clean-Gaussian-on-flat-white assumption. **Input
  normalization** (opt-in `--normalize` or `unpixel.WithNormalize(...)`) preprocesses the image via
  grayscale morphological background estimation, removing additive/multiplicative background and
  auto-inverting dark themes, so the redaction matches the model assumptions. Validated on synthetic
  fixtures with textured/vignette/dark/JPEG blocking; on real images it restores the assumption
  but **does not by itself recover the text** — font fidelity (P-B / `--font`) and deblurring (P-C)
  remain the blocking factors. **Design insight:** normalization is the lever, not deconvolution —
  sharpening the input (Wiener/blind L0 deconvolution) fights the generate-and-test loop, so P-C
  normalizes the input and feeds the existing σ-search unchanged. Wiener/blind L0 are deferred for
  future investigation.

**Multi-format input support:** the CLI now decodes JPEG, GIF, WebP, BMP, and TIFF in addition to
PNG (via `image` package registration), so real-world `.jpg`/`.jpeg`/`.webp` captures load cleanly.

**Roadmap** ([`PROGRESS.md`](PROGRESS.md) § P5–P7): phased pure-Go extensions — HMM/Viterbi mosaic
decoder (P-A, delivered), Depix-style reference-matching with self-synthesized fonts (P-B),
input normalization for blur recovery (P-C, delivered), and foundation work (P-D: robust auto-detect,
best-effort surfacing). All opt-in, benchmarked, zero regressions on synthetic corpus.

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
