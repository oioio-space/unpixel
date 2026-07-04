# Geometry isolation diagnostic (P2)

Snapshot produced by `mise run geomeasure` (harness: `geomeasure_test.go`, build tag
`geomeasure`). Goal: for every `real` and `wild` image, run each pipeline stage
(**localize → grid → segment → font**) in isolation and record *where the first break is*,
BEFORE any decode — so decoder work targets the actual failing stage instead of guessing.

Machine-readable: `benchmarks/geometry/run-*.json`. Re-run regenerates this table.

## Real corpus (rich geometry ground truth: block / offset / font / lines)

| image | loc | sz | phX | phY | conf | errSz | errPhX | errPhY | segL | gtL | L✓ | top-3 fonts | gt-font | F✓ | verdict |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| hello-world.png | ✓ | 32 | 25 | 9 | 1.00 | 0 | 25 | 9 | 1 | 1 | ✓ | Source Code Pro<br>Noto Sans Mono<br>JetBrains Mono | Noto Sans Mono | ✓ | **ok** |
| hello-world-noisy.png | ✗ | 0 | 0 | 0 | 0.00 | 32 | 0 | 0 | 1 | 1 | ✓ | Liberation Sans<br>Liberation Serif<br>Liberation Mono | Noto Sans Mono | ✗ | **localize** |
| marx.png | ✓ | 0 | 0 | 0 | 0.00 | 19 | 5 | 5 | 2 | 2 | ✓ | Liberation Mono<br>Source Code Pro<br>JetBrains Mono | Noto Sans | ✗ | **grid** |

## Wild corpus (mosaic only — no geometry GT; text known for m4/m5)

| image | loc | sz | phX | phY | conf | robSz | robSup | segL | knLen | expAdv | plaus | top-3 fonts | verdict |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| m1_unredacter_secret.png | ✓ | 8 | 0 | 0 | 1.00 | 8 | 0.97 | 1 | 0 | 0.00 |  | Carlito<br>Liberation Sans<br>Liberation Serif | **unknown** |
| m2_depix_testimage1_pixels.png | ✓ | 5 | 3 | 0 | 1.00 | 15 | 0.99 | 1 | 0 | 0.00 |  | Carlito<br>Liberation Sans<br>Liberation Serif | **unknown** |
| m3_depix_testimage2_pixels.png | ✓ | 5 | 3 | 0 | 1.00 | 10 | 1.00 | 1 | 0 | 0.00 |  | Carlito<br>Liberation Sans<br>Liberation Mono | **unknown** |
| m4_depix_testimage3_pixels.png | ✓ | 5 | 0 | 0 | 1.00 | 15 | 1.00 | 1 | 25 | 8.20 | sub-glyph | Liberation Sans<br>Carlito<br>Liberation Serif | **unknown** |
| m5_depix_sublime_pixels.png | ✓ | 5 | 0 | 0 | 1.00 | 100 | 0.93 | 1 | 25 | 10.00 | sub-glyph | Caladea<br>Liberation Sans<br>Liberation Serif | **unknown** |

## Findings (actionable)

1. **`marx` breaks at the GRID stage, not font.** Localize succeeds, but `InferBlockGrid`
   returns `Size=0, Confidence=0.00` on a 19-px block with offset (5,5) rendered in a
   proportional sans-serif — the detector fails completely. Font mismatch (Noto Sans absent
   from the 9-font bundle → mono-heavy top-3) is *downstream* of an already-dead grid. **The
   grid detector must be fixed before font work can help marx.** This corrects the pre-diagnosis
   assumption that real was primarily a font-space problem.

2. **`hello-world-noisy` breaks at LOCALIZE.** 4 % salt-pepper noise defeats `LocateRedaction`
   and the grid simultaneously (`Size=0`). Confirms the noise path needs the denoise prepass to
   fire *before* geometry, not after.

3. **`hello-world` is fully healthy** (block=32 exact, true font in top-3, verdict `ok`) —
   yet real is 0/3 exact-match, so its remaining failure is purely in the decode/scoring tier,
   not geometry. (Note: `phX=25, phY=9` are recorded as non-zero but do not block recovery since
   the block size is exact; phase convention differs from the manifest's offset origin.)

4. **`wild` is NOT a localize failure.** All 5 mosaics localize and report a confident grid.
   The real problems are: (a) **sub-harmonic grid** — m5 gives `GridSize=5` but robust
   autocorrelation gives `robSz=100`; the GCD detector locks onto a sub-multiple; (b) **sub-glyph
   blocks** — m4/m5 block (5 px) is finer than one character advance (~8–10 px), so no cell maps
   1:1 to a glyph → a multi-block character model is required, not per-cell matching.

## Consequences for the program

- **real** → fix grid detection on small / non-uniform (proportional) blocks + non-zero offset
  (marx). Denoise-before-geometry ordering (noisy). THEN font-space expansion (P3) has a chance.
- **wild** → grid sub-harmonic disambiguation (prefer the autocorrelation period when GCD and
  autocorrelation disagree) + a sub-glyph / multi-block character model. Localize is fine.
