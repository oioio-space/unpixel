# UnPixel

**Recovery of text concealed by pixelation (mosaic) or blur.**

Redacting text by pixelating or blurring it does not render it confidential; both
transformations are reversible. UnPixel reconstructs the concealed text. It is a pure-Go
port of [Bishop Fox's **unredacter**](https://github.com/bishopfox/unredacter)
(see [why pixelation is inadequate](https://bishopfox.com/blog/unredacter-tool-never-pixelation)).

[![CI](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml/badge.svg)](https://github.com/oioio-space/unpixel/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/oioio-space/unpixel.svg)](https://pkg.go.dev/github.com/oioio-space/unpixel) [![Go Report Card](https://goreportcard.com/badge/github.com/oioio-space/unpixel)](https://goreportcard.com/report/github.com/oioio-space/unpixel) [![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat)](https://go.dev/dl/) [![License GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue)](LICENSE)

---

## Overview

Given an image containing a pixelated or blurred line of text, UnPixel determines the
redaction parameters automatically — block size, blur magnitude, font, and language —
and reports its best estimate of the original text.

It operates by **reconstructing** the redaction rather than by sharpening the image: it
renders candidate text, applies the same blur or pixelation, and retains whichever
candidate reproduces the redacted pixels exactly. See
[How it works](docs/concepts/how-it-works.md) for the method in detail.

## Installation

Command-line tool:

```bash
go install github.com/oioio-space/unpixel/cmd/unpixel@latest
```

Go library:

```bash
go get github.com/oioio-space/unpixel
```

Go 1.26 or later is required. For building from source, see the
[getting-started guide](docs/getting-started.md).

## Usage

**1. Automatic recovery** — UnPixel detects all parameters from the image:

```bash
unpixel redacted.png
```

**2. With a known font.** Supplying the exact font substantially improves results on
real-world images:

```bash
unpixel --font Consolas.ttf --font-size 24 redacted.png
```

**3. Blur instead of pixelation:**

```bash
unpixel --redaction blur redacted.png
```

**4. Decoder ensemble** — run multiple decoders and select by exact image-distance (no-regression guarantee):

```bash
unpixel --decoder ensemble redacted.png
```

**5. Multi-frame decode** — combine sub-pixel-jittered frames of the same redaction:

```bash
unpixel --frame frame1.png --frame frame2.png --frame frame3.png redacted.png
```

**6. DID context-aware emission** — fix boundary blocks by rendering glyphs with neighbours:

```bash
unpixel --decoder did --did-context redacted.png
```

The best estimate is written to standard output (so that it pipes cleanly); ranked
alternatives and progress information are written to standard error. Add `--format json`
for machine-readable output.

In Go, recovery is a single call:

```go
import (
	"context"
	"fmt"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/mosaictext"
	_ "github.com/oioio-space/unpixel/defaults" // wires the default pipeline
)

// Single-frame:
res, _ := unpixel.RecoverFile(context.Background(), "redacted.png")
fmt.Println(res.BestGuess)

// Decoder ensemble (multi-decoder with exact re-score):
ens, _ := mosaictext.DecodeEnsemble(ctx, img)
fmt.Println(ens.BestGuess)

// Multi-frame decode (requires sub-pixel-jittered frames):
multi, _ := mosaictext.DecodeMultiFrame(ctx, []image.Image{frame1, frame2, frame3})
fmt.Println(multi.BestGuess)

// DID with context-aware emission:
did, _ := mosaictext.DecodeDID(ctx, img, mosaictext.WithDIDContext(true))
fmt.Println(did.BestGuess)
```

## Real-world / zero-config usage

For photos taken at angles, with undetected colorspace, or when a known prefix is present, use these opt-in flags:

**Auto-recovery flags:**

```bash
unpixel --auto redacted.png                           # auto-crop + auto-colorspace + auto-calibrate
unpixel --auto-crop redacted.png                      # align to mosaic grid boundaries
unpixel --auto-colorspace redacted.png                # detect sRGB vs linear-light pixelation
unpixel --auto-calibrate redacted.png                 # infer font size and x-stretch
unpixel --rectify redacted.png                        # decode photos taken at an angle
```

**Constrained recovery** (when part of the text is known):

```bash
unpixel --prefix "https://" redacted.png              # lock first N characters
unpixel --prefix "admin" --visible-region redacted.png  # known prefix + calibrate from visible text
```

**Calibration from visible text** (when the image contains both clear and redacted text in the same font):

```bash
unpixel --visible-text "Username:" --visible-region "50,50,150,70" redacted.png  # text + bbox
```

**Calibration from a separate font sample** (when a clean sample of the target font exists elsewhere):

```bash
unpixel --font-sample sample.png --font-sample-text "The quick brown fox" redacted.png
```

In Go, pass options to `unpixel.Recover`:

```go
res, _ := unpixel.Recover(ctx, img,
  unpixel.WithAuto(),                                 // enables auto-crop + auto-colorspace + auto-calibrate
  unpixel.WithPrefix("https://"),                     // constrain to known prefix
  unpixel.WithAutoCalibrate(),                        // infer grid phase and x-stretch
)
```

**Caveat:** `--auto*` flags and multi-decoder options (`--decoder ensemble`, `--frame`, `--did-context`) target real captures and boundary cases. Synthetic fixtures in the test panel already decode without them (panel remains 17/17 unchanged); their value lies in zero-config real-world use and tackling edge cases (JPEG boundaries, sub-pixel jitter, context-dependent pixelation).

## Effectiveness

UnPixel recovers **synthetic** redactions reliably (text redacted with a known font and
subsequently recovered). On **real-world images**, recovery is considerably more
difficult; success depends primarily on matching the exact font, and supplying `--font`
is the single most significant factor. The [limitations](docs/concepts/limits.md) page
documents the operating envelope candidly and should be consulted before relying on the
tool.

## Documentation

| Objective | Reference |
|-----------|-----------|
| Install and run the common cases | [Getting started](docs/getting-started.md) |
| Understand the method | [How it works](docs/concepts/how-it-works.md) |
| Recover mosaic versus blur | [Mosaic vs. blur](docs/concepts/mosaic-vs-blur.md) |
| Configure the font | [Fonts & calibration](docs/concepts/fonts-and-calibration.md) |
| Select a decoder | [Decoders](docs/concepts/decoders.md) |
| Decode a photo taken at an angle | [Decoders → `--rectify`](docs/concepts/decoders.md) |
| Review the limitations | [Limits](docs/concepts/limits.md) |
| Look up a CLI flag | [CLI reference](docs/reference/cli.md) |
| Use the Go API | [API reference](docs/reference/api.md) |
| Browse the full documentation | [docs/](docs/README.md) |

The project roadmap and decision log are maintained in [`PROGRESS.md`](PROGRESS.md). A
comparison with the original Bishop Fox tool is provided in
[comparison](docs/comparison.md).

## Credits

- **Original work:** [Bishop Fox's unredacter](https://github.com/bishopfox/unredacter)
  and the article [*Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).
- **Fonts and libraries:** the bundled Liberation, Carlito, Caladea, Source Code Pro,
  and JetBrains Mono families (SIL OFL / Apache 2.0),
  [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image), and
  [`orisano/pixelmatch`](https://github.com/orisano/pixelmatch). The complete list and
  research references appear in
  [docs/reference/references.md](docs/reference/references.md).

## License

**GPL-3.0-or-later** — see [`LICENSE`](LICENSE). UnPixel is a derivative work of Bishop
Fox's unredacter (GPL-3.0); the copyleft license is preserved. © the UnPixel authors;
original © Bishop Fox.
