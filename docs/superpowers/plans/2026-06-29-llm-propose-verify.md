# LLM-propose → physics-verify Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make UnPixel's verification decisive — a public `unpixel.Verify` that scores candidate strings with the engine's faithful forward model and an absolute match threshold — and give an LLM client the cues to propose (`unpixel_propose_hints`), so the propose→physics-verify loop can break the long-sentence wall.

**Architecture:** `unpixel.Verify` reuses `Recover`'s preparation (auto-fingerprint operator from #2 + inferred block/style + component wiring) to build the same forward model, then scores each candidate at its best grid offset via `internal/search`'s `PipelineScorer` — distances calibrated like `BestTotal` (≈0 on exact). MCP `verify_candidates` is rebranched onto it (gaining a decisive pick); a new `unpixel_propose_hints` tool aggregates a character-count estimate + block/font/bbox + leaked PDF/Office context (#1). All additive — `Recover`/`New`/the panel are untouched.

**Tech Stack:** Go (pure, `CGO_ENABLED=0`), `internal/search` (PipelineScorer/DiscoverOffsets), `internal/capacity`, `internal/leak` (#1), `internal/forensics` (#2 via WithAuto), MCP go-sdk.

## Global Constraints

Apply to **every** task.

- **Pure Go, no CGO** (`cgo:check` gate). No new dependency.
- **Run tests caged**: `./scripts/gotest-caged.sh go test ./<pkg>/ -run <Name> -v`. Never bare `go test`.
- **Invariant**: panel fixtures 17/17 fidélité 1.000 + blur 13/14 unchanged. This feature is **additive** — do NOT modify `Recover`, `New`, `Engine.Run`, or any decode path. `Verify` replicates the needed preparation steps; it must not change `Recover`. (If you find yourself editing `Recover`, stop — the spec mandates the core stays untouched so the invariant is trivial.)
- **In-memory fixtures only** — forge bytes in the test; NEVER a gitignored/network file (broke CI before).
- **No cross-metric comparison pitfall**: `Verify` compares distances from the SAME faithful model across candidates (calibrated like BestTotal) — never mixes metrics.
- **Coverage** ≥ 85% for changed packages; `mise run cover:check` stays green.
- **Bound work**: cap candidates at `maxVerifyCandidates = 256`; reuse `CachingScorer`. `Verify` runs outside the core search loop (per request), not a hot path — but don't add gratuitous allocation.
- **Commit ritual (mandatory gate):** arm the `/simplify` marker in a **SEPARATE** bash call from `git commit` (the hook blocks arm+commit in one call):
  ```bash
  GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
  ```
  A post-commit hook re-stages `PROGRESS.md`; if it appears staged, `git restore --staged PROGRESS.md` before arming. Stage only the task's files. Commit messages end with:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** all work on `feat/llm-propose-verify` (create before Task 1). Never commit on master.

**Existing signatures this plan consumes (verbatim):**
- `internal/search.NewPipelineScorer(redacted *image.RGBA, cfg unpixel.Config) *search.PipelineScorer`
- `internal/search.NewCachingScorer(inner *search.PipelineScorer, maxEntries int) *search.CachingScorer`
- `(*search.CachingScorer).TotalScore(ctx context.Context, guess string, offset unpixel.Offset) float64` (returns whole-image distance in [0,1]; 1 on error)
- `internal/search.DiscoverOffsets(ctx context.Context, scorer search.Scorer, cfg unpixel.Config, emit func(unpixel.Progress)) []unpixel.Offset`
- root `unpixel`: `type Offset`, `type Config`, `type Option`, `applyDefaults(cfg Config) Config` (unexported, same package), `applyAutoFingerprint(cfg *Config, rgba *image.RGBA)` (unexported), `detectAndDeskew(rgba *image.RGBA) (*image.RGBA, …, grid)` (unexported — read its exact return in Recover), `InferBlockSize(img image.Image) int`, `var DefaultComponents func(cfg *Config) error`, `imutil.ToRGBA`.
- `internal/capacity.Analyze(ctx, r unpixel.Renderer, charset string, fontSize float64, block int, phase image.Point, opts ...capacity.Option) (capacity.Capacity, error)` (read `capacity.Capacity` for the char-count field).
- `internal/leak.Scan(path string, opts leak.Options) (leak.Result, bool, error)` (#1).
- MCP: `mcpsdk.AddTool(srv, tool, handler)`; handler `func(ctx, *mcpsdk.CallToolRequest, in) (*mcpsdk.CallToolResult, out, error)`; `errResult`, `toolJSON`, `loadImage`. Current `VerifyReport{Ranked []RankedCandidate, Best string, Margin float64}`, `RankedCandidate{Text string, Distance float64}`.

---

### Task 1: public `unpixel.Verify` (faithful decisive scorer)

**Files:**
- Create: `verify.go`
- Test: `verify_test.go`

**Interfaces:**
- Consumes: `internal/search` scorer/offsets, `Recover`'s prep helpers (same package), `DefaultComponents`.
- Produces:
  ```go
  // Verdict is the result of verifying one candidate string.
  type Verdict struct {
      Text     string  // the candidate
      Distance float64 // faithful whole-image distance in [0,1] (≈0 = exact)
      Match    bool    // Distance < VerifyMatchThreshold
  }
  // VerifyMatchThreshold is the absolute distance below which a candidate is a
  // confident physical match. Calibrated so exact recoveries (≈0) match and
  // clearly-wrong candidates do not. Tuned in Task 4.
  const VerifyMatchThreshold = 0.10
  // Verify scores each candidate against img with the engine's faithful forward
  // model (same render→operator→metric as Recover), at the candidate's best grid
  // offset. opts mirror Recover's (WithCharset/WithBlockSize/WithStyle/WithAuto…);
  // WithAuto-style fingerprinting is applied by default. Candidates beyond
  // maxVerifyCandidates are ignored. Returns one Verdict per accepted candidate,
  // in input order.
  func Verify(ctx context.Context, img image.Image, candidates []string, opts ...Option) ([]Verdict, error)
  ```

**Implementation:** Build `cfg` from `opts` (default to the auto path: set the auto flags as `WithAuto` does, unless the caller set a Pixelator/BlockSize). Replicate Recover's preparation on `rgba := imutil.ToRGBA(img)`: deskew/auto-crop (only if autoCrop set — for Verify, mirror exactly what Recover does in its prologue lines ~505-575), block-size inference, `cfg = applyDefaults(cfg)`, `applyAutoFingerprint(&cfg, rgba)`. Then wire components: `if cfg.Renderer == nil || cfg.Pixelator == nil || cfg.Metric == nil { if DefaultComponents != nil { _ = DefaultComponents(&cfg) } }`. Build `scorer := search.NewCachingScorer(search.NewPipelineScorer(rgba, cfg), cfg.CacheSize)`; `offsets := search.DiscoverOffsets(ctx, scorer, cfg, func(unpixel.Progress){})`. For each candidate (cap `maxVerifyCandidates`): `dist := min over offsets of scorer.TotalScore(ctx, cand, off)` (if `len(offsets)==0`, score at `Offset{}`); `Verdict{cand, dist, dist < VerifyMatchThreshold}`.

IMPLEMENTER NOTE: read `Recover` (unpixel.go ~lines 500-600) and replicate ONLY its preparation prologue (deskew/crop/block/applyDefaults/applyAutoFingerprint), NOT its search/Run. Do not edit `Recover`. Correctness is proven empirically by Step 1's dist≈0 assertion — if "go" doesn't score ≈0, the prep is missing a step; align it with Recover until it does. Add `maxVerifyCandidates = 256` const.

- [ ] **Step 1: Write the failing test (true string ≈0/match, wrong string high/no-match)**

```go
package unpixel_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire DefaultComponents
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// goMosaic renders "go" via the default renderer path is overkill here; instead
// reuse the project's fixture file would need disk. Build a mosaic by rendering
// the candidate through the SAME engine is circular. Simplest reliable fixture:
// load a committed fixture image. block08_go.png is committed (testdata/fixtures).
func TestVerify_decisive(t *testing.T) {
	img, err := loadFixtureImage(t, "testdata/fixtures/block08_go.png") // helper below
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	vs, err := unpixel.Verify(t.Context(), img, []string{"go", "xy"},
		unpixel.WithCharset("go abcde"), unpixel.WithMaxLength(2))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	byText := map[string]unpixel.Verdict{}
	for _, v := range vs {
		byText[v.Text] = v
	}
	if got := byText["go"]; !got.Match || got.Distance > 0.2 {
		t.Errorf("Verify(go) = {dist %.3f, match %v}, want match with dist≈0", got.Distance, got.Match)
	}
	if got := byText["xy"]; got.Match {
		t.Errorf("Verify(xy) = match (dist %.3f), want no-match for a wrong string", got.Distance)
	}
	_ = pixelate.NewBlockAverage // keep import only if used; remove if not
	_ = color.RGBA{}
}
```
IMPLEMENTER NOTE: `block08_go.png` IS committed (it is in the recovery panel, not gitignored — unlike testdata/wild). Provide `loadFixtureImage(t, path)` (png.Decode of os.Open) in the test. Trim the unused `pixelate`/`color` imports. If `WithMaxLength` isn't the exact option name, grep options in unpixel.go.

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./ -run TestVerify_decisive -v`
Expected: FAIL — `unpixel.Verify` undefined.

- [ ] **Step 3: Implement `verify.go`** per the Implementation section.

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./ -run TestVerify_decisive -v`
Expected: PASS. If `go` doesn't score ≈0, align the prep with Recover (block size / operator / style) until it does.

- [ ] **Step 5: Non-regression — panel untouched**

Run: `mise run bench:panel`
Expected: fixtures 17/17 fidélité 1.000, blur 13/14 (Recover unchanged → trivially holds; this confirms no accidental core edit).

- [ ] **Step 6: Commit**

```bash
git add verify.go verify_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(unpixel): Verify — decisive candidate scoring via the faithful forward model

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: rebranch MCP `verify_candidates` onto `unpixel.Verify`

**Files:**
- Modify: `mcp/server.go` (`handleVerify`, `verifyScore`/scoring helper, `VerifyReport`, `RankedCandidate`)
- Test: `mcp/verify_decisive_test.go`

**Interfaces:**
- Consumes: `unpixel.Verify`/`Verdict` (Task 1).
- Produces: `RankedCandidate{Text, Distance, Match}` (add `Match bool json:"match"`); `VerifyReport{Ranked, Best, Margin, Pick}` (add `Pick string json:"pick"` — the lowest-distance candidate with `Match==true`, else `""`).

- [ ] **Step 1: Write the failing test (decisive pick on a fixture)**

```go
package mcpserver_test

import (
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

func TestVerifyCandidates_decisivePick(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep, err := mcp.VerifyCandidates(t.Context(), img, []string{"go", "ab", "xy"})
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}
	if rep.Pick != "go" {
		t.Errorf("Pick = %q, want %q (decisive physical match)", rep.Pick, "go")
	}
}
```
IMPLEMENTER NOTE: expose/confirm an exported `mcp.VerifyCandidates(ctx, img, candidates) (VerifyReport, error)` core (the handler delegates to it). `loadFixture` exists in mcp test helpers.

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run TestVerifyCandidates_decisivePick -v`
Expected: FAIL — `Pick` undefined / wrong.

- [ ] **Step 3: Rebranch the verify core**

In `mcp/server.go`: change the scoring core to call `unpixel.Verify(ctx, img, candidates)` instead of `mosaictext.ScoreCandidates`. Build `Ranked` from the verdicts (sort ascending Distance, carry `Match`). Set `Best` = lowest-distance text (unchanged semantics). Set `Pick` = lowest-distance text whose `Match` is true, else `""`. Keep `Margin`. Update the doc comment to describe the faithful model + decisive pick. Remove the `mosaictext.ScoreCandidates` import use here (leave the function in `mosaictext`).

- [ ] **Step 4: Run to verify it passes + existing verify tests**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run 'TestVerify' -v`
Expected: PASS (new + any existing verify tests; update an existing test only if its assertion encodes the OLD looser-distance semantics — adjust to the faithful distances, do not weaken intent).

- [ ] **Step 5: Commit**

```bash
git add mcp/server.go mcp/verify_decisive_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(mcp): verify_candidates is decisive (faithful model + match + pick)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: new MCP `unpixel_propose_hints` tool

**Files:**
- Create: `mcp/propose_hints.go`
- Modify: `mcp/server.go` (register `toolProposeHints` in `NewServer`)
- Test: `mcp/propose_hints_test.go`

**Interfaces:**
- Consumes: `Analyze` (block/font/bbox), `internal/capacity.Analyze` (char count), `internal/leak.Scan` (#1), default renderer (from `defaults`/`DefaultComponents`).
- Produces: tool `unpixel_propose_hints(image_path string)` → `HintsReport{ CharCountEstimate int json:"char_count_estimate"; BlockSize int json:"block_size"; FontSizePt float64 json:"font_size_pt"; RedactionBbox []int json:"redaction_bbox,omitzero"; CharsetHint string json:"charset_hint,omitzero"; LeakedContext string json:"leaked_context,omitzero" }`. Exported `mcp.ProposeHints(path string) (HintsReport, error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestProposeHints_charCount(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep, err := mcp.ProposeHintsImage(img) // image-based core; path wrapper handles leak
	if err != nil {
		t.Fatalf("ProposeHints: %v", err)
	}
	if rep.CharCountEstimate < 1 || rep.CharCountEstimate > 6 {
		t.Errorf("CharCountEstimate = %d, want a small count (~2 for 'go')", rep.CharCountEstimate)
	}
	if rep.BlockSize != 8 {
		t.Errorf("BlockSize = %d, want 8", rep.BlockSize)
	}
}
```
IMPLEMENTER NOTE: split a `ProposeHintsImage(img)` core (Analyze + capacity, no file) from `ProposeHints(path)` (adds `leak.Scan` for LeakedContext) so the image-only test needs no temp file. capacity.Analyze needs a renderer+charset+fontSize+block+phase — derive fontSize/block from Analyze, use the default renderer (obtain via a zero Config + DefaultComponents, or a render.New default), charset = the recommended/alnum, phase from grid (or image.Point{}). Read capacity.Capacity for the count field name.

- [ ] **Step 2: Run to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run TestProposeHints -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `propose_hints.go` + register**

Implement `ProposeHintsImage`/`ProposeHints` + `toolProposeHints`/`handleProposeHints`; register `mcpsdk.AddTool(srv, toolProposeHints, handleProposeHints)` in `NewServer`. `LeakedContext` = `leak.Scan(path, leak.Options{}).Text` when found and the source is a text leak (pdf/office); empty otherwise. CharsetHint: a coarse guess from Analyze's recommended charset.

- [ ] **Step 4: Run to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run TestProposeHints -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add mcp/propose_hints.go mcp/propose_hints_test.go mcp/server.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(mcp): unpixel_propose_hints — char-count + block/font/bbox + leaked context

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: calibrate threshold, validate, coverage, docs

**Files:**
- Modify: `verify.go` (const if needed), `verify_test.go` (calibration test), `mcp/resources.go` (methods/tools doc if applicable)

- [ ] **Step 1: Calibrate `VerifyMatchThreshold`**

Add a calibration test: for ≥2 committed fixtures (e.g. `block08_go.png`→"go", `text_hello.png`→"hello", `alnum_Go2.png`→"Go2"), assert the true string scores `< VerifyMatchThreshold` and a deliberately-wrong same-length string scores `≥ VerifyMatchThreshold`. Adjust the const if needed so all true<τ≤false; document the chosen value with the observed spread. (Use the per-fixture truth from `testdata/fixtures/manifest.json`.)

- [ ] **Step 2: Coverage**

Run: `./scripts/gotest-caged.sh go test -coverprofile=/tmp/c.out ./ ./mcp/ && go tool cover -func=/tmp/c.out | tail -1`
Add direct tests for any under-covered new branch (Verify with `maxVerifyCandidates` truncation; empty candidates → empty result; propose_hints with a leaked PDF via #1). Then `mise run cover:check` ≥ 85% PASS — paste the line.

- [ ] **Step 3: Full gate**

Run: `mise run ci > /tmp/ci.log 2>&1; echo "EXIT=$?"; grep -E "Total coverage|ERROR|FAIL|cover:check" /tmp/ci.log`
Expected: `EXIT=0`, coverage ≥ 85%, no ERROR/FAIL.

- [ ] **Step 4: Docs**

If `mcp/resources.go` enumerates tools/methods, add a one-line entry for `unpixel_propose_hints` and note that `verify_candidates` is now decisive. (grep the resource; skip if it's only the read-only catalogue — say so.)

- [ ] **Step 5: Commit**

```bash
git add -A
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "test(unpixel,mcp): calibrate VerifyMatchThreshold + cover verify/hints paths

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §3.1 public `unpixel.Verify` (faithful model + threshold + Verdict) → Task 1. ✅
- §3.2 MCP verify_candidates rebranched (Match + decisive Pick, drops ScoreCandidates) → Task 2. ✅
- §3.3 `unpixel_propose_hints` (char-count via capacity + Analyze + leak context) → Task 3. ✅
- §4 faithful forward model + calibrated threshold → Task 1 (model) + Task 4 (calibration). ✅
- §5 additive, core/panel untouched → Global Constraints + Task 1 Step 5 (panel) + no Recover edits. ✅
- §6 tests: decisive true/false (T1), decisive pick (T2), char-count (T3), calibration + coverage (T4); in-memory/committed fixtures, caged. ✅
- §2 success criteria: faithful Verify (T1), correct decisive pick where ranker was ambiguous (T2/T4), char-count hint (T3), additive/pure-Go/caged (all). ✅
- §8 bounds (maxVerifyCandidates), cross-metric avoidance (same model), τ calibration → Global + T1 + T4. ✅

**Placeholder scan:** No TBD/TODO. IMPLEMENTER NOTES (T1 prep-replication + fixture loader; T2 exported core; T3 ProposeHintsImage split + capacity field) name exactly what to read/build and how correctness is proven (empirical dist≈0). The plan deliberately does not transcribe Recover's full prologue (it's existing code to mirror, not invent) — the dist≈0 test is the spec of correctness.

**Type consistency:** `Verdict{Text,Distance,Match}` + `VerifyMatchThreshold` (T1) consumed by T2/T4; `RankedCandidate.Match`/`VerifyReport.Pick` (T2); `HintsReport`/`ProposeHints`/`ProposeHintsImage` (T3) consumed by T3/T4. `mcp.VerifyCandidates` exported core (T2) used by its handler + T2 test. Names consistent across tasks.

**Known deferrals (recorded in PROGRESS.md / spec):** the propose→verify→refine loop is client-driven (not server); OCR-based context is Tier-2 (#5); a future DRY refactor could extract Recover's prologue shared with Verify (kept separate now to preserve the panel invariant trivially).
