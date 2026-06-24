# Fonts & calibration

**The font is the most important input.** [Generate-and-test](how-it-works.md) only
works if UnPixel's rendered candidates are drawn the same way the original text was. If
the typeface, size, or spacing is off, every candidate's blocks are slightly wrong and
nothing matches cleanly. On real-world images, **getting the font right is the single
biggest lever on success.**

## How UnPixel gets the font

### 1. Zero-config font sweep (no `--font`)

With no font specified, UnPixel renders candidates in a bundled set of redistributable
fonts **in parallel** and keeps the best fit by whole-image score:

- Liberation Sans / Serif / Mono (≈ Arial / Times / Courier)
- Carlito (≈ Calibri), Caladea (≈ Cambria)
- Source Code Pro, JetBrains Mono, Adwaita Mono, Noto Sans Mono (for code)

This is convenient but **exploratory** — if the real font isn't close to a bundled
one, the sweep won't match well.

### 2. Supply the exact font (recommended for real images)

```bash
unpixel --font Consolas.ttf --font-size 24 --letter-spacing -0.2 redacted.png
```

Or sweep your own candidates / a directory and keep the best:

```bash
unpixel --font Arial.ttf --font Consolas.ttf redacted.png
unpixel --font-dir /usr/share/fonts/truetype redacted.png
```

In Go, build one renderer per font and rank them with `RecoverMultiFont` — results
come back best-fit first.

## Calibrating size and spacing

Even with the right typeface, the **size** and **letter-spacing** must match:

- **Font size** is calibrated from the content height when unset (`InferFontSize`), or
  pin it with `--font-size`.
- **Letter-spacing** (extra pixels after each glyph, like CSS) can be swept with
  `--letter-spacing-search` (the Bishop-Fox method); the winner is recorded in
  `Result.LetterSpacing`.
- **Gamma / color space** matters for block averaging: `--gamma auto` tries both
  sRGB and linear-light (GIMP/GEGL render in linear light) and keeps the lower
  distance.

## Reconstructing the font from context

Sometimes you don't have the font file, but the image gives you clues. UnPixel has two
context-assisted approaches:

- **Variable-font fitting** (`--decoder varfont`): if the redaction was made with a
  variable font, UnPixel fits the font's axes (e.g. weight) to match the redacted
  pixels, rather than guessing among fixed faces.
- **Calibrate from visible text:** when there is *un-redacted* text next to the
  redaction (same font), UnPixel can calibrate the rendering parameters against that
  visible sample, then apply them to the hidden region. A font sample supplied as a
  separate image is also supported (`--font-sample`).

These are promising on synthetic fixtures (calibration nails the font, distance ≈ 0)
but blind recovery of the redacted text itself remains hard — see [limits](limits.md).

## Rule of thumb

1. If you know the font, **always pass `--font`**.
2. Calibrate size/spacing/gamma if the first pass looks close but not exact.
3. Only rely on the zero-config sweep for quick triage or bundled-font synthetics.

## See also

- [Decoders](decoders.md) — `varfont` and the calibration modes.
- [Limits](limits.md) — why font fidelity caps real-world recovery.
- [CLI reference](../reference/cli.md) — all `--font*`, `--gamma`, spacing flags.
