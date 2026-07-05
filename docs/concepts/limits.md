# Limits — the operating envelope

UnPixel is deliberately candid about its operating envelope. This page is the single
authoritative account of what is achievable.

## Summary

- **Synthetic redactions** (text redacted with a known font and subsequently recovered)
  are recovered **reliably and exactly.** The release panel stands at 17/17 exact.
- **Real-world images** (screenshots, GIMP exports, JPEG captures) do **not** recover in full
  by *blind per-character search* — at coarse block sizes each glyph spans only ~2-3 block
  columns, so per-character decoding is information-starved and no amount of search resolves it.
- **But real redactions ARE recoverable via propose-and-verify.** When a candidate string is
  *proposed* (by a human, a language model, or OSINT) and the geometry is calibrated (font,
  block, linear/sRGB, anisotropic `XScale`), `unpixel.Verify` now confirms it by whole-image
  physical re-pixelation with exhaustive alignment: on the real `hello-world.png` GIMP mosaic it
  scores the true "Hello World !" at distance **0.0000** (Match) and rejects wrong-shaped decoys.
  The recoverable path is therefore **generative proposer → whole-string physical verify →
  language prior as tie-breaker** (some substitutions such as `W`↔`N` are physically identical at
  coarse blocks and only the language model separates them). This is the intended
  LLM-propose/verify loop, and it works on real images.
- **Measured discrimination** (`mise run verifymeasure`): given calibrated config and hard
  confusable decoys, propose-and-verify ranks the truth #1 on **10/10** sick sentences/digits and
  **6/9** context secrets. The remaining misses are the **information-theoretic ceiling**: at
  correct rendering, high-entropy secrets pixelated at coarse blocks become *physical homoglyph
  ties* (e.g. `0`↔`O`) that whole-string scoring cannot separate and a global language prior
  breaks only inconsistently — resolving them needs learned per-character emissions (an ML tier).

This is not a transient defect; it reflects genuine information-theoretic and rendering
limits. UnPixel should be regarded as a powerful instrument for *demonstrating that
pixelation and blur are unsafe*, and for recovering redactions where the rendering is known
or controlled — not as a guaranteed de-anonymizer for arbitrary internet images.

## Why real images are difficult

### 1. Font fidelity (the dominant factor)
Generate-and-test requires candidates rendered as the original was rendered. UnPixel uses a
pure-Go rasterizer (`golang.org/x/image`), which is not byte-identical to Chromium, GIMP, or
whatever produced the redaction (owing to differing hinting and anti-aliasing). If the font
lies outside the bundled set, the sweep will not match well. **Mitigation:** supply the exact
font with `--font` or `--font-dir`. See [fonts & calibration](fonts-and-calibration.md).

The forward model also accounts for **anisotropic scaling** (`Style.XScale`): screenshots
produced by unequal x/y zoom (e.g. GIMP layer scale) stretch glyph ink horizontally in a way
inter-glyph spacing cannot express. With the right font, block-average mode, and XScale, the
direct model can reproduce a real redaction *exactly* — on `testdata/real/hello-world.png` it
reaches pixelmatch **0.0000** (vs 0.0972 without the stretch). Crucially, when the model matches
this well, **font fidelity is no longer the wall**: what remains is *alignment and scoring*. The
whole-string `unpixel.Verify` path resolves this — cropping to the redaction band
(`unpixel.WithCrop`) plus exhaustive sub-block alignment (phase sweep + coarse-to-fine position
refinement in `verifyCore`) now confirms the **entire** "Hello World !" line at **0.0000**, not
just the first cell, and this recovery is drivable end-to-end from an MCP client via
`unpixel_verify_candidates` with hints from analyze / rank_fonts / calibrate. So the dominant
obstacle for real images is image-dependent: model fidelity for unknown fonts (blind decode),
resolved by propose-and-verify when a candidate and its calibration are available.

### 2. Coarse blocks carry little information
Large block sizes applied to small fonts average away nearly all per-character signal, so
many strings produce nearly identical blocks. Beyond a certain point this is
information-theoretically unrecoverable: insufficient information remains to distinguish
candidates.

This wall is **measured, not assumed**. The `internal/infoleak` study (`mise run infoleak`,
results in [`JOURNAL.md` §#8](../JOURNAL.md)) quantified the two channels most often proposed
as ways around it, and both add nothing exploitable for a block-average mosaic:

- **Anti-aliasing:** sub-pixel AA coverage *survives* averaging but, measured across the 9
  bundled fonts, the separability gain on confusable pairs (`rn`/`m`, `0`/`O`, …) is
  **negative** — AA softens edges, making confusables *more* alike after block-averaging.
- **JPEG compression:** adds only signal-dependent noise to the *already-known* block values
  (drift grows as quality drops); it does not reconstruct averaged-away detail.

The only genuine super-resolution lever is **multiple grid phases** of the same content
(`mosaictext.DecodeMultiFrame`, IBP fusion). Otherwise the sole recoverable signal is the
block values themselves, which the generate-and-test engine already exploits.

### 3. Boundary coupling
After pixelation, pixels straddling two glyphs average together. Decoders that score glyphs
in isolation (`did`, `ref-match`) therefore diverge from the true full-line pixelation at
those boundaries — the principal reason that recovery of the "sick sentence" corpus remains
incomplete even when the DP or matching is itself correct.

### 4. Real blur is not a clean Gaussian
Internet blur is frequently motion or defocus blur, accompanied by JPEG noise, on a textured
or dark background — not a clean Gaussian on flat white. The σ-search assumes the clean
model. **Mitigation:** `--normalize` restores the background assumption and `--remosaic`
reduces σ-mismatch and JPEG artefacts; neither recovers the text on its own, as font fidelity
still governs the result.

### 5. Geometry drift (learned models)
`trained-hmm` is trained on a specific tuple of (block size, font size, face, block phase). On
independent images with differing geometry, accuracy declines sharply (the paper's
offset-sensitivity result). For out-of-sample geometry, the `window-hmm` beam is more robust.

Grid *detection* itself is now robust to non-zero grid phase and proportional (non-monospace)
blocks: `InferBlockGrid` previously returned no grid on offset mosaics such as
`testdata/real/marx.png` (19 px block at offset (5,5)), and elsewhere locked onto a
sub-harmonic of the true period. Both are fixed (a sub-harmonic guard that only upgrades the
period when the robust autocorrelation does not already support the candidate). The per-stage
`mise run geomeasure` diagnostic ([`GEOMETRY.md`](../GEOMETRY.md)) confirms geometry is no
longer the first failing stage on the real corpus — it isolates whichever stage
(localize → grid → segment → font) actually breaks per image.

## The measured decoder picture

The recovery [journal](../JOURNAL.md) tracks every decoder across the real, wild, and sick
corpora, release by release. The candid current position is as follows:

- Synthetic fixtures: 17/17 exact, fidelity 1.000.
- `ref-match` is the strongest on the SICK corpus (highest exact count) but remains partial.
- Calibration recovers the font precisely on context fixtures (distance ≈ 0), yet blind
  recovery of the redacted text from that calibration remains weak.
- The real and wild corpora remain flat — a consequence of the limits above, not of a tuning
  deficiency.

## When UnPixel performs well

- The text was redacted by the user, or the exact font, size, and block/σ are known.
- The content is short, or structured (digits and PINs with `trained-hmm`), or a language
  prior can be supplied.
- The objective is demonstrative: establishing that a given redaction is reversible.

## Further reading

The roadmap for extending the envelope (context-assisted decoding, learned emissions, font
reconstruction) is maintained in [`PROGRESS.md`](../../PROGRESS.md). The research foundations
are listed in [references](../reference/references.md).
