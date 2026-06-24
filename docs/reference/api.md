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
| `With*` options (`WithCharset`, `WithWorkers`, `WithRenderer`, `WithStrategy`, `WithPriors`, …) | Tweak common knobs; `WithConfig` seeds a full `Config` |
| `New(redacted image.Image, cfg Config) (*Engine, error)` | Build an engine; zero `Config` = faithful defaults |
| `(*Engine).Run(ctx) (<-chan Progress, <-chan Result)` | Run the search; stream progress, deliver the result |
| `(*Engine).Config() Config` | Resolved config (e.g. the inferred block size) |
| `OnProgress(ch <-chan Progress, fn func(Progress))` | Drain progress events into a callback (any UI) |
| `InferBlockSize(image.Image) int` | Detect the mosaic block size (exact GCD of grid boundaries) |
| `InferBlockSizeRobust(image.Image) (blockSize int, support float64)` | Robust block-size detection (resampled/anti-aliased/JPEG'd grids) |
| `InferBlurSigma(image.Image) float64` | Estimate Gaussian blur radius σ from image contrast |
| `InferImpulseNoise(image.Image) float64` | Detect impulse (salt-pepper) noise; used by `blind.Recover` to auto-denoise |
| `Renderer`, `Pixelator`, `Metric`, `Strategy` | Pluggable pipeline interfaces (defined in root; implementations under `internal/`) |
| `Config`, `Style`, `Result`, `FontResult`, `Eval`, `Offset`, `Progress`, `EventKind` | Configuration and result/event types |

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
| `mosaictext.DecodeVarFont(ctx, img, ...VarFontOption) (VarFontResult, error)` | `varfont` — variable-font axis fitting + calibration |

Each decoder's options follow a `With<Decoder>*` naming pattern (e.g. `WithRefFont`,
`WithWHMMCharset`, `WithTHMMK`, `WithDIDLanguage`, `WithVarFontText`) for font, charset,
color space (sRGB/linear-light), and decoder-specific tuning. The exact set is on
[pkg.go.dev](https://pkg.go.dev/github.com/oioio-space/unpixel/mosaictext).

## See also

- [CLI reference](cli.md) — the command-line equivalents.
- [Architecture](../architecture.md) — the faithful pipeline behind these calls.
