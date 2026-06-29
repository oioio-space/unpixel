# Task 5 — Final validation: fingerprint-operator feature

**Status:** DONE
**Commit:** `a4dd18f` (feature) + follow-up fix (blur delegation)
**Branch:** `feat/fingerprint-operator`

## §2.3 test result

`TestRecover_autoEqualsManualBlur` now PASSES. On `testdata/blur/blur_go_s2.png` (text "go", σ=2):

- `RecoverBlurred(...)` → `BestGuess="go"` `BestTotal=0.0000` `BlurSigma=1.93`
- `Recover(WithAuto(), ...)` → `BestGuess="go"` `BestTotal=0.0000` (identical)

**Fix:** In `Recover`, after applying opts, when `cfg.autoBlur` is set and
`cfg.Pixelator == nil`, call `forensics.Fingerprint` early. If the detected
`op.Kind == KindBlur && op.Conf.Kind >= 0.5`, delegate immediately to
`RecoverBlurred(ctx, redacted, opts...)` and return its `Result`.

**Recursion safety:** `RecoverBlurred` → `recoverAtSigma` → `Recover(WithPixelator(blur))`.
At that point `cfg.Pixelator != nil`, so the delegation branch is skipped; the mosaic
engine runs normally. No recursion.

**Seam chosen:** the early check lives at the top of `Recover` (before `New`), keeping
`applyAutoFingerprint` and the mosaic path byte-identical for all non-blur inputs.

## `TestRecover` suite result (caged)

All 33 `TestRecover*` tests pass:

```
--- PASS: TestRecover_autoBlurSafeFallback (0.00s)
--- PASS: TestRecover_autoFingerprintInstallsLinear (0.00s)
--- PASS: TestRecover_autoEqualsManualBlur (0.12s)   ← §2.3 criterion
--- PASS: TestRecoverBlurred_matrix (5.90s)           ← 12/14 (2 skipped: too slow w/o LM)
--- PASS: TestRecover_roundTrip (1.91s)
... (all others pass)
```

## Panel non-regression

Mosaic panel: **17/17 exact (100%), fidelity 1.000**, total 744 ms (unchanged).

```
→ exact 17/17 (100.0%)  meanAcc 1.0000  meanFidelity 1.000  total 744.2 ms
Δ vs baseline: exact 100.0% → 100.0% (+0.0 pt), fidelity 1.000 → 1.000, time -2.8%
```

Blur matrix (`TestRecoverBlurred_matrix`): 12/12 tested pass, 2 skipped (connect σ3/σ6 —
too slow without language prior, as before).

## Lint

`mise run lint` → green, no new findings.

## Previous concern — resolved

The §2.3 architectural gap (mosaic engine running on blur inputs when `WithAuto()` is used)
is now closed. `Recover+WithAuto()` correctly routes blur-detected images through the
dedicated beam-search + σ-sweep pipeline.
