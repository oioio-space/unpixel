# Task 1 Report: `pixelate.DetectBlur`

## Status: DONE

## Files created

- `internal/pixelate/detectblur.go`
- `internal/pixelate/detectblur_test.go`

## Test summary

`TestDetectBlur_mosaicClassifiedAsMosaic` PASS, `TestDetectBlur_gaussianClassifiedAndSigmaEstimated` PASS.
`BenchmarkDetectBlur`: ~2 888 ns/op, 0 B/op, 0 allocs/op.

## Threshold adjustment

The plan's starting value `confScale = 8.0` produced `Conf = 0.46` for the mosaic fixture (just
under the required ≥ 0.5). Adjusted to `confScale = 5.0`; the mosaic fixture now returns
`Conf ≈ 0.57`. The `mosaicVarEps = 4.0` constant was kept as specified.

## Bugs fixed during implementation

- Index-out-of-range in the gradient-row loop: the original sketch used `pix[off-4]` with `off`
  starting at column 0 (x=1 iteration but `off` indexed at x=0). Fixed by computing `off0` and
  `off1` explicitly from `rowOff + (x-1)*4` and `rowOff + x*4`.

## Algorithm notes

- Mosaic detection: two-pass intra-block luminance variance over the `block` grid. Zero allocations.
- Sigma estimation: finds max-gradient-sum row, extracts luminance profile, measures 10-90% rise
  width, converts via `sigma = riseWidth / 2.563`. Falls back to 1.0 on degenerate inputs.
- Kernel guess: blurs a synthetic step with both `NewGaussianBlur` and `NewFastBlur`, picks the
  one with lower SSD against the observed mid-row profile; defaults to `BlurKernelTrueGauss` on tie.
- All paths are zero-alloc except `guessKernel` (two `Pixelate` calls that allocate their output
  images — acceptable for a one-shot detection call).

## Lint / gates

`mise run lint` clean. Full `pixelate` package test suite green (no regressions).
