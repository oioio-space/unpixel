# Blind Font Prior (#4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A blind (no-known-text) font prior that reorders the multi-font sweep (byte-identical result, faster) and optionally prunes it to the top-K fonts, reusing the existing pixelated-signature ranker, with an ML-ready seam behind `//go:build ml` (no ML built now).

**Architecture:** A new top-level `fontprior` package (cycle-safe: it may import `unpixel` + `fonts` + `internal/fontrank`, none of which import it; root `unpixel` cannot import the prior because `internal/fontrank` imports `unpixel`). The heuristic `Histogram` prior wraps `fontrank.RankFonts` (block-luminance histogram, already blind). `fontprior.RecoverWithPrior` ranks `fonts.All()`, always reorders, optionally truncates to top-K, then delegates to `unpixel.RecoverMultiFont`. Root `unpixel` gains two opt-in options/Config fields. MCP and CLI gain blind/top-K surfaces.

**Tech Stack:** Pure Go (no CGO). Reuses `internal/fontrank`, `fonts`, `internal/render`, `internal/pixelate`. No new dependency (no gonum until ML is actually built).

## Global Constraints

- **NO CGO, ever.** `CGO_ENABLED=0` is pinned; never add `import "C"` or a cgo-requiring dependency. Enforced by `mise run cgo:check`. No gonum / no ML runtime added in this plan.
- **Opt-in / byte-identical default:** the single-font `Recover` path and the existing `RecoverMultiFont` are untouched. The 17/17 synthetic fixture panel must stay 17/17. Reordering the sweep yields the same winner (`RecoverMultiFont` ranks by `BestTotal`, independent of input order); only the opt-in top-K prune can change results.
- **No import cycle:** the prior lives in a new top-level `fontprior` package, above `fonts` and `internal/fontrank`. Root `unpixel` only gains plain Config fields + options (no new import). Verify with `CGO_ENABLED=0 go build ./...`.
- **Caged tests only:** never run `go test` bare. Use `scripts/gotest-caged.sh` for every test run (the `unpixel`/`mcp` suites take minutes — expected).
- **In-memory fixtures only:** every test image is rendered/pixelated in code; never load a gitignored or network fixture (CI runs on a fresh checkout).
- **Coverage gate:** `COVER_MIN=85`; keep coverage ≥ 85% (`mise run cover:check`).
- **Commit gate:** each commit goes through the pre-commit review gate. Arm the `/simplify` marker (`$GIT_DIR/claude-simplify-ok`, `git diff --cached | sha1sum | cut -d' ' -f1`) only AFTER genuinely reviewing, in a SEPARATE bash call from `git commit`. Run `git restore --staged PROGRESS.md` before arming (the post-commit hook re-stages it).
- **Commit trailer:** every commit message ends with
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** `feat/font-prior` (already created from `master`; do not commit on `master`).
- Follow `go-style-guide` and `use-modern-go` (`for range`, `min`/`max`, `any`, `t.Context()`, table tests with field names, `got` before `want`).

## Verified facts (do not re-litigate)

- `internal/fontrank` (`internal/fontrank/fontrank.go`): `type NamedFont struct{ Name string; Data []byte }`; `type FontScore struct{ Name string; Score float64 }`; `func RankFonts(ctx, img image.Image, fonts []NamedFont) ([]FontScore, error)` — **already blind** (builds target block-luminance histogram, renders each font's exemplar pangram, pixelates, L1-compares; sorted best-first, lowest Score). Internally: `target := imutil.ToRGBA(img); blockSize := detectBlockSize(target); targetHist := blockLumHistogram(target, blockSize)` then concurrent `scoreFont(f.Data, blockSize, targetHist)`.
- `fonts` (`fonts/fonts.go`): `type Font struct{ Name, Style, Approx, File string; Data []byte }`; `func All() []Font` (9 bundled fonts, Data populated); `func Renderers() ([]unpixel.Renderer, error)`. `fonts` imports `defaults`.
- `defaults.RendererFromFonts(regularTTF, boldTTF []byte) (unpixel.Renderer, error)`.
- `unpixel` (`unpixel.go`): `type FontResult struct{ Result Result; Index int }`; `func RecoverMultiFont(ctx, redacted image.Image, renderers []Renderer, opts ...Option) ([]FontResult, error)` — ranks by `Result.BestTotal`, `ret[0]` best, errored renderers omitted; errors only if redacted nil / renderers empty / all failed. `Config.BlockSize` is exported. `Result.BestGuess`, `Result.BestTotal` exist; `Result.Fidelity()` exists.
- `mcp/rankfonts.go`: `RankFonts(ctx, img, knownText string) (RankFontsReport, error)` currently REQUIRES `knownText`; blends `fontrank.RankFonts` (histogram) + `fontrank.RankByMetrics` (needs the known-text fingerprint). `RankFontsReport{ Ranked []FontRankEntry; Best string }`, `FontRankEntry{ Font string; Score float64 }`.
- `cmd/unpixel/main.go`: `bundleCandidates()` builds `[]candidateFont{r, display, jsonName}` from `fonts.All()`; `sweepRecover(ctx, img, base, cands, p)` calls `unpixel.RecoverMultiFont`. Flags are urfave/cli/v3 `&cli.BoolFlag{}` / `&cli.IntFlag{}` in the `Flags:` slice.

## File Structure

- **Modify** `internal/fontrank/fontrank.go` — add `RankFontsAt(…, blockSize int)`; `RankFonts` delegates with `0`.
- **Modify** `unpixel.go` — add exported `Config.FontPrior bool` + `Config.FontPriorTopK int`; `WithFontPrior()` + `WithFontPriorTopK(k)` options.
- **Create** `fontprior/fontprior.go` — `Ranked`, `Prior`, `Histogram`, `RecoverWithPrior`, `FontResult`.
- **Create** `fontprior/default_noml.go` (`//go:build !ml`) — `func Default() Prior { return Histogram{} }`.
- **Create** `fontprior/ml.go` (`//go:build ml`) — `Default()` returns the `mlPrior` stub; `ErrMLNotBuilt`.
- **Create** `fontprior/fontprior_test.go` + `fontprior/ml_test.go` (`//go:build ml`).
- **Modify** `mcp/rankfonts.go` — `known_text` optional (blind histogram-only mode).
- **Modify** `mcp/decode.go` — optional `font_prior_top_k` field.
- **Modify** `cmd/unpixel/main.go` — `--font-prior` / `--font-prior-top-k` flags; reorder/truncate the bundle sweep.

---

## Task 1: `internal/fontrank.RankFontsAt` — block-size-aware ranking

**Files:**
- Modify: `internal/fontrank/fontrank.go`
- Test: `internal/fontrank/fontrank_test.go`

**Interfaces:**
- Consumes: existing `RankFonts` internals (`imutil.ToRGBA`, `detectBlockSize`, `blockLumHistogram`, `scoreFont`).
- Produces (used by Task 3): `func RankFontsAt(ctx context.Context, img image.Image, fonts []NamedFont, blockSize int) ([]FontScore, error)`. `RankFonts` becomes a thin delegate.

- [ ] **Step 1: Write the failing test**

Add to `internal/fontrank/fontrank_test.go`:

```go
func TestRankFontsAt_explicitBlockMatchesAuto(t *testing.T) {
	// Build an in-memory mosaic of text in a known bundled font.
	img, block := mosaicFixtureForFont(t, "Liberation Mono", "ABC123")

	auto, err := RankFonts(t.Context(), img, bundledNamedFonts(t))
	if err != nil {
		t.Fatalf("RankFonts: %v", err)
	}
	explicit, err := RankFontsAt(t.Context(), img, bundledNamedFonts(t), block)
	if err != nil {
		t.Fatalf("RankFontsAt: %v", err)
	}
	if len(auto) != len(explicit) {
		t.Fatalf("len mismatch: auto %d, explicit %d", len(auto), len(explicit))
	}
	// When the explicit block equals the detected block, the order must match.
	for i := range auto {
		if auto[i].Name != explicit[i].Name {
			t.Errorf("rank %d: auto %q != explicit %q", i, auto[i].Name, explicit[i].Name)
		}
	}
}

func TestRankFontsAt_zeroBlockAutoDetects(t *testing.T) {
	img, _ := mosaicFixtureForFont(t, "Liberation Sans", "Hello")
	got, err := RankFontsAt(t.Context(), img, bundledNamedFonts(t), 0)
	if err != nil {
		t.Fatalf("RankFontsAt(0): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("RankFontsAt(0) returned no scores")
	}
}
```

Add these test helpers in the same file (render+pixelate in memory; reuse `fonts.All()` for the bundle). NOTE: `internal/fontrank` already imports `internal/render` and `internal/pixelate`; it must NOT import `fonts` if that introduces a cycle — check with `go list -deps`. If importing `github.com/oioio-space/unpixel/fonts` from the test (package `fontrank` is internal; the `_test.go` may import `fonts` since `fonts` does not import `fontrank`) is clean, use it; the test below assumes it is:

```go
func bundledNamedFonts(t *testing.T) []NamedFont {
	t.Helper()
	all := fonts.All()
	out := make([]NamedFont, len(all))
	for i, f := range all {
		out[i] = NamedFont{Name: f.Name, Data: f.Data}
	}
	return out
}

func mosaicFixtureForFont(t *testing.T, fontName, text string) (image.Image, int) {
	t.Helper()
	var data []byte
	for _, f := range fonts.All() {
		if f.Name == fontName {
			data = f.Data
		}
	}
	if data == nil {
		t.Fatalf("bundled font %q not found", fontName)
	}
	r, err := render.NewXImageFromFonts(data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	rendered, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}
	const block = 6
	pixelated := pixelate.NewBlockAverage(block).Pixelate(rendered, 0, 0)
	return pixelated, block
}
```

> Confirm the imports the test needs: `context` is via `t.Context()`, plus `image`, `testing`, and `github.com/oioio-space/unpixel`, `.../fonts`, `.../internal/render`, `.../internal/pixelate`. Verify `fonts` is import-cycle-clean from a `fontrank` test with `CGO_ENABLED=0 go vet ./internal/fontrank/`.

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./internal/fontrank/ -run RankFontsAt -v -count=1`
Expected: FAIL — `undefined: RankFontsAt`.

- [ ] **Step 3: Refactor `RankFonts` → `RankFontsAt`**

In `internal/fontrank/fontrank.go`, replace the `RankFonts` function body with a delegate and add `RankFontsAt` holding the original logic plus the block-size parameter:

```go
// RankFonts scores each candidate font by how well a small glyph exemplar
// matches the block-luminance profile of img, auto-detecting the mosaic block
// size, and returns the list sorted best-first (lowest Score first). See
// [RankFontsAt] for the full contract.
func RankFonts(ctx context.Context, img image.Image, fonts []NamedFont) ([]FontScore, error) {
	return RankFontsAt(ctx, img, fonts, 0)
}

// RankFontsAt is [RankFonts] with an explicit mosaic block size. When blockSize
// is <= 0 it auto-detects the block size from img (identical to RankFonts); when
// > 0 it uses the caller's known block size and skips detection. It is blind: it
// needs no known plaintext. RankFontsAt is safe for concurrent use; it returns a
// non-nil error only on context cancellation.
func RankFontsAt(ctx context.Context, img image.Image, fonts []NamedFont, blockSize int) ([]FontScore, error) {
	if len(fonts) == 0 {
		return nil, nil
	}

	target := imutil.ToRGBA(img)
	if blockSize <= 0 {
		blockSize = detectBlockSize(target)
	}
	targetHist := blockLumHistogram(target, blockSize)

	scores := make([]FontScore, len(fonts))
	var wg sync.WaitGroup
	for i, f := range fonts {
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			scores[i] = FontScore{
				Name:  f.Name,
				Score: scoreFont(f.Data, blockSize, targetHist),
			}
		})
	}
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	slices.SortStableFunc(scores, func(a, b FontScore) int {
		return cmp.Compare(a.Score, b.Score)
	})
	return scores, nil
}
```

(Keep all other functions — `scoreFont`, `detectBlockSize`, `blockLumHistogram`, `l1Distance`, `cropToSentinel`, consts — unchanged.)

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./internal/fontrank/ -run RankFontsAt -v -count=1`
Expected: PASS.

- [ ] **Step 5: Regression — existing fontrank tests still pass**

Run: `scripts/gotest-caged.sh go test ./internal/fontrank/ -count=1`
Expected: PASS (all pre-existing tests unchanged in behaviour).

- [ ] **Step 6: Commit**

```bash
git add internal/fontrank/fontrank.go internal/fontrank/fontrank_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, then arm marker in a SEPARATE call, then:
git commit -m "feat(fontrank): RankFontsAt — explicit block size skips detection

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Root `unpixel` options + Config fields

**Files:**
- Modify: `unpixel.go` (add two exported Config fields + two options).
- Test: `unpixel_test.go` (options set the fields).

**Interfaces:**
- Produces (used by Task 4 + Task 6/7): `Config.FontPrior bool`, `Config.FontPriorTopK int`, `func WithFontPrior() Option`, `func WithFontPriorTopK(k int) Option`.

**Design note:** the fields are EXPORTED (like `BlockSize`/`BeamWidth`) so the separate `fontprior` package can read them. They are inert in the core search (`Recover`/`RecoverMultiFont` never read them) ⇒ byte-identical default preserved.

- [ ] **Step 1: Write the failing test**

Add to `unpixel_test.go`:

```go
func TestWithFontPriorOptions(t *testing.T) {
	var c Config
	WithFontPrior()(&c)
	if !c.FontPrior {
		t.Errorf("WithFontPrior did not set FontPrior")
	}
	WithFontPriorTopK(3)(&c)
	if c.FontPriorTopK != 3 {
		t.Errorf("FontPriorTopK = %d; want 3", c.FontPriorTopK)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test . -run TestWithFontPriorOptions -count=1`
Expected: FAIL — `c.FontPrior undefined`.

- [ ] **Step 3: Add the fields and options**

In the `Config` struct in `unpixel.go` (near the other tuning knobs like `BlockSize`/`BeamWidth` — find with `grep -n 'BeamWidth int' unpixel.go`), add:

```go
	// FontPrior, when true, signals callers (CLI, MCP) to route a multi-font
	// recovery through the blind font prior in the fontprior package, which
	// reorders the sweep so the likeliest font is tried first. It is inert in the
	// core search (Recover/RecoverMultiFont ignore it); fontprior.RecoverWithPrior
	// reorders unconditionally regardless of this flag. Default false.
	FontPrior bool

	// FontPriorTopK, when > 0, prunes the prior-ordered multi-font sweep to the K
	// best-ranked fonts (see fontprior.RecoverWithPrior). 0 (default) or a value
	// >= the font count means reorder-only (no truncation). Pruning can change the
	// result, so it is opt-in.
	FontPriorTopK int
```

Add the options next to `WithExpectedFormat` (find with `grep -n 'func WithExpectedFormat' unpixel.go`):

```go
// WithFontPrior enables the blind font prior for multi-font recovery: callers
// such as the CLI and MCP server route the sweep through
// [github.com/oioio-space/unpixel/fontprior.RecoverWithPrior], which ranks the
// bundled fonts by how well each explains the redaction and tries the likeliest
// first. Reordering alone does not change the result (the sweep still ranks by
// whole-image distance); it only speeds up the common case. Combine with
// [WithFontPriorTopK] to also prune the sweep.
func WithFontPrior() Option { return func(c *Config) { c.FontPrior = true } }

// WithFontPriorTopK prunes the prior-ordered multi-font sweep to the k
// best-ranked fonts. k <= 0 or k >= the font count means reorder-only. Pruning
// is faster but can drop the true font when k is too small, so it is opt-in;
// k >= 3 is recommended. Implies the font prior.
func WithFontPriorTopK(k int) Option { return func(c *Config) { c.FontPriorTopK = k } }
```

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test . -run TestWithFontPriorOptions -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add unpixel.go unpixel_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(unpixel): WithFontPrior / WithFontPriorTopK options (inert in core)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `fontprior` package — seam + heuristic prior

**Files:**
- Create: `fontprior/fontprior.go`
- Create: `fontprior/default_noml.go`
- Test: `fontprior/fontprior_test.go`

**Interfaces:**
- Consumes: `fonts.Font`/`fonts.All` (Task verified), `fontrank.NamedFont`/`FontScore`/`RankFontsAt` (Task 1).
- Produces (used by Task 4, 5): `type Ranked struct{ Name string; Score float64 }`; `type Prior interface { Rank(ctx, img image.Image, blockSize int, fnts []fonts.Font) ([]Ranked, error) }`; `type Histogram struct{}` implementing it; `func Default() Prior`.

- [ ] **Step 1: Write the failing test**

Create `fontprior/fontprior_test.go`:

```go
package fontprior_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/fontprior"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// mosaicOf renders text in the named bundled font and pixelates it in memory.
func mosaicOf(t *testing.T, fontName, text string, block int) image.Image {
	t.Helper()
	var data []byte
	for _, f := range fonts.All() {
		if f.Name == fontName {
			data = f.Data
		}
	}
	if data == nil {
		t.Fatalf("bundled font %q not found", fontName)
	}
	r, err := render.NewXImageFromFonts(data, nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	rendered, sx, err := r.Render(text, unpixel.Style{FontSize: 28})
	if err != nil || sx <= 0 {
		t.Fatalf("render: %v sx=%d", err, sx)
	}
	return pixelate.NewBlockAverage(block).Pixelate(rendered, 0, 0)
}

func rankOf(ranked []fontprior.Ranked, name string) int {
	for i, r := range ranked {
		if r.Name == name {
			return i
		}
	}
	return -1
}

func TestHistogram_ranksTrueFontTop3(t *testing.T) {
	// Distinct-category fonts the histogram should separate reliably.
	cases := []struct{ font, text string }{
		{"Liberation Mono", "ABC123"},
		{"Liberation Serif", "Hello World"},
		{"Liberation Sans", "Mosaic"},
	}
	const block = 6
	top1, top3 := 0, 0
	for _, c := range cases {
		img := mosaicOf(t, c.font, c.text, block)
		ranked, err := fontprior.Default().Rank(t.Context(), img, block, fonts.All())
		if err != nil {
			t.Fatalf("Rank(%q): %v", c.font, err)
		}
		r := rankOf(ranked, c.font)
		if r == 0 {
			top1++
		}
		if r >= 0 && r < 3 {
			top3++
		} else {
			t.Errorf("font %q ranked %d (want top-3); order=%v", c.font, r, ranked)
		}
	}
	t.Logf("font prior: top1 %d/%d, top3 %d/%d", top1, len(cases), top3, len(cases))
}

func TestHistogram_emptyFontsReturnsNil(t *testing.T) {
	got, err := fontprior.Default().Rank(t.Context(), image.NewRGBA(image.Rect(0, 0, 10, 10)), 0, nil)
	if err != nil {
		t.Fatalf("Rank(nil fonts): %v", err)
	}
	if got != nil {
		t.Errorf("Rank(nil fonts) = %v; want nil", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./fontprior/ -run Histogram -v -count=1`
Expected: FAIL — package `fontprior` does not exist.

- [ ] **Step 3: Implement the package**

Create `fontprior/fontprior.go`:

```go
// Package fontprior ranks the bundled fonts by how well each explains a mosaic
// redaction, blind — without any known plaintext — so the multi-font sweep can
// try the likeliest font first (reordering, result-preserving) or prune to the
// top-K (faster, opt-in).
//
// The default prior is a pure-Go pixelated-signature heuristic ([Histogram],
// wrapping internal/fontrank): it renders each font's exemplar, re-pixelates at
// the redaction's block size, and compares block-luminance histograms. A future
// ML classifier trained on the render->pixelate domain can replace the default
// via the //go:build ml seam (see Default) without changing callers.
//
// Use [RecoverWithPrior] for a one-call prior-ordered recovery over the bundled
// fonts:
//
//	res, err := fontprior.RecoverWithPrior(ctx, img,
//	    unpixel.WithBlockSize(6), unpixel.WithFontPriorTopK(3))
//	best := res[0] // best.Font, best.Result.BestGuess
package fontprior

import (
	"context"
	"image"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fontrank"
)

// Ranked is one prior result: a bundled font name and its match score, sorted
// best-first by the prior (lower Score is a better match).
type Ranked struct {
	// Name is the bundled font name, e.g. "Liberation Sans".
	Name string
	// Score is the prior's distance; lower is better. For the Histogram prior it
	// is the L1 block-luminance histogram distance in [0,1].
	Score float64
}

// Prior ranks candidate fonts by how well each explains a mosaic redaction,
// blind (no known plaintext). blockSize is the mosaic block side in pixels; pass
// 0 to auto-detect. Implementations must be safe for concurrent use and return a
// non-nil error only on context cancellation or an unrecoverable failure.
type Prior interface {
	Rank(ctx context.Context, img image.Image, blockSize int, fnts []fonts.Font) ([]Ranked, error)
}

// Histogram is the default pure-Go prior. It delegates to
// internal/fontrank.RankFontsAt (block-luminance histogram signature), which
// needs no known plaintext. The zero value is ready to use.
type Histogram struct{}

// Rank implements [Prior] using the block-luminance histogram ranker.
func (Histogram) Rank(ctx context.Context, img image.Image, blockSize int, fnts []fonts.Font) ([]Ranked, error) {
	if len(fnts) == 0 {
		return nil, nil
	}
	named := make([]fontrank.NamedFont, len(fnts))
	for i, f := range fnts {
		named[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}
	scores, err := fontrank.RankFontsAt(ctx, img, named, blockSize)
	if err != nil {
		return nil, err
	}
	ranked := make([]Ranked, len(scores))
	for i, s := range scores {
		ranked[i] = Ranked{Name: s.Name, Score: s.Score}
	}
	return ranked, nil
}
```

Create `fontprior/default_noml.go`:

```go
//go:build !ml

package fontprior

// Default returns the prior used by [RecoverWithPrior] and recommended for
// callers. Without the "ml" build tag it is the pure-Go [Histogram] heuristic.
// Building with -tags ml swaps in the trained ML classifier (see ml.go).
func Default() Prior { return Histogram{} }
```

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./fontprior/ -run Histogram -v -count=1`
Expected: PASS. If a case's font does not land in top-3, switch its text to a longer, more glyph-diverse string (the histogram needs enough ink to separate families) — keep the assertion at top-3, do not weaken it.

- [ ] **Step 5: Build check (cycle-free)**

Run: `CGO_ENABLED=0 go build ./...`
Expected: clean (no import cycle introduced by the new package).

- [ ] **Step 6: Commit**

```bash
git add fontprior/fontprior.go fontprior/default_noml.go fontprior/fontprior_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(fontprior): blind font-prior seam + Histogram heuristic

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `fontprior.RecoverWithPrior` — reorder + opt-in top-K

**Files:**
- Modify: `fontprior/fontprior.go` (add `FontResult` + `RecoverWithPrior` + helpers).
- Test: `fontprior/recover_test.go`

**Interfaces:**
- Consumes: `Default()`/`Prior` (Task 3), `Config.FontPriorTopK`/`Config.BlockSize` (Task 2, exported), `fonts.All`, `defaults.RendererFromFonts`, `unpixel.RecoverMultiFont`/`FontResult`/`Option`/`Result`.
- Produces (used by Task 6/7): `type FontResult struct{ Result unpixel.Result; Font string }`; `func RecoverWithPrior(ctx context.Context, img image.Image, opts ...unpixel.Option) ([]FontResult, error)`.

**Design notes:**
- Resolve a throwaway `unpixel.Config` from `opts` to read `FontPriorTopK` and `BlockSize` — then pass the SAME `opts` through to `RecoverMultiFont` unchanged.
- Always reorder by the prior. On prior error or empty ranking, fall back to catalog order (never worse than today).
- Build renderers in ranked order; `RecoverMultiFont` returns `FontResult{Result, Index}` where `Index` is into that ordered renderer slice — map it back to the ordered font's `Name`. `RecoverMultiFont` already ranks the returned slice best-first by `BestTotal`, so preserve its order while attaching names.

- [ ] **Step 1: Write the failing test**

Create `fontprior/recover_test.go`:

```go
package fontprior_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fontprior"
)

func TestRecoverWithPrior_reorderMatchesMultiFontWinner(t *testing.T) {
	const block = 6
	img := mosaicOf(t, "Liberation Mono", "GO2024", block)

	// Reorder-only (no top-K): winner must equal the plain multi-font winner.
	pri, err := fontprior.RecoverWithPrior(t.Context(), img, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("RecoverWithPrior: %v", err)
	}
	if len(pri) == 0 || pri[0].Font == "" {
		t.Fatalf("RecoverWithPrior returned no named result")
	}

	// Build the same sweep without the prior, via the library multi-font path.
	rs := bundledRenderers(t)
	multi, err := unpixel.RecoverMultiFont(t.Context(), img, rs, unpixel.WithBlockSize(block))
	if err != nil {
		t.Fatalf("RecoverMultiFont: %v", err)
	}
	if got, want := pri[0].Result.BestGuess, multi[0].Result.BestGuess; got != want {
		t.Errorf("prior winner %q != multi-font winner %q (reorder must preserve the winner)", got, want)
	}
}

func TestRecoverWithPrior_topKLimitsDecodes(t *testing.T) {
	const block = 6
	img := mosaicOf(t, "Liberation Serif", "Redacted", block)
	res, err := fontprior.RecoverWithPrior(t.Context(), img,
		unpixel.WithBlockSize(block), unpixel.WithFontPriorTopK(2))
	if err != nil {
		t.Fatalf("RecoverWithPrior topK: %v", err)
	}
	if len(res) > 2 {
		t.Errorf("top-K=2 returned %d font results; want <= 2", len(res))
	}
}
```

Add the `bundledRenderers` helper to `fontprior_test.go`:

```go
func bundledRenderers(t *testing.T) []unpixel.Renderer {
	t.Helper()
	rs, err := fonts.Renderers()
	if err != nil {
		t.Fatalf("fonts.Renderers: %v", err)
	}
	return rs
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./fontprior/ -run RecoverWithPrior -v -count=1`
Expected: FAIL — `undefined: fontprior.RecoverWithPrior`.

- [ ] **Step 3: Implement `RecoverWithPrior`**

Append to `fontprior/fontprior.go` (and add `"github.com/oioio-space/unpixel"` and `"github.com/oioio-space/unpixel/defaults"` to its imports):

```go
// FontResult is one prior-ordered recovery: the recovery and the bundled font
// that produced it. The slice returned by [RecoverWithPrior] is best-first by
// whole-image distance.
type FontResult struct {
	// Result is the recovery produced with this font.
	Result unpixel.Result
	// Font is the bundled font name that produced Result.
	Font string
}

// RecoverWithPrior recovers img over the bundled fonts, using the default blind
// prior to order the sweep so the likeliest font is decoded first. It always
// reorders; [unpixel.WithFontPriorTopK] additionally prunes the sweep to the K
// best-ranked fonts. opts are forwarded unchanged to [unpixel.RecoverMultiFont]
// (e.g. WithBlockSize, WithCharset, WithWorkers). Results are best-first by
// whole-image distance.
//
// If the prior fails (degenerate image), RecoverWithPrior falls back to the full
// sweep in catalog order — never worse than an unordered sweep. It returns the
// same errors as [unpixel.RecoverMultiFont] (nil image, no fonts, all failed).
func RecoverWithPrior(ctx context.Context, img image.Image, opts ...unpixel.Option) ([]FontResult, error) {
	if img == nil {
		return nil, unpixel.ErrNilImage
	}

	// Resolve a throwaway config to read the prior knobs; opts pass through intact.
	var cfg unpixel.Config
	for _, o := range opts {
		o(&cfg)
	}

	ordered := fonts.All()
	if ranked, err := Default().Rank(ctx, img, cfg.BlockSize, ordered); err == nil && len(ranked) == len(ordered) {
		ordered = reorderByRank(ordered, ranked)
		if k := cfg.FontPriorTopK; k > 0 && k < len(ordered) {
			ordered = ordered[:k]
		}
	}
	// else: prior failed → keep catalog order (full unordered sweep).

	renderers := make([]unpixel.Renderer, 0, len(ordered))
	names := make([]string, 0, len(ordered))
	for _, f := range ordered {
		r, err := defaults.RendererFromFonts(f.Data, nil)
		if err != nil {
			continue // skip a corrupt font rather than aborting the whole sweep
		}
		renderers = append(renderers, r)
		names = append(names, f.Name)
	}

	results, err := unpixel.RecoverMultiFont(ctx, img, renderers, opts...)
	if err != nil {
		return nil, err
	}

	out := make([]FontResult, len(results))
	for i, fr := range results {
		name := ""
		if fr.Index >= 0 && fr.Index < len(names) {
			name = names[fr.Index]
		}
		out[i] = FontResult{Result: fr.Result, Font: name}
	}
	return out, nil
}

// reorderByRank returns fnts reordered to match the prior ranking (best-first).
// Fonts present in fnts but absent from ranked are appended in their original
// order, so the result is always a permutation of fnts.
func reorderByRank(fnts []fonts.Font, ranked []Ranked) []fonts.Font {
	byName := make(map[string]fonts.Font, len(fnts))
	for _, f := range fnts {
		byName[f.Name] = f
	}
	out := make([]fonts.Font, 0, len(fnts))
	seen := make(map[string]bool, len(fnts))
	for _, r := range ranked {
		if f, ok := byName[r.Name]; ok && !seen[r.Name] {
			out = append(out, f)
			seen[r.Name] = true
		}
	}
	for _, f := range fnts {
		if !seen[f.Name] {
			out = append(out, f)
		}
	}
	return out
}
```

> Verify `unpixel.ErrNilImage` exists (`grep -n 'ErrNilImage' unpixel.go`). It does (used by Verify/RecoverMultiFont). If the exact name differs, use the one RecoverMultiFont returns for a nil image.

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./fontprior/ -run RecoverWithPrior -v -count=1`
Expected: PASS.

- [ ] **Step 5: Full package test + build**

Run: `scripts/gotest-caged.sh go test ./fontprior/ -count=1` then `CGO_ENABLED=0 go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add fontprior/fontprior.go fontprior/recover_test.go fontprior/fontprior_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(fontprior): RecoverWithPrior — prior-ordered sweep + opt-in top-K

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: ML-ready seam (`//go:build ml` stub)

**Files:**
- Create: `fontprior/ml.go` (`//go:build ml`)
- Test: `fontprior/ml_test.go` (`//go:build ml`)

**Interfaces:**
- Consumes: `Prior`, `Ranked` (Task 3).
- Produces: under `-tags ml`, `Default()` returns `mlPrior{}`; `var ErrMLNotBuilt error`. The `!ml` `Default()` (Task 3) and the `ml` `Default()` are mutually exclusive build-tagged definitions of the same symbol.

**Design note:** this is the seam ONLY — no weights, no gonum, no training. The stub documents the future contract and fails loudly until a model is trained, so callers compiled with `-tags ml` get a clear error rather than silent wrong behaviour.

- [ ] **Step 1: Write the failing test (ml-tagged)**

Create `fontprior/ml_test.go`:

```go
//go:build ml

package fontprior_test

import (
	"errors"
	"image"
	"testing"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/fontprior"
)

func TestMLPrior_notBuiltYet(t *testing.T) {
	_, err := fontprior.Default().Rank(t.Context(), image.NewRGBA(image.Rect(0, 0, 8, 8)), 6, fonts.All())
	if !errors.Is(err, fontprior.ErrMLNotBuilt) {
		t.Errorf("ml Default().Rank err = %v; want ErrMLNotBuilt", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails (with the ml tag)**

Run: `scripts/gotest-caged.sh go test -tags ml ./fontprior/ -run MLPrior -v -count=1`
Expected: FAIL — `fontprior.ErrMLNotBuilt` / `mlPrior` undefined.

- [ ] **Step 3: Implement the stub**

Create `fontprior/ml.go`:

```go
//go:build ml

package fontprior

import (
	"context"
	"errors"
	"image"

	"github.com/oioio-space/unpixel/fonts"
)

// ErrMLNotBuilt is returned by the ML prior until a trained model is wired in.
// It exists so callers built with -tags ml fail loudly rather than silently
// degrading. Build without the tag for the pure-Go [Histogram] prior.
var ErrMLNotBuilt = errors.New("fontprior: ML prior not built — train and embed a model, or build without -tags ml")

// Default returns the ML prior when built with -tags ml. Until a model is
// trained and embedded, its Rank returns [ErrMLNotBuilt]; RecoverWithPrior then
// falls back to the catalog-order sweep.
func Default() Prior { return mlPrior{} }

// mlPrior is the seam for a CNN font classifier trained on the
// render->pixelate->font-label synthetic domain (the renderer is the labeller).
// Training and weights live outside this repo; inference would be a hand-written
// pure-Go forward pass (no CGO). It is intentionally unimplemented: this commit
// ships only the build-tag seam so the model can drop in without touching callers.
type mlPrior struct{}

// Rank reports [ErrMLNotBuilt] until a trained model is embedded.
func (mlPrior) Rank(_ context.Context, _ image.Image, _ int, _ []fonts.Font) ([]Ranked, error) {
	return nil, ErrMLNotBuilt
}
```

- [ ] **Step 4: Run to verify it passes (ml tag) and the default build is unaffected**

Run: `scripts/gotest-caged.sh go test -tags ml ./fontprior/ -run MLPrior -v -count=1` → PASS.
Run: `CGO_ENABLED=0 go build -tags ml ./fontprior/` → clean.
Run: `scripts/gotest-caged.sh go test ./fontprior/ -count=1` → still PASS (default `!ml` build unchanged).

- [ ] **Step 5: Commit**

```bash
git add fontprior/ml.go fontprior/ml_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(fontprior): //go:build ml seam (ErrMLNotBuilt stub, no model yet)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: MCP — blind `rank_fonts` + `font_prior_top_k`

**Files:**
- Modify: `mcp/rankfonts.go` (`known_text` optional → blind histogram-only mode).
- Modify: `mcp/decode.go` (optional `font_prior_top_k`, forwarded on the multi-font/engine path).
- Test: `mcp/rankfonts_test.go` (blind mode) + `mcp/decode_format_test.go` or a new `mcp/decode_fontprior_test.go`.

**Interfaces:**
- Consumes: `fontrank.RankFonts` (blind histogram), existing `RankFontsReport`; `fontprior.RecoverWithPrior` (Task 4) or `unpixel.WithFontPriorTopK` (Task 2).

**Design note for `rank_fonts`:** when `known_text` is empty, skip the fingerprint + `RankByMetrics` (which need known glyphs) and return the histogram-only ranking from `fontrank.RankFonts`. Keep the blended behaviour when `known_text` is supplied.

- [ ] **Step 1: Write the failing tests**

Add to `mcp/rankfonts_test.go` (create if absent; package `mcpserver`):

```go
func TestRankFonts_blindNoKnownText(t *testing.T) {
	img := mosaicFixtureMCP(t, "Liberation Mono", "ABC123", 6)
	rep, err := RankFonts(t.Context(), img, "") // blind: empty known_text
	if err != nil {
		t.Fatalf("blind RankFonts: %v", err)
	}
	if len(rep.Ranked) == 0 || rep.Best == "" {
		t.Errorf("blind RankFonts returned empty ranking")
	}
}
```

Provide `mosaicFixtureMCP` in this test file (render via `fonts.All()` + `pixelate.NewBlockAverage(block).Pixelate(..., 0, 0)`), or reuse an existing in-memory mcp fixture helper if one exists (`grep -n 'func.*[Ff]ixture\|Pixelate' mcp/*_test.go`).

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./mcp/ -run 'RankFonts_blind' -v -count=1`
Expected: FAIL — current `RankFonts` errors on empty `known_text`.

- [ ] **Step 3: Make `known_text` optional in `mcp/rankfonts.go`**

Replace the empty-knownText guard in `RankFonts` with a blind branch:

```go
func RankFonts(ctx context.Context, img image.Image, knownText string) (RankFontsReport, error) {
	all := fonts.All()
	named := make([]fontrank.NamedFont, len(all))
	for i, f := range all {
		named[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}

	// Blind mode: no known text → histogram-only ranking (no glyph fingerprint).
	if knownText == "" {
		histScores, err := fontrank.RankFonts(ctx, img, named)
		if err != nil {
			return RankFontsReport{}, fmt.Errorf("unpixel_rank_fonts: rank fonts: %w", err)
		}
		return reportFromScores(histScores), nil
	}

	// Known-text mode: blend histogram + glyph-metric fingerprint (existing path).
	// ... keep the existing fingerprint + RankByMetrics + blend code unchanged ...
}
```

Extract the existing "build FontRankEntry slice + Best" tail into a helper `reportFromScores(scores []fontrank.FontScore) RankFontsReport` and reuse it for the blind branch. Update the tool description and the `KnownText` jsonschema to say it is OPTIONAL (blind histogram ranking when omitted). Update the `toolRankFonts` Description line "Requires a known_text string…" to "Optionally takes a known_text string; without it, ranks blind by pixelated-signature histogram."

- [ ] **Step 4: Add `font_prior_top_k` to `mcp/decode.go`**

Add to `decodeInput` (after `ExpectedFormat`, with `omitzero`):

```go
	// FontPriorTopK, when > 0, runs a blind font-prior-ordered multi-font sweep
	// pruned to the K best-ranked bundled fonts (engine path). 0 = disabled
	// (single default font). See fontprior.RecoverWithPrior.
	FontPriorTopK int `json:"font_prior_top_k,omitzero" jsonschema:"Engine-only: run a blind font-prior sweep pruned to the top-K bundled fonts (0 = off)"`
```

Add the matching `DecodeOptions.FontPriorTopK int` field and thread it through the `Decode` and `handleDecode` literals (like `ExpectedFormat`). In `decodeEngine`, when `in.FontPriorTopK > 0`, route through the prior sweep instead of single-font `Recover`:

```go
	if in.FontPriorTopK > 0 {
		fopts := append([]unpixel.Option{}, opts...)
		fopts = append(fopts, unpixel.WithFontPriorTopK(in.FontPriorTopK))
		ranked, ferr := fontprior.RecoverWithPrior(ctx, img, fopts...)
		if ferr != nil {
			return DecodeResult{}, ferr
		}
		best := ranked[0]
		notes = append(notes, fmt.Sprintf("font prior top-%d; chose %s", in.FontPriorTopK, best.Font))
		return DecodeResult{
			Text:       best.Result.BestGuess,
			Distance:   best.Result.BestTotal,
			Fidelity:   best.Result.Fidelity(),
			Font:       best.Font,
			MethodUsed: "engine",
			Notes:      notes,
		}, nil
	}
```

Place this block in `decodeEngine` AFTER `opts` is fully assembled (charset/block/font/expected_format) and BEFORE the single-font `unpixel.Recover` call, so the prior sweep inherits the same options. Add the `fontprior` import. Guard: `ranked` is non-empty because `RecoverWithPrior` errors when all fonts fail.

- [ ] **Step 5: Write the decode test**

Create `mcp/decode_fontprior_test.go` (package `mcpserver_test` to match the existing decode test style):

```go
//go:build !ml

package mcpserver_test

import (
	"strings"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

func TestDecodeEngine_fontPriorTopK(t *testing.T) {
	img := mosaicFixtureExt(t, "Liberation Mono", "GO2024", 6)
	res, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{
		CharsetPreset: "alnum",
		BlockSize:     6,
		FontSize:      28,
		FontPriorTopK: 3,
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Font == "" {
		t.Errorf("expected a chosen font in the result")
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "font prior top-3") {
		t.Errorf("notes = %v; want a font-prior note", res.Notes)
	}
}
```

Provide `mosaicFixtureExt` in this file (render via `fonts.All()` + pixelate). If the existing external decode test file already exports a fixture helper, reuse it.

- [ ] **Step 6: Run and verify**

Run: `scripts/gotest-caged.sh go test ./mcp/ -run 'RankFonts_blind|DecodeEngine_fontPriorTopK' -v -count=1`
Expected: PASS. Then `CGO_ENABLED=0 go build ./...` clean.

- [ ] **Step 7: Commit**

```bash
git add mcp/rankfonts.go mcp/decode.go mcp/rankfonts_test.go mcp/decode_fontprior_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(mcp): blind rank_fonts (optional known_text) + decode font_prior_top_k

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: CLI flags + full validation

**Files:**
- Modify: `cmd/unpixel/main.go` (`--font-prior` / `--font-prior-top-k` flags; reorder/truncate the bundle sweep).
- Test: `cmd/unpixel/main_test.go` (flags parse + reorder applied).

**Interfaces:**
- Consumes: `fontprior.Default()` (Task 3) / `fontprior.Ranked`, the existing `bundleCandidates()`/`sweepRecover()` flow, `flagParams`.

**Design note:** keep the CLI's existing sweep machinery (escalation tiers, confidence gate). Only reorder/truncate the `[]candidateFont` before `sweepRecover` when `--font-prior` is set, ranking `fonts.All()` via `fontprior.Default().Rank` and matching by name.

- [ ] **Step 1: Write the failing test**

Add to `cmd/unpixel/main_test.go` a unit test for the reorder helper (the helper is added in Step 3):

```go
func TestApplyFontPrior_reordersAndTruncates(t *testing.T) {
	cands := []candidateFont{
		{display: "Liberation Sans", jsonName: "Liberation Sans"},
		{display: "Liberation Mono", jsonName: "Liberation Mono"},
		{display: "Liberation Serif", jsonName: "Liberation Serif"},
	}
	ranked := []fontprior.Ranked{
		{Name: "Liberation Serif", Score: 0.1},
		{Name: "Liberation Mono", Score: 0.2},
		{Name: "Liberation Sans", Score: 0.3},
	}
	got := applyFontPrior(cands, ranked, 2)
	if len(got) != 2 {
		t.Fatalf("top-2 returned %d", len(got))
	}
	if got[0].jsonName != "Liberation Serif" || got[1].jsonName != "Liberation Mono" {
		t.Errorf("order = [%s,%s]; want [Liberation Serif, Liberation Mono]", got[0].jsonName, got[1].jsonName)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./cmd/unpixel/ -run ApplyFontPrior -v -count=1`
Expected: FAIL — `undefined: applyFontPrior`.

- [ ] **Step 3: Add the flags, the helper, and the wiring**

Add two flags to the `Flags:` slice in `cmd/unpixel/main.go`:

```go
			&cli.BoolFlag{
				Name:  "font-prior",
				Usage: "order the bundled-font sweep by a blind font prior (likeliest font first)",
			},
			&cli.IntFlag{
				Name:  "font-prior-top-k",
				Usage: "with --font-prior, decode only the top-K ranked fonts (0 = all; implies --font-prior)",
			},
```

Add the two fields to `flagParams` (find the struct with `grep -n 'type flagParams' cmd/unpixel/main.go`) and populate them where `flagParams` is built from the cli context (mirror an existing bool/int flag read):

```go
	fontPrior     bool
	fontPriorTopK int
```

Add the pure helper (near `bundleCandidates`):

```go
// applyFontPrior reorders cands to match the prior ranking (best-first), then
// truncates to topK when 0 < topK < len. Candidates absent from ranked keep
// their original relative order at the end. cands is returned unchanged when
// ranked is empty.
func applyFontPrior(cands []candidateFont, ranked []fontprior.Ranked, topK int) []candidateFont {
	if len(ranked) == 0 {
		return cands
	}
	pos := make(map[string]int, len(ranked))
	for i, r := range ranked {
		if _, dup := pos[r.Name]; !dup {
			pos[r.Name] = i
		}
	}
	ordered := append([]candidateFont(nil), cands...)
	slices.SortStableFunc(ordered, func(a, b candidateFont) int {
		ra, oka := pos[a.jsonName]
		rb, okb := pos[b.jsonName]
		switch {
		case oka && okb:
			return cmp.Compare(ra, rb)
		case oka:
			return -1
		case okb:
			return 1
		default:
			return 0
		}
	})
	if topK > 0 && topK < len(ordered) {
		ordered = ordered[:topK]
	}
	return ordered
}
```

Wire it into the bundle sweep path: where `bundleCandidates()` feeds `sweepRecover`, when `p.fontPrior || p.fontPriorTopK > 0`, rank and reorder before the sweep:

```go
	if p.fontPrior || p.fontPriorTopK > 0 {
		if ranked, err := fontprior.Default().Rank(ctx, img, base.BlockSize, fonts.All()); err == nil {
			cands = applyFontPrior(cands, ranked, p.fontPriorTopK)
			if !p.quiet && p.format != "json" {
				fmt.Fprintf(os.Stderr, "Font prior: trying %s first\n", cands[0].display)
			}
		}
	}
```

> Locate the exact call site with `grep -n 'bundleCandidates\|sweepRecover' cmd/unpixel/main.go` and insert the block after `cands` is built and `img`/`base` are available, before `sweepRecover`. Add imports: `slices`, `cmp`, `github.com/oioio-space/unpixel/fontprior`, and `github.com/oioio-space/unpixel/fonts` (likely already imported). Read the surrounding function to get `ctx`, `img`, `base`, `p` names right.

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./cmd/unpixel/ -run ApplyFontPrior -v -count=1`
Expected: PASS.

- [ ] **Step 5: Full validation gate**

Run, in order:
1. `CGO_ENABLED=0 go build ./...` → clean.
2. `scripts/gotest-caged.sh go test ./... -count=1` → PASS (incl. 17/17 panel; default paths unchanged).
3. `CGO_ENABLED=0 go build -tags ml ./...` and `scripts/gotest-caged.sh go test -tags ml ./fontprior/ -count=1` → clean + PASS (ml seam compiles and the stub errors as designed).
4. `mise run cover:check` → ≥ 85% (report the %). If short, add table cases to `fontprior/fontprior_test.go` (more fonts in `TestHistogram_ranksTrueFontTop3`) or `internal/fontrank/fontrank_test.go`.
5. `mise run ci` → all green (lint + test + cgo:check + scans).

- [ ] **Step 6: Commit**

```bash
git add cmd/unpixel/main.go cmd/unpixel/main_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(cli): --font-prior / --font-prior-top-k order+prune the font sweep

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/superpowers/specs/2026-06-29-font-prior-design.md`):
- §3.1 `fontprior` seam (Ranked, Prior, Histogram, Default) → Task 3. ✅
- §3.1 Histogram wraps `fontrank.RankFonts` → Task 3 (via `RankFontsAt`). ✅
- §3.2 ML seam `//go:build ml`, `ErrMLNotBuilt`, no weights/gonum → Task 5. ✅
- §3.3 `fontrank.RankFontsAt(blockSize)`, `RankFonts` delegates → Task 1. ✅
- §3.4 `RecoverWithPrior` always reorders + opt-in top-K + fallback → Task 4. ✅
- §3.5 root options `WithFontPrior`/`WithFontPriorTopK` (exported Config fields) → Task 2. ✅
- §3.6 MCP blind `rank_fonts` + `font_prior_top_k` → Task 6. ✅
- §3.7 CLI `--font-prior`/`--font-prior-top-k` → Task 7. ✅
- §2.1 reorder byte-identical winner → Task 4 test. ✅
- §2.2 prior ranks true font top-3 → Task 3 test. ✅
- §2.3 top-K prune limits decodes / no regress → Task 4 test. ✅
- §2.4 pure-Go, caged, in-memory, ≥85%, panel 17/17 → Global Constraints + Task 7 step 5. ✅

**2. Placeholder scan:** every code step shows complete code. The `grep`-to-confirm notes (call-site location, `ErrNilImage` name, fixture-helper reuse) are explicit verification steps against real code, not logic placeholders. The one prose pointer in Task 6 Step 3 ("keep the existing fingerprint…code unchanged") refers to extracting an existing block, with the surrounding new code fully shown. ✅

**3. Type consistency:** `Ranked{Name,Score}`, `Prior.Rank(ctx,img,blockSize,[]fonts.Font)`, `Histogram`, `Default() Prior`, `FontResult{Result,Font}`, `RecoverWithPrior(ctx,img,opts) ([]FontResult,error)`, `RankFontsAt(ctx,img,[]NamedFont,int)`, `Config.FontPrior`/`Config.FontPriorTopK`, `WithFontPrior`/`WithFontPriorTopK` are used identically across Tasks 1–7. The `ml` and `!ml` `Default()` are mutually exclusive build-tagged definitions of one symbol (consistent signature). ✅

> **Known open detail for the implementer:** Task 6 and Task 7 fixtures render via `fonts.All()` + `pixelate`. Confirm `internal/pixelate` and `internal/render` are importable from `mcp`/`cmd` test files (they are internal but rooted in the same module). If a package already provides an in-memory mosaic fixture helper, reuse it instead of duplicating.
