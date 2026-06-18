# UnPixel — core design & faithful algorithm

A faithful-then-improve Go port of [bishopfox/unredacter](https://github.com/bishopfox/unredacter)
(see the [Bishop Fox writeup](https://bishopfox.com/blog/unredacter-tool-never-pixelation)).

The tool does **not** un-blur an image. It runs *generate-and-test*: render a candidate
string, re-pixelate it with the **same** block grid as the redacted target, measure the
image distance, keep candidates that match, and extend the string character by character.
Pixelation (per-block mean color) is a deterministic function of its input, so — given a
faithful rendering pipeline — only the true text reproduces the target's blocks.

## Faithful pipeline (per candidate)

Constants (original): `blockSize=8`, `maxLength=20`, `threshold=0.25`, space-threshold `0.5`,
charset `a–z` + space, diff threshold `0.02`.

1. **Render** the candidate text in a fixed style (original CSS: `font-family:'Arial';
   font-size:32px; font-weight:normal; white-space:pre; padding:8px 0 0 8px; background:white`),
   followed by a **blue `█` sentinel** span. Bundled font: **Liberation Sans** (metrically
   identical to Arial, SIL OFL — GPL-compatible).
2. **getBlueMargin** — scan the middle row to find the first blue pixel → `blueMargin` (exact
   right edge of the text); scan that column to find the blue box's vertical `center`.
3. **Crop to grid origin** — `crop(offset_x, offset_y, blueMargin-offset_x, h-offset_y)`;
   `imageCenter -= offset_y`.
4. **White-pad** width up to a multiple of `blockSize`.
5. **Pixelate** — replace every `8×8` block by its mean RGBA (the operation being attacked).
6. **getLeftEdge** — first non-white column (text start).
7. **Vertical crop** to the redacted height around a block-aligned center:
   `adjustedCenter = imageCenter - imageCenter%blockSize + 4`;
   `crop(leftEdge, adjustedCenter - redactedH/2, w-leftEdge, redactedH)`.
8. **Marginal region** (`score`) — diff this render against the *previous* candidate's render
   (`Jimp.diff`, threshold 0.02), find the changed band's left boundary (`getMargins` = first
   red column), crop both guess and redacted target to `[left_boundary, …]`. If identical
   (e.g. consecutive spaces) use the previous width.
9. **Trim the right-most block** off both images (the next, unknown letter bleeds into it):
   `adjustedBlueMargin = (blueMargin-left_boundary)-leftEdge-offset_x`.
10. **score** = `Jimp.diff(guess, redacted, 0.02).percent` over that bounded region (used for
    pruning + extension). **totalScore** = diff of the whole guess vs whole redacted (UI only).
    **tooBig** = redacted width < scaled guess width.

`Jimp.diff(a,b,0.02).percent` = fraction of pixels differing beyond a YIQ perceptual
threshold (Jimp wraps pixelmatch). Faithful default metric: `github.com/orisano/pixelmatch`
(maintained Go port of mapbox/pixelmatch), wrapped to return `[0,1]`.

## Guided search (preload.ts)

1. **Offset discovery** — for each of the `8×8=64` grid origins `(x,y)`, take the best
   single-char `score`; keep offsets with best `< 0.25`; sort ascending.
2. For each surviving offset: seed with every single char scoring `< 0.25`, sorted; then
   `guessRecursive`: render parent; if `tooBig` → prune; else append each charset char,
   compute the **marginal** score, keep `< 0.25` (`< 0.5` for space), sort, recurse. Stop at
   `maxLength`.

## Known limitations (documented gaps)

Requires *known* font family/size/weight and that the region is text; char bleed-over and
whitespace need fuzzy thresholds; variable-width fonts cascade position error (DFS mitigates);
unknown grid offset costs `blockSize²` permutations. **Rendering-engine fidelity** is the big
one: the original rasterizes via Chromium; an x/image rasterizer will not be byte-identical
(different hinting/AA). Therefore **port correctness is judged by self-consistency** (redact
with our own renderer → recover), with cross-engine fidelity against Chromium-produced
redactions (e.g. the original `secret.png`) treated as a Phase-2 (`chromedp` renderer) goal.

## Go libraries

- **Text→raster:** `golang.org/x/image/font/opentype` + `golang.org/x/image/font` (`font.Drawer`).
  Pure-Go, deterministic, pixel-level baseline/advance control. (Alternatives weighed:
  `fogleman/gg` — stale; `golang/freetype` — superseded; `tdewolff/canvas` — heavier than needed.)
- **Pixelate / images:** stdlib `image` + `golang.org/x/image/draw` for padding/compositing.
- **Metric:** `github.com/orisano/pixelmatch` (faithful default) behind a `Metric` interface;
  a simple RGB-fraction metric as an alternative.

## Package layout

```
github.com/oioio-space/unpixel
├── unpixel.go              // root: Engine, Config, Result, Eval, Offset; the 4 interfaces; progress types
├── internal/
│   ├── imutil/             // crop/pad/compose; blueMargin / leftEdge / margins detection
│   ├── pixelate/           // BlockAverage pixelator; grid-origin crop; white padding
│   ├── metric/             // pixelmatch (default) + rgb metrics
│   ├── render/             // xImageRenderer (x/image/font) + embedded fonts + sentinel block
│   └── search/             // offset discovery + marginal cropping + GuidedDFS
└── cmd/unpixel/            // thin CLI (later)
```

Pluggable interfaces (in root `unpixel`): `Renderer`, `Pixelator`, `Metric`, `Strategy`.

## Library-agnostic progress API

A single typed `Progress` event streamed on a **buffered channel** (with an `OnProgress`
callback adapter). High-frequency events (`EventCandidate`, `EventOffsetProbed`) are
drop-on-full (`select … default`); `EventNewBest` / `EventDone` are always delivered. The core
imports no UI package. Event carries: best guess + score, current guess + score, depth, offset,
evaluated count, estimated space, offsets done/total, optional preview images (opt-in to avoid
alloc churn), elapsed, done flag, err. See `unpixel.go` for the exact types.

## TDD build order (tests first per layer)

1. `imutil` — hand-built tiny RGBA images; assert exact columns from blueMargin/leftEdge/margins.
2. `pixelate` — golden 16×16 → each 8×8 block equals its mean; idempotence property.
3. `metric` — identical→0, inverted→~1, N-pixel diff→1/N (rgb backend); pixelmatch sanity.
4. `render` — golden PNG (tolerance for AA); `sentinelX` == measured text width.
5. `search` — pruning/threshold/space-threshold logic via a **mock Scorer** (no rendering).
6. `unpixel` end-to-end — **self-redaction round-trip**: redact known plaintext with our own
   pixelator at a known offset → `Engine.Run` recovers it. Plus ctx-cancel and progress-channel
   tests (EventDone always arrives). `internal/testdata/secret.png` kept as a Phase-2 fidelity
   fixture (not asserted to recover under the x/image renderer).

## Phase-2 improvements (behind the interfaces; faithful default unchanged)

Beam-search strategy; goroutine fan-out over chars/offsets (with deterministic merge);
SSIM/edge-aware metrics; auto block-size & offset inference (autocorrelation); `chromedp`
fidelity renderer; top-N candidate confidence/ambiguity reporting; cased/digit charsets +
dictionary priors; prefix-render memoization.
