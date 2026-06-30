# API reference

The full, always-current API is on
[pkg.go.dev](https://pkg.go.dev/github.com/oioio-space/unpixel). This page is an
orientation map: the high-level helpers, the `Config` fields, and the specialized
decoders.

Import the root package and the `defaults` package (its side-effect import wires the
faithful renderer/pixelator/metric/strategy):

```go
import (
	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults"
)
```

## One-call recovery

```go
res, err := unpixel.RecoverFile(ctx, "redacted.png")  // path
res, err := unpixel.Recover(ctx, img, opts...)         // image.Image
res, err := unpixel.RecoverReader(ctx, r, opts...)     // io.Reader
fmt.Println(res.BestGuess)
```

With options for the common knobs (the rest is auto-detected):

```go
res, err := unpixel.Recover(ctx, img,
	unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz0123456789 "),
	unpixel.WithWorkers(8),
)
```

Unknown typeface? Rank candidate fonts in parallel, best-fit first:

```go
ranked, err := unpixel.RecoverMultiFont(ctx, img, renderers, unpixel.WithBlockSize(5))
best := ranked[0] // lowest BestTotal — the font that fit best
```

For streaming progress or full control, drop to the low-level `Engine`:

```go
eng, err := unpixel.New(img, unpixel.Config{}) // zero Config = faithful defaults
progress, results := eng.Run(ctx)
go unpixel.OnProgress(progress, func(p unpixel.Progress) {
	fmt.Printf("\rbest: %-20q (%.3f)", p.BestGuess, p.BestScore)
})
fmt.Println((<-results).BestGuess)
```

## Root package (`unpixel`)

| Symbol | Purpose |
|--------|---------|
| `Recover(ctx, image.Image, ...Option) (Result, error)` | One call: search and return the best result |
| `RecoverReader(ctx, io.Reader, ...Option)` / `RecoverFile(ctx, path, ...Option)` | Decode then `Recover` |
| `RecoverMultiFont(ctx, image.Image, []Renderer, ...Option) ([]FontResult, error)` | Sweep candidate fonts in parallel; ranked best-fit first by `BestTotal` |
| `RecoverBlurred(ctx, image.Image, ...Option) (Result, error)` | Zero-config Gaussian-blur recovery: auto-estimates σ, searches it as a dimension, defaults to beam+language prior |
| `Verify(ctx, image.Image, []string, ...Option) ([]Verdict, error)` | Physically score caller-proposed candidate strings against the redaction (render→re-pixelate→metric); `Verdict{Text, Distance, Match}`, `Match` when `Distance < VerifyMatchThreshold` (0.10). The anti-hallucination gate for an external propose→verify loop |
| `VerifyImage(ctx, redacted, restored image.Image, ...Option) (ImageVerdict, error)` | Physics-verify an externally-restored *image* (e.g. from a diffusion sidecar): re-applies the forward operator and compares. `ImageVerdict{Distance, Match}`. `Verify(text) ≡ VerifyImage(render(text))`. See [sidecar protocol](../sidecar-protocol.md) |
| `With*` options (`WithCharset`, `WithWorkers`, `WithRenderer`, `WithStrategy`, `WithPriors`, …) | Tweak common knobs; `WithConfig` seeds a full `Config` |
| Opt-in priors/constraints: `WithFontPrior`/`WithFontPriorTopK` (blind font-sweep ordering/pruning), `WithRerankWeight` (language-blended candidate re-rank), `WithExpectedFormat` (structured-secret search pruning — its enum is `internal/secrets.Format`, so it is reached in practice via the CLI/MCP, see below) | All default-off; the core search is byte-identical when unset |
| `New(redacted image.Image, cfg Config) (*Engine, error)` | Build an engine; zero `Config` = faithful defaults |
| `(*Engine).Run(ctx) (<-chan Progress, <-chan Result)` | Run the search; stream progress, deliver the result |
| `(*Engine).Config() Config` | Resolved config (e.g. the inferred block size) |
| `OnProgress(ch <-chan Progress, fn func(Progress))` | Drain progress events into a callback (any UI) |
| `InferBlockSize(image.Image) int` | Detect the mosaic block size (exact GCD of grid boundaries) |
| `InferBlockSizeRobust(image.Image) (blockSize int, support float64)` | Robust block-size detection (resampled/anti-aliased/JPEG'd grids) |
| `InferBlurSigma(image.Image) float64` | Estimate Gaussian blur radius σ from image contrast |
| `InferImpulseNoise(image.Image) float64` | Detect impulse (salt-pepper) noise; used by `blind.Recover` to auto-denoise |
| `Renderer`, `Pixelator`, `Metric`, `Strategy` | Pluggable pipeline interfaces (defined in root; implementations under `internal/`) |
| `Config`, `Style`, `Result`, `FontResult`, `Eval`, `Offset`, `Progress`, `EventKind`, `Verdict`, `ImageVerdict` | Configuration and result/event types |

The faithful default metric is `orisano/pixelmatch`; `defaults.PixelmatchFastMetric()`
skips anti-aliasing detection on block-average mosaic for identical results ~35% faster,
and keeps faithful `Pixelmatch` on blur.

## `Config` fields

Pass a `Config` to `unpixel.New`. Every zero value falls back to a documented default.

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `Charset` | `string` | `"abcdefghijklmnopqrstuvwxyz "` | Candidate characters to search |
| `MaxLength` | `int` | `20` | Maximum plaintext length |
| `BlockSize` | `int` | `0` → auto / `8` | Pixelation block size; `≤0` auto-detects, else falls back to `8` |
| `Threshold` | `float64` | `0.25` | Max image-distance score (0–1) to keep a candidate |
| `SpaceThreshold` | `float64` | `0.5` | Looser threshold for extending with a space |
| `ThresholdFor` | `func(rune) float64` | space→`SpaceThreshold`, else `Threshold` | Per-character threshold |
| `TopN` | `int` | `5` | Ranked candidates kept per offset in `Result.TopN` |
| `Style` | `Style` | Liberation Sans, 32 px, white bg | Font size/weight/padding and `LetterSpacing` |
| `Renderer` | `Renderer` | `x/image/font` (pure Go) | Text → raster |
| `Pixelator` | `Pixelator` | block-average | Raster → pixelated |
| `Metric` | `Metric` | `orisano/pixelmatch` | Image-distance score |
| `Strategy` | `Strategy` | guided DFS | Candidate-space search |
| `BeamWidth` | `int` | `16` | Candidates kept per depth level — beam strategy only |
| `CacheSize` | `int` | `4096` | LRU size for prefix-render memoization — beam strategy only (`0` disables) |
| `Workers` | `int` | `0` → all CPUs | Grid offsets probed/searched concurrently; `1` forces sequential. Never changes output |

Select beam search instead of the default DFS:

```go
cfg := unpixel.Config{Strategy: defaults.BeamStrategy(0)} // 0 = use BeamWidth (default 16)
```

## Blind recovery (`blind`)

```go
import "github.com/oioio-space/unpixel/blind"

res, err := blind.Recover(ctx, img,
	blind.WithLanguage(blind.French), // or blind.English (default)
	blind.WithDenoise(-1),            // auto-detect (default); 0 = off, N = force N×N
)
fmt.Println(res.Text, res.Font, res.Block, res.Dist)
```

| Symbol | Purpose |
|--------|---------|
| `blind.Recover(ctx, image.Image, ...Option) (*Recovery, error)` | Blind bilingual recovery (FR/EN); re-exports `English`/`French`/`ParseLanguage` |
| `blind.With*` (`WithLanguage`, `WithBlock`, `WithOffset`, `WithFontSize`, `WithLinear`, `WithFonts`, `WithMetric`, `WithGamma`, `WithLetterSpacingSearch`, `WithDenoise`) | Fine-tune or override auto-detection |

## Specialized decoders (`mosaictext`)

Each returns the decoded `string` (or a result struct) and takes functional options.
See [decoders](../concepts/decoders.md) for when to use which.

| Function | Decoder |
|----------|---------|
| `mosaictext.Decode(ctx, img, ...Option)` | Zero-config monospace mosaic decoder (auto grid + recognition) |
| `mosaictext.DecodeHMM(ctx, img, ...Option)` | `mono-hmm` — LM-guided beam for long monospace text |
| `mosaictext.DecodeReference(ctx, img, ...RefOption)` | `ref-match` — arbitrary content, proportional fonts |
| `mosaictext.DecodeWindowHMM(ctx, img, ...WHMMOption)` | `window-hmm` — proportional-font grid-window beam |
| `mosaictext.DecodeTrainedHMM(ctx, img, ...THMMOption)` | `trained-hmm` — learned-emission Viterbi (constrained alphabets) |
| `mosaictext.DecodeDID(ctx, img, ...DIDOption)` | `did` — boundary-free DP trellis |
| `mosaictext.DecodeMultiFrameAuto(ctx, imgs []image.Image, ...Option) (Result, error)` | multi-frame auto-phase — decodes from several phase-diverse mosaics of the same content; auto-detects per-frame grid phase and scores each candidate against all frames |
| `mosaictext.DecodeVarFont(ctx, img, ...VarFontOption) (VarFontResult, error)` | `varfont` — variable-font axis fitting + calibration |
| `mosaictext.DecodePerspective(ctx, img, ...PerspectiveOption) (PerspectiveResult, error)` | perspective — forward-model beam decode of an angled photo; `WithPerspectiveQuad` corners or `WithPerspectiveAutoQuad` |

Each decoder's options follow a `With<Decoder>*` naming pattern (e.g. `WithRefFont`,
`WithWHMMCharset`, `WithTHMMK`, `WithDIDLanguage`, `WithVarFontText`) for font, charset,
color space (sRGB/linear-light), and decoder-specific tuning. The exact set is on
[pkg.go.dev](https://pkg.go.dev/github.com/oioio-space/unpixel/mosaictext).

## Blind font prior (`fontprior`)

Orders (and optionally prunes) the bundled-font sweep by a blind pixelated-signature
prior — no known plaintext required — so the likeliest font is decoded first.

| Symbol | Purpose |
|--------|---------|
| `fontprior.RecoverWithPrior(ctx, image.Image, ...unpixel.Option) ([]fontprior.FontResult, error)` | Rank `fonts.All()` by the prior, reorder the sweep (result-preserving), optionally truncate to the top-K (`unpixel.WithFontPriorTopK`), then decode. `FontResult{Result, Font}` best-first |
| `fontprior.Default() Prior` / `fontprior.Histogram` | The active prior (pure-Go block-luminance heuristic by default; a trained CNN drops in behind `//go:build ml`) |

Limit: ranks only the 9 bundled fonts, and the histogram is noisy on very short/low-ink
redactions — reordering never loses the answer, but a small `--font-prior-top-k` can.

## Candidate re-rank (`rerank`)

Re-orders already-physically-scored candidates by blending image distance with a
language score — generalises the search's narrow language tie-break into a tunable stage.

| Symbol | Purpose |
|--------|---------|
| `rerank.Rerank(ctx, image.Image, []string, ...unpixel.Option) ([]rerank.Ranked, error)` | `Verify` the candidates, then blend distance with the language prior; weight via `unpixel.WithRerankWeight` (0 = physical order). `Ranked{Text, Distance, LMScore, Blended}` best-first |
| `rerank.Default() Reranker` / `rerank.Linguistic` | The active reranker (pure-Go language blend by default; a CTC model drops in behind `//go:build ml`) |

## Structured-secret search pruning (`WithExpectedFormat`)

`unpixel.WithExpectedFormat(secrets.Format)` prunes the guided search to candidates
feasible for a declared structured secret (credit card/Luhn, IBAN/mod-97, date, phone
FR/US/E164, digits) and drops complete-but-invalid candidates. Strictly opt-in
(`FormatNone`/unset is byte-identical). Because the `Format` enum lives in
`internal/secrets`, external callers reach it through the **MCP** `unpixel_decode`
`expected_format` field rather than the Go option directly.

## Variable-font geometry calibration (`varfont`)

Recover exact font size and horizontal stretch from a sharp visible region before decoding:

```go
import "github.com/oioio-space/unpixel/mosaictext"

// Calibrate from visible text and region
result, _ := mosaictext.DecodeVarFont(ctx, redactedImg,
	mosaictext.WithVarFontCalibrateGeometry(),
	mosaictext.WithVarFontVisibleText("Sample"),
	mosaictext.WithVarFontVisibleRegion(image.Rect(10, 10, 100, 40)),
)

// Or use a separate font sample
result, _ := mosaictext.DecodeVarFont(ctx, redactedImg,
	mosaictext.WithVarFontCalibrateGeometry(),
	mosaictext.WithVarFontSampleImage(sampleImg),
	mosaictext.WithVarFontSampleText("The quick brown fox"),
)
```

| Symbol | Purpose |
|--------|---------|
| `mosaictext.WithVarFontCalibrateGeometry()` | Enable geometry calibration (font size + x-stretch) from visible text |
| `mosaictext.WithVarFontVisibleText(string)` | Visible text for calibration |
| `mosaictext.WithVarFontVisibleRegion(image.Rectangle)` | Bounding box of visible text |
| `mosaictext.WithVarFontSampleImage(image.Image)` | Font sample image (alternative to visible region) |
| `mosaictext.WithVarFontSampleText(string)` | Text content in the sample image |
| `varfont.CalibrateGeometry(cfg GeometryConfig) (GeometryResult, error)` | Low-level geometry optimizer; returns `{FontSizePx, XStretch, Distance}` |

**Caveat:** geometry calibration requires an ink-tight visible crop with sufficient text width. Large white margins or very short text degrade the fit; aim for ≥20–30 pixels of rendered text width.

## See also

- [CLI reference](cli.md) — the command-line equivalents.
- [Architecture](../architecture.md) — the faithful pipeline behind these calls.
