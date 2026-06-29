# Fingerprint-operator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-detect the redaction's forward operator (mosaic-vs-blur, gamma, σ, kernel) for a given image and apply it to the existing generate-and-test path, with a safe low-confidence fallback that never regresses what already works.

**Architecture:** New `internal/forensics` package emits one `Operator` descriptor by aggregating the existing colorspace detector and a new `internal/pixelate.DetectBlur`. It imports only `internal/pixelate` (+ `imutil` + stdlib) — never the root `unpixel` package — so there is no import cycle. The root `unpixel` package keeps inferring block size / grid phase / x-stretch exactly as today and passes the block size into `forensics.Fingerprint` via a `Hint`. The auto-flags (`WithAuto`, new `WithAutoBlur`, and the existing `WithAutoColorspace`/`WithAutoCalibrate`) route through forensics; under a confidence threshold the engine falls back to today's exact default (byte-identical).

**Tech Stack:** Go (pure, `CGO_ENABLED=0`), `golang.org/x/image`, `internal/imutil`, `internal/pixelate`.

## Global Constraints

These apply to **every** task below.

- **Pure Go, no CGO.** Never add `import "C"` or a cgo-requiring dependency. The `cgo:check` gate enforces it. (Reference: spec §1, CLAUDE.md.)
- **Run tests caged**, never bare `go test`: `./scripts/gotest-caged.sh go test ./<pkg>/ -run <Name> -v`. (Memory: caged-go-test.)
- **Invariant**: panel fixtures **17/17 fidélité 1.000** and blur **13/14** must stay unchanged. The safe fallback guarantees this.
- **No comparing distances across different operators** in this sub-project. Selection is by *detection confidence*, never by `argmin(distance)` (that's deferred to #1B). (Spec §8.)
- **Benchmarks for new detection code** (`BenchmarkDetectBlur`, `BenchmarkFingerprint`, `b.ReportAllocs()`); prove any perf-affecting change with benchstat. (CLAUDE.md hot-path rule.)
- **Commit ritual (mandatory gate):** before each `git commit`, arm the `/simplify` marker in a **separate** bash call:
  ```bash
  GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
  ```
  then commit. Every commit message ends with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Existing signatures this plan consumes (verbatim):**
  - `pixelate.DetectColorspace(redacted *image.RGBA, block int) (linear bool, confidence float64)`
  - `pixelate.NewBlockAverage(blockSize int) *pixelate.BlockAverage`
  - `pixelate.NewLinearBlockAverage(blockSize int) *pixelate.BlockAverage`
  - `pixelate.NewGaussianBlur(sigma float64) *pixelate.GaussianBlur` (has `.Sigma() float64`)
  - `pixelate.NewFastBlur(sigma float64) *pixelate.FastBlur`
  - `imutil.ToRGBA(img image.Image) *image.RGBA`
  - root `unpixel.InferBlockSize(img image.Image) int`
  - All `pixelate` operator constructors return types satisfying `unpixel.Pixelator` (method `Pixelate(img *image.RGBA, originX, originY int) *image.RGBA`).

---

### Task 1: `pixelate.DetectBlur` — mosaic-vs-blur + σ + kernel detector

**Files:**
- Create: `internal/pixelate/detectblur.go`
- Test: `internal/pixelate/detectblur_test.go`

**Interfaces:**
- Consumes: `imutil.ToRGBA`, existing `pixelate.NewBlockAverage`/`NewGaussianBlur`/`NewFastBlur`.
- Produces:
  ```go
  // BlurKind classifies the redaction's forward operator family.
  type BlurKind uint8
  const (
      BlurKindUnknown BlurKind = iota
      BlurKindMosaic
      BlurKindGaussian
  )
  // BlurKernel distinguishes a true Gaussian from a 3-pass box approximation.
  type BlurKernel uint8
  const (
      BlurKernelUnknown BlurKernel = iota
      BlurKernelTrueGauss
      BlurKernelBox3
  )
  // BlurInfo is the result of DetectBlur.
  type BlurInfo struct {
      Kind   BlurKind
      Sigma  float64    // meaningful when Kind == BlurKindGaussian
      Kernel BlurKernel
      Conf   float64    // confidence of Kind in [0,1]
  }
  // DetectBlur classifies whether redacted is mosaic or Gaussian blur, estimates
  // sigma, and guesses the kernel family. block is the inferred mosaic block size
  // (>=2); pass 0 if unknown (then mosaic detection uses an autocorrelation grid).
  func DetectBlur(redacted *image.RGBA, block int) BlurInfo
  ```

**Algorithm (concrete):** A mosaic block is piecewise-constant: intra-block variance ≈ 0 at the true grid. Blur is smooth: intra-block variance is high and there is no constant-block tiling. Decision: compute mean per-block luminance variance over the `block`-grid; `meanIntraVar < mosaicVarEps` ⇒ mosaic. Otherwise blur. For blur, estimate σ from the average edge-spread: take the global gradient magnitude, fit σ via the relation between gradient spread and a Gaussian's std (calibrate against `NewGaussianBlur(σ).Pixelate` on a synthetic step). Kernel: re-blur a synthetic step with `NewGaussianBlur(σ)` vs `NewFastBlur(σ)` and keep whichever residual against the observed edge profile is smaller. `Conf = tanh(|meanIntraVar - mosaicVarEps| / scale)`.

- [ ] **Step 1: Write the failing test (mosaic is detected as mosaic)**

```go
package pixelate

import (
	"image"
	"testing"
)

// solidColumns renders a crude 2-tone "glyph" then mosaics it, to exercise detection.
func mosaicFixture(t *testing.T, block int) *image.RGBA {
	t.Helper()
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := byte(255)
			if (x/3+y/3)%2 == 0 { // some structure
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return NewBlockAverage(block).Pixelate(src, 0, 0)
}

func TestDetectBlur_mosaicClassifiedAsMosaic(t *testing.T) {
	img := mosaicFixture(t, 8)
	got := DetectBlur(img, 8)
	if got.Kind != BlurKindMosaic {
		t.Errorf("DetectBlur(mosaic).Kind = %v, want BlurKindMosaic", got.Kind)
	}
	if got.Conf < 0.5 {
		t.Errorf("DetectBlur(mosaic).Conf = %.2f, want >= 0.5", got.Conf)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run TestDetectBlur_mosaicClassifiedAsMosaic -v`
Expected: FAIL — `undefined: DetectBlur` (build error).

- [ ] **Step 3: Implement `DetectBlur` (types + mosaic path first)**

Create `internal/pixelate/detectblur.go` with the types from the Interfaces block and the mosaic detection: compute mean intra-block luminance variance over the `block` grid; if `block < 2`, return `BlurInfo{Kind: BlurKindUnknown}`. If `meanIntraVar < mosaicVarEps` (start `const mosaicVarEps = 4.0` in 8-bit² units) set `Kind: BlurKindMosaic`. Else `Kind: BlurKindGaussian` and estimate `Sigma`/`Kernel` (see Step 7). Set `Conf = math.Tanh(math.Abs(meanIntraVar-mosaicVarEps) / 8.0)`. Use `imutil.Lum601` for per-pixel luminance (already used in `detect.go`).

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run TestDetectBlur_mosaicClassifiedAsMosaic -v`
Expected: PASS.

- [ ] **Step 5: Write the failing test (blur is detected as blur, σ within tolerance)**

```go
func TestDetectBlur_gaussianClassifiedAndSigmaEstimated(t *testing.T) {
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := byte(255)
			if x >= w/2 { // single hard edge
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	const sigma = 3.0
	img := NewGaussianBlur(sigma).Pixelate(src, 0, 0)

	got := DetectBlur(img, 0)
	if got.Kind != BlurKindGaussian {
		t.Fatalf("DetectBlur(blur).Kind = %v, want BlurKindGaussian", got.Kind)
	}
	if got.Sigma < sigma*0.6 || got.Sigma > sigma*1.4 {
		t.Errorf("DetectBlur(blur).Sigma = %.2f, want within +-40%% of %.1f", got.Sigma, sigma)
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run TestDetectBlur_gaussianClassifiedAndSigmaEstimated -v`
Expected: FAIL — σ out of tolerance or Kind wrong (σ estimation not yet implemented).

- [ ] **Step 7: Implement σ estimation + kernel guess**

In the blur branch of `DetectBlur`: estimate σ by measuring the 10–90 % rise width of the sharpest edge (max horizontal luminance gradient row), mapping rise-width `r` to σ via `sigma = r / 2.563` (10–90 % width of a Gaussian erf). Guess kernel: render a synthetic step blurred with `NewGaussianBlur(sigma)` and `NewFastBlur(sigma)`, compare each edge profile's sum-of-squared-difference to the observed profile, set `Kernel` to the smaller; default `BlurKernelTrueGauss` on a tie.

- [ ] **Step 8: Run both DetectBlur tests to verify they pass**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run TestDetectBlur -v`
Expected: PASS (both subtests).

- [ ] **Step 9: Add the benchmark**

```go
var sinkBlur BlurInfo

func BenchmarkDetectBlur(b *testing.B) {
	img := mosaicFixture(b, 8)
	b.ReportAllocs()
	for b.Loop() {
		sinkBlur = DetectBlur(img, 8)
	}
}
```
(`mosaicFixture` takes `testing.TB`; change its parameter type to `testing.TB` so the benchmark can call it.)

- [ ] **Step 10: Run the benchmark to confirm it executes**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run x -bench BenchmarkDetectBlur -benchmem`
Expected: prints `BenchmarkDetectBlur-… ns/op … B/op`.

- [ ] **Step 11: Commit**

```bash
git add internal/pixelate/detectblur.go internal/pixelate/detectblur_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(pixelate): DetectBlur — classify mosaic vs Gaussian, estimate sigma+kernel

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `internal/forensics` — `Operator` descriptor + `Fingerprint`

**Files:**
- Create: `internal/forensics/forensics.go`
- Test: `internal/forensics/forensics_test.go`

**Interfaces:**
- Consumes: `pixelate.DetectColorspace`, `pixelate.DetectBlur` (Task 1), `pixelate.New{Block,LinearBlock}Average`, `pixelate.New{Gaussian,Fast}Blur`, `imutil.ToRGBA`.
- Produces:
  ```go
  package forensics

  type Kind uint8
  const (KindUnknown Kind = iota; KindMosaic; KindBlur)
  type Gamma uint8
  const (GammaUnknown Gamma = iota; GammaSRGB; GammaLinear)
  type Kernel uint8
  const (KernelUnknown Kernel = iota; KernelTrueGauss; KernelBox3)

  // Conf holds per-attribute detection confidence, each in [0,1].
  type Conf struct{ Kind, Gamma, Sigma float64 }

  // Operator describes the detected forward (redaction) operator.
  type Operator struct {
      Kind   Kind
      Gamma  Gamma
      Block  int     // from Hint (caller-inferred); 0 if unknown
      Sigma  float64 // when Kind == KindBlur
      Kernel Kernel
      Tool   string  // best-effort informative label
      Conf   Conf
  }

  // Hint carries what the caller already knows, to avoid re-detection.
  type Hint struct{ Block int } // 0 = unknown

  // Pixelator is the subset of unpixel.Pixelator this package constructs.
  // Structurally identical, so a returned value satisfies unpixel.Pixelator.
  type Pixelator interface {
      Pixelate(img *image.RGBA, originX, originY int) *image.RGBA
  }

  func Fingerprint(img image.Image, hint Hint) Operator
  // Build returns the forward operator for o, or ok=false when the decisive
  // attribute's confidence is below threshold (caller keeps its default).
  func (o Operator) Build(threshold float64) (Pixelator, bool)
  ```

- [ ] **Step 1: Write the failing test (Fingerprint on an sRGB mosaic)**

```go
package forensics

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

func srgbMosaic(block int) *image.RGBA {
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := byte(255)
			if (x/3+y/3)%2 == 0 {
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return pixelate.NewBlockAverage(block).Pixelate(src, 0, 0)
}

func TestFingerprint_srgbMosaic(t *testing.T) {
	op := Fingerprint(srgbMosaic(8), Hint{Block: 8})
	if op.Kind != KindMosaic {
		t.Errorf("Kind = %v, want KindMosaic", op.Kind)
	}
	if op.Block != 8 {
		t.Errorf("Block = %d, want 8", op.Block)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestFingerprint_srgbMosaic -v`
Expected: FAIL — package/`Fingerprint` undefined.

- [ ] **Step 3: Implement the types + `Fingerprint`**

Create `internal/forensics/forensics.go`. `Fingerprint`: `rgba := imutil.ToRGBA(img)`; `bi := pixelate.DetectBlur(rgba, hint.Block)`; map `bi.Kind`→`Kind`, `bi.Kernel`→`Kernel`, copy `Sigma`, `Conf.Kind = bi.Conf`. If `KindMosaic` and `hint.Block >= 2`: `linear, gconf := pixelate.DetectColorspace(rgba, hint.Block)`; set `Gamma` (GammaLinear if linear else GammaSRGB), `Conf.Gamma = gconf`. Set `Block = hint.Block`. Derive `Tool` best-effort: linear+box3 → "GEGL/CSS"; srgb+mosaic → "Photoshop"; else "".

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestFingerprint_srgbMosaic -v`
Expected: PASS.

- [ ] **Step 5: Write the failing test (`Build` returns operator above threshold, falls back below)**

```go
func TestOperatorBuild_thresholdGate(t *testing.T) {
	op := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.9, Gamma: 0.9}}
	if px, ok := op.Build(0.5); !ok || px == nil {
		t.Errorf("Build(0.5) ok=%v px=%v, want ok=true non-nil", ok, px)
	}
	low := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.2, Gamma: 0.2}}
	if _, ok := low.Build(0.5); ok {
		t.Errorf("Build(0.5) on low-confidence op = ok, want ok=false (fallback)")
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestOperatorBuild_thresholdGate -v`
Expected: FAIL — `Build` undefined.

- [ ] **Step 7: Implement `Build`**

```go
func (o Operator) Build(threshold float64) (Pixelator, bool) {
	switch o.Kind {
	case KindMosaic:
		if o.Block < 2 || o.Conf.Gamma < threshold {
			return nil, false // let caller use its default
		}
		if o.Gamma == GammaLinear {
			return pixelate.NewLinearBlockAverage(o.Block), true
		}
		return pixelate.NewBlockAverage(o.Block), true
	case KindBlur:
		if o.Conf.Kind < threshold || o.Sigma <= 0 {
			return nil, false
		}
		if o.Kernel == KernelBox3 {
			return pixelate.NewFastBlur(o.Sigma), true
		}
		return pixelate.NewGaussianBlur(o.Sigma), true
	default:
		return nil, false
	}
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestOperatorBuild_thresholdGate -v`
Expected: PASS.

- [ ] **Step 9: Add `BenchmarkFingerprint`**

```go
var sinkOp Operator

func BenchmarkFingerprint(b *testing.B) {
	img := srgbMosaic(8)
	b.ReportAllocs()
	for b.Loop() {
		sinkOp = Fingerprint(img, Hint{Block: 8})
	}
}
```

- [ ] **Step 10: Run package tests + benchmark**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -v` then
`./scripts/gotest-caged.sh go test ./internal/forensics/ -run x -bench BenchmarkFingerprint -benchmem`
Expected: all PASS; benchmark prints metrics.

- [ ] **Step 11: Commit**

```bash
git add internal/forensics/
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(forensics): Operator descriptor + Fingerprint with threshold-gated Build

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Wire forensics into `Recover` (safe fallback) + `WithAutoBlur`

**Files:**
- Modify: `unpixel.go` (the auto-colorspace block around `:541`; the options near `:1744`–`:1786`)
- Test: `recover_fingerprint_test.go` (root package `unpixel`, new file)

**Interfaces:**
- Consumes: `forensics.Fingerprint`, `forensics.Operator.Build`, `forensics.Hint`, root `InferBlockSize`.
- Produces: new option `func WithAutoBlur() Option`; extended behavior of `WithAuto()`. New unexported field `autoBlur bool` on `Config`.

**Design:** Add `autoBlur bool` to `Config`. In `Recover` (where `autoColorspace` is handled today, `unpixel.go:541`), replace the bespoke `DetectColorspace` call with a single forensics pass when `cfg.Pixelator == nil` and any of `autoColorspace || autoBlur` is set and `cfg.BlockSize >= 2`:
```go
if (cfg.autoColorspace || cfg.autoBlur) && cfg.Pixelator == nil && cfg.BlockSize >= 2 {
	op := forensics.Fingerprint(rgba, forensics.Hint{Block: cfg.BlockSize})
	if px, ok := op.Build(0.5); ok {
		cfg.Pixelator = px // forensics.Pixelator satisfies unpixel.Pixelator
	}
	// ok == false → leave Pixelator nil → DefaultComponents wires the standard
	// BlockAverage, byte-identical to today's default.
}
```
`WithAuto()` sets `autoBlur = true` in addition to the existing three. `WithAutoColorspace()` keeps working (it still enables the mosaic-gamma path through the same block). Keep `autoCalibrate` (grid phase / x-stretch seeding) exactly as is — unchanged.

- [ ] **Step 1: Write the failing test (low-confidence input ⇒ default unchanged)**

```go
package unpixel

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// A near-uniform image gives DetectColorspace/DetectBlur ~0 confidence, so
// WithAutoBlur must NOT install a non-default pixelator (safe fallback).
func TestRecover_autoBlurSafeFallback(t *testing.T) {
	img := pixelate.NewBlockAverage(8).Pixelate(image.NewRGBA(image.Rect(0, 0, 32, 16)), 0, 0)
	cfg := Config{BlockSize: 8}
	WithAutoBlur()(&cfg)
	WithAutoColorspace()(&cfg)
	applyAutoFingerprint(&cfg, ToRGBAExported(img)) // helper extracted in Step 3
	if cfg.Pixelator != nil {
		t.Errorf("Pixelator = %T, want nil (safe fallback on low-confidence input)", cfg.Pixelator)
	}
}
```
(If a `ToRGBAExported` shim is undesirable, the test may call `imutil.ToRGBA` directly via an internal test in package `unpixel`; `imutil` is already imported there.)

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./ -run TestRecover_autoBlurSafeFallback -v`
Expected: FAIL — `WithAutoBlur`/`applyAutoFingerprint` undefined.

- [ ] **Step 3: Implement the option, field, and extract `applyAutoFingerprint`**

Add `autoBlur bool` to `Config`. Add:
```go
// WithAutoBlur enables automatic mosaic-vs-blur detection: Recover fingerprints
// the target, and when confident installs the matching blur or block operator.
// Below the confidence threshold it leaves the default untouched. Default off.
func WithAutoBlur() Option { return func(c *Config) { c.autoBlur = true } }
```
Add `c.autoBlur = true` inside `WithAuto()`. Extract the wiring block into `func applyAutoFingerprint(cfg *Config, rgba *image.RGBA)` containing the snippet from the Design above, and call it from `Recover` where the old `autoColorspace` block was. Remove the old direct `pixelate.DetectColorspace` call (now inside forensics).

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./ -run TestRecover_autoBlurSafeFallback -v`
Expected: PASS.

- [ ] **Step 5: Write the failing test (confident linear mosaic ⇒ linear pixelator installed)**

```go
func TestRecover_autoFingerprintInstallsLinear(t *testing.T) {
	// Build a high-variance linear-light mosaic so DetectColorspace is confident.
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := byte(255)
			if (x/2+y/2)%2 == 0 {
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	img := pixelate.NewLinearBlockAverage(8).Pixelate(src, 0, 0)
	cfg := Config{BlockSize: 8}
	WithAuto()(&cfg)
	applyAutoFingerprint(&cfg, img)
	if _, ok := cfg.Pixelator.(*pixelate.BlockAverage); !ok {
		t.Errorf("Pixelator = %T, want *pixelate.BlockAverage (linear variant)", cfg.Pixelator)
	}
}
```

- [ ] **Step 6: Run test to verify it fails, then passes**

Run: `./scripts/gotest-caged.sh go test ./ -run TestRecover_autoFingerprintInstallsLinear -v`
Expected: PASS (the Step-3 implementation already covers it; if it fails, the confidence threshold or DetectColorspace mapping needs adjustment — fix `applyAutoFingerprint`).

- [ ] **Step 7: Non-regression — run the panel + blur via the journal-free fast checks**

Run: `mise run test` (full unit suite, caged by mise env) and confirm green. Then the recovery panel:
Run: `mise run bench:panel`
Expected: fixtures **17/17 fidélité 1.000**, blur **13/14** — unchanged from baseline.

- [ ] **Step 8: Commit**

```bash
git add unpixel.go recover_fingerprint_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(unpixel): route auto-flags through forensics + WithAutoBlur, safe fallback

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Expose `Operator` in `mcp/analyze`

**Files:**
- Modify: `mcp/analyze.go`
- Test: `mcp/analyze_test.go` (add a case)

**Interfaces:**
- Consumes: `forensics.Fingerprint`, `forensics.Hint`, the existing `AnalysisReport` struct in `mcp/analyze.go`.
- Produces: new fields on `AnalysisReport` carrying the detected operator (e.g. `ForwardOperator struct { Kind, Gamma, Kernel, Tool string; Sigma float64; Confidence float64 }`).

- [ ] **Step 1: Read the current report shape**

Run: `./scripts/rtkx.sh sed -n '1,80p' mcp/analyze.go` (or open the file). Identify `AnalysisReport` and where block size is computed.

- [ ] **Step 2: Write the failing test**

```go
func TestAnalyze_reportsForwardOperator(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	got, err := mcpserver.Analyze(img)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got.ForwardOperator.Kind == "" {
		t.Errorf("ForwardOperator.Kind = empty, want a detected kind (e.g. \"mosaic\")")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run TestAnalyze_reportsForwardOperator -v`
Expected: FAIL — `ForwardOperator` field undefined.

- [ ] **Step 4: Implement — add the field and populate it**

Add `ForwardOperator` (with string-ified enum values via small `func (k forensics.Kind) String()` helpers added in `internal/forensics` — add those `String()` methods with their own TDD step if not present, or map inline in analyze). In `Analyze`, after block size is known: `op := forensics.Fingerprint(rgba, forensics.Hint{Block: blockSize})` and fill the report fields.

- [ ] **Step 5: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run TestAnalyze_reportsForwardOperator -v`
Expected: PASS.

- [ ] **Step 6: Update the methods/resources doc if needed**

If `mcp/resources.go` documents the analyze report shape, add one line describing `ForwardOperator`. (Grep: `grep -n ForwardOperator mcp/resources.go`; skip if not documented there.)

- [ ] **Step 7: Commit**

```bash
git add mcp/analyze.go mcp/analyze_test.go internal/forensics/ mcp/resources.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(mcp): analyze reports the detected forward operator (forensics)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Validate the success criteria + journal entry

**Files:**
- Modify: `docs/JOURNAL.md` (trend note only — append-only; do NOT delete rows) — via the journal harness, not hand edits to tables.

**Interfaces:** none (validation task).

- [ ] **Step 1: Confirm criterion §2.3 — auto-blur equals manual `RecoverBlurred`**

Add a test in `recover_fingerprint_test.go` that, for one blur fixture, asserts `Recover(img, WithAuto())` yields the same `BestGuess` as the manual `RecoverBlurred` path. Run it caged. (If `RecoverBlurred`'s exact signature differs, read it first: `grep -n "func RecoverBlurred" unpixel.go`.)

- [ ] **Step 2: Run the full gate**

Run: `mise run ci`
Expected: lint + tests + cgo:check + scans all green.

- [ ] **Step 3: Benchstat the new detection code (no hot-path regression)**

Run: `mise run bench:baseline` was captured before Task 1; now run `mise run bench:compare` (or compare `internal/pixelate` + `internal/forensics` benches). Confirm no statistically-significant regression in the existing hot-path benches; the new `BenchmarkDetectBlur`/`BenchmarkFingerprint` are informational (run once per decode).

- [ ] **Step 4: Record a journal trend note**

Run the journal harness per CLAUDE.md (`mise run bench:panel:record`) so a row is appended and refresh the `## Analyse de tendance` note in `docs/JOURNAL.md` describing the fingerprint capability (append a new verdict block; do not delete existing rows — memory `journal-never-delete`).

- [ ] **Step 5: Final commit**

```bash
git add docs/JOURNAL.md benchmarks/ recover_fingerprint_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "test(forensics): validate auto-fingerprint success criteria + journal note

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §3 `internal/forensics` + `Operator` → Task 2. ✅
- §4 detection methods: gamma reused (Task 2 via DetectColorspace), block via Hint (Task 3 passes `InferBlockSize`), mosaic-vs-blur/σ/kernel new (Task 1), grid-phase/stretch stay in root `autoCalibrate` (unchanged — noted in Architecture). ✅ *(Deviation from spec §3: GridPhase/Stretch are NOT moved into the Operator struct, to avoid an import cycle; they remain in the working root path. Flagged to the user at handoff.)*
- §5 integration: `WithAuto()` extended + `WithAutoBlur()`, old options delegate, safe fallback → Task 3; MCP analyze → Task 4. ✅
- §6 tests: round-trip (Tasks 1–2), non-regression (Task 3 Step 7), benchmarks (Tasks 1,2,5), journal (Task 5). ✅
- §2 success criteria 1 (invariant) → Task 3 Step 7; 2 (round-trip ≥90%) → Tasks 1–2; 3 (auto=manual blur) → Task 5 Step 1; 4 (benchmarks) → Tasks 1,2,5. ✅

**Placeholder scan:** No TBD/TODO; every code step has concrete code. The one intentional read-first step (Task 4 Step 1) inspects an existing file whose exact shape isn't pre-known — acceptable.

**Type consistency:** `BlurKind`/`BlurKernel`/`BlurInfo` (Task 1) consumed by `Fingerprint` (Task 2); `Operator`/`Conf`/`Hint`/`Build`/`Pixelator` (Task 2) consumed by Task 3 (`applyAutoFingerprint`) and Task 4 (analyze). `Build` returns `(Pixelator, bool)` and `forensics.Pixelator` is structurally `unpixel.Pixelator` — consistent across Tasks 2–3.

**Known follow-ups (out of scope, in PROGRESS.md):** #1B operator-zoo + top-2 meta-strategy consumes this `Operator`; edge-handling detection and `Tool` labels are best-effort here.
