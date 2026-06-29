# Operator-zoo + secured top-2 meta-strategy — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the operator fingerprint is ambiguous, recover correctly by trying the top-2 candidate operators and selecting via a secured rule (hard distance threshold + cross-operator text agreement + coherence-margin tiebreak, else abstain) — never a confident-wrong answer.

**Architecture:** A named tool-profile zoo (deduped to distinct operators) is ranked search-free by `forensics.FingerprintN`. In the ambiguous confidence band, `Recover` runs the top-2 profiles through the existing engine and a pure selection function in `forensics/meta.go` picks the winner or abstains. Confident (≥0.95) and very-low (<floor) inputs keep #2's single-operator and safe-fallback paths unchanged, so the panel invariant holds.

**Tech Stack:** Go (pure, `CGO_ENABLED=0`), `golang.org/x/image`, `internal/imutil`, `internal/pixelate`, `internal/forensics`.

## Global Constraints

Apply to **every** task.

- **Pure Go, no CGO.** Never add `import "C"` or a cgo dependency (`cgo:check` gate). (Spec §1.)
- **Run tests caged**: `./scripts/gotest-caged.sh go test ./<pkg>/ -run <Name> -v`. Never bare `go test`.
- **Invariant**: panel fixtures **17/17 fidélité 1.000** + blur **13/14** unchanged. The band never fires on confident inputs (≥0.95), so fixtures are untouched.
- **Securing**: selection is NEVER `argmin(distance)` across operators. Use: hard distance threshold → eligibility; cross-operator **text agreement** → winner; **coherence-margin** tiebreak on disagreement; else **abstain**. (Spec §4.)
- **Layering**: `internal/forensics` imports ONLY `internal/pixelate`, `internal/imutil`, stdlib — never root `unpixel` (import cycle). `meta.go` is pure: it takes already-decoded candidates, so it never invokes the engine.
- **Benchmarks for new search-free detection** (`BenchmarkFingerprintN`); prove perf-affecting changes with benchstat.
- **Commit ritual (mandatory gate):** before each `git commit`, arm the marker in a **separate** bash call:
  ```bash
  GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
  ```
  then commit. Stage only the task's files. Every commit message ends with:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** all work on `feat/operator-zoo` (create it before Task 1: `git checkout -b feat/operator-zoo`). Never commit on master.

**Existing signatures this plan consumes (verbatim, from #2 — merged at 172294b):**
- `forensics.Operator` struct: fields `Kind Kind`, `Gamma Gamma`, `Block int`, `Sigma float64`, `Kernel Kernel`, `Tool string`, `Conf Conf`.
- `forensics.Conf` struct: `Kind, Gamma, Sigma float64`.
- `forensics.Kind` {`KindUnknown, KindMosaic, KindBlur`}; `Gamma` {`GammaUnknown, GammaSRGB, GammaLinear`}; `Kernel` {`KernelUnknown, KernelTrueGauss, KernelBox3`}. Each has `String() string`.
- `forensics.Hint{Block int}`.
- `forensics.Fingerprint(img image.Image, hint Hint) Operator`.
- `forensics.Pixelator` interface: `Pixelate(img *image.RGBA, originX, originY int) *image.RGBA`.
- `forensics.Operator.Build(threshold float64) (Pixelator, bool)`.
- `pixelate.NewGaussianBlur(sigma float64) *GaussianBlur` (clamped edges; `radius = ceil(3σ)`; has `.Sigma()`).
- `pixelate.NewFastBlur(sigma float64) *FastBlur` (3-pass box). `pixelate.NewBlockAverage(b)`, `NewLinearBlockAverage(b)`.

---

### Task 1: `pixelate` Gaussian blur with selectable edge handling

**Files:**
- Create: `internal/pixelate/bluredge.go`
- Test: `internal/pixelate/bluredge_test.go`

**Interfaces:**
- Consumes: existing `pixelate.NewGaussianBlur` internals (kernel build, separable passes).
- Produces:
  ```go
  // Edge selects how a blur samples beyond the image border.
  type Edge uint8
  const (
      EdgeClamp   Edge = iota // repeat the border pixel (default; == NewGaussianBlur)
      EdgeReflect              // mirror across the border
      EdgeWrap                 // wrap to the opposite border
  )
  // NewGaussianBlurEdge returns a separable Gaussian blur (sigma in px) using the
  // given border mode. EdgeClamp is byte-identical to NewGaussianBlur(sigma).
  func NewGaussianBlurEdge(sigma float64, edge Edge) *GaussianBlur
  ```
  (Add an `edge Edge` field to the existing `GaussianBlur` struct; the index math in the H and V passes maps an out-of-range index `i` to a valid one per `edge`.)

- [ ] **Step 1: Write the failing test — EdgeClamp byte-identical to NewGaussianBlur**

```go
package pixelate

import (
	"bytes"
	"image"
	"testing"
)

func edgeTestSrc() *image.RGBA {
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			c := byte(0)
			if x >= 8 {
				c = 255
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return src
}

func TestNewGaussianBlurEdge_clampMatchesDefault(t *testing.T) {
	src := edgeTestSrc()
	got := NewGaussianBlurEdge(2.0, EdgeClamp).Pixelate(src, 0, 0)
	want := NewGaussianBlur(2.0).Pixelate(src, 0, 0)
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Errorf("EdgeClamp output differs from NewGaussianBlur(2.0); want byte-identical")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run TestNewGaussianBlurEdge_clampMatchesDefault -v`
Expected: FAIL — `undefined: NewGaussianBlurEdge` / `EdgeClamp`.

- [ ] **Step 3: Implement Edge + NewGaussianBlurEdge**

In `bluredge.go`: define `Edge` + constants. Add an `edge Edge` field to `GaussianBlur` (in blur.go, or keep struct in blur.go and set field here — set it in the constructor). Implement `NewGaussianBlurEdge` reusing the same kernel build as `NewGaussianBlur` (extract a shared `newGaussianKernel(sigma)` if cleaner, DRY). Refactor the H and V passes (in blur.go `Pixelate`) to map the sampled index through a helper `func sampleIndex(i, n int, edge Edge) int` (clamp/reflect/wrap). `NewGaussianBlur` sets `edge=EdgeClamp` so its behavior is unchanged. Keep the existing pool/contract.

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run TestNewGaussianBlurEdge_clampMatchesDefault -v`
Expected: PASS.

- [ ] **Step 5: Write the failing test — reflect/wrap differ at the border, agree in the interior**

```go
func TestNewGaussianBlurEdge_modesDifferAtBorder(t *testing.T) {
	src := edgeTestSrc()
	clamp := NewGaussianBlurEdge(2.0, EdgeClamp).Pixelate(src, 0, 0)
	reflect := NewGaussianBlurEdge(2.0, EdgeReflect).Pixelate(src, 0, 0)
	wrap := NewGaussianBlurEdge(2.0, EdgeWrap).Pixelate(src, 0, 0)
	// Interior pixel far from any border: all modes equal.
	cx := clamp.PixOffset(8, 8)
	if clamp.Pix[cx] != reflect.Pix[cx] || clamp.Pix[cx] != wrap.Pix[cx] {
		t.Errorf("interior pixel differs across edge modes; want equal")
	}
	// Left-border column 0: wrap pulls in the white right edge, so it differs from clamp.
	bx := clamp.PixOffset(0, 8)
	if clamp.Pix[bx] == wrap.Pix[bx] {
		t.Errorf("border pixel identical for clamp and wrap; want different")
	}
}
```

- [ ] **Step 6: Run the test, verify pass (fix sampleIndex if needed)**

Run: `./scripts/gotest-caged.sh go test ./internal/pixelate/ -run TestNewGaussianBlurEdge -v`
Expected: PASS (both). If border test fails, the `sampleIndex` wrap/reflect math is off — fix it.

- [ ] **Step 7: Commit**

```bash
git add internal/pixelate/bluredge.go internal/pixelate/bluredge_test.go internal/pixelate/blur.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(pixelate): NewGaussianBlurEdge — selectable border handling (clamp/reflect/wrap)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Named tool-profile zoo (deduped to distinct operators)

**Files:**
- Create: `internal/forensics/profiles.go`
- Test: `internal/forensics/profiles_test.go`

**Interfaces:**
- Consumes: `forensics.Kind/Gamma/Kernel`, `pixelate.NewGaussianBlurEdge` (Task 1), existing `pixelate` constructors.
- Produces:
  ```go
  // Profile is a named redaction-tool forward-operator configuration.
  type Profile struct {
      Tool   string  // "GEGL", "Photoshop", "GIMP", "CSS", "ffmpeg", "OpenCV"
      Kind   Kind
      Gamma  Gamma   // mosaic only
      Kernel Kernel  // blur only
      Edge   pixelate.Edge // blur only
  }
  // Zoo returns the catalogue of known tool profiles.
  func Zoo() []Profile
  // configKey is the dedup key: profiles with the same key build the same operator.
  func (p Profile) configKey(block int, sigma float64) string
  ```
  Note: the *operator construction* for a profile is added in Task 3 (it needs block/sigma from the observed image). Task 2 delivers the catalogue + the dedup key only.

- [ ] **Step 1: Write the failing test — Zoo has the named tools and dedups by config**

```go
package forensics

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

func TestZoo_namesAndDedup(t *testing.T) {
	zoo := Zoo()
	names := map[string]bool{}
	for _, p := range zoo {
		names[p.Tool] = true
	}
	for _, want := range []string{"GEGL", "Photoshop", "CSS", "ffmpeg"} {
		if !names[want] {
			t.Errorf("Zoo() missing tool %q", want)
		}
	}
	// Two mosaic profiles with the same gamma must share a config key (dedup).
	a := Profile{Tool: "GEGL", Kind: KindMosaic, Gamma: GammaLinear}
	b := Profile{Tool: "CSS", Kind: KindMosaic, Gamma: GammaLinear}
	if a.configKey(8, 0) != b.configKey(8, 0) {
		t.Errorf("same-config profiles got different keys: %q vs %q", a.configKey(8, 0), b.configKey(8, 0))
	}
	// Different gamma → different key.
	c := Profile{Tool: "Photoshop", Kind: KindMosaic, Gamma: GammaSRGB}
	if a.configKey(8, 0) == c.configKey(8, 0) {
		t.Errorf("linear and sRGB mosaic share a key; want distinct")
	}
	_ = pixelate.EdgeClamp // ensure pixelate import is used
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestZoo_namesAndDedup -v`
Expected: FAIL — `undefined: Zoo` / `Profile`.

- [ ] **Step 3: Implement Profile, Zoo, configKey**

`profiles.go`: define `Profile`; `Zoo()` returns a literal slice covering GEGL (linear mosaic + box3 blur), Photoshop (sRGB mosaic + true-gauss blur), GIMP (sRGB mosaic), CSS (linear mosaic + box3 blur clamp), ffmpeg (true-gauss blur), OpenCV (true-gauss blur). `configKey` builds a string from the operator-determining fields only (Kind, Gamma for mosaic incl. block; Kind, Kernel, Edge, sigma bucket for blur) — NOT Tool — so same-config different-tool profiles collide. Use `fmt.Sprintf`.

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestZoo_namesAndDedup -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/forensics/profiles.go internal/forensics/profiles_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(forensics): named tool-profile zoo with config dedup key

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `FingerprintN` — rank the zoo (search-free); `Fingerprint` delegates

**Files:**
- Modify: `internal/forensics/forensics.go`
- Test: `internal/forensics/fingerprintn_test.go`

**Interfaces:**
- Consumes: existing `Fingerprint` detection (DetectBlur/DetectColorspace), `Zoo()` + `Profile` (Task 2), `Profile.configKey`.
- Produces:
  ```go
  // FingerprintN ranks the whole tool zoo against img, most-likely first.
  // It is search-free (no candidate render). The returned Operators carry the
  // observed Block/Sigma and a per-operator Conf reflecting agreement with the
  // detected signature. Profiles that resolve to the same operator config are
  // deduplicated. FingerprintN(img,hint)[0] equals Fingerprint(img,hint).
  func FingerprintN(img image.Image, hint Hint) []Operator
  ```
  `Fingerprint` is refactored to `return FingerprintN(img, hint)[0]` (keep its doc).

- [ ] **Step 1: Write the failing test — ranking + Fingerprint delegation equivalence**

```go
package forensics

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

func srgbMosaicN(block int) *image.RGBA {
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
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

func TestFingerprintN_ranksAndDelegates(t *testing.T) {
	img := srgbMosaicN(8)
	ranked := FingerprintN(img, Hint{Block: 8})
	if len(ranked) == 0 {
		t.Fatalf("FingerprintN returned no operators")
	}
	// Top operator must equal the singular Fingerprint (delegation contract).
	got := ranked[0]
	want := Fingerprint(img, Hint{Block: 8})
	if got.Kind != want.Kind || got.Gamma != want.Gamma || got.Block != want.Block {
		t.Errorf("FingerprintN[0] = {%v,%v,%d}, Fingerprint = {%v,%v,%d}; want equal",
			got.Kind, got.Gamma, got.Block, want.Kind, want.Gamma, want.Block)
	}
	// Confidence is monotonic non-increasing.
	for i := 1; i < len(ranked); i++ {
		if ranked[i].Conf.Kind > ranked[i-1].Conf.Kind {
			t.Errorf("ranking not sorted by Conf.Kind at %d: %.3f > %.3f", i, ranked[i].Conf.Kind, ranked[i-1].Conf.Kind)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestFingerprintN_ranksAndDelegates -v`
Expected: FAIL — `undefined: FingerprintN`.

- [ ] **Step 3: Implement FingerprintN + refactor Fingerprint**

In `forensics.go`: `FingerprintN` computes the observed signature ONCE (reuse the current `Fingerprint` body: DetectBlur + DetectColorspace), then for each deduped `Zoo()` profile builds an `Operator{Kind,Gamma,Kernel from profile; Block/Sigma from observed; Tool from profile.Tool}` with a `Conf` scored by agreement: `Conf.Kind = observed.Conf.Kind` scaled by (profile.Kind==observed.Kind ? 1 : penalty), similarly `Conf.Gamma`. Dedup via `configKey`. Sort by `Conf.Kind` desc (tiebreak `Conf.Gamma` desc). Ensure the observed operator (matching detected signature) sorts first so `[0]` == `Fingerprint`. Refactor `Fingerprint` to `return FingerprintN(img, hint)[0]`. Keep the existing `Fingerprint` doc comment.

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestFingerprintN -v`
Expected: PASS. Then run the whole package to confirm #2's tests still pass:
`./scripts/gotest-caged.sh go test ./internal/forensics/ -v` → all PASS.

- [ ] **Step 5: Add `BenchmarkFingerprintN`**

```go
var sinkRanked []Operator

func BenchmarkFingerprintN(b *testing.B) {
	img := srgbMosaicN(8)
	b.ReportAllocs()
	for b.Loop() {
		sinkRanked = FingerprintN(img, Hint{Block: 8})
	}
}
```

- [ ] **Step 6: Run the benchmark**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run x -bench BenchmarkFingerprintN -benchmem`
Expected: prints ns/op + allocs (search-free → cheap; should be within a small multiple of BenchmarkFingerprint).

- [ ] **Step 7: Commit**

```bash
git add internal/forensics/forensics.go internal/forensics/fingerprintn_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(forensics): FingerprintN ranks the tool zoo; Fingerprint delegates to [0]

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `meta.go` — pure secured selection

**Files:**
- Create: `internal/forensics/meta.go`
- Test: `internal/forensics/meta_test.go`

**Interfaces:**
- Consumes: `forensics.Operator` (its `Conf.Kind`).
- Produces:
  ```go
  // Candidate is one decoded hypothesis from the engine: an operator and the
  // text it recovered with its image distance (lower = better fit).
  type Candidate struct {
      Op   Operator
      Text string
      Dist float64
  }
  // Selection is the meta-strategy verdict.
  type Selection struct {
      Op   Operator
      Text string
  }
  // Select applies the secured rule (see spec §4):
  //   1. eligible = candidates with Dist < distThreshold.
  //   2. none eligible → abstain (ok=false).
  //   3. exactly one eligible → it wins.
  //   4. ≥2 eligible AND all eligible share the same Text → that text wins.
  //   5. eligible disagree → winner is the highest-Conf.Kind eligible IFF its
  //      lead over the runner-up exceeds coherenceMargin; else abstain.
  // ok=false means the caller must fall back (no confident answer).
  func Select(cands []Candidate, distThreshold, coherenceMargin float64) (Selection, bool)
  ```

- [ ] **Step 1: Write the failing tests (table-driven)**

```go
package forensics

import "testing"

func TestSelect_securedRule(t *testing.T) {
	const dt, cm = 0.2, 0.25
	op := func(conf float64) Operator { return Operator{Conf: Conf{Kind: conf}} }
	tests := []struct {
		name      string
		cands     []Candidate
		wantOK    bool
		wantText  string
	}{
		{"none eligible (all above threshold)", []Candidate{{op(.9), "go", 0.5}, {op(.8), "ho", 0.6}}, false, ""},
		{"single eligible wins", []Candidate{{op(.9), "go", 0.05}, {op(.8), "ho", 0.6}}, true, "go"},
		{"agreement wins", []Candidate{{op(.6), "go", 0.05}, {op(.55), "go", 0.07}}, true, "go"},
		{"disagree, decisive coherence lead", []Candidate{{op(.9), "go", 0.05}, {op(.5), "ho", 0.07}}, true, "go"},
		{"disagree, indecisive lead → abstain", []Candidate{{op(.6), "go", 0.05}, {op(.55), "ho", 0.07}}, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, ok := Select(tc.cands, dt, cm)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && sel.Text != tc.wantText {
				t.Errorf("Text = %q, want %q", sel.Text, tc.wantText)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestSelect_securedRule -v`
Expected: FAIL — `undefined: Select`.

- [ ] **Step 3: Implement Select**

`meta.go`: filter eligible (`Dist < distThreshold`); 0 → `(Selection{}, false)`; 1 → that one; ≥2 → if all eligible `Text` equal → that text (pick the lowest-Dist among them for `Op`); else sort eligible by `Conf.Kind` desc and if `conf[0]-conf[1] > coherenceMargin` → `eligible[0]` else `(Selection{}, false)`. Pure; no I/O, no engine.

- [ ] **Step 4: Run to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/forensics/ -run TestSelect_securedRule -v`
Expected: PASS (all 5 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/forensics/meta.go internal/forensics/meta_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(forensics): meta.Select — secured top-2 selection (agreement + coherence + abstain)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Wire the banded meta-strategy into `Recover`

**Files:**
- Modify: `unpixel.go` (the `applyAutoFingerprint`/blur-delegation region in `Recover`, ~lines 540-595 and ~1905-1945)
- Test: `recover_meta_test.go` (package `unpixel`)

**Interfaces:**
- Consumes: `forensics.FingerprintN`, `forensics.Operator.Build`, `forensics.Candidate`, `forensics.Select`, the engine (`New`+`Run`) via a small per-operator decode helper.
- Produces: no new public option (activates under existing `WithAuto()`); behavior change only in the ambiguous band.

**Design:** In `Recover`, when `cfg.autoBlur && cfg.Pixelator == nil` (the existing auto entry):
```
ranked := forensics.FingerprintN(rgba, forensics.Hint{Block: cfg.BlockSize})
top := ranked[0]
switch {
case top.Conf.Kind >= 0.95:        // confident → existing single-op path (#2), unchanged
case top.Conf.Kind < 0.30:         // floor → safe fallback (default), unchanged
default:                            // ambiguous band: top-2 + meta
    var cands []forensics.Candidate
    for _, op := range ranked[:min(2,len(ranked))] {
        px, ok := op.Build(0)      // threshold 0: build unconditionally for the trial
        if !ok { continue }
        res := runWithPixelator(ctx, redacted, cfg, px)   // engine run, Pixelator set (no recursion)
        cands = append(cands, forensics.Candidate{Op: op, Text: res.BestGuess, Dist: res.BestTotal})
    }
    if sel, ok := forensics.Select(cands, distThreshold, coherenceMargin); ok {
        return the Result whose BestGuess == sel.Text   // (kept from the trial runs)
    }
    // abstain → fall through to existing single-best/default path
}
```
Keep the trial `Result`s (map text→Result) so the winner is returned without re-running. `runWithPixelator` sets `WithPixelator(px)` so the inner path does NOT re-enter auto-fingerprint (recursion guard, same as #2's blur delegation). Constants `distThreshold`/`coherenceMargin` are package consts, calibrated in Task 6.

- [ ] **Step 1: Write the failing test — ambiguous fixture: meta recovers where single-best misses**

```go
package unpixel

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// A linear-light mosaic whose colorspace confidence sits in the ambiguous band:
// single-best (#2) may pick sRGB and miss; the top-2 meta should try both and,
// via agreement/coherence, recover the truth. Built from the varying-fill source.
func ambiguousLinearMosaic(t *testing.T) (*image.RGBA, string) {
	t.Helper()
	// Reuse the project's known-good fixture path is simplest:
	img, err := loadTestImage("testdata/fixtures/block08_go.png") // helper below
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return imutilToRGBA(img), "go"
}

func TestRecover_metaRecoversAmbiguous(t *testing.T) {
	img, want := ambiguousLinearMosaic(t)
	res, err := Recover(t.Context(), img, WithAuto(), WithCharset("go abcde"), WithMaxLength(3))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if res.BestGuess != want {
		t.Errorf("BestGuess = %q, want %q", res.BestGuess, want)
	}
}
```
NOTE to implementer: this test's exact fixture must be one that genuinely lands in the ambiguous band (top-1 Conf.Kind in [0.30,0.95)). FIRST probe candidate fixtures with a throwaway `FingerprintN` print to pick one whose `Conf.Kind` is in-band; if none of the committed fixtures land in-band, synthesize one (linear mosaic with moderate variance) the way `internal/forensics` fixtures do, and assert the band membership in the test setup. Do not weaken the assertion. Replace `loadTestImage`/`imutilToRGBA` with the real helpers already in the package (grep `func.*image.Image` in existing `*_test.go`).

- [ ] **Step 2: Run to verify it fails (or is not yet in-band)**

Run: `./scripts/gotest-caged.sh go test ./ -run TestRecover_metaRecoversAmbiguous -v`
Expected: FAIL initially (meta path not wired).

- [ ] **Step 3: Implement the banded meta wiring + `runWithPixelator`**

Add the banded block above to `Recover`. Add an unexported `runWithPixelator(ctx, img, cfg, px) Result` that clones `cfg`, sets `cfg.Pixelator = px`, builds the engine and runs it (mirror how `Recover` already builds+runs `New`/`Run`). Add package consts `metaBandLow = 0.30`, `metaBandHigh = 0.95`, `metaDistThreshold`, `metaCoherenceMargin` (start `0.10`, `0.25`; tune in Task 6). Use `min` builtin.

- [ ] **Step 4: Run to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./ -run TestRecover_metaRecoversAmbiguous -v`
Expected: PASS.

- [ ] **Step 5: Write the abstain test — disagreement does not produce confident-wrong**

```go
func TestRecover_metaAbstainsOnDisagreement(t *testing.T) {
	// A target where the two operators disagree and neither is decisively more
	// coherent must NOT yield a high-fidelity wrong answer: the meta abstains and
	// the result falls back (low confidence). Assert we do not return a confident
	// wrong string — i.e. Fidelity() is not ~1.0 with a wrong guess.
	img, want := ambiguousLinearMosaic(t)
	res, err := Recover(t.Context(), img, WithAuto(), WithCharset("go abcde"), WithMaxLength(3))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if res.BestGuess != want && res.Fidelity() > 0.9 {
		t.Errorf("confident-wrong: guess=%q (want %q) at fidelity %.2f", res.BestGuess, want, res.Fidelity())
	}
}
```
NOTE: if the chosen fixture always agrees (so abstain never triggers), construct a second fixture that forces disagreement (e.g. a borderline mosaic-vs-blur input) for this test; the assertion is the contract (no confident-wrong), not the specific path.

- [ ] **Step 6: Run abstain test**

Run: `./scripts/gotest-caged.sh go test ./ -run TestRecover_metaAbstains -v`
Expected: PASS.

- [ ] **Step 7: Non-regression — panel + blur**

Run: `mise run bench:panel`
Expected: fixtures **17/17 fidélité 1.000**, blur **13/14** — unchanged (band never fires on confident fixtures). If a fixture regressed, the band thresholds are wrong (a confident fixture entered the band) — fix `metaBandHigh`/the confidence scoring.

- [ ] **Step 8: Commit**

```bash
git add unpixel.go recover_meta_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(unpixel): banded top-2 meta-strategy in Recover under WithAuto()

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Validate, calibrate, coverage

**Files:**
- Modify: `recover_meta_test.go` (calibration assertions if needed); `internal/forensics/*_test.go` (coverage)

**Interfaces:** none (validation/calibration).

- [ ] **Step 1: Calibrate the band/threshold constants**

With a throwaway print, record `FingerprintN[0].Conf.Kind` and per-operator `BestTotal` for: the 17 fixtures (must be ≥0.95 → confident, never in-band), 2-3 blur fixtures, and the ambiguous fixtures from Task 5. Adjust `metaBandHigh` (≥ every fixture's confidence is NOT required — fixtures must be ABOVE it) / `metaDistThreshold` / `metaCoherenceMargin` so: (a) all 17 fixtures stay confident, (b) the ambiguous fixture recovers, (c) the disagreement fixture abstains. Document the chosen values with a one-line rationale comment each.

- [ ] **Step 2: Coverage — ensure forensics stays ≥ 85% overall**

Run: `./scripts/gotest-caged.sh go test -coverprofile=/tmp/c.out ./internal/forensics/ && go tool cover -func=/tmp/c.out | tail -1`
If any new function (profiles/FingerprintN/meta paths) is under-covered, add a direct unit test asserting real behavior (no assert-nothing padding). Then `mise run cover:check` → must report ≥ 85% PASS. Paste the line.

- [ ] **Step 3: Full gate**

Run: `mise run ci > /tmp/ci.log 2>&1; echo "EXIT=$?"; grep -E "Total coverage|ERROR|cover:check" /tmp/ci.log`
Expected: `EXIT=0`, coverage ≥ 85%, no ERROR. (Note: `mise run ci` exit code — do not mask it behind a pipe.)

- [ ] **Step 4: Benchstat the search path**

Confirm the meta band is bounded: `BenchmarkFingerprintN` is search-free (cheap); the 2× cost is only in-band. No hot-path (`render`/`search`/`pixelate`/`metric`) regression — run `mise run bench:baseline` was captured at branch start; if not, compare `internal/pixelate` + `internal/forensics` benches before/after for no regression on shared functions. The new `NewGaussianBlurEdge`'s clamp path must not slow `NewGaussianBlur` (byte-identical; benchstat `BenchmarkGaussianBlur` if present).

- [ ] **Step 5: Commit any calibration/coverage changes**

```bash
git add -A
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "test(forensics): calibrate meta band + cover zoo/meta paths

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §3.1 FingerprintN (rank zoo, search-free, Fingerprint delegates) → Task 3. ✅
- §3.2 named-profile zoo deduped → Task 2. ✅
- §3.3 new pixelate operators (edge handling) → Task 1. ✅ (box-N truncation omitted — YAGNI; no profile in Task 2's zoo requires it. If Task 2 adds a profile needing it, extend Task 1.)
- §3.4 meta.go pure secured selection → Task 4; wired in Recover → Task 5. ✅
- §4 securing (hard distance threshold + agreement + coherence-margin tiebreak + abstain) → Task 4 (`Select`) + Task 5 (band). ✅
- §5 integration under WithAuto(), single/fallback unchanged → Task 5. ✅
- §6 tests: round-trip ambiguity (T5 S1), anti-confident-wrong abstain (T5 S5), panel/blur non-regression (T5 S7), benchmarks (T3 S5, T6 S4). ✅
- §2 success criteria 1 (invariant) → T5 S7; 2 (gain) → T5 S1; 3 (no confident-wrong) → T5 S5; 4 (cost bounded) → T6 S4. ✅

**Placeholder scan:** No TBD/TODO. Two tasks carry explicit implementer NOTES (T5 fixture must land in-band; pick real helpers) — these are calibration realities, not placeholders; each says exactly what to verify and forbids weakening the assertion.

**Type consistency:** `Profile`/`Zoo`/`configKey` (T2) → consumed by `FingerprintN` (T3). `Operator`/`Conf.Kind` (#2) → `Candidate`/`Select` (T4) → `Recover` (T5). `pixelate.Edge`/`NewGaussianBlurEdge` (T1) → `Profile.Edge` (T2). `Select(cands, distThreshold, coherenceMargin) (Selection, bool)` consistent across T4/T5. `Fingerprint` delegates to `FingerprintN[0]` (T3) — contract asserted in T3 S1.

**Known deferral (in PROGRESS.md / memory, out of scope):** impulse-noise filter for `hello-world-noisy`; box-N kernel truncation.
