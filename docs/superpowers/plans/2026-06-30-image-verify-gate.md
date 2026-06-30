# Image-Restoration Verify Gate (#7) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A pure-Go `unpixel.VerifyImage` that re-applies the forward operator to an externally-restored image and physics-compares it to the redaction (the missing anti-hallucination gate), plus an MCP `unpixel_verify_image` tool and a documented sidecar protocol. The diffusion model itself is deferred (external sidecar).

**Architecture:** `VerifyImage` is the lower half of the existing `Verify`: `Verify(text) ≡ VerifyImage(render(text))`. Refactor `Verify`'s prep prologue into a shared `prepareVerify` helper (byte-identical), then add `VerifyImage` + an `ImageVerdict` type + a `DefaultVerifyImageCore` hook (mirroring `DefaultVerifyCore`) implemented in `defaults` as `verifyImageCore` (resize restored → re-pixelate over grid phases → metric, take min). MCP gets a `verify_image` tool; `docs/` gets the sidecar protocol. No new package, no `//go:build` tag, no `os/exec`.

**Tech Stack:** Pure Go (no CGO). Reuses `Verify`'s prologue, `cfg.Pixelator`/`cfg.Metric`, `internal/imutil.ToRGBA`, `golang.org/x/image/draw` (already a dep via `golang.org/x/image v0.43.0`) for the pure-Go resize.

## Global Constraints

- **NO CGO, ever.** `CGO_ENABLED=0` is pinned; enforced by `mise run cgo:check`. No new dependency beyond `golang.org/x/image/draw` (already present).
- **NO `os/exec` / no subprocess.** The repo has never shelled out; the restorer is an *external* process orchestrated by the caller/LLM (like today's string propose→verify). Do not add subprocess code.
- **NO `//go:build` seam.** Unlike #4/#5 (in-Go forward passes), #7's model is external — there is nothing in-Go to build-tag. Do not create one.
- **Opt-in / byte-identical:** `Verify`/`Recover`/core search/the 17/17 panel are untouched. The `prepareVerify` extraction must leave `Verify`'s behaviour byte-identical (mechanical refactor — same steps, same order, same hooks).
- **Caged tests only:** never run `go test` bare. Use `scripts/gotest-caged.sh` for every test run.
- **In-memory fixtures only:** every test image is rendered/pixelated in code; never load a gitignored or network fixture.
- **Coverage gate:** `COVER_MIN=85`; keep ≥ 85% (`mise run cover:check`).
- **Commit gate:** arm the `/simplify` marker (`$GIT_DIR/claude-simplify-ok`, `git diff --cached | sha1sum | cut -d' ' -f1`) only AFTER genuinely reviewing, in a SEPARATE bash call from `git commit`. Run `git restore --staged PROGRESS.md` before arming.
- **Commit trailer:** every commit message ends with
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** `feat/image-verify-gate` (already created from `master`; do not commit on `master`).
- Follow `go-style-guide` and `use-modern-go` (`for range`, `min`/`max`, `any`, `t.Context()`, table tests with field names, `got` before `want`).

## Verified facts (do not re-litigate)

- `verify.go:48` `Verify(ctx, img image.Image, candidates []string, opts ...Option) ([]Verdict, error)`: nil img → `ErrNilImage`; `DefaultVerifyCore == nil` → `ErrNoComponents`; then a **prep prologue** (verify.go:56-133: build cfg from opts → auto flags when no Pixelator/BlockSize → `imutil.ToRGBA` → `darkBackground`/`invertColors` → `detectAndDeskew` → auto-crop via `DefaultLocateMosaicBand` → block inference → `applyDefaults` → `applyAutoFingerprint` → auto-calibrate LetterSpacing → wire via `DefaultComponents`); then `capped := candidates[:min(len,maxVerifyCandidates)]`; `return DefaultVerifyCore(ctx, rgba, cfg, capped)`.
- `Verdict{Text, Distance, Match}` (verify.go:12); `VerifyMatchThreshold = 0.10` (verify.go:25).
- Hooks (root, var): `DefaultVerifyCore func(ctx, rgba *image.RGBA, cfg Config, candidates []string) ([]Verdict, error)` (unpixel.go:1597); `DefaultComponents` (1553); `DefaultLocateMosaicBand` (1572). Add `DefaultVerifyImageCore` beside `DefaultVerifyCore`.
- `defaults/defaults.go`: `init()` wires `unpixel.DefaultVerifyCore = verifyCore` (defaults.go:67). `verifyCore(ctx, rgba *image.RGBA, cfg unpixel.Config, candidates []string)` builds a scorer, `search.DiscoverOffsets`, scores each candidate at best offset. `defaults` imports `internal/search`, `internal/imutil`, `internal/pixelate`, etc.
- `unpixel.Pixelator` interface: `Pixelate(img *image.RGBA, originX, originY int) *image.RGBA`. `unpixel.Metric`: `Compare(a, b *image.RGBA) float64` (in [0,1], 0 = identical). `internal/imutil.ToRGBA(image.Image) *image.RGBA`.
- `golang.org/x/image v0.43.0` is in `go.mod` → `golang.org/x/image/draw` (`draw.CatmullRom.Scale`) is available, pure-Go.
- MCP: tools registered in `mcp/server.go` `NewServer` via `mcpsdk.AddTool(srv, toolX, handleX)`; handlers return `(*mcpsdk.CallToolResult, <Report>, error)` and use helpers `loadImage`, `toolJSON`, `errResult` (see `mcp/server.go`/`mcp/decode.go`).

## File Structure

- **Modify** `verify.go` — extract `prepareVerify`; add `ImageVerdict`, `VerifyImage`; add `DefaultVerifyImageCore` hook var (in `unpixel.go` next to `DefaultVerifyCore`, or `verify.go` — keep hooks together in unpixel.go).
- **Modify** `defaults/defaults.go` — `verifyImageCore` + wire in `init()`.
- **Create** `mcp/verify_image.go` — `unpixel_verify_image` tool + `VerifyImageMCP` core; register in `server.go`.
- **Create** `docs/sidecar-protocol.md` — the external-restorer contract + anti-hallucination loop + limits.
- **Tests:** `verify_test.go` (or existing), `defaults/*_test.go`, `mcp/verify_image_test.go`.

---

## Task 1: Refactor `Verify`'s prologue into `prepareVerify` (byte-identical)

**Files:**
- Modify: `verify.go`
- Test: `verify_test.go` (non-regression — the existing `Verify` tests must stay green; add one explicit guard).

**Interfaces:**
- Produces (used by Task 2): `func prepareVerify(img image.Image, opts []Option) (*image.RGBA, Config, error)` — runs verify.go:56-133 and returns the prepped rgba + resolved cfg (or a `DefaultComponents` error).

**Design note:** pure mechanical extraction. `Verify` keeps its `ErrNilImage` and `ErrNoComponents` checks; the per-Verify `capped`/`DefaultVerifyCore` stay in `Verify`. Behaviour MUST be identical.

- [ ] **Step 1: Confirm the baseline is green**

Run: `scripts/gotest-caged.sh go test . -run 'Verify' -count=1`
Expected: PASS (record which tests run — they are the non-regression baseline).

- [ ] **Step 2: Extract `prepareVerify`**

In `verify.go`, add:

```go
// prepareVerify runs the shared preparation prologue for the Verify family:
// it resolves opts into a Config (enabling the auto path when neither a
// Pixelator nor a block size is set), converts img to RGBA, auto-contrast /
// deskews / crops it, infers the block size and forward operator, auto-calibrates
// letter spacing, and wires the default components. It returns the prepped image
// and resolved Config, or the error from DefaultComponents. It does not score
// anything — Verify and VerifyImage supply their own scoring step.
func prepareVerify(img image.Image, opts []Option) (*image.RGBA, Config, error) {
	var cfg Config
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.Pixelator == nil && cfg.BlockSize <= 0 {
		cfg.autoCrop = true
		cfg.autoColorspace = true
		cfg.autoBlur = true
		cfg.autoCalibrate = true
	}

	rgba := imutil.ToRGBA(img)
	if darkBackground(rgba) {
		rgba = invertColors(rgba)
	}

	var grid BlockGrid
	rgba, _, grid = detectAndDeskew(rgba)

	if cfg.autoCrop && DefaultLocateMosaicBand != nil {
		if band, ok := DefaultLocateMosaicBand(rgba); ok {
			b := rgba.Bounds()
			if band.Dx() < b.Dx() || band.Dy() < b.Dy() {
				ox, oy := band.Min.X-b.Min.X, band.Min.Y-b.Min.Y
				rgba = imutil.Crop(rgba, ox, oy, band.Dx(), band.Dy())
				grid.Size = 0
			}
		}
	}

	if cfg.BlockSize <= 0 {
		if grid.Size >= 2 {
			cfg.BlockSize = grid.Size
		} else if s := InferBlockSize(rgba); s >= 2 {
			cfg.BlockSize = s
		}
	}
	cfg = applyDefaults(cfg)

	applyAutoFingerprint(&cfg, rgba)

	if cfg.autoCalibrate && cfg.Style.LetterSpacing == 0 && cfg.BlockSize >= 2 && cfg.Style.FontSize > 0 {
		if refW := int(cfg.Style.FontSize * 0.6); refW > 0 {
			if stretch, ok := InferXStretch(rgba, cfg.BlockSize, refW); ok && (stretch < 0.98 || stretch > 1.02) {
				cfg.Style.LetterSpacing = (stretch - 1) * float64(refW)
			}
		}
	}

	if cfg.Renderer == nil || cfg.Pixelator == nil || cfg.Metric == nil {
		if DefaultComponents != nil {
			if err := DefaultComponents(&cfg); err != nil {
				return nil, cfg, err
			}
		}
	}

	return rgba, cfg, nil
}
```

Replace `Verify`'s body (after the two guard checks) with:

```go
func Verify(ctx context.Context, img image.Image, candidates []string, opts ...Option) ([]Verdict, error) {
	if img == nil {
		return nil, ErrNilImage
	}
	if DefaultVerifyCore == nil {
		return nil, ErrNoComponents
	}
	rgba, cfg, err := prepareVerify(img, opts)
	if err != nil {
		return nil, err
	}
	capped := candidates[:min(len(candidates), maxVerifyCandidates)]
	return DefaultVerifyCore(ctx, rgba, cfg, capped)
}
```

- [ ] **Step 3: Add an explicit non-regression test**

Add to `verify_test.go` (in-memory fixture; render a word, pixelate, verify true vs false):

```go
func TestVerify_unchangedAfterRefactor(t *testing.T) {
	// Build an in-memory mosaic of "the" and confirm Verify still ranks the true
	// text below the match threshold and a wrong candidate above it.
	img, block := mosaicWord(t, "the")
	verdicts, err := unpixel.Verify(t.Context(), img, []string{"the", "xyz"},
		unpixel.WithBlockSize(block), unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(verdicts) != 2 {
		t.Fatalf("verdicts len = %d; want 2", len(verdicts))
	}
	if !verdicts[0].Match {
		t.Errorf("true text %q not a Match (distance %.3f)", verdicts[0].Text, verdicts[0].Distance)
	}
	if verdicts[1].Match {
		t.Errorf("wrong text %q unexpectedly Match (distance %.3f)", verdicts[1].Text, verdicts[1].Distance)
	}
}
```

Add the `mosaicWord` helper (package `unpixel_test`, imports `defaults` for side-effects, `fonts`, `internal/render`, `internal/pixelate`):

```go
func mosaicWord(t *testing.T, text string) (image.Image, int) {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil) // Liberation Sans
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	clean, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}
	const block = 6
	return pixelate.NewBlockAverage(block).Pixelate(clean, 0, 0), block
}
```

> Confirm `verify_test.go`'s package clause and whether such a helper already exists (`grep -n 'func mosaic\|Pixelate\|package ' verify_test.go`). Reuse an existing helper if present. If `verify_test.go` doesn't exist, create it as `package unpixel_test` with the imports above plus `_ "github.com/oioio-space/unpixel/defaults"`.

- [ ] **Step 4: Run the baseline + new test**

Run: `scripts/gotest-caged.sh go test . -run 'Verify' -count=1` then `CGO_ENABLED=0 go build ./...`
Expected: PASS + clean build (refactor is behaviour-preserving).

- [ ] **Step 5: Commit**

```bash
git add verify.go verify_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "refactor(verify): extract prepareVerify prologue (byte-identical)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `ImageVerdict` + `VerifyImage` + `DefaultVerifyImageCore` hook

**Files:**
- Modify: `verify.go` (ImageVerdict, VerifyImage), `unpixel.go` (DefaultVerifyImageCore hook var).
- Test: `verify_test.go` (nil / no-components paths; full behaviour is Task 3 once the hook is wired).

**Interfaces:**
- Consumes: `prepareVerify` (Task 1), `imutil.ToRGBA`, `ErrNilImage`/`ErrNoComponents`/`VerifyMatchThreshold`.
- Produces (used by Task 3, 4): `type ImageVerdict struct{ Distance float64; Match bool }`; `func VerifyImage(ctx, redacted, restored image.Image, opts ...Option) (ImageVerdict, error)`; `var DefaultVerifyImageCore func(ctx context.Context, redacted, restored *image.RGBA, cfg Config) (ImageVerdict, error)`.

- [ ] **Step 1: Write the failing test (nil/no-components contract)**

Add to `verify_test.go`:

```go
func TestVerifyImage_nilImages(t *testing.T) {
	img, _ := mosaicWord(t, "the")
	if _, err := unpixel.VerifyImage(t.Context(), nil, img); err != unpixel.ErrNilImage {
		t.Errorf("nil redacted err = %v; want ErrNilImage", err)
	}
	if _, err := unpixel.VerifyImage(t.Context(), img, nil); err != unpixel.ErrNilImage {
		t.Errorf("nil restored err = %v; want ErrNilImage", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test . -run TestVerifyImage_nilImages -count=1`
Expected: FAIL — `unpixel.VerifyImage` undefined.

- [ ] **Step 3: Add the hook var (`unpixel.go`)**

Next to `DefaultVerifyCore` (unpixel.go:1597), add:

```go
// DefaultVerifyImageCore is a hook populated by importing the defaults package
// for its side-effect. It physically verifies an already-prepped restored image
// against the prepped redaction by re-applying the forward operator (cfg's
// Pixelator) at the best grid phase and comparing with cfg's Metric. VerifyImage
// delegates to it to avoid an import cycle with internal packages. A nil hook
// makes VerifyImage return ErrNoComponents.
var DefaultVerifyImageCore func(ctx context.Context, redacted, restored *image.RGBA, cfg Config) (ImageVerdict, error)
```

- [ ] **Step 4: Add `ImageVerdict` + `VerifyImage` (`verify.go`)**

```go
// ImageVerdict is the result of physically verifying a restored image against a
// redaction by re-applying the forward operator and comparing.
type ImageVerdict struct {
	// Distance is the whole-image distance in [0,1] between the redaction and the
	// re-pixelated restored image at its best grid phase (lower = more consistent).
	Distance float64
	// Match reports whether Distance is below VerifyMatchThreshold.
	Match bool
}

// VerifyImage physically verifies a restored (clean) image against a redaction:
// it re-applies the engine's forward operator to restored (re-pixelate at the
// mosaic block, or blur when a blur Pixelator is set via WithPixelator) and
// compares the result to redacted with the faithful metric, at the best grid
// phase. It is the image-input analogue of [Verify]: VerifyImage(redacted,
// render(text)) is the physical core of Verify(redacted, []string{text}).
//
// Use it as an anti-hallucination gate for an external restorer (e.g. a diffusion
// sidecar): a faithful restoration re-pixelates back to the observed redaction
// (low Distance, Match=true); a hallucination does not — except where the mosaic
// is genuinely ambiguous (many restorations map to the same mosaic), which no
// physical check can disambiguate.
//
// VerifyImage returns ErrNilImage when either image is nil and ErrNoComponents
// when the defaults package is not imported. opts mirror Verify's
// (WithBlockSize/WithCharset/WithPixelator/WithAuto…); for blurred redactions
// pass an explicit blur operator via WithPixelator, like Verify.
func VerifyImage(ctx context.Context, redacted, restored image.Image, opts ...Option) (ImageVerdict, error) {
	if redacted == nil || restored == nil {
		return ImageVerdict{}, ErrNilImage
	}
	if DefaultVerifyImageCore == nil {
		return ImageVerdict{}, ErrNoComponents
	}
	rgba, cfg, err := prepareVerify(redacted, opts)
	if err != nil {
		return ImageVerdict{}, err
	}
	return DefaultVerifyImageCore(ctx, rgba, imutil.ToRGBA(restored), cfg)
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test . -run TestVerifyImage_nilImages -count=1` → PASS.
Run: `CGO_ENABLED=0 go build ./...` → clean.

- [ ] **Step 6: Commit**

```bash
git add verify.go unpixel.go verify_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(unpixel): VerifyImage + ImageVerdict + DefaultVerifyImageCore hook

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `defaults.verifyImageCore` — re-pixelate + phase search + metric

**Files:**
- Modify: `defaults/defaults.go`
- Test: `defaults/verifyimage_test.go`

**Interfaces:**
- Consumes: `unpixel.ImageVerdict`/`Config`/`VerifyMatchThreshold` (Task 2), `cfg.Pixelator`/`cfg.Metric`, `golang.org/x/image/draw`.
- Produces: `unpixel.DefaultVerifyImageCore = verifyImageCore` wired in `init()`.

**Design note:** resize `restored` to the redaction's bounds (pure-Go `draw.CatmullRom.Scale`) only when sizes differ; re-pixelate at each grid phase `ox,oy ∈ [0, blockSize)`; take the min distance ("best grid offset", consistent with `verifyCore`). A blur Pixelator ignores the offsets, so the loop yields a single value (harmless).

- [ ] **Step 1: Write the failing test**

Create `defaults/verifyimage_test.go`:

```go
package defaults_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// cleanAndMosaic renders text and returns (clean image, its mosaic, block).
func cleanAndMosaic(t *testing.T, text string, block int) (*image.RGBA, image.Image) {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	clean, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}
	mosaic := pixelate.NewBlockAverage(block).Pixelate(clean, 0, 0)
	return clean, mosaic
}

func TestVerifyImage_acceptsTrueRejectsHallucination(t *testing.T) {
	const block = 6
	clean, mosaic := cleanAndMosaic(t, "the", block)

	// The true clean image re-pixelates back to the observed mosaic → Match.
	good, err := unpixel.VerifyImage(t.Context(), mosaic, clean, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(true): %v", err)
	}
	if !good.Match {
		t.Errorf("true restoration not a Match (distance %.4f)", good.Distance)
	}

	// A different clean image (wrong text) does not re-pixelate to the mosaic.
	wrongClean, _ := cleanAndMosaic(t, "xyz", block)
	bad, err := unpixel.VerifyImage(t.Context(), mosaic, wrongClean, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(wrong): %v", err)
	}
	if bad.Match {
		t.Errorf("hallucinated restoration unexpectedly Match (distance %.4f)", bad.Distance)
	}
	if !(bad.Distance > good.Distance) {
		t.Errorf("wrong distance %.4f should exceed true distance %.4f", bad.Distance, good.Distance)
	}
}

func TestVerifyImage_resizesRestored(t *testing.T) {
	const block = 6
	clean, mosaic := cleanAndMosaic(t, "the", block)
	// Upscale the clean image 2× so VerifyImage must resize it back.
	big := image.NewRGBA(image.Rect(0, 0, clean.Bounds().Dx()*2, clean.Bounds().Dy()*2))
	// nearest-neighbour blow-up is fine for the test; just exercise the resize path.
	for y := big.Bounds().Min.Y; y < big.Bounds().Max.Y; y++ {
		for x := big.Bounds().Min.X; x < big.Bounds().Max.X; x++ {
			big.Set(x, y, clean.At(x/2, y/2))
		}
	}
	v, err := unpixel.VerifyImage(t.Context(), mosaic, big, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage(resized): %v", err)
	}
	if v.Distance > 0.5 {
		t.Errorf("resized true restoration distance %.4f too high (resize path broken?)", v.Distance)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./defaults/ -run TestVerifyImage -v -count=1`
Expected: FAIL — `DefaultVerifyImageCore` nil → `ErrNoComponents` (hook not wired yet).

- [ ] **Step 3: Implement `verifyImageCore` + wire it**

In `defaults/defaults.go` imports, add:

```go
	xdraw "golang.org/x/image/draw"
```

In `init()`, after the `DefaultVerifyCore` line:

```go
	unpixel.DefaultVerifyImageCore = verifyImageCore
```

Add the function (near `verifyCore`):

```go
// verifyImageCore implements the DefaultVerifyImageCore hook. It re-applies the
// forward operator (cfg.Pixelator) to the restored image and compares it to the
// redaction (cfg.Metric), taking the minimum distance over grid phases — the
// image analogue of verifyCore's best-offset search. restored is resized to the
// redaction's bounds (pure-Go CatmullRom) when their sizes differ.
func verifyImageCore(ctx context.Context, redacted, restored *image.RGBA, cfg unpixel.Config) (unpixel.ImageVerdict, error) {
	rb := redacted.Bounds()

	rest := restored
	if restored.Bounds().Dx() != rb.Dx() || restored.Bounds().Dy() != rb.Dy() {
		scaled := image.NewRGBA(rb)
		xdraw.CatmullRom.Scale(scaled, rb, restored, restored.Bounds(), xdraw.Over, nil)
		rest = scaled
	}

	block := cfg.BlockSize
	if block < 1 {
		block = 1
	}

	best := 1.0
	for oy := range block {
		for ox := range block {
			if ctx.Err() != nil {
				return unpixel.ImageVerdict{}, ctx.Err()
			}
			reMosaic := cfg.Pixelator.Pixelate(rest, ox, oy)
			if d := cfg.Metric.Compare(reMosaic, redacted); d < best {
				best = d
			}
		}
	}

	return unpixel.ImageVerdict{Distance: best, Match: best < unpixel.VerifyMatchThreshold}, nil
}
```

> Verify `cfg.Metric.Compare` tolerates the bounds: `reMosaic` has `rest`'s bounds (`rb`) and `redacted` has `rb` — same rectangle. If `Compare` asserts identical `Bounds()`, this holds because `scaled`/`rest` are built with `rb`. If `rest == restored` (no resize) but `restored.Bounds()` has a non-zero Min differing from `rb.Min`, normalise by building `rest` with `image.NewRGBA(rb)` and copying — confirm with `grep -n 'func.*Compare' internal/metric/*.go` whether Compare uses Dx/Dy or the Rectangle. Adjust only if needed.

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./defaults/ -run TestVerifyImage -v -count=1` → PASS.
Run: `CGO_ENABLED=0 go build ./...` → clean.

- [ ] **Step 5: Commit**

```bash
git add defaults/defaults.go defaults/verifyimage_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(defaults): verifyImageCore — re-pixelate restored image + phase search

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: MCP `unpixel_verify_image` tool

**Files:**
- Create: `mcp/verify_image.go`
- Modify: `mcp/server.go` (register the tool)
- Test: `mcp/verify_image_test.go`

**Interfaces:**
- Consumes: `unpixel.VerifyImage` (Task 2), mcp helpers `loadImage`/`toolJSON`/`errResult`.
- Produces: `VerifyImageMCP(ctx, redacted, restored image.Image, blockSize int) (ImageVerifyReport, error)`; tool `toolVerifyImage` + handler `handleVerifyImage`; registered in `NewServer`.

- [ ] **Step 1: Write the failing test**

Create `mcp/verify_image_test.go` (match the existing mcp test package — `mcpserver` or `mcpserver_test`; this uses the internal package so it can call the core directly):

```go
package mcpserver

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

func cleanMosaicVI(t *testing.T, text string, block int) (*image.RGBA, image.Image) {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	clean, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}
	return clean, pixelate.NewBlockAverage(block).Pixelate(clean, 0, 0)
}

func TestVerifyImageMCP_trueVsHallucination(t *testing.T) {
	const block = 6
	clean, mosaic := cleanMosaicVI(t, "the", block)

	good, err := VerifyImageMCP(t.Context(), mosaic, clean, block)
	if err != nil {
		t.Fatalf("VerifyImageMCP(true): %v", err)
	}
	if !good.Match {
		t.Errorf("true restoration not Match (distance %.4f)", good.Distance)
	}

	wrong, _ := cleanMosaicVI(t, "xyz", block)
	bad, err := VerifyImageMCP(t.Context(), mosaic, wrong, block)
	if err != nil {
		t.Fatalf("VerifyImageMCP(wrong): %v", err)
	}
	if bad.Match {
		t.Errorf("hallucination unexpectedly Match (distance %.4f)", bad.Distance)
	}
}
```

> If the existing mcp tests are `package mcpserver_test`, move these to that package and call the exported `mcpserver.VerifyImageMCP`. Match the neighbouring test files.

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./mcp/ -run TestVerifyImageMCP -v -count=1`
Expected: FAIL — `VerifyImageMCP` undefined.

- [ ] **Step 3: Implement the tool**

Create `mcp/verify_image.go`:

```go
package mcpserver

import (
	"context"
	"fmt"
	"image"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
)

// toolVerifyImage is the tool descriptor for unpixel_verify_image.
var toolVerifyImage = &mcpsdk.Tool{
	Name: "unpixel_verify_image",
	Description: "Physically verifies a RESTORED image (e.g. from an external diffusion restorer) " +
		"against a redaction by re-applying the forward operator (re-pixelate at the mosaic block) " +
		"and comparing. Returns a distance in [0,1] and a match flag (distance < 0.10). " +
		"Use it as an anti-hallucination gate: a faithful restoration re-pixelates back to the " +
		"observed redaction (match=true); a hallucination does not — except where the mosaic is " +
		"genuinely ambiguous (no physical check can disambiguate that). NOT a recoverer: it scores " +
		"a proposed restoration, it does not produce one.",
}

// verifyImageInput is the JSON input for unpixel_verify_image.
type verifyImageInput struct {
	// RedactionPath is the filesystem path of the pixelated redaction image.
	RedactionPath string `json:"redaction_path" jsonschema:"Filesystem path to the pixelated redaction PNG/JPEG"`
	// RestoredPath is the filesystem path of the proposed restored (clean) image.
	RestoredPath string `json:"restored_path" jsonschema:"Filesystem path to the proposed restored (clean) PNG/JPEG to verify"`
	// BlockSize overrides the auto-detected mosaic block size (0 = auto).
	BlockSize int `json:"block_size,omitzero" jsonschema:"Override mosaic block size in pixels (0 = auto-detect)"`
}

// ImageVerifyReport is the output of unpixel_verify_image.
type ImageVerifyReport struct {
	// Distance is the whole-image distance in [0,1] (lower = more consistent).
	Distance float64 `json:"distance"`
	// Match reports a confident physical match (Distance < 0.10).
	Match bool `json:"match"`
}

// handleVerifyImage is the tool handler for unpixel_verify_image.
func handleVerifyImage(ctx context.Context, _ *mcpsdk.CallToolRequest, in verifyImageInput) (*mcpsdk.CallToolResult, ImageVerifyReport, error) {
	redacted, err := loadImage(in.RedactionPath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_image: load redaction: %w", err)), ImageVerifyReport{}, nil
	}
	restored, err := loadImage(in.RestoredPath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_image: load restored: %w", err)), ImageVerifyReport{}, nil
	}
	report, err := VerifyImageMCP(ctx, redacted, restored, in.BlockSize)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_image: %w", err)), ImageVerifyReport{}, nil
	}
	return toolJSON(report)
}

// VerifyImageMCP verifies restored against redacted with unpixel.VerifyImage and
// returns an ImageVerifyReport. blockSize pins the mosaic block size (0 = auto).
// It is the testable core of the unpixel_verify_image MCP tool.
func VerifyImageMCP(ctx context.Context, redacted, restored image.Image, blockSize int) (ImageVerifyReport, error) {
	var opts []unpixel.Option
	if blockSize > 0 {
		opts = append(opts, unpixel.WithBlockSize(blockSize))
	}
	v, err := unpixel.VerifyImage(ctx, redacted, restored, opts...)
	if err != nil {
		return ImageVerifyReport{}, err
	}
	return ImageVerifyReport{Distance: v.Distance, Match: v.Match}, nil
}
```

Register in `mcp/server.go` `NewServer`, after `AddTool(srv, toolVerify, handleVerify)`:

```go
	mcpsdk.AddTool(srv, toolVerifyImage, handleVerifyImage)
```

Add `unpixel_verify_image` to the package-doc tool list if one exists (`grep -n 'unpixel_verify_candidates' mcp/server.go` — update the doc comment if it enumerates tools).

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./mcp/ -run TestVerifyImageMCP -v -count=1` → PASS.
Run: `CGO_ENABLED=0 go build ./...` → clean.

- [ ] **Step 5: Commit**

```bash
git add mcp/verify_image.go mcp/server.go mcp/verify_image_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(mcp): unpixel_verify_image — physics-gate a restored image

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Sidecar protocol doc + full validation

**Files:**
- Create: `docs/sidecar-protocol.md`
- Test: `verify_test.go` (the identity check) + run the full gate.

**Interfaces:** consumes everything above.

- [ ] **Step 1: Write the identity test (`Verify` ≡ `VerifyImage∘render`)**

Add to `verify_test.go`:

```go
func TestVerifyImage_isVerifyLowerHalf(t *testing.T) {
	const block = 6
	clean, mosaic := func() (*image.RGBA, image.Image) {
		r, _ := render.NewXImageFromFonts(fonts.All()[0].Data, nil)
		c, _, _ := r.Render("the", unpixel.Style{FontSize: 28})
		return c, pixelate.NewBlockAverage(block).Pixelate(c, 0, 0)
	}()

	// VerifyImage on the true clean render and Verify on the true string should
	// AGREE that the true text is a confident physical match.
	vi, err := unpixel.VerifyImage(t.Context(), mosaic, clean, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("VerifyImage: %v", err)
	}
	vs, err := unpixel.Verify(t.Context(), mosaic, []string{"the"},
		unpixel.WithBlockSize(block), unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !vi.Match || !vs[0].Match {
		t.Errorf("disagreement: VerifyImage.Match=%v (d=%.4f), Verify.Match=%v (d=%.4f)",
			vi.Match, vi.Distance, vs[0].Match, vs[0].Distance)
	}
}
```

(This asserts agreement on the Match verdict — not exact distance equality, since Verify renders+offset-discovers while VerifyImage takes the image directly and phase-searches.)

- [ ] **Step 2: Write `docs/sidecar-protocol.md`**

Create `docs/sidecar-protocol.md` documenting:
- **Purpose:** how an external image restorer (e.g. a Python diffusion sidecar) integrates with UnPixel's anti-hallucination gate. UnPixel does NOT run the restorer; it verifies the restorer's output.
- **Loop:** (1) caller gets hints via `unpixel_propose_hints` (block size, char count, bbox, charset, leaked context); (2) external restorer produces N candidate restored images of the redaction band; (3) caller calls `unpixel_verify_image` (or `unpixel.VerifyImage`) on each; (4) keep only `match=true` results; ties/ambiguity are unresolved by design.
- **Contract (JSON, illustrative):** restorer input `{redaction_png_path, block_size, char_count_estimate, charset_hint, bbox}` → output `[{restored_png_path}, …]`. Note UnPixel only consumes the verify side; the restorer side is the integrator's responsibility.
- **Limits (verbatim from spec §4):** mosaic ambiguity not disambiguable; blur > mosaic; phase-search is optimistic; the diffusion model is deferred — this ships the gate + protocol, not a recoverer; no `os/exec` in UnPixel (the restorer is a separate process/MCP server the orchestrator drives).

Keep it concise and honest; no over-promise.

- [ ] **Step 3: Run the identity test + full suite**

Run: `scripts/gotest-caged.sh go test . -run 'Verify' -count=1` → PASS.
Run: `scripts/gotest-caged.sh go test ./... -count=1` → PASS (incl. 17/17 panel; `Verify`/`Recover` unchanged).

- [ ] **Step 4: Coverage gate**

Run: `mise run cover:check` → ≥ 85% (report %). If short, add a `verifyImageCore` table case (e.g. blur Pixelator via `WithPixelator(defaults.GaussianBlur(σ))` on a blurred fixture asserting Match) to `defaults/verifyimage_test.go`.

- [ ] **Step 5: Full CI gate**

Run: `mise run ci` → all green (lint + test + cgo:check + scans). (Uses `-short`.)

- [ ] **Step 6: Commit**

```bash
git add docs/sidecar-protocol.md verify_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "docs(sidecar): external-restorer protocol + VerifyImage identity test

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/superpowers/specs/2026-06-30-image-verify-gate-design.md`):
- §3.1 `prepareVerify` byte-identical refactor → Task 1. ✅
- §3.2 `ImageVerdict` + `VerifyImage` → Task 2. ✅
- §3.3 `DefaultVerifyImageCore` hook → Task 2. ✅
- §3.4 `verifyImageCore` (resize + phase search + metric) → Task 3. ✅
- §3.5 MCP `unpixel_verify_image` → Task 4. ✅
- §3.6 sidecar protocol doc → Task 5. ✅
- §2.1 identity Verify≡VerifyImage∘render → Task 5 test (Match-agreement). ✅
- §2.2/2.3 accept true / reject hallucination → Task 3 tests. ✅
- §2.4 refactor non-regression → Task 1 test + Task 5 full suite. ✅
- §2.5 pure-Go, caged, in-memory, ≥85% → Global Constraints + Task 5. ✅
- §3 no `//go:build`, no `os/exec`, no new package → honoured (none added). ✅

**2. Placeholder scan:** every step has complete code. The `grep`-to-confirm notes (metric bounds-handling, test package style, package-doc tool list) are explicit verification steps against real code, not logic placeholders.

**3. Type consistency:** `ImageVerdict{Distance,Match}`, `VerifyImage(ctx, redacted, restored image.Image, opts...) (ImageVerdict, error)`, `DefaultVerifyImageCore func(ctx, redacted, restored *image.RGBA, cfg Config) (ImageVerdict, error)`, `verifyImageCore` (same signature), `VerifyImageMCP(ctx, redacted, restored image.Image, blockSize int) (ImageVerifyReport, error)`, `ImageVerifyReport{Distance,Match}`, `prepareVerify(img image.Image, opts []Option) (*image.RGBA, Config, error)` are used identically across Tasks 1–5.

> **Known cross-task note for the implementer:** Task 1's `prepareVerify` extraction must be byte-identical — diff `Verify`'s old vs new body mentally before committing; the only allowed change is moving lines 56-133 into the helper. Task 3 flags the one real risk: `cfg.Metric.Compare` bounds handling — build `rest` with `image.NewRGBA(redacted.Bounds())` so reMosaic and redacted share the exact rectangle; confirm with a grep of `internal/metric`.
