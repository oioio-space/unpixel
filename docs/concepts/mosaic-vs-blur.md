# Mosaic vs. blur

UnPixel handles the two common ways people redact text. Both are reversible because
both are deterministic functions of the original pixels — the
[generate-and-test](how-it-works.md) attack applies to each.

## Mosaic (pixelation)

Mosaic redaction divides the region into a grid and replaces each block with its
average color. The two things UnPixel needs are the **block size** and the **grid
offset** (where the blocks line up).

- **Block size** is auto-detected from the image (`InferBlockSize`, or
  `InferBlockSizeRobust` for resampled / anti-aliased / JPEG-compressed grids). Pass
  `--block-size`/`-b` to pin it.
- **Grid offset** is discovered as the first step of the search.

Smaller blocks carry less information per character, so a tighter `--threshold` (e.g.
`0.1`) helps prune coincidental matches and lets the whole-image score pick the
complete answer.

## Blur (Gaussian)

Blur mixes each pixel with its neighbors using a Gaussian kernel of width **σ**
(sigma). UnPixel reproduces the same blur on its candidates, so the only unknown is σ.

- **σ is auto-estimated** (`InferBlurSigma`, density-adaptive gradient-percentile,
  accurate to ~±2% for σ ∈ {1, 2, 4, 8}), then searched as a dimension of the recovery
  — the same way block size is for mosaic.
- Blur recovery defaults to **beam search with a language prior**, because the
  per-character image signal in blur is weaker, so context helps recover longer words.
- The CLI auto-searches σ under `--redaction blur` when `--blur-sigma` is unset.
  `Result.BlurSigma` records the recovered value.

```bash
unpixel --redaction blur redacted.png        # auto σ
unpixel --redaction blur --blur-sigma 5 x.png  # pin σ
```

### Real-world blur is messy

Internet captures are rarely a clean Gaussian on flat white. Two opt-in tools help:

- **Input normalization** (`--normalize`): removes textured / vignette / dark-theme
  backgrounds and auto-inverts dark mode, so the region matches the clean-Gaussian
  assumption. It restores the assumption but does **not** by itself recover the text —
  font fidelity still dominates.
- **Re-mosaic error correction** (`--remosaic`): applies the Hill–Zhou–Saul–Shacham
  (PETS-2016) composite blur→block-average operator to collapse σ-mismatch and JPEG
  noise. Helpful for real σ-mismatch/JPEG; never regresses on clean synthetics.

There's also an optional, exploratory Richardson-Lucy deconvolution (`--deblur`) and a
non-blind L0 text deblur (`--l0-deblur`) for known-PSF cases — preprocessing aids, not
the main attack.

## Which is it? Let UnPixel decide

`--redaction auto` (the default) detects whether the region is a pixelated grid or a
blur and routes accordingly. True pixelizations go to the mosaic pipeline; ambiguous
cases go to blur. When a result falls below the acceptance threshold, UnPixel can still
surface the best candidate (`Result.BelowThreshold`) for exploratory analysis.

## See also

- [Fonts & calibration](fonts-and-calibration.md) — the dominant real-world factor.
- [Limits](limits.md) — why real blur is harder than synthetic blur.
- [CLI reference](../reference/cli.md) — all blur/mosaic flags.
