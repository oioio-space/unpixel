# Architecture & the faithful algorithm

This is the engineering reference: the faithful port of
[bishopfox/unredacter](https://github.com/bishopfox/unredacter), the per-candidate
pipeline step by step, the package layout, and the library choices. For the plain-
language explanation start with [how it works](concepts/how-it-works.md).

UnPixel does **not** un-blur an image. It runs *generate-and-test*: render a candidate
string, re-pixelate it with the **same** block grid as the redacted target, measure the
image distance, keep candidates that match, and extend the string character by
character. Pixelation (per-block mean color) is a deterministic function of its input,
so — given a faithful rendering pipeline — only the true text reproduces the target's
blocks.

## Faithful pipeline (per candidate)

Constants (original): `blockSize=8`, `maxLength=20`, `threshold=0.25`, space-threshold
`0.5`, charset `a–z` + space, diff threshold `0.02`.

1. **Render** the candidate text in a fixed style (original CSS: `font-family:'Arial';
   font-size:32px; font-weight:normal; white-space:pre; padding:8px 0 0 8px;
   background:white`), followed by a **blue `█` sentinel** span. Bundled font:
   **Liberation Sans** (metrically identical to Arial, SIL OFL — GPL-compatible).
2. **getBlueMargin** — scan the middle row to find the first blue pixel → `blueMargin`
   (exact right edge of the text); scan that column to find the blue box's vertical
   `center`.
3. **Crop to grid origin** — `crop(offset_x, offset_y, blueMargin-offset_x,
   h-offset_y)`; `imageCenter -= offset_y`.
4. **White-pad** width up to a multiple of `blockSize`.
5. **Pixelate** — replace every `8×8` block by its mean RGBA (the operation being
   attacked).
6. **getLeftEdge** — first non-white column (text start).
7. **Vertical crop** to the redacted height around a block-aligned center:
   `adjustedCenter = imageCenter - imageCenter%blockSize + 4`;
   `crop(leftEdge, adjustedCenter - redactedH/2, w-leftEdge, redactedH)`.
8. **Marginal region** (`score`) — diff this render against the *previous* candidate's
   render (`Jimp.diff`, threshold 0.02), find the changed band's left boundary
   (`getMargins` = first red column), crop both guess and redacted target to
   `[left_boundary, …]`. If identical (e.g. consecutive spaces) use the previous width.
9. **Trim the right-most block** off both images (the next, unknown letter bleeds into
   it): `adjustedBlueMargin = (blueMargin-left_boundary)-leftEdge-offset_x`.
10. **score** = `Jimp.diff(guess, redacted, 0.02).percent` over that bounded region
    (used for pruning + extension). **totalScore** = diff of the whole guess vs whole
    redacted (UI only). **tooBig** = redacted width < scaled guess width.

`Jimp.diff(a,b,0.02).percent` = fraction of pixels differing beyond a YIQ perceptual
threshold (Jimp wraps pixelmatch). Faithful default metric:
`github.com/orisano/pixelmatch` (maintained Go port of mapbox/pixelmatch), wrapped to
return `[0,1]`.

## Guided search

1. **Offset discovery** — for each of the `8×8=64` grid origins `(x,y)`, take the best
   single-char `score`; keep offsets with best `< 0.25`; sort ascending.
2. For each surviving offset: seed with every single char scoring `< 0.25`, sorted;
   then `guessRecursive`: render parent; if `tooBig` → prune; else append each charset
   char, compute the **marginal** score, keep `< 0.25` (`< 0.5` for space), sort,
   recurse. Stop at `maxLength`.

## Package layout

```
github.com/oioio-space/unpixel
├── unpixel.go              # Engine, Config, Result, Eval, Offset, Progress; the 4 interfaces
├── defaults/               # wires the default components (breaks the root↔internal import cycle)
├── fonts/                  # bundled redistributable fonts (OFL/Apache) for the zero-config sweep
├── blind/                  # zero-config bilingual (FR/EN) blind recovery
├── mosaictext/             # the specialized decoders (mono-hmm, ref-match, window-hmm, …)
├── internal/
│   ├── imutil/             # crop / pad / compose; blueMargin & leftEdge detection
│   ├── pixelate/           # block-average pixelator; grid-origin crop; white padding
│   ├── metric/             # pixelmatch (faithful default) + simple RGB metric
│   ├── render/             # pure-Go x/image/font renderer (embedded or custom fonts; letter-spacing)
│   │   └── fonts/          #   embedded Liberation Sans (Regular/Bold) + OFL license
│   └── search/             # offset discovery + marginal cropping + guided DFS + whole-image ranking
└── cmd/unpixel/            # CLI (urfave/cli/v3): recovery, font sweep, text/JSON output
```

The four interfaces (`Renderer`, `Pixelator`, `Metric`, `Strategy`) live in the root
package so they can be implemented and injected from outside; the concrete
implementations stay under `internal/`.

## Go libraries

- **Text→raster:** `golang.org/x/image/font/opentype` + `golang.org/x/image/font`
  (`font.Drawer`). Pure-Go, deterministic, pixel-level baseline/advance control.
  (Alternatives weighed: `fogleman/gg` — stale; `golang/freetype` — superseded;
  `tdewolff/canvas` — heavier than needed.)
- **Pixelate / images:** stdlib `image` + `golang.org/x/image/draw` for
  padding/compositing.
- **Metric:** `github.com/orisano/pixelmatch` (faithful default) behind a `Metric`
  interface; a simple RGB-fraction metric as an alternative.

## Library-agnostic progress API

A single typed `Progress` event is streamed on a **buffered channel** (with an
`OnProgress` callback adapter). High-frequency events (`EventCandidate`,
`EventOffsetProbed`) are drop-on-full (`select … default`); `EventNewBest` /
`EventDone` are always delivered. The core imports no UI package. Each event carries:
best guess + score, current guess + score, depth, offset, evaluated count, estimated
space, offsets done/total, optional preview images (opt-in to avoid alloc churn),
elapsed, done flag, err. See `unpixel.go` for the exact types.

## TDD build order (tests first per layer)

1. `imutil` — hand-built tiny RGBA images; assert exact columns from
   blueMargin/leftEdge/margins.
2. `pixelate` — golden 16×16 → each 8×8 block equals its mean; idempotence property.
3. `metric` — identical→0, inverted→~1, N-pixel diff→1/N (rgb backend); pixelmatch
   sanity.
4. `render` — golden PNG (tolerance for AA); `sentinelX` == measured text width.
5. `search` — pruning/threshold/space-threshold logic via a **mock Scorer** (no
   rendering).
6. `unpixel` end-to-end — **self-redaction round-trip**: redact known plaintext with
   our own pixelator at a known offset → `Engine.Run` recovers it. Plus ctx-cancel and
   progress-channel tests (EventDone always arrives).

## Rendering fidelity & the Chromium question

The original `unredacter` rasterizes via Chromium; UnPixel uses pure-Go
`golang.org/x/image/font`. Byte-identical glyphs across engines aren't possible
(different hinting/anti-aliasing), so **correctness is judged by self-consistency**:
redact a known plaintext with UnPixel's own renderer, then recover it. Recovering a
Chromium-produced redaction (e.g. the original `secret.png`) is a deferred goal that
would require a `chromedp` renderer (and a Chrome binary at runtime/CI). This is the
single largest fidelity gap — see [limits](concepts/limits.md).

**Landed beyond the faithful baseline:** top-N confidence/ambiguity reporting; a
beam-search strategy with prefix-render memoization; an SSIM metric; automatic
block-size inference; goroutine fan-out over offset discovery and per-offset search
with a deterministic merge; the specialized decoders in `mosaictext/`; and blur
recovery. See [`PROGRESS.md`](../PROGRESS.md) for the full evolution and
[performance.md](performance.md) for the hot-path work.
