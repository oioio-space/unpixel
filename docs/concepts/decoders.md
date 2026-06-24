# Decoders

A **decoder** is the strategy by which UnPixel converts a redacted region into text. The
default performs well for short words in a known charset; the others are specialized for
long text, arbitrary secrets, proportional fonts, or numeric codes. A decoder is selected
with `--decoder <name>` (command line) or the corresponding `mosaictext.Decode*` function
(Go).

Every decoder is subject to the same fundamental constraint: **recovery is bounded by font
fidelity.** The exact font (`--font yourfont.ttf`) should be supplied for real images; for
all decoders, it is the difference between success and failure.

## Decoder selection

| Content | Decoder | Rationale |
|---------|---------|-----------|
| Short word, small charset, uncertain | **`default`** | Guided DFS/beam; the faithful baseline |
| Long monospace text (10–50+ characters) | **`mono-hmm`** | Language-model beam; polynomial in length |
| Passwords, code, random strings | **`ref-match`** | No language assumption; arbitrary content; proportional fonts |
| Proportional-font text (variable widths) | **`window-hmm`** | Per-cell window scoring; not restricted to monospace |
| Digits, PINs, check numbers | **`trained-hmm`** | Learned-emission Viterbi; exact on constrained alphabets |
| Short proportional or monospace text | **`did`** | Boundary-free DP; jointly recovers boundaries and characters |
| Variable-font redaction, or font from context | **`varfont`** | Fits font axes to the pixels |
| Unknown font/block/language, prose estimate | **`--blind`** | Detects all parameters; French/English dictionary prior |

## The decoders

### `default` — guided DFS / beam
The faithful baseline. Constructs the solution character by character, pruning branches
that cease to match. Best suited to short text in a small charset. The underlying search
is selected with `--strategy guided|beam|mono`.

### `mono-hmm` — LM-guided monospace beam
Intended for long monospace text where the per-character signal is weak. A bigram language
model is fused into a left-to-right beam search, so the cost scales **polynomially** in
length rather than exponentially. Options: `--lang en|fr`, `--font`, `--charset`.
(`mosaictext.DecodeHMM`.)

### `ref-match` — reference matching (Depix-style)
Recovers **arbitrary content** — passwords, source code, random strings — with **no
language assumption**, and operates on **proportional fonts**. It renders per-glyph
references, pixelates them with the target grid, and matches block columns left to right.
Exact on self-consistent fixtures when the font is known. (`mosaictext.DecodeReference`.)

### `window-hmm` — grid-window beam
Intended for **proportional-font** mosaics (variable glyph widths). It slides a window over
the grid cells and scores each candidate by per-window block MSE, allowing the alignment to
vary by position. This is the beam variant, which is robust to font mismatch at some cost in
optimality. (`mosaictext.DecodeWindowHMM`.)

### `trained-hmm` — learned-emission Viterbi HMM
The genuine Hill et al. (PETS-2016) model: it trains on rendered examples (k-means-quantized
block observations and empirical transitions), then decodes with a single Viterbi pass,
yielding the **globally optimal path** with no assumed boundaries. Exact on constrained
alphabets (digits and PINs) for self-consistent redactions, but **sensitive to grid and
render-geometry mismatch** on real images. Options include `--thmm-lang` and `--thmm-jpeg`.
(`mosaictext.DecodeTrainedHMM`.)

### `did` — Document-Image-Decoding trellis
Recovers character boundaries **and** identities jointly, via dynamic programming over glyph
start columns. Exact on clean short monospace **and** proportional text when the font is
known. Its per-glyph-isolated emission is the limiting factor on real images: at glyph
boundaries, adjacent pixels average together after pixelation, so the isolated emissions do
not match the full-line pixelation. (`mosaictext.DecodeDID`.)

### `varfont` — variable-font fitting
Fits a variable font's axes (for example, weight) to the redacted pixels via coordinate
descent or Nelder-Mead, rather than selecting among fixed faces. It provides a calibration
mode (known text) and a best-effort blind mode, and also underpins calibration of the font
from visible adjacent text or from a separate font-sample image.
(`mosaictext.DecodeVarFont`.)

### `--blind` — automatic bilingual recovery
Not a `--decoder` value but a complete pipeline: it detects the redaction region, the block
and font sizes, sweeps the bundled fonts, and scores candidates with a frequency-weighted
French or English prior, denoising salt-and-pepper noise automatically. It is experimental
and most reliable on synthetic mosaics rendered in bundled fonts. (`blind.Recover`.)

## A note on Viterbi versus beam

An exact global-MAP Viterbi over independent per-cell emissions was attempted for monospace
and rejected: block-boundary averaging **couples** adjacent cells, so the per-cell emissions
are not independent and the bigram look-ahead overwhelms the (correct) emission signal. The
beam decoders avoid this by scoring each cell against the already-committed context.
`trained-hmm` makes Viterbi viable by *learning* the emission distribution rather than
assuming independence.

## See also

- [Fonts & calibration](fonts-and-calibration.md) — the dominant factor for all decoders.
- [Limits](limits.md) — where each decoder's recovery ceases on real images.
- [CLI reference](../reference/cli.md) and [API reference](../reference/api.md) — every flag
  and function.
