# UnPixel

**Recover text hidden behind pixelation (mosaic) or blur.**

Redacting text by pixelating or blurring it does **not** make it secret. Both are
reversible. UnPixel reconstructs the hidden text — it's a pure-Go port of
[Bishop Fox's **unredacter**](https://github.com/bishopfox/unredacter)
([why pixelation fails](https://bishopfox.com/blog/unredacter-tool-never-pixelation)).

[![CI](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml/badge.svg)](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/oioio-space/unpixel.svg)](https://pkg.go.dev/github.com/oioio-space/unpixel) [![Go Report Card](https://goreportcard.com/badge/github.com/oioio-space/unpixel)](https://goreportcard.com/report/github.com/oioio-space/unpixel) [![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat)](https://go.dev/dl/) [![License GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

---

## What it does

Give UnPixel an image with a pixelated or blurred line of text. It figures out the
redaction settings on its own (block size, blur amount, font, language) and prints
its best guess at the original text.

It works by **re-creating** the redaction, not by sharpening the image: it renders
candidate text, blurs/pixelates it the same way, and keeps whatever reproduces the
redacted pixels exactly. [How it works →](docs/concepts/how-it-works.md)

## Install

Command-line tool:

```bash
go install github.com/oioio-space/unpixel/cmd/unpixel@latest
```

Go library:

```bash
go get github.com/oioio-space/unpixel
```

Requires Go 1.26+. (Building from source? See [getting started](docs/getting-started.md).)

## Try it

**1. Just point it at an image** — UnPixel auto-detects everything:

```bash
unpixel redacted.png
```

**2. Know the font?** Supplying the exact font dramatically improves real-world results:

```bash
unpixel --font Consolas.ttf --font-size 24 redacted.png
```

**3. Blurred instead of pixelated?**

```bash
unpixel --redaction blur redacted.png
```

The best guess prints to stdout (so it pipes cleanly); ranked alternatives and live
progress go to stderr. Add `--format json` for machine-readable output.

In Go, it's one call:

```go
import (
	"context"
	"fmt"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wires the default pipeline
)

res, _ := unpixel.RecoverFile(context.Background(), "redacted.png")
fmt.Println(res.BestGuess)
```

## How well does it work?

UnPixel reliably recovers **synthetic** redactions (text redacted with a known font,
then recovered). On **real-world internet images** it is much harder — success
depends mostly on matching the exact font. Supplying `--font` is the single biggest
lever. UnPixel is honest about this: read the [limits](docs/concepts/limits.md)
before relying on it.

## Learn more

| I want to… | Go here |
|------------|---------|
| Install and run common cases | [Getting started](docs/getting-started.md) |
| Understand how it works | [How it works](docs/concepts/how-it-works.md) |
| Know mosaic vs. blur recovery | [Mosaic vs. blur](docs/concepts/mosaic-vs-blur.md) |
| Get the font right | [Fonts & calibration](docs/concepts/fonts-and-calibration.md) |
| Pick the right decoder | [Decoders](docs/concepts/decoders.md) |
| Understand what it can't do | [Limits](docs/concepts/limits.md) |
| Look up a CLI flag | [CLI reference](docs/reference/cli.md) |
| Use the Go API | [API reference](docs/reference/api.md) |
| See the full doc map | [docs/](docs/README.md) |

For the project's roadmap and decision log, see [`PROGRESS.md`](PROGRESS.md). For how
UnPixel compares to the original Bishop Fox tool, see [comparison](docs/comparison.md).

## Credits

- **Original work:** [Bishop Fox's unredacter](https://github.com/bishopfox/unredacter)
  and the write-up [*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).
- **Fonts & libraries:** bundled Liberation/Carlito/Caladea/Source Code Pro/JetBrains
  Mono (SIL OFL / Apache 2.0), [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image),
  and [`orisano/pixelmatch`](https://github.com/orisano/pixelmatch). Full list and
  research references: [docs/reference/references.md](docs/reference/references.md).

## License

**GPL-3.0-or-later** — see [`LICENSE`](LICENSE). UnPixel is a derivative work of Bishop
Fox's unredacter (GPL-3.0); the copyleft license is preserved. © the UnPixel authors;
original © Bishop Fox.
