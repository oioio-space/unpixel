# Mosaic vs. blur

UnPixel addresses the two principal methods of redacting text. Both are reversible
because both are deterministic functions of the original pixels, and the
[generate-and-test](how-it-works.md) approach applies to each.

## Mosaic (pixelation)

Mosaic redaction partitions the region into a grid and replaces each block with its
average colour. Two quantities are required: the **block size** and the **grid offset**
(the alignment of the blocks).

- The **block size** is detected automatically (`InferBlockSize`, or
  `InferBlockSizeRobust` for resampled, anti-aliased, or JPEG-compressed grids). It may be
  fixed with `--block-size`/`-b`.
- The **grid offset** is determined as the first stage of the search.

Smaller blocks carry less information per character; a tighter `--threshold` (for example
`0.1`) therefore helps prune coincidental matches and allows the whole-image score to
select the complete solution.

## Blur (Gaussian)

Blur combines each pixel with its neighbours using a Gaussian kernel of width **σ**
(sigma). UnPixel reproduces the same blur on its candidates, so the sole unknown is σ.

- **σ is estimated automatically** (`InferBlurSigma`, a density-adaptive
  gradient-percentile method accurate to approximately ±2% for σ ∈ {1, 2, 4, 8}) and then
  searched as a dimension of the recovery, in the same manner as the block size for
  mosaic.
- Blur recovery defaults to **beam search with a language prior**, because the
  per-character image signal in blur is weaker and contextual information assists the
  recovery of longer words.
- The command line searches σ automatically under `--redaction blur` when `--blur-sigma`
  is unset. `Result.BlurSigma` records the recovered value.

```bash
unpixel --redaction blur redacted.png          # automatic σ
unpixel --redaction blur --blur-sigma 5 x.png  # fixed σ
```

### Real-world blur is less well-conditioned

Captures obtained from the internet are seldom a clean Gaussian on a flat white
background. Two optional facilities assist:

- **Input normalization** (`--normalize`): removes textured, vignetted, or dark-theme
  backgrounds and inverts dark mode automatically, so that the region conforms to the
  clean-Gaussian assumption. It restores the assumption but does not, on its own, recover
  the text; font fidelity remains the determining factor.
- **Re-mosaic error correction** (`--remosaic`): applies the Hill–Zhou–Saul–Shacham
  (PETS-2016) composite blur-then-block-average operator to reduce σ-mismatch and JPEG
  noise. It is beneficial for real σ-mismatch and JPEG artefacts and never regresses on
  clean synthetic inputs.

An optional, exploratory Richardson-Lucy deconvolution (`--deblur`) and a non-blind L0
text deblur (`--l0-deblur`) are also provided for known-PSF cases; these are preprocessing
aids rather than the principal method.

## Automatic discrimination

`--redaction auto` (the default) determines whether the region is a pixelated grid or a
blur and routes it accordingly. True pixelizations are directed to the mosaic pipeline and
ambiguous cases to the blur pipeline. When a result falls below the acceptance threshold,
UnPixel can nonetheless surface the best candidate (`Result.BelowThreshold`) for
exploratory analysis.

## See also

- [Fonts & calibration](fonts-and-calibration.md) — the dominant real-world factor.
- [Limits](limits.md) — why real blur is more difficult than synthetic blur.
- [CLI reference](../reference/cli.md) — all blur and mosaic flags.
