# Getting started

## Install

**As a command-line tool:**

```bash
go install github.com/oioio-space/unpixel/cmd/unpixel@latest
```

**As a Go library:**

```bash
go get github.com/oioio-space/unpixel
```

**From source** (for development) — uses [mise](https://mise.jdx.dev) for the toolchain:

```bash
git clone https://github.com/oioio-space/unpixel.git
cd unpixel
mise run setup     # install pinned tools + wire git hooks
mise run test      # verify the build
mise run           # list all tasks
```

Requires Go 1.26+.

## The common cases

### 1. Recover with zero configuration

Point UnPixel at the image and let it detect the block size, font, and language:

```bash
unpixel redacted.png
```

Read a PNG from stdin, or ask for ranked alternatives as JSON:

```bash
cat redacted.png | unpixel -
unpixel --format json --top 10 redacted.png
```

### 2. Supply the font (best real-world results)

Font fidelity dominates recovery quality. If you know the typeface, pass it — this is
the single biggest improvement you can make on real images:

```bash
unpixel --font Consolas.ttf --font-size 24 --letter-spacing -0.2 -b 5 redacted.png
```

You can also sweep several candidate fonts (or a whole directory) and keep the best
fit:

```bash
unpixel --font Arial.ttf --font Consolas.ttf --font Courier.ttf -b 5 redacted.png
unpixel --font-dir /usr/share/fonts/truetype -b 5 redacted.png
```

More on this in [fonts & calibration](concepts/fonts-and-calibration.md).

### 3. Recover blurred text

Blur is reversible too. UnPixel auto-estimates the blur amount (σ):

```bash
unpixel --redaction blur redacted.png
unpixel --normalize --redaction blur real-blur.jpg   # normalize a noisy/dark capture first
```

See [mosaic vs. blur](concepts/mosaic-vs-blur.md).

### 4. Blind, bilingual recovery (experimental)

When you don't know the font, block size, or language, the blind path detects them
and scores candidates with a French or English dictionary prior:

```bash
unpixel --blind --lang fr testdata/real/marx.png
unpixel --blind --lang en --block-size 8 image.png
```

### 5. Pick a specialized decoder

For long text, arbitrary secrets, proportional fonts, or digit codes, a dedicated
decoder often beats the default. The [decoders guide](concepts/decoders.md) has a
"which one do I use?" table; for example:

```bash
unpixel --decoder mono-hmm --lang en image.png      # long monospace text
unpixel --decoder ref-match --font Arial.ttf pw.png # passwords / arbitrary content
unpixel --decoder did image.png                     # short proportional text
```

## Using it from Go

The one-call helpers auto-detect everything they can:

```go
res, err := unpixel.RecoverFile(ctx, "redacted.png")  // or Recover(ctx, img) / RecoverReader(ctx, r)
fmt.Println(res.BestGuess)
```

Full API, options, and `Config` fields: [API reference](reference/api.md).

## Next steps

- Every flag, in one table: [CLI reference](reference/cli.md).
- What works and what doesn't: [limits](concepts/limits.md).
- Contributing and the build/test gates: [`CLAUDE.md`](../CLAUDE.md).
