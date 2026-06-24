# Getting started

## Installation

**As a command-line tool:**

```bash
go install github.com/oioio-space/unpixel/cmd/unpixel@latest
```

**As a Go library:**

```bash
go get github.com/oioio-space/unpixel
```

**From source** (for development), using [mise](https://mise.jdx.dev) for the toolchain:

```bash
git clone https://github.com/oioio-space/unpixel.git
cd unpixel
mise run setup     # install pinned tools and configure git hooks
mise run test      # verify the build
mise run           # list all tasks
```

Go 1.26 or later is required.

## Common cases

### 1. Recovery without configuration

Provide the image and allow UnPixel to detect the block size, font, and language:

```bash
unpixel redacted.png
```

A PNG may be read from standard input, and ranked alternatives may be requested as JSON:

```bash
cat redacted.png | unpixel -
unpixel --format json --top 10 redacted.png
```

### 2. Supplying the font (recommended for real-world images)

Font fidelity dominates recovery quality. When the typeface is known, it should be
supplied; this is the most effective single improvement available on real images:

```bash
unpixel --font Consolas.ttf --font-size 24 --letter-spacing -0.2 -b 5 redacted.png
```

Several candidate fonts — or an entire directory — may also be swept, with the
best-fitting result retained:

```bash
unpixel --font Arial.ttf --font Consolas.ttf --font Courier.ttf -b 5 redacted.png
unpixel --font-dir /usr/share/fonts/truetype -b 5 redacted.png
```

This subject is treated in detail in [fonts & calibration](concepts/fonts-and-calibration.md).

### 3. Recovering blurred text

Blur is likewise reversible. UnPixel estimates the blur magnitude (σ) automatically:

```bash
unpixel --redaction blur redacted.png
unpixel --normalize --redaction blur real-blur.jpg   # normalize a noisy or dark capture first
```

See [mosaic vs. blur](concepts/mosaic-vs-blur.md).

### 4. Blind, bilingual recovery (experimental)

When the font, block size, and language are unknown, the blind pipeline detects them and
scores candidates with a French or English dictionary prior:

```bash
unpixel --blind --lang fr testdata/real/marx.png
unpixel --blind --lang en --block-size 8 image.png
```

### 5. Selecting a specialized decoder

For long text, arbitrary secrets, proportional fonts, or numeric codes, a dedicated
decoder frequently outperforms the default. The [decoders guide](concepts/decoders.md)
provides a selection table by content type; for example:

```bash
unpixel --decoder mono-hmm --lang en image.png      # long monospace text
unpixel --decoder ref-match --font Arial.ttf pw.png # passwords and arbitrary content
unpixel --decoder did image.png                     # short proportional text
```

## Use from Go

The single-call helpers detect every parameter they can:

```go
res, err := unpixel.RecoverFile(ctx, "redacted.png")  // or Recover(ctx, img) / RecoverReader(ctx, r)
fmt.Println(res.BestGuess)
```

The complete API, options, and `Config` fields are documented in the
[API reference](reference/api.md).

## Further reading

- The complete flag table: [CLI reference](reference/cli.md).
- The operating envelope: [limits](concepts/limits.md).
- Contribution guidelines and the build/test gates: [`CLAUDE.md`](../CLAUDE.md).
