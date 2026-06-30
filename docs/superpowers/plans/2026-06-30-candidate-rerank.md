# Candidate Re-rank Stage (#5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An opt-in post-search re-rank stage that fuses each candidate's physical `Verify` distance with a linguistic score over the top-K, with a tunable weight (weight 0 = today's physical order), plus an ML-ready CTC seam behind `//go:build ml` (no model built now).

**Architecture:** A new top-level `rerank` package (cycle-safe: imports `unpixel` + `internal/lang`; neither imports it; root `unpixel` imports neither). The pure-Go `Linguistic` reranker blends `Verdict.Distance` with an LM score; `Rerank` is a one-call convenience that runs `unpixel.Verify` then fuses. Root `unpixel` gains an inert opt-in option/field; MCP `verify_candidates` gains `rerank_weight`. The CTC CRNN is deferred to a `//go:build ml` stub.

**Tech Stack:** Pure Go (no CGO). Reuses `unpixel.Verify` (#3), `unpixel.Verdict`, `internal/lang.PriorFor`. No new dependency (no gonum until the CTC is built).

## Global Constraints

- **NO CGO, ever.** `CGO_ENABLED=0` is pinned; enforced by `mise run cgo:check`. No gonum / ML runtime added in this plan.
- **Opt-in / byte-identical default:** the core search, `unpixel.Verify`, `RankFinal`, and the 17/17 synthetic fixture panel are untouched. `Config.RerankWeight` is inert (read only by the `rerank` package). `weight ≤ 0` ⇒ candidates ordered by ascending physical distance (today's behaviour).
- **No import cycle:** `rerank` is a new top-level package above `unpixel` and `internal/lang`. Root `unpixel` gains only a plain Config field + option (no new import). Verify with `CGO_ENABLED=0 go build ./...`.
- **Caged tests only:** never run `go test` bare. Use `scripts/gotest-caged.sh` for every test run (the `unpixel`/`mcp` suites take minutes — expected).
- **In-memory fixtures only:** every test image is rendered/pixelated in code; never load a gitignored or network fixture.
- **Coverage gate:** `COVER_MIN=85`; keep ≥ 85% (`mise run cover:check`).
- **Commit gate:** arm the `/simplify` marker (`$GIT_DIR/claude-simplify-ok`, `git diff --cached | sha1sum | cut -d' ' -f1`) only AFTER genuinely reviewing, in a SEPARATE bash call from `git commit`. Run `git restore --staged PROGRESS.md` before arming.
- **Commit trailer:** every commit message ends with
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** `feat/candidate-rerank` (already created from `master`; do not commit on `master`).
- Follow `go-style-guide` and `use-modern-go` (`slices.SortStableFunc`, `cmp.Compare`, `any`, `t.Context()`, table tests with field names, `got` before `want`).

## Verified facts (do not re-litigate)

- `unpixel.Verify(ctx, img image.Image, candidates []string, opts ...Option) ([]Verdict, error)` (verify.go:48) — returns one `Verdict` per candidate, **in input order**.
- `unpixel.Verdict struct { Text string; Distance float64; Match bool }` (verify.go:12); `Distance` ∈ [0,1] lower=better; `Match` = `Distance < unpixel.VerifyMatchThreshold` (exported const = 0.10).
- `unpixel.Config.LanguageModel func(string) float64` (unpixel.go:125); `unpixel.WithLanguageModel`. Root `unpixel` does NOT import `internal/lang`.
- `internal/lang.PriorFor(l lang.Language) func(string) float64` (bilingual.go:255); `lang.English` / `lang.French` consts (bilingual.go:37-39); higher = more plausible. `internal/lang` does NOT import root `unpixel` → `rerank` may import both.
- `Config` exported tuning fields sit together: `FontPrior bool` (unpixel.go:190), `FontPriorTopK int` (unpixel.go:196). Options live near `WithFontPriorTopK` (unpixel.go:1954).
- `mcp/server.go`: `verifyInput{ImagePath, Candidates, BlockSize, Charset}` (:360); `RankedCandidate{Text, Distance, Match}` (:374); `VerifyReport{Ranked, Best, Margin, Pick}` (:385); `VerifyCandidates(ctx, img, candidates []string, blockSize int, charset string) (VerifyReport, error)` (:430) — builds `ranked` from verdicts, `slices.SortFunc` by Distance, `Best=ranked[0].Text`, `Margin=ranked[1].Distance-ranked[0].Distance`, `Pick=` first `Match` in distance order. `mcp/server.go` imports `unpixel`, `slices`, `cmp` but NOT `internal/lang`.

## File Structure

- **Modify** `unpixel.go` — add inert exported `Config.RerankWeight float64` + `WithRerankWeight(w)`.
- **Create** `rerank/rerank.go` — `Ranked`, `Reranker`, `Linguistic`, `Rerank` convenience.
- **Create** `rerank/default_noml.go` (`//go:build !ml`) — `Default() → Linguistic{}`.
- **Create** `rerank/ml.go` (`//go:build ml`) — `Default() → ctcReranker` stub + `ErrCTCNotBuilt`.
- **Create** `rerank/rerank_test.go`, `rerank/ml_test.go` (`//go:build ml`).
- **Modify** `mcp/server.go` — `rerank_weight` field + `VerifyCandidates` fusion wiring.
- **Modify/Create** `mcp/*_test.go` — rerank_weight tests.

---

## Task 1: Root `unpixel` option + Config field

**Files:**
- Modify: `unpixel.go`
- Test: `unpixel_test.go`

**Interfaces:**
- Produces (used by Task 2 + Task 4): `Config.RerankWeight float64`, `func WithRerankWeight(w float64) Option`.

**Design note:** exported (the separate `rerank` package reads it) and INERT in the core (`Recover`/`Verify`/`RankFinal` never read it) ⇒ byte-identical default.

- [ ] **Step 1: Write the failing test**

Add to `unpixel_test.go`:

```go
func TestWithRerankWeight(t *testing.T) {
	var c Config
	WithRerankWeight(0.1)(&c)
	if c.RerankWeight != 0.1 {
		t.Errorf("RerankWeight = %v; want 0.1", c.RerankWeight)
	}
}
```

(If `unpixel_test.go` is package `unpixel_test`, qualify as `unpixel.Config` / `unpixel.WithRerankWeight` to match the file's existing style — check the top of the file.)

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test . -run TestWithRerankWeight -count=1`
Expected: FAIL — `c.RerankWeight undefined`.

- [ ] **Step 3: Add the field and option**

In the `Config` struct, after `FontPriorTopK int` (unpixel.go:196):

```go
	// RerankWeight, when > 0, enables the post-search re-rank stage in the rerank
	// package: candidate ordering blends physical Verify distance with a language
	// score (blended = distance − RerankWeight·(lmScore − bestLM)). 0 (default)
	// means physical order only. It is inert in the core search (Recover/Verify/
	// RankFinal ignore it); only rerank.Rerank reads it. A conservative starting
	// value is ~0.05–0.1; too high lets the language model override correct physics.
	RerankWeight float64
```

Add the option near `WithFontPriorTopK` (unpixel.go:1954):

```go
// WithRerankWeight sets Config.RerankWeight, the weight the rerank package uses
// to blend a candidate's language score into its physical distance when
// re-ordering the top-K. 0 (default) leaves candidates in physical-distance
// order. Higher values let a strong language preference override a small physical
// distance gap; keep it small (~0.05–0.1). See [github.com/oioio-space/unpixel/rerank.Rerank].
func WithRerankWeight(w float64) Option { return func(c *Config) { c.RerankWeight = w } }
```

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test . -run TestWithRerankWeight -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add unpixel.go unpixel_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(unpixel): WithRerankWeight option + inert Config.RerankWeight

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `rerank` package — Linguistic reranker + convenience

**Files:**
- Create: `rerank/rerank.go`
- Create: `rerank/default_noml.go`
- Test: `rerank/rerank_test.go`

**Interfaces:**
- Consumes: `unpixel.Verify`/`Verdict`/`Option`/`Config`/`VerifyMatchThreshold` (Task 1's `RerankWeight`), `lang.PriorFor`/`lang.English`.
- Produces (used by Task 3, 4): `type Ranked struct{ Text string; Distance, LMScore, Blended float64 }`; `type Reranker interface { Rerank(ctx, img image.Image, verdicts []unpixel.Verdict, lm func(string) float64, weight float64) ([]Ranked, error) }`; `type Linguistic struct{}`; `func Default() Reranker`; `func Rerank(ctx, img image.Image, candidates []string, opts ...unpixel.Option) ([]Ranked, error)`.

- [ ] **Step 1: Write the failing tests**

Create `rerank/rerank_test.go`:

```go
package rerank_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/rerank"
)

// verdicts is a tiny constructor for test input.
func vd(text string, dist float64) unpixel.Verdict {
	return unpixel.Verdict{Text: text, Distance: dist, Match: dist < unpixel.VerifyMatchThreshold}
}

func TestLinguistic_weightZeroIsPhysicalOrder(t *testing.T) {
	// Input in arbitrary order; weight 0 must sort by ascending distance.
	in := []unpixel.Verdict{vd("b", 0.30), vd("a", 0.10), vd("c", 0.20)}
	got, err := rerank.Linguistic{}.Rerank(t.Context(), nil, in, func(string) float64 { return 0 }, 0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	want := []string{"a", "c", "b"}
	for i, w := range want {
		if got[i].Text != w {
			t.Errorf("pos %d = %q; want %q (physical order)", i, got[i].Text, w)
		}
	}
}

func TestLinguistic_lmRescuesPlausibleCandidate(t *testing.T) {
	// Physics marginally prefers the implausible "rn"; the LM strongly prefers "m".
	in := []unpixel.Verdict{vd("rn", 0.10), vd("m", 0.14)}
	lm := func(s string) float64 {
		if s == "m" {
			return 0.0 // plausible
		}
		return -5.0 // implausible
	}

	// weight 0: physics wins → "rn" first.
	phys, _ := rerank.Linguistic{}.Rerank(t.Context(), nil, in, lm, 0)
	if phys[0].Text != "rn" {
		t.Errorf("weight 0 top = %q; want rn (physics)", phys[0].Text)
	}

	// weight 0.02: blended("m") = 0.14 − 0.02·(0−0) = 0.14;
	// blended("rn") = 0.10 − 0.02·(−5−0) = 0.10 + 0.10 = 0.20 → "m" wins.
	fused, _ := rerank.Linguistic{}.Rerank(t.Context(), nil, in, lm, 0.02)
	if fused[0].Text != "m" {
		t.Errorf("weight 0.02 top = %q; want m (LM rescue)", fused[0].Text)
	}
}

func TestLinguistic_nilLMIsPhysicalOrder(t *testing.T) {
	in := []unpixel.Verdict{vd("b", 0.30), vd("a", 0.10)}
	got, _ := rerank.Linguistic{}.Rerank(t.Context(), nil, in, nil, 0.5)
	if got[0].Text != "a" {
		t.Errorf("nil LM top = %q; want a (physical)", got[0].Text)
	}
}

func TestLinguistic_empty(t *testing.T) {
	got, err := rerank.Linguistic{}.Rerank(t.Context(), nil, nil, nil, 0.1)
	if err != nil {
		t.Fatalf("Rerank(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Rerank(empty) len = %d; want 0", len(got))
	}
}

func TestDefault_isLinguistic(t *testing.T) {
	// In the default (!ml) build, Default returns a working pure-Go reranker.
	got, err := rerank.Default().Rerank(t.Context(), nil,
		[]unpixel.Verdict{vd("a", 0.1)}, func(string) float64 { return 0 }, 0)
	if err != nil {
		t.Fatalf("Default().Rerank: %v", err)
	}
	if len(got) != 1 || got[0].Text != "a" {
		t.Errorf("Default().Rerank = %+v; want single 'a'", got)
	}
}
```

> The end-to-end `Rerank` convenience (which calls `unpixel.Verify`, needing the `defaults` components) is exercised in Task 5's integration test with a real in-memory fixture; this task's tests use synthetic verdicts so they stay fast and pure.

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./rerank/ -count=1`
Expected: FAIL — package `rerank` does not exist.

- [ ] **Step 3: Implement the package**

Create `rerank/rerank.go`:

```go
// Package rerank re-orders decode candidates that have already been scored
// physically (by unpixel.Verify), by blending each candidate's image distance
// with a language score. It generalises the narrow language tie-break buried in
// the search into a first-class, tunable, inspectable post-search stage.
//
// The default reranker ([Linguistic]) is pure Go and reuses the existing
// language models (internal/lang). A future discriminative CTC model trained on
// the render→pixelate domain can replace the default via the //go:build ml seam
// (see Default) without changing callers.
//
// One-call use over the bundled forward model:
//
//	ranked, err := rerank.Rerank(ctx, img, candidates,
//	    unpixel.WithBlockSize(6), unpixel.WithRerankWeight(0.08))
//	best := ranked[0].Text
package rerank

import (
	"cmp"
	"context"
	"image"
	"slices"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/lang"
)

// Ranked is one re-ranked candidate.
type Ranked struct {
	// Text is the candidate string.
	Text string
	// Distance is the physical Verify distance in [0,1] (lower is better).
	Distance float64
	// LMScore is the language score (higher is more plausible); 0 when no LM.
	LMScore float64
	// Blended is the fused ordering key (lower is better): the candidates are
	// returned sorted ascending by Blended.
	Blended float64
}

// Reranker re-orders physically-scored candidates. img is provided for a future
// discriminative (CTC) implementation; the pure-Go default ignores it. lm scores
// linguistic plausibility (higher is better); weight is the blend strength in
// distance-units per language-unit. Implementations must be safe for concurrent
// use and return a non-nil error only on an unrecoverable failure.
type Reranker interface {
	Rerank(ctx context.Context, img image.Image, verdicts []unpixel.Verdict, lm func(string) float64, weight float64) ([]Ranked, error)
}

// Linguistic is the pure-Go default reranker. It blends each candidate's physical
// distance with its language score, relative to the most plausible candidate:
// blended = distance − weight·(lmScore − bestLM). With weight ≤ 0 or lm == nil
// the blend is the physical distance, so the result is physical-distance order.
// The zero value is ready to use.
type Linguistic struct{}

// Rerank implements [Reranker]. It ignores img (the language blend needs only the
// candidate strings and their physical distances).
func (Linguistic) Rerank(_ context.Context, _ image.Image, verdicts []unpixel.Verdict, lm func(string) float64, weight float64) ([]Ranked, error) {
	if len(verdicts) == 0 {
		return nil, nil
	}

	lmScores := make([]float64, len(verdicts))
	bestLM := 0.0
	useLM := lm != nil && weight > 0
	if useLM {
		for i, v := range verdicts {
			s := lm(v.Text)
			lmScores[i] = s
			if i == 0 || s > bestLM {
				bestLM = s
			}
		}
	}

	ranked := make([]Ranked, len(verdicts))
	for i, v := range verdicts {
		blended := v.Distance
		if useLM {
			blended = v.Distance - weight*(lmScores[i]-bestLM)
		}
		ranked[i] = Ranked{Text: v.Text, Distance: v.Distance, LMScore: lmScores[i], Blended: blended}
	}

	slices.SortStableFunc(ranked, func(a, b Ranked) int {
		if c := cmp.Compare(a.Blended, b.Blended); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Distance, b.Distance); c != 0 {
			return c
		}
		return cmp.Compare(a.Text, b.Text)
	})
	return ranked, nil
}

// Rerank verifies candidates against img with the faithful forward model
// (unpixel.Verify) and re-orders them with [Default] by blending physical
// distance with a language score. The language model is cfg.LanguageModel when
// set (via WithLanguageModel/WithPriors), else the bundled English prior; the
// blend weight is cfg.RerankWeight (set via WithRerankWeight; 0 = physical
// order). opts are forwarded to Verify (e.g. WithBlockSize, WithCharset).
// Results are sorted best-first. It returns the errors of unpixel.Verify.
func Rerank(ctx context.Context, img image.Image, candidates []string, opts ...unpixel.Option) ([]Ranked, error) {
	verdicts, err := unpixel.Verify(ctx, img, candidates, opts...)
	if err != nil {
		return nil, err
	}
	var cfg unpixel.Config
	for _, o := range opts {
		o(&cfg)
	}
	lm := cfg.LanguageModel
	if lm == nil {
		lm = lang.PriorFor(lang.English)
	}
	return Default().Rerank(ctx, img, verdicts, lm, cfg.RerankWeight)
}
```

Create `rerank/default_noml.go`:

```go
//go:build !ml

package rerank

// Default returns the reranker used by [Rerank]. Without the "ml" build tag it is
// the pure-Go [Linguistic] reranker. Building with -tags ml swaps in the trained
// CTC model (see ml.go).
func Default() Reranker { return Linguistic{} }
```

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./rerank/ -count=1`
Expected: PASS (all sub-tests).

- [ ] **Step 5: Build check (cycle-free)**

Run: `CGO_ENABLED=0 go build ./...`
Expected: clean (no import cycle).

- [ ] **Step 6: Commit**

```bash
git add rerank/rerank.go rerank/default_noml.go rerank/rerank_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(rerank): Linguistic reranker (physics+LM blend) + Rerank convenience

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: CTC seam (`//go:build ml` stub)

**Files:**
- Create: `rerank/ml.go` (`//go:build ml`)
- Test: `rerank/ml_test.go` (`//go:build ml`)

**Interfaces:**
- Consumes: `Reranker`, `Ranked` (Task 2), `unpixel.Verdict`.
- Produces: under `-tags ml`, `Default() → ctcReranker{}`; `var ErrCTCNotBuilt error`. The `!ml` `Default()` (Task 2) and this `ml` `Default()` are mutually-exclusive build-tagged definitions of one symbol.

**Design note:** seam ONLY — no weights, no gonum, no training. Fails loudly so `-tags ml` callers don't silently degrade.

- [ ] **Step 1: Write the failing test (ml-tagged)**

Create `rerank/ml_test.go`:

```go
//go:build ml

package rerank_test

import (
	"errors"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/rerank"
)

func TestCTCReranker_notBuiltYet(t *testing.T) {
	_, err := rerank.Default().Rerank(t.Context(), nil,
		[]unpixel.Verdict{{Text: "a", Distance: 0.1}}, func(string) float64 { return 0 }, 0.1)
	if !errors.Is(err, rerank.ErrCTCNotBuilt) {
		t.Errorf("ml Default().Rerank err = %v; want ErrCTCNotBuilt", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails (with the ml tag)**

Run: `scripts/gotest-caged.sh go test -tags ml ./rerank/ -run CTCReranker -v -count=1`
Expected: FAIL — `rerank.ErrCTCNotBuilt` / `ctcReranker` undefined.

- [ ] **Step 3: Implement the stub**

Create `rerank/ml.go`:

```go
//go:build ml

package rerank

import (
	"context"
	"errors"
	"image"

	"github.com/oioio-space/unpixel"
)

// ErrCTCNotBuilt is returned by the CTC reranker until a trained model is wired
// in. It exists so callers built with -tags ml fail loudly rather than silently
// degrading. Build without the tag for the pure-Go [Linguistic] reranker.
var ErrCTCNotBuilt = errors.New("rerank: CTC reranker not built — train and embed a model, or build without -tags ml")

// Default returns the CTC reranker when built with -tags ml. Until a model is
// trained and embedded, its Rerank returns [ErrCTCNotBuilt].
func Default() Reranker { return ctcReranker{} }

// ctcReranker is the seam for a CRNN-CTC model trained on the
// render→pixelate→text synthetic domain (the renderer is the labeller). Unlike
// the language-only [Linguistic] blend, a CTC head scores P(text | image)
// discriminatively and can recognise fonts outside the bundled set that the
// forward model cannot render. Inference would be a hand-written pure-Go forward
// pass (conv+BiLSTM+CTC, no CGO). It is intentionally unimplemented: this commit
// ships only the build-tag seam so the model can drop in without touching callers.
type ctcReranker struct{}

// Rerank reports [ErrCTCNotBuilt] until a trained model is embedded.
func (ctcReranker) Rerank(_ context.Context, _ image.Image, _ []unpixel.Verdict, _ func(string) float64, _ float64) ([]Ranked, error) {
	return nil, ErrCTCNotBuilt
}
```

- [ ] **Step 4: Verify both builds**

Run: `scripts/gotest-caged.sh go test -tags ml ./rerank/ -run CTCReranker -v -count=1` → PASS.
Run: `CGO_ENABLED=0 go build -tags ml ./rerank/` → clean.
Run: `scripts/gotest-caged.sh go test ./rerank/ -count=1` → still PASS (default `!ml` build unchanged).

- [ ] **Step 5: Commit**

```bash
git add rerank/ml.go rerank/ml_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(rerank): //go:build ml CTC seam (ErrCTCNotBuilt stub, no model yet)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: MCP `verify_candidates` rerank_weight

**Files:**
- Modify: `mcp/server.go`
- Test: `mcp/server_test.go` (or a new `mcp/rerank_test.go` — match the existing mcp test package).

**Interfaces:**
- Consumes: `rerank.Default()`/`Ranked` (Task 2), `lang.PriorFor`/`lang.English`, `unpixel.Verify`/`Verdict`/`VerifyMatchThreshold`.
- Produces: `verifyInput.RerankWeight`; `VerifyCandidates(ctx, img, candidates, blockSize, charset, rerankWeight float64)`.

**Design note:** when `rerankWeight > 0`, the `Ranked` list is built in **fused order** (via `rerank`); `Best`/`Margin` follow that order. **`Pick` stays a physical decision** — always the lowest-distance candidate whose `Match` is true, independent of the fused order (refactor it to compute from the verdicts, which keeps today's behaviour when `rerankWeight == 0`).

- [ ] **Step 1: Write the failing test**

Add to the mcp test file (package `mcpserver` or `mcpserver_test` — match the file you add to; this example uses the internal `mcpserver` package so it can call `VerifyCandidates` directly):

```go
func TestVerifyCandidates_rerankReordersByLM(t *testing.T) {
	// Build an in-memory mosaic of the real word so both candidates verify, then
	// confirm a positive rerank weight can promote the linguistically-plausible
	// candidate. Use a fixture where the true text is a dictionary word.
	img := mosaicFixtureMCP(t, "Liberation Sans", "the", 6)

	// rerank_weight 0 → physics order (baseline, unchanged behaviour).
	base, err := VerifyCandidates(t.Context(), img, []string{"the", "tho"}, 6, "lower", 0)
	if err != nil {
		t.Fatalf("VerifyCandidates(0): %v", err)
	}
	if base.Best == "" {
		t.Fatal("empty Best")
	}

	// rerank_weight > 0 → fused order is well-formed and Pick stays physical.
	fused, err := VerifyCandidates(t.Context(), img, []string{"the", "tho"}, 6, "lower", 0.1)
	if err != nil {
		t.Fatalf("VerifyCandidates(0.1): %v", err)
	}
	if len(fused.Ranked) != 2 {
		t.Fatalf("fused ranked len = %d; want 2", len(fused.Ranked))
	}
	// Pick (if any) must be a physical Match, never invented by the LM.
	if fused.Pick != "" {
		var matched bool
		for _, rc := range fused.Ranked {
			if rc.Text == fused.Pick && rc.Match {
				matched = true
			}
		}
		if !matched {
			t.Errorf("Pick %q is not a physical Match in Ranked", fused.Pick)
		}
	}
}
```

Provide `mosaicFixtureMCP` (render via `fonts.All()` + `pixelate.NewBlockAverage(block).Pixelate(img,0,0)`) if not already present in the mcp test files (`grep -n 'func.*[Ff]ixture\|Pixelate' mcp/*_test.go` — reuse an existing helper if one exists).

> If the existing mcp tests are `package mcpserver_test` (external), call the exported wrapper instead: there is no exported `VerifyCandidates` rerank entry until this task adds the param. Since `VerifyCandidates` IS exported, an external test can call `mcpserver.VerifyCandidates(...)`. Match whichever package the neighbouring verify test uses.

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./mcp/ -run 'VerifyCandidates_rerank' -v -count=1`
Expected: FAIL — `VerifyCandidates` takes 5 args, not 6.

- [ ] **Step 3: Add the field + thread it + wire the fusion**

In `mcp/server.go` imports, add:

```go
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/rerank"
```

Add to `verifyInput` (after `Charset`, mcp/server.go:370):

```go
	// RerankWeight, when > 0, re-orders the ranked candidates by blending the
	// physical distance with a language score (English prior). 0 (default) keeps
	// pure physical-distance order. Pick always remains the lowest-distance
	// physical match regardless of this value.
	RerankWeight float64 `json:"rerank_weight,omitzero" jsonschema:"Blend weight for language re-ranking of candidates (0 = physical order only; ~0.05–0.1 to enable)"`
```

Update the tool descriptor `Description` (the `unpixel_verify_candidates` tool var) to mention: "Set rerank_weight>0 to blend a language prior into the ranking (Pick stays a physical match)."

In `handleVerify`, pass the new field:

```go
	report, err := VerifyCandidates(ctx, img, in.Candidates, in.BlockSize, in.Charset, in.RerankWeight)
```

Replace `VerifyCandidates` (mcp/server.go:430) signature and body tail so it accepts `rerankWeight` and fuses when positive. The new body:

```go
func VerifyCandidates(ctx context.Context, img image.Image, candidates []string, blockSize int, charset string, rerankWeight float64) (VerifyReport, error) {
	var opts []unpixel.Option
	if blockSize > 0 {
		opts = append(opts, unpixel.WithBlockSize(blockSize))
	}
	if charset != "" {
		opts = append(opts, unpixel.WithCharset(charset))
	}

	verdicts, err := unpixel.Verify(ctx, img, candidates, opts...)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("score candidates: %w", err)
	}

	ranked := make([]RankedCandidate, len(verdicts))
	if rerankWeight > 0 {
		// Fused order: blend physical distance with the English language prior.
		rr, rerr := rerank.Default().Rerank(ctx, img, verdicts, lang.PriorFor(lang.English), rerankWeight)
		if rerr != nil {
			return VerifyReport{}, fmt.Errorf("rerank candidates: %w", rerr)
		}
		for i, r := range rr {
			ranked[i] = RankedCandidate{Text: r.Text, Distance: r.Distance, Match: r.Distance < unpixel.VerifyMatchThreshold}
		}
	} else {
		// Physical order (unchanged behaviour).
		for i, v := range verdicts {
			ranked[i] = RankedCandidate{Text: v.Text, Distance: v.Distance, Match: v.Match}
		}
		slices.SortFunc(ranked, func(a, b RankedCandidate) int {
			return cmp.Compare(a.Distance, b.Distance)
		})
	}

	report := VerifyReport{Ranked: ranked}
	if len(ranked) > 0 {
		report.Best = ranked[0].Text
	}
	if len(ranked) >= 2 {
		report.Margin = ranked[1].Distance - ranked[0].Distance
	}
	// Pick: lowest-distance candidate with a confident physical match — always a
	// physical decision, independent of any language re-ranking.
	report.Pick = lowestDistanceMatch(verdicts)
	return report, nil
}

// lowestDistanceMatch returns the Text of the verdict with the smallest Distance
// among those whose Match is true, or "" when none match.
func lowestDistanceMatch(verdicts []unpixel.Verdict) string {
	best := ""
	bestDist := 0.0
	for _, v := range verdicts {
		if v.Match && (best == "" || v.Distance < bestDist) {
			best = v.Text
			bestDist = v.Distance
		}
	}
	return best
}
```

(Verify the `Decode` package's other callers of `VerifyCandidates`, if any, get the new `0` argument: `grep -rn 'VerifyCandidates(' mcp/ | grep -v _test` and update each call site to pass `0` for the physical-order default.)

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./mcp/ -run 'VerifyCandidates' -v -count=1`
Expected: PASS. Then `CGO_ENABLED=0 go build ./...` clean.

- [ ] **Step 5: Commit**

```bash
git add mcp/server.go mcp/*_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(mcp): verify_candidates rerank_weight (LM-fused order, physical Pick)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Integration validation + coverage

**Files:**
- Create: `rerank/integration_test.go`

**Interfaces:**
- Consumes: `rerank.Rerank` (Task 2) end-to-end with the real forward model.

- [ ] **Step 1: Write the end-to-end test**

Create `rerank/integration_test.go`:

```go
package rerank_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire Renderer/Pixelator/Metric
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/rerank"
)

func mosaicOf(t *testing.T, fontName, text string, block int) image.Image {
	t.Helper()
	var data []byte
	for _, f := range fonts.All() {
		if f.Name == fontName {
			data = f.Data
			break
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

func TestRerank_endToEnd(t *testing.T) {
	const block = 6
	img := mosaicOf(t, "Liberation Sans", "the", block)
	cands := []string{"the", "tho", "thn"}

	// weight 0: physical order; the true text should verify well.
	phys, err := rerank.Rerank(t.Context(), img, cands,
		unpixel.WithBlockSize(block), unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz"))
	if err != nil {
		t.Fatalf("Rerank(0): %v", err)
	}
	if len(phys) != len(cands) {
		t.Fatalf("ranked len = %d; want %d", len(phys), len(cands))
	}

	// weight > 0: still returns all candidates, best-first by blended score.
	fused, err := rerank.Rerank(t.Context(), img, cands,
		unpixel.WithBlockSize(block), unpixel.WithCharset("abcdefghijklmnopqrstuvwxyz"),
		unpixel.WithRerankWeight(0.08))
	if err != nil {
		t.Fatalf("Rerank(0.08): %v", err)
	}
	if len(fused) != len(cands) {
		t.Errorf("fused len = %d; want %d", len(fused), len(cands))
	}
	// Blended is sorted ascending.
	for i := 1; i < len(fused); i++ {
		if fused[i].Blended < fused[i-1].Blended {
			t.Errorf("not sorted by Blended at %d: %v", i, fused)
		}
	}
}
```

- [ ] **Step 2: Run the end-to-end test**

Run: `scripts/gotest-caged.sh go test ./rerank/ -run TestRerank_endToEnd -v -count=1`
Expected: PASS. (If `"the"` does not verify cleanly at block=6/size=28, adjust the block/font/charset so Verify produces sensible distances — the assertion is structural, not a specific winner.)

- [ ] **Step 3: Coverage gate**

Run: `mise run cover:check`
Expected: ≥ 85% (report the %). If short, add `Linguistic.Rerank` table cases (e.g. a 3-candidate fusion with mixed LM scores asserting exact blended values) to `rerank/rerank_test.go`.

- [ ] **Step 4: ml build + default build both green**

Run: `CGO_ENABLED=0 go build -tags ml ./...` → clean.
Run: `scripts/gotest-caged.sh go test -tags ml ./rerank/ -count=1` → PASS (the `!ml`-only convenience/Linguistic tests should be tagged `//go:build !ml` if they call `Default()` and assume Linguistic — Task 2's `TestDefault_isLinguistic` does; add `//go:build !ml` to `rerank_test.go` OR change `TestDefault_isLinguistic` to call `rerank.Linguistic{}` directly. Prefer the latter: only `Default()`-as-Linguistic assumptions need guarding; the synthetic `Linguistic{}` tests are tag-agnostic. Specifically: leave the `Linguistic{}` tests untagged; make `TestDefault_isLinguistic` call `Linguistic{}` OR move it under `//go:build !ml`.)

- [ ] **Step 5: Full CI gate**

Run: `scripts/gotest-caged.sh go test ./... -count=1` → PASS (incl. 17/17 panel; default paths unchanged).
Run: `mise run ci` → all green (lint + test + cgo:check + scans).

- [ ] **Step 6: Commit**

```bash
git add rerank/integration_test.go rerank/rerank_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "test(rerank): end-to-end Rerank over the forward model + coverage

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/superpowers/specs/2026-06-30-candidate-rerank-design.md`):
- §3.1 `rerank` package: `Ranked`, `Reranker`, `Linguistic` (blend), `Rerank` convenience → Task 2. ✅
- §3.1 blend `distance − weight·(lmScore − bestLM)`, weight≤0/nil-lm ⇒ physical → Task 2 `Linguistic.Rerank` + tests. ✅
- §3.2 CTC seam `//go:build ml`, `ErrCTCNotBuilt`, no model/gonum → Task 3. ✅
- §3.3 root `WithRerankWeight` + inert `Config.RerankWeight` → Task 1. ✅
- §3.4 MCP `rerank_weight`, fused Best/Margin, physical Pick → Task 4. ✅
- §2.1 weight-0 physical order → Task 2 `TestLinguistic_weightZeroIsPhysicalOrder`. ✅
- §2.2 fusion rescues plausible candidate → Task 2 `TestLinguistic_lmRescuesPlausibleCandidate`. ✅
- §2.3 end-to-end Rerank → Task 5. ✅
- §2.4 pure-Go, caged, in-memory, ≥85%, panel 17/17 → Global Constraints + Task 5. ✅
- §6 ml seam compiles + stub errors → Task 3. ✅

**2. Placeholder scan:** every step has complete code. The `grep`-to-confirm notes (mcp test package style, other `VerifyCandidates` call sites, fixture-helper reuse) are explicit verification steps against real code, not logic placeholders.

**3. Type consistency:** `Ranked{Text,Distance,LMScore,Blended}`, `Reranker.Rerank(ctx,img,[]unpixel.Verdict,func(string)float64,float64)`, `Linguistic`, `Default() Reranker`, `Rerank(ctx,img,[]string,...Option)`, `Config.RerankWeight`/`WithRerankWeight`, `VerifyCandidates(...,rerankWeight float64)` are used identically across Tasks 1–5. The `!ml`/`ml` `Default()` are mutually-exclusive build-tagged definitions of one symbol.

> **Known cross-task note for the implementer:** Task 4 changes the `VerifyCandidates` signature (adds `rerankWeight`). Grep all call sites (`grep -rn 'VerifyCandidates(' mcp/`) — at minimum `handleVerify`; pass `0` at any non-rerank call site to preserve physical-order behaviour. Task 5 Step 4 flags the `-tags ml` test-tagging detail (keep `Linguistic{}` tests tag-agnostic; only `Default()`-assumes-Linguistic needs `Linguistic{}` directly or an `!ml` tag).
