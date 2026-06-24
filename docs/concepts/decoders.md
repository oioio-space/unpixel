# Decoders

A **decoder** is the strategy UnPixel uses to turn a redacted region into text. The
default works well for short words in a known charset; the others are specialized for
long text, arbitrary secrets, proportional fonts, or digit codes. Select one with
`--decoder <name>` (CLI) or the matching `mosaictext.Decode*` function (Go).

Every decoder shares the same hard truth: **recovery is bounded by font fidelity.**
Supply the exact font (`--font yourfont.ttf`) for real images — it's the difference
between "works" and "doesn't" for all of them.

## Which decoder do I use?

| Your content | Use | Why |
|--------------|-----|-----|
| Short word, small charset, unsure | **`default`** | Guided DFS/beam; the faithful baseline |
| Long monospace text (10–50+ chars) | **`mono-hmm`** | Language-model beam; polynomial in length |
| Passwords, code, random strings | **`ref-match`** | No language assumption; arbitrary content; proportional fonts |
| Proportional-font text (variable widths) | **`window-hmm`** | Per-cell window scoring; not monospace-limited |
| Digits / PINs / check numbers | **`trained-hmm`** | Learned-emission Viterbi; exact on constrained alphabets |
| Short proportional or monospace text | **`did`** | Boundary-free DP; finds glyph boundaries + characters jointly |
| Variable-font redaction, or font from context | **`varfont`** | Fits font axes to the pixels |
| Unknown font/block/language, want a prose guess | **`--blind`** | Auto-detects everything; FR/EN dictionary prior |

## The decoders

### `default` — guided DFS / beam
The faithful baseline. Builds the answer character by character, pruning branches that
stop matching. Best for short text in a small charset. Switch the underlying search
with `--strategy guided|beam|mono`.

### `mono-hmm` — LM-guided monospace beam
For long monospace text where per-character signal is weak. Fuses a bigram language
model into a left-to-right beam search, so it scales **polynomially** in length instead
of exponentially. Options: `--lang en|fr`, `--font`, `--charset`.
(`mosaictext.DecodeHMM`.)

### `ref-match` — reference matching (Depix-style)
Recovers **arbitrary content** — passwords, source code, random strings — with **no
language assumption**, and works on **proportional fonts**. Renders per-glyph
references, pixelates them with the target grid, and matches columns left-to-right.
Exact on self-consistent fixtures when the font is known. (`mosaictext.DecodeReference`.)

### `window-hmm` — grid-window beam
For **proportional-font** mosaics (variable glyph widths). Slides a window over grid
cells and scores each candidate by per-window block MSE, so alignment can vary per
position. This is the beam variant — robust to font mismatch, trading some optimality.
(`mosaictext.DecodeWindowHMM`.)

### `trained-hmm` — learned-emission Viterbi HMM
The genuine Hill et al. (PETS-2016) model: trains on rendered examples (k-means
quantized block observations + empirical transitions), then decodes with a single
Viterbi pass — **globally optimal path**, no assumed boundaries. Exact on
constrained alphabets (digits/PINs) on self-consistent redactions; **brittle to grid /
render-geometry mismatch** on real images. Options include `--thmm-lang`,
`--thmm-jpeg`. (`mosaictext.DecodeTrainedHMM`.)

### `did` — Document-Image-Decoding trellis
Discovers character boundaries **and** identities together via dynamic programming over
glyph start-columns. Exact on clean short monospace **and** proportional text when the
font is known. Its per-glyph-isolated emission is the limit on real images: at glyph
boundaries, adjacent pixels average together after pixelation, so isolated emissions
mismatch the full-line pixelation. (`mosaictext.DecodeDID`.)

### `varfont` — variable-font fitting
Fits a variable font's axes (e.g. weight) to the redacted pixels via coordinate
descent / Nelder-Mead, instead of choosing among fixed faces. Has a calibration mode
(known text) and a best-effort blind mode. Also underpins calibrating the font from
visible adjacent text or a separate font-sample image. (`mosaictext.DecodeVarFont`.)

### `--blind` — zero-config bilingual recovery
Not a `--decoder` value but a full pipeline: auto-detects the redaction region, block
and font size, sweeps bundled fonts, and scores candidates with a frequency-weighted
French or English prior, denoising salt-and-pepper noise automatically. Experimental;
most reliable on synthetic mosaics in bundled fonts. (`blind.Recover`.)

## A note on Viterbi vs. beam

An exact global-MAP Viterbi over independent per-cell emissions was tried for monospace
and rejected: block-boundary averaging **couples** adjacent cells, so the per-cell
emissions aren't independent and the bigram lookahead overwhelms the (correct) emission
signal. The beam decoders avoid this by scoring each cell against already-committed
context. `trained-hmm` makes Viterbi work by *learning* the emission distribution
instead of assuming independence.

## See also

- [Fonts & calibration](fonts-and-calibration.md) — the dominant factor for all decoders.
- [Limits](limits.md) — where each decoder's recovery stops on real images.
- [CLI reference](../reference/cli.md) and [API reference](../reference/api.md) — every flag and function.
