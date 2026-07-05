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

**2b. Blind font prior** — when the font is unknown, a pixelated-signature prior ranks
the 9 bundled fonts, so the likeliest font is tried first (faster):

```bash
unpixel --font-prior redacted.png              # order fonts by blind prior
unpixel --font-prior --font-prior-top-k 3 redacted.png  # decode only top-3 (even faster, but riskier)
```

**3. Blur instead of pixelation:**

```bash
unpixel --redaction blur redacted.png
```

**4. Decoder ensemble** — run multiple decoders and select by exact image-distance (no-regression guarantee):

```bash
unpixel --decoder ensemble redacted.png
```

**5. Multi-frame decode** — combine sub-pixel-jittered frames of the same redaction. When frame offsets are unknown, they are auto-detected per-frame (luma-variance grid-phase detection):

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
unpixel --calibrate-geometry --visible-text "Login" --visible-region "10,10,80,30" redacted.png  # recover font size + x-stretch
```

**Calibration from a separate font sample** (when a clean sample of the target font exists elsewhere):

```bash
unpixel --font-sample sample.png --font-sample-text "The quick brown fox" redacted.png
```

**Geometry calibration** (recover exact font size and horizontal stretch before decoding):

```bash
unpixel --calibrate-geometry --visible-text "Sample text" --visible-region "x,y,w,h" redacted.png  # requires a sharp visible crop with enough text
```

*Caveat:* `--calibrate-geometry` needs an ink-tight visible crop with sufficient text width (large white margins or very short text degrade the fit).

In Go, pass options to `unpixel.Recover`:

```go
res, _ := unpixel.Recover(ctx, img,
  unpixel.WithAuto(),                                 // enables auto-crop + auto-colorspace + auto-calibrate
  unpixel.WithPrefix("https://"),                     // constrain to known prefix
  unpixel.WithAutoCalibrate(),                        // infer grid phase and x-stretch
)
```

**Caveat:** `--auto*` flags and multi-decoder options (`--decoder ensemble`, `--frame`, `--did-context`) target real captures and boundary cases. Synthetic fixtures in the test panel already decode without them (panel remains 17/17 unchanged); their value lies in zero-config real-world use and tackling edge cases (JPEG boundaries, sub-pixel jitter, context-dependent pixelation).

## MCP server (LLM integration)

UnPixel ships an [MCP](https://modelcontextprotocol.io) server (`cmd/unpixel-mcp`, pure Go)
that exposes the engine as an agent-callable toolbox so an LLM can drive recovery
conversationally — inspect an image, pick a decoder, score its own candidate guesses, and
render results for visual comparison.

```bash
go install github.com/oioio-space/unpixel/cmd/unpixel-mcp@latest
unpixel-mcp   # speaks MCP over stdio; point your MCP client at it
```

Tools: `unpixel_analyze` (inspect → recommend decoder/quad), `unpixel_decode` (13 methods
behind one `method` enum; async for long runs; `multi-frame` auto-detects per-frame phase when offsets are 0), `unpixel_verify_candidates` (LLM proposes
strings, UnPixel scores them by physical re-pixelation; accepts `rerank_weight` to blend
a language prior into candidate ranking, with 0 = physical order and `Pick` staying a pure physical match;
for a REAL redaction, pass the physical-calibration hints — `crop` (analyze's `redaction_bbox`), `font`
(from `unpixel_rank_fonts`), `linear_light`, `font_size`/`x_scale` — to recover it end-to-end, as
`unpixel_analyze` → `unpixel_rank_fonts` → propose → verify),
`unpixel_verify_image` (physics-verifies a restored image against a redaction by re-applying the forward
operator; anti-hallucination gate for external diffusion restorers; see library entry `unpixel.VerifyImage` and
`docs/sidecar-protocol.md` for the restorer contract),
`unpixel_render`, `unpixel_rank_fonts`
(now supports blind histogram ranking without `known_text`), `unpixel_calibrate`; resources 
`unpixel://{fonts,charsets,methods,operating-envelope}`. Custom fonts upload via 
`font_path`/`font_base64`. `unpixel_decode` accepts `font_prior_top_k` to run a blind font-prior 
sweep (orders the bundled-font search by pixelated-signature ranking) and `expected_format` 
(`digits|credit_card|iban|date|phone_fr|phone_us|phone_e164`) to prune the engine search to a 
structured secret (Luhn/mod-97/date/phone-validated). See [docs](docs/) for the full schema.

## Effectiveness

UnPixel recovers **synthetic** redactions reliably (text redacted with a known font and
subsequently recovered). Blind per-character recovery of **real-world images** is
considerably more difficult; success depends primarily on matching the exact font, and
supplying `--font` is the single most significant factor. But real redactions **are**
recoverable by **propose-and-verify**: given a candidate string and calibration
(font, block, colourspace, crop), `unpixel.Verify` / `unpixel_verify_candidates` confirms
it by whole-string physical re-pixelation — it recovers the real `hello-world.png` GIMP
mosaic at distance 0.0000 and ranks the truth #1 on 10/10 sick and 6/9 context secrets
(`mise run verifymeasure`). The [limitations](docs/concepts/limits.md) page documents the
operating envelope candidly and should be consulted before relying on the tool.

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
