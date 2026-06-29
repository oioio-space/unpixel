# Task 2 Report — `internal/forensics`

**Status:** DONE  
**Commit:** `9a2fddd` on `feat/fingerprint-operator`  
**Test summary:** 2/2 PASS (`TestFingerprint_srgbMosaic`, `TestOperatorBuild_thresholdGate`); `BenchmarkFingerprint` 5136 ns/op, 0 B/op, 0 allocs/op  
**Lint:** clean (golangci-lint, gosec, govulncheck, cgo:check)

**Concerns:** None. The `Build` threshold gate for `KindMosaic` gates on `Conf.Gamma` (not `Conf.Kind`) as specified in the plan's Step 7 verbatim code — callers that want a purely Kind-confidence gate for blur should use `KindBlur` which gates on `Conf.Kind`.

---

## Review-fix patch (4 findings from commit 9a2fddd)

**Changes applied:**

- **FIX 1 (Critical) — dead-code Tool label:** Dropped the `Kernel` parameter from `mosaicTool`. The mosaic branch always produces `KernelUnknown` so the `linear+box3` compound guard was unreachable. The function now switches on `Gamma` alone: `GammaLinear → "GEGL/CSS"`, `GammaSRGB → "Photoshop/GIMP"`. Call site updated to `mosaicTool(op.Gamma)`; the now-redundant `mapKernel(bi.Kernel)` re-assignment in the mosaic branch removed (FIX 4).

- **FIX 2 (Important) — `Build` mosaic gate checks both confidences:** Changed `o.Block < 2 || o.Conf.Gamma < threshold` to `o.Block < 2 || o.Conf.Kind < threshold || o.Conf.Gamma < threshold`, enforcing spec §5 per-attribute confidence. Doc comment updated to reflect the dual gate. New test case added to `TestOperatorBuild_thresholdGate`: `Conf.Kind=0.2, Conf.Gamma=0.9` → `Build` returns `ok=false`.

- **FIX 3 (Minor) — `TestFingerprint_srgbMosaic` now asserts `Gamma`:** Added `op.Gamma != GammaSRGB` check with `got`-before-`want` `t.Errorf` message, so the sRGB colorspace branch is actually verified.

**Caged test run:**

```
$ ./scripts/gotest-caged.sh go test ./internal/forensics/ -v
=== RUN   TestFingerprint_srgbMosaic
--- PASS: TestFingerprint_srgbMosaic (0.00s)
=== RUN   TestOperatorBuild_thresholdGate
--- PASS: TestOperatorBuild_thresholdGate (0.00s)
PASS
ok  	github.com/oioio-space/unpixel/internal/forensics	0.002s
```

**Lint:** clean (golangci-lint v2, no findings)  
**Concerns:** None.
