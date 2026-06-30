# Fonts & calibration

**The font is the most important input.** [Generate-and-test](how-it-works.md) succeeds
only when UnPixel's rendered candidates are drawn as the original text was drawn. If the
typeface, size, or spacing is incorrect, every candidate's blocks are slightly displaced
and none matches cleanly. On real-world images, **obtaining the correct font is the single
most significant determinant of success.**

## How UnPixel obtains the font

### 1. Automatic font sweep (no `--font`)

When no font is specified, UnPixel renders candidates in a bundled set of redistributable
fonts **in parallel** and retains the best fit by whole-image score:

- Liberation Sans / Serif / Mono (≈ Arial / Times / Courier)
- Carlito (≈ Calibri), Caladea (≈ Cambria)
- Source Code Pro, JetBrains Mono, Adwaita Mono, Noto Sans Mono (for code)

This is convenient but **exploratory**: if the true font is not close to a bundled one,
the sweep will not match well.

`--font-prior` orders this sweep by a blind pixelated-signature prior so the likeliest
bundled font is tried first (result unchanged, just faster); `--font-prior-top-k N` prunes
the sweep to the N best-ranked fonts (faster still, but a too-small N can drop the true
font). The prior ranks only the bundled set — it does not widen it.

### 2. Supplying the exact font (recommended for real images)

```bash
unpixel --font Consolas.ttf --font-size 24 --letter-spacing -0.2 redacted.png
```

Custom candidates or an entire directory may be swept, retaining the best fit:

```bash
unpixel --font Arial.ttf --font Consolas.ttf redacted.png
unpixel --font-dir /usr/share/fonts/truetype redacted.png
```

In Go, one renderer is constructed per font and the candidates are ranked with
`RecoverMultiFont`, which returns results best-fit first.

## Calibrating size and spacing

Even with the correct typeface, the **size** and **letter-spacing** must match:

- The **font size** is calibrated from the content height when unset (`InferFontSize`), or
  fixed with `--font-size`.
- **Letter-spacing** (additional pixels following each glyph, as in CSS) may be swept with
  `--letter-spacing-search` (the Bishop-Fox method); the selected value is recorded in
  `Result.LetterSpacing`.
- **Gamma / colour space** affects block averaging: `--gamma auto` evaluates both sRGB and
  linear light (GIMP and GEGL render in linear light) and retains the lower distance.

## Reconstructing the font from context

When the font file is unavailable, the image may nonetheless provide cues. UnPixel offers
two context-assisted approaches:

- **Variable-font fitting** (`--decoder varfont`): when the redaction was produced with a
  variable font, UnPixel fits the font's axes (for example, weight) to match the redacted
  pixels, rather than selecting among fixed faces.
- **Calibration from visible text:** when un-redacted text in the same font appears
  adjacent to the redaction, UnPixel can calibrate the rendering parameters against that
  visible sample and then apply them to the concealed region. A font sample supplied as a
  separate image is also supported (`--font-sample`).

These approaches are promising on synthetic fixtures — calibration recovers the font
parameters precisely, with distance approaching zero — but blind recovery of the redacted
text itself remains difficult; see [limits](limits.md).

## Recommended practice

1. When the font is known, **always supply `--font`.**
2. Calibrate size, spacing, and gamma when the first pass is close but not exact.
3. Rely on the automatic sweep only for rapid triage or for bundled-font synthetics.

## See also

- [Decoders](decoders.md) — `varfont` and the calibration modes.
- [Limits](limits.md) — why font fidelity bounds real-world recovery.
- [CLI reference](../reference/cli.md) — all `--font*`, `--gamma`, and spacing flags.
