# Limits — what UnPixel can and can't do

UnPixel is deliberately honest about its operating envelope. This page is the single
home for "what actually works."

## The short version

- **Synthetic redactions** (text redacted with a known font, then recovered): UnPixel
  recovers these **reliably and exactly**. The release panel is 17/17 exact.
- **Real-world internet images** (screenshots, GIMP exports, JPEG captures): **much
  harder.** Success depends mostly on whether you can match the exact font. Out of the
  box, real/wild redactions usually do **not** fully recover.

This isn't a temporary bug — it reflects genuine information-theoretic and rendering
limits. Treat UnPixel as a powerful tool for *demonstrating that pixelation/blur is
unsafe* and for recovering redactions when you control or know the rendering, not as a
guaranteed de-anonymizer for arbitrary internet images.

## What makes real images hard

### 1. Font fidelity (the dominant factor)
Generate-and-test needs candidates rendered the way the original was. UnPixel uses a
pure-Go rasterizer (`golang.org/x/image`), which is not byte-identical to Chromium,
GIMP, or whatever produced the redaction (different hinting/anti-aliasing). If the font
is out of the bundled set, the sweep won't match well.
**Mitigation:** supply the exact font via `--font` / `--font-dir`. See
[fonts & calibration](fonts-and-calibration.md).

### 2. Coarse blocks carry little information
Large block sizes over small fonts average away almost all per-character signal, so
many strings produce nearly identical blocks. Beyond a point this is information-
theoretically unrecoverable — there simply isn't enough left to distinguish candidates.

### 3. Boundary coupling
After pixelation, pixels straddling two glyphs average together. Decoders that score
glyphs in isolation (`did`, `ref-match`) mismatch the true full-line pixelation at
those boundaries — the main reason real "sick sentence" recovery stays incomplete even
when the DP/matching itself is correct.

### 4. Real blur isn't clean Gaussian
Internet blur is often motion or defocus, plus JPEG noise, on a textured or dark
background — not a clean Gaussian on flat white. The σ-search assumes the clean model.
**Mitigation:** `--normalize` restores the background assumption and `--remosaic`
collapses σ-mismatch/JPEG — but neither recovers the text by itself; font fidelity
still gates the result.

### 5. Geometry drift (learned models)
`trained-hmm` is trained on a specific (block size, font size, face, block phase)
tuple. On independent images with different geometry, accuracy drops sharply (the
paper's offset-sensitivity result). For out-of-sample geometry, the `window-hmm` beam
is more robust.

## The decoder reality, measured

The recovery [journal](../JOURNAL.md) tracks every decoder over real / wild / sick
corpora release-over-release. The honest current picture:

- Synthetic fixtures: 17/17 exact, fidelity 1.000.
- `ref-match` is the strongest on the SICK corpus (best exact count) but still partial.
- Calibration *nails the font* on context fixtures (distance ≈ 0), yet blind recovery
  of the redacted text from that calibration stays weak.
- Real / wild corpora remain flat — the walls above, not a tuning gap.

## When UnPixel works well

- You redacted the text yourself (or know the exact font, size, and block/σ).
- The content is short, or structured (digits/PINs with `trained-hmm`), or you can
  supply a language prior.
- You're proving a point: "this redaction is reversible."

## Going further

The roadmap for pushing the envelope (context-assisted decoding, learned emissions,
font reconstruction) lives in [`PROGRESS.md`](../../PROGRESS.md). The research foundations
are in [references](../reference/references.md).
