# CLI reference

`unpixel [flags] <image>` — recover text from a pixelated or blurred image. The best
guess prints to **stdout** (so it pipes cleanly); the ranked table and live progress go
to **stderr**. `--format json` emits a stable schema (`best_guess`, `confidence`,
`total_score`, `top`, and a ranked `fonts` array when sweeping).

Run `unpixel --help` for the authoritative, version-specific list.

## Examples

```bash
# Zero-config: sweep the built-in fonts, print the best guess
unpixel redacted.png
cat redacted.png | unpixel -                 # read PNG from stdin
unpixel --format json --top 10 redacted.png
unpixel --strategy beam --metric ssim --workers 8 redacted.png

# Match a known typeface yourself (skips the sweep) — e.g. a Consolas code screenshot
unpixel --font Consolas.ttf --font-size 24 --letter-spacing -0.2 -b 5 redacted.png

# Sweep your own candidate fonts (or a whole directory)
unpixel --font Arial.ttf --font Consolas.ttf --font Courier.ttf -b 5 redacted.png
unpixel --font-dir /usr/share/fonts/truetype -b 5 redacted.png

# Decoders (see docs/concepts/decoders.md)
unpixel --decoder mono-hmm --lang en image.png                            # LM-guided monospace
unpixel --decoder mono-hmm --lang fr --font "JetBrains Mono" long.png     # with a specific font
unpixel --decoder ref-match --font "Liberation Sans" passwords.png        # arbitrary content
unpixel --decoder window-hmm --lang en image.png                          # proportional fonts
unpixel --decoder trained-hmm image.png                                   # digit/PIN codes
unpixel --decoder varfont image.png                                       # variable-font fitting
unpixel --decoder varfont --varfont-text "known" image.png               # varfont calibration mode
unpixel --decoder did image.png                                           # boundary-free DP
unpixel --normalize --redaction blur real-blur.jpg                        # normalize + blur on JPEG

# Perspective: a redaction photographed at an angle. Give the 4 corners of the
# redaction quad (top-left, top-right, bottom-right, bottom-left) and the block size,
# or "auto" to detect the quad (one convex region on a uniform background).
unpixel --rectify "40,30 180,45 170,150 20,140" -b 8 --font "Liberation Sans" photo.png
unpixel --rectify auto -b 8 --font "Liberation Sans" photo.png

# Blind, bilingual recovery
unpixel --blind --lang fr testdata/real/marx.png
unpixel --blind --lang en --block-size 8 image.png
unpixel --blind --denoise 0 image.png        # disable auto-denoise
unpixel --blind --denoise 3 image.png        # force a 3×3 median window

# Quick wins
unpixel --gamma auto image.png                                            # auto sRGB vs linear-light
unpixel --blind --letter-spacing-search image.png                        # calibrate letter-spacing

# Deblurring (preprocessing aids)
unpixel --l0-deblur image.png                                            # non-blind L0 text deblur

# Advanced
unpixel --decoder varfont --varfont-axes "wght:200:900:500" image.png    # sweep weight axis
unpixel --thmm-lang en --thmm-jpeg 85 image.png                          # trained-HMM + JPEG aug

# Re-mosaic error correction (Hill–Zhou–Saul–Shacham, PETS-2016 §4)
unpixel --remosaic --redaction blur blurred.png
unpixel --remosaic-grid 4 --redaction blur image.png                     # pin the remosaic grid
unpixel --remosaic-linear --redaction blur gimp-output.png               # linear-light (GEGL/GIMP)
```

## Flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--charset` | `a–z` + space | Candidate characters to try |
| `--charset-preset` | — | Named charset when `--charset` is unset: `lower`, `alnum`, `ascii`/`code` |
| `--block-size`, `-b` | `0` (auto) | Pixelation block size; `0` auto-detects from the image |
| `--font` | embedded (Liberation Sans) | TTF/OTF font to render candidates; **repeat to sweep** and keep the best fit |
| `--font-dir` | — | Directory of TTF/OTF fonts to sweep (each tried; best whole-image fit wins) |
| `--font-size` | `0` (32) | Font size in points to match the redaction |
| `--letter-spacing` | `0` | Extra px after each glyph, like CSS `letter-spacing` (may be negative) |
| `--redaction` | `auto` | `auto`, `mosaic`, or `blur` (blur auto-detected when there's no mosaic grid) |
| `--blur-sigma` | `0` (auto) | Gaussian blur radius for `--redaction blur`; `0` estimates it from the image |
| `--blur-exact` | off | Force the exact Gaussian (default uses the ~3× faster box approx at large σ) |
| `--deblur` | `0` (off) | Optional Richardson-Lucy deconvolution iterations (exploratory preprocessing) |
| `--denoise` | `-1` (auto) | Median denoise for `--blind`: `-1` auto-detect, `0` disable, `N` force N×N window |
| `--decoder` | `default` | `default`, `mono-hmm`, `ref-match`, `window-hmm`, `trained-hmm`, `varfont`, `did` (see [decoders](../concepts/decoders.md)) |
| `--gamma` | `srgb` | `auto` (pick sRGB or linear-light, keep lower distance), `linear`, `srgb` |
| `--letter-spacing-search` | off | Enable letter-spacing sweep (Bishop-Fox method); records `Result.LetterSpacing` |
| `--varfont-text` | — | Known text for variable-font calibration mode (bypasses blind search) |
| `--varfont-axes` | — | Variable-font axis bounds, e.g. `wght:200:900:500` (axis:min:max:step) |
| `--varfont-linear` | off | Use linear-light rendering for variable-font fitting |
| `--thmm-lang` | off | Language-structure training for trained-HMM (`en` or `fr`) |
| `--thmm-jpeg` | `0` | JPEG quality augmentation for trained-HMM emissions (e.g. `85`) |
| `--remosaic` | off | Hill–Zhou–Saul–Shacham PETS-2016 §4 composite blur→remosaic error correction |
| `--remosaic-grid` | `0` (auto) | Block grid for `--remosaic`; `0` auto-detects as `max(2, round(σ))` |
| `--remosaic-linear` | off | Linear-light block averaging for `--remosaic` (GEGL/GIMP targets) |
| `--strategy` | `guided` | `guided` (full DFS), `beam` (bounded), or `mono` (monospace fast-path) |
| `--beam-width` | `0` (16) | Candidates kept per depth level under `--strategy beam` |
| `--metric` | `pixelmatch` | `pixelmatch` (faithful; auto `pixelmatch-fast` on block-average mosaic) or `ssim` |
| `--language` | off | Break ties between equal-image candidates toward plausible text (char-bigram prior) |
| `--secrets` | off | Boost structured formats (UUID, API token, Luhn) and dictionary words |
| `--workers` | `0` (all CPUs) | Grid offsets searched concurrently; also the sweep's core budget |
| `--top`, `-n` | `5` | Ranked candidates to report |
| `--normalize` | off | Input normalization for blur: morphological background removal + dark-theme inversion |
| `--normalize-bg` | `divide` | Background removal: `divide` (multiplicative), `subtract` (additive), or `none` |
| `--deblock` | `0` (off) | Median deblocking radius for JPEG (`0` = off, `N` = force (2N+1)×(2N+1) kernel) |
| `--format`, `-f` | `text` | `text` or machine-readable `json` |
| `--timeout` | `0` (none) | Max recovery time |
| `--l0-deblur` | off | Non-blind Pan-CVPR-2014 L0 text deblur (requires a known σ from blur-sigma inference) |
| `--rectify` | — | Decode a perspective-distorted redaction: 4 quad corners `"x0,y0 x1,y1 x2,y2 x3,y3"` (TL TR BR BL, photo px), or `auto` to detect the quad (one convex region on a uniform background). Pure forward-model beam search: candidates are rendered/re-pixelated and scored against the native photo pixels via the planar homography — no rectify resampling. Decodes correctly when `--block-size` and `--font` match the original. Takes precedence over other decoders |

> **Tip:** lower block sizes carry less information per glyph, so a tighter
> `--threshold` (e.g. `0.1`) prunes coincidental matches and lets the whole-image score
> pick the complete answer.

## See also

- [Getting started](../getting-started.md) — the common cases, explained.
- [Decoders](../concepts/decoders.md) — which `--decoder` to use.
- [API reference](api.md) — the equivalent Go library functions.
