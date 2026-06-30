# Information-Leak Feasibility Study (#8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A pure-Go `internal/infoleak` measurement package + a `//go:build infoleak` study runner that quantifies how much *exploitable* information a block-average mosaic leaks under AA-rendering and JPEG compression, plus a `docs/JOURNAL.md` writeup documenting the boundary. Ships a measurement + finding, not an exploiter.

**Architecture:** `internal/infoleak` holds pure-Go primitives (`Separability`, `JPEGRoundTrip`, `binarizeHardEdge`) and measurement functions (`MeasureAALeak`, `MeasureJPEGImpact`) over the existing render→pixelate pipeline. Primitives + a minimal measurement are unit-tested in the default suite (coverage). A `//go:build infoleak`-tagged study runner executes the full corpus, prints a report, and asserts sanity invariants — invoked via `mise run infoleak`. The numbers go into `docs/JOURNAL.md`.

**Tech Stack:** Pure Go (no CGO). Reuses `unpixel.Renderer`/`Style`, `internal/render`, `internal/pixelate`, `internal/imutil` (`Lum601`/`ToRGBA`), `fonts`, and stdlib `image/jpeg`. No new dependency.

## Global Constraints

- **NO CGO, ever.** `CGO_ENABLED=0` pinned; enforced by `mise run cgo:check`. No new dependency (stdlib `image/jpeg` only).
- **Additive / no regression:** new package + tests + one mise task + a JOURNAL section. Core/`Verify`/`Recover`/the 17/17 panel are untouched.
- **No name collision:** `mise run leak` is the goroutine-leak gate. Use the build tag and mise task **`infoleak`** (never `leak`).
- **No import cycle:** `internal/infoleak` imports `unpixel` (the `Renderer` interface + `Style`), `internal/pixelate`, `internal/imutil`, stdlib. Nothing imports `internal/infoleak`. The study test additionally imports `fonts` + `internal/render`. Verify with `CGO_ENABLED=0 go build ./...`.
- **Caged tests only:** never run `go test` bare. Use `scripts/gotest-caged.sh`.
- **In-memory fixtures only:** render/pixelate in code; never load a gitignored/network file.
- **Coverage gate:** `COVER_MIN=85`; keep ≥ 85% (`mise run cover:check`). The untagged unit tests must cover the primitives + a minimal `MeasureAALeak`/`MeasureJPEGImpact` call (the `infoleak`-tagged study is NOT in the default coverage run).
- **Commit gate:** arm the `/simplify` marker (`$GIT_DIR/claude-simplify-ok`, `git diff --cached | sha1sum | cut -d' ' -f1`) only AFTER genuinely reviewing, in a SEPARATE bash call from `git commit`. Run `git restore --staged PROGRESS.md` before arming.
- **Commit trailer:** every commit message ends with
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** `feat/infoleak-study` (already created from `master`; do not commit on `master`).
- Follow `go-style-guide` and `use-modern-go` (`for range`, `min`/`max`, `any`, `t.Context()`, table tests with field names, `got` before `want`).

## Verified facts (do not re-litigate)

- `internal/imutil.Lum601(r, g, b uint8) int` returns `(299r+587g+114b)/1000` ∈ [0,255]. `imutil.ToRGBA(image.Image) *image.RGBA`.
- `internal/render.NewXImageFromFonts(regular, bold []byte) (*render.XImage, error)`; `(*XImage).Render(text string, style unpixel.Style) (*image.RGBA, int, error)`. `*render.XImage` satisfies `unpixel.Renderer` (interface: `Render(text string, style Style) (img *image.RGBA, sentinelX int, err error)`).
- `internal/pixelate.NewBlockAverage(blockSize int) *BlockAverage`; `(*BlockAverage).Pixelate(src *image.RGBA, originX, originY int) *image.RGBA`.
- `fonts.All() []fonts.Font` (9 bundled fonts; `.Name`, `.Data`).
- `unpixel.Style{FontSize float64, ...}`. `unpixel.Renderer` is an interface in `unpixel.go`.
- Build-tag study pattern: `panel_test.go` starts `//go:build panel` + blank line + `package unpixel`; `paper_parity_test.go` uses `//go:build journal`. mise tasks live in `mise.toml` as `[tasks.NAME]` with `description` + `run`; the goroutine gate is `[tasks.leak]` (mise.toml:184).
- stdlib `image/jpeg`: `jpeg.Encode(w io.Writer, m image.Image, o *jpeg.Options) error` (`Options{Quality int}`); `jpeg.Decode(r io.Reader) (image.Image, error)`.

## File Structure

- **Create** `internal/infoleak/infoleak.go` — package doc, primitives, measurement functions, report types.
- **Create** `internal/infoleak/infoleak_test.go` — untagged unit tests (default suite, coverage).
- **Create** `internal/infoleak/study_test.go` (`//go:build infoleak`) — the heavy study runner + invariants.
- **Modify** `mise.toml` — add `[tasks.infoleak]`.
- **Modify** `docs/JOURNAL.md` — append the #8 study section with measured numbers + conclusions.

---

## Task 1: `infoleak` primitives — Separability, JPEGRoundTrip, binarizeHardEdge

**Files:**
- Create: `internal/infoleak/infoleak.go`
- Test: `internal/infoleak/infoleak_test.go`

**Interfaces:**
- Consumes: `imutil.Lum601`/`ToRGBA`, stdlib `image`, `image/jpeg`, `bytes`.
- Produces (used by Tasks 2-4): `func Separability(a, b *image.RGBA) float64`; `func JPEGRoundTrip(img *image.RGBA, quality int) (*image.RGBA, error)`; `func binarizeHardEdge(img *image.RGBA, threshold int) *image.RGBA` (unexported — used by Task 2 in-package).

- [ ] **Step 1: Write the failing tests**

Create `internal/infoleak/infoleak_test.go`:

```go
package infoleak

import (
	"image"
	"testing"
)

// solid returns a w×h RGBA filled with the given gray level.
func solid(w, h int, gray uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = gray, gray, gray, 255
	}
	return img
}

func TestSeparability_identicalIsZero(t *testing.T) {
	a := solid(8, 8, 128)
	if got := Separability(a, a); got != 0 {
		t.Errorf("Separability(x,x) = %v; want 0", got)
	}
}

func TestSeparability_differsIsPositive(t *testing.T) {
	a := solid(8, 8, 0)
	b := solid(8, 8, 255)
	got := Separability(a, b)
	if got <= 0.9 { // black vs white ≈ 1.0
		t.Errorf("Separability(black,white) = %v; want ≈1.0", got)
	}
}

func TestJPEGRoundTrip_preservesDimsAltersPixels(t *testing.T) {
	// Vertical 1px stripes: a high-frequency pattern JPEG must distort at low q.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(img.Pix); i += 4 {
		px := (i / 4) % 16
		var v uint8
		if px%2 == 0 {
			v = 255
		}
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = v, v, v, 255
	}

	out, err := JPEGRoundTrip(img, 30)
	if err != nil {
		t.Fatalf("JPEGRoundTrip: %v", err)
	}
	if out.Bounds().Dx() != 16 || out.Bounds().Dy() != 16 {
		t.Errorf("dims = %v; want 16×16", out.Bounds())
	}
	if Separability(img, out) == 0 {
		t.Errorf("JPEG q=30 of a striped image should alter pixels (Separability > 0)")
	}
}

func TestBinarizeHardEdge_twoLevels(t *testing.T) {
	// Gray ramp → after binarize only {0,255} luminance remain.
	img := image.NewRGBA(image.Rect(0, 0, 16, 1))
	for x := 0; x < 16; x++ {
		v := uint8(x * 16)
		img.Pix[x*4], img.Pix[x*4+1], img.Pix[x*4+2], img.Pix[x*4+3] = v, v, v, 255
	}
	out := binarizeHardEdge(img, 128)
	levels := map[int]bool{}
	for x := 0; x < 16; x++ {
		levels[imLum(out, x, 0)] = true
	}
	for l := range levels {
		if l != 0 && l != 255 {
			t.Errorf("binarize produced luminance %d; want only 0 or 255", l)
		}
	}
}

// imLum is a tiny test helper reading a pixel's Lum601.
func imLum(img *image.RGBA, x, y int) int {
	o := img.PixOffset(x, y)
	return (299*int(img.Pix[o]) + 587*int(img.Pix[o+1]) + 114*int(img.Pix[o+2])) / 1000
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./internal/infoleak/ -count=1`
Expected: FAIL — package `infoleak` does not exist.

- [ ] **Step 3: Implement the primitives**

Create `internal/infoleak/infoleak.go`:

```go
// Package infoleak quantifies how much exploitable information a block-average
// mosaic leaks under anti-aliased rendering and JPEG compression. It is a
// measurement/feasibility study, not a decoder: for a true block-average mosaic
// the only recoverable signal is the block values themselves (the "missing
// avalanche effect" the engine already exploits via generate-and-test). These
// primitives let the //go:build infoleak study runner put numbers on the
// information boundary; see docs/JOURNAL.md for the recorded findings.
package infoleak

import (
	"bytes"
	"image"
	"image/jpeg"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// Separability is the mean per-pixel absolute luminance difference between a and
// b, normalised to [0,1] (0 means indistinguishable). The images are compared
// over their common minimum width and height, so near-equal-width candidates
// (e.g. the confusable pair "rn"/"m") can be compared directly.
func Separability(a, b *image.RGBA) float64 {
	ab, bb := a.Bounds(), b.Bounds()
	w := min(ab.Dx(), bb.Dx())
	h := min(ab.Dy(), bb.Dy())
	if w == 0 || h == 0 {
		return 0
	}
	var sum int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ao := a.PixOffset(ab.Min.X+x, ab.Min.Y+y)
			bo := b.PixOffset(bb.Min.X+x, bb.Min.Y+y)
			la := imutil.Lum601(a.Pix[ao], a.Pix[ao+1], a.Pix[ao+2])
			lb := imutil.Lum601(b.Pix[bo], b.Pix[bo+1], b.Pix[bo+2])
			d := la - lb
			if d < 0 {
				d = -d
			}
			sum += d
		}
	}
	return float64(sum) / (float64(w*h) * 255.0)
}

// JPEGRoundTrip encodes img as JPEG at the given quality (1..100) and decodes it
// back to RGBA, simulating a JPEG-compressed capture of a mosaic. Lower quality
// adds more signal-dependent noise to the (otherwise block-constant) values.
func JPEGRoundTrip(img *image.RGBA, quality int) (*image.RGBA, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	decoded, err := jpeg.Decode(&buf)
	if err != nil {
		return nil, err
	}
	return imutil.ToRGBA(decoded), nil
}

// binarizeHardEdge thresholds an anti-aliased render to two luminance levels —
// black (Lum < threshold) or white — removing the sub-pixel AA coverage so its
// contribution can be isolated by comparison against the AA original.
func binarizeHardEdge(img *image.RGBA, threshold int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			var v uint8 = 255
			if imutil.Lum601(img.Pix[o], img.Pix[o+1], img.Pix[o+2]) < threshold {
				v = 0
			}
			oo := out.PixOffset(x, y)
			out.Pix[oo], out.Pix[oo+1], out.Pix[oo+2], out.Pix[oo+3] = v, v, v, 255
		}
	}
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./internal/infoleak/ -count=1` → PASS.
Run: `CGO_ENABLED=0 go build ./...` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/infoleak/infoleak.go internal/infoleak/infoleak_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(infoleak): Separability / JPEGRoundTrip / binarizeHardEdge primitives

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `MeasureAALeak` — AA vs hard-edge separability of confusable pairs

**Files:**
- Modify: `internal/infoleak/infoleak.go`
- Test: `internal/infoleak/infoleak_test.go`

**Interfaces:**
- Consumes: `Separability`, `binarizeHardEdge` (Task 1); `unpixel.Renderer`/`Style`; `pixelate.NewBlockAverage`.
- Produces (used by Task 4): `type PairResult struct{ A, B string; AASep, HardSep, Gain float64 }`; `type AAReport struct{ Font string; Pairs []PairResult; MeanAASep, MeanHardSep, MeanGain float64 }`; `func MeasureAALeak(r unpixel.Renderer, fontName string, pairs [][2]string, block int, fontSize float64) (AAReport, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/infoleak/infoleak_test.go` (imports now also need `unpixel`, `fonts`, `internal/render`):

```go
func testRenderer(t *testing.T) unpixel.Renderer {
	t.Helper()
	r, err := render.NewXImageFromFonts(fonts.All()[0].Data, nil) // Liberation Sans
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	return r
}

func TestMeasureAALeak_runsAndAggregates(t *testing.T) {
	r := testRenderer(t)
	rep, err := MeasureAALeak(r, "Liberation Sans", [][2]string{{"rn", "m"}, {"0", "O"}}, 6, 28)
	if err != nil {
		t.Fatalf("MeasureAALeak: %v", err)
	}
	if len(rep.Pairs) != 2 {
		t.Fatalf("pairs = %d; want 2", len(rep.Pairs))
	}
	// Aggregates are the means of the per-pair values.
	if rep.MeanGain != (rep.Pairs[0].Gain+rep.Pairs[1].Gain)/2 {
		t.Errorf("MeanGain %v != mean of pair gains", rep.MeanGain)
	}
	// Separabilities are in [0,1].
	for _, p := range rep.Pairs {
		if p.AASep < 0 || p.AASep > 1 || p.HardSep < 0 || p.HardSep > 1 {
			t.Errorf("pair %q/%q separabilities out of [0,1]: %+v", p.A, p.B, p)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./internal/infoleak/ -run MeasureAALeak -count=1`
Expected: FAIL — `MeasureAALeak` undefined.

- [ ] **Step 3: Implement**

Add to `internal/infoleak/infoleak.go` (add imports `github.com/oioio-space/unpixel` and `github.com/oioio-space/unpixel/internal/pixelate`):

```go
// PairResult is the separability of one confusable pair under AA vs hard-edge
// rendering. Gain = AASep − HardSep is how much sub-pixel anti-aliasing adds to
// the pair's distinguishability after block-averaging.
type PairResult struct {
	A, B                  string
	AASep, HardSep, Gain  float64
}

// AAReport aggregates MeasureAALeak over a set of confusable pairs for one font.
type AAReport struct {
	Font                              string
	Pairs                             []PairResult
	MeanAASep, MeanHardSep, MeanGain  float64
}

// MeasureAALeak renders each confusable pair with renderer r at fontSize, block-
// averages at block, and reports the pair's separability under anti-aliased
// rendering (AASep) versus a hard-edge (binarised) render (HardSep). A positive
// mean Gain means anti-aliasing leaves more sub-pixel signal in the mosaic that
// distinguishes the pair. It returns an error if rendering fails.
func MeasureAALeak(r unpixel.Renderer, fontName string, pairs [][2]string, block int, fontSize float64) (AAReport, error) {
	pix := pixelate.NewBlockAverage(block)
	rep := AAReport{Font: fontName, Pairs: make([]PairResult, 0, len(pairs))}
	var sumAA, sumHard, sumGain float64
	for _, p := range pairs {
		aImg, _, err := r.Render(p[0], unpixel.Style{FontSize: fontSize})
		if err != nil {
			return AAReport{}, err
		}
		bImg, _, err := r.Render(p[1], unpixel.Style{FontSize: fontSize})
		if err != nil {
			return AAReport{}, err
		}
		aaSep := Separability(pix.Pixelate(aImg, 0, 0), pix.Pixelate(bImg, 0, 0))
		hardSep := Separability(
			pix.Pixelate(binarizeHardEdge(aImg, 128), 0, 0),
			pix.Pixelate(binarizeHardEdge(bImg, 128), 0, 0),
		)
		pr := PairResult{A: p[0], B: p[1], AASep: aaSep, HardSep: hardSep, Gain: aaSep - hardSep}
		rep.Pairs = append(rep.Pairs, pr)
		sumAA += aaSep
		sumHard += hardSep
		sumGain += pr.Gain
	}
	if n := float64(len(pairs)); n > 0 {
		rep.MeanAASep, rep.MeanHardSep, rep.MeanGain = sumAA/n, sumHard/n, sumGain/n
	}
	return rep, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./internal/infoleak/ -run MeasureAALeak -count=1` → PASS. Then `CGO_ENABLED=0 go build ./...` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/infoleak/infoleak.go internal/infoleak/infoleak_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(infoleak): MeasureAALeak — AA vs hard-edge confusable separability

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `MeasureJPEGImpact` — JPEG drift + margin impact

**Files:**
- Modify: `internal/infoleak/infoleak.go`
- Test: `internal/infoleak/infoleak_test.go`

**Interfaces:**
- Consumes: `Separability`, `JPEGRoundTrip` (Task 1); `unpixel.Renderer`/`Style`; `pixelate.NewBlockAverage`.
- Produces (used by Task 4): `type JPEGPoint struct{ Quality int; Drift float64; TrueStillWins bool }`; `type JPEGReport struct{ Text, Wrong string; Points []JPEGPoint }`; `func MeasureJPEGImpact(r unpixel.Renderer, text, wrong string, block int, fontSize float64, qualities []int) (JPEGReport, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/infoleak/infoleak_test.go`:

```go
func TestMeasureJPEGImpact_driftGrowsAsQualityDrops(t *testing.T) {
	r := testRenderer(t)
	rep, err := MeasureJPEGImpact(r, "the", "tho", 6, 28, []int{90, 50, 20})
	if err != nil {
		t.Fatalf("MeasureJPEGImpact: %v", err)
	}
	if len(rep.Points) != 3 {
		t.Fatalf("points = %d; want 3", len(rep.Points))
	}
	// Drift is monotonically non-decreasing as quality drops (90 → 50 → 20).
	if !(rep.Points[0].Drift <= rep.Points[1].Drift && rep.Points[1].Drift <= rep.Points[2].Drift) {
		t.Errorf("drift not non-decreasing as quality drops: %+v", rep.Points)
	}
	// At high quality the true candidate should still win.
	if !rep.Points[0].TrueStillWins {
		t.Errorf("q=90: true candidate should still win")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `scripts/gotest-caged.sh go test ./internal/infoleak/ -run MeasureJPEGImpact -count=1`
Expected: FAIL — `MeasureJPEGImpact` undefined.

- [ ] **Step 3: Implement**

Add to `internal/infoleak/infoleak.go`:

```go
// JPEGPoint is the JPEG impact at one quality: Drift is how far the compressed
// mosaic moved from the clean mosaic (Separability), and TrueStillWins reports
// whether the compressed observation is still closer to the true candidate's
// mosaic than to a wrong candidate's.
type JPEGPoint struct {
	Quality       int
	Drift         float64
	TrueStillWins bool
}

// JPEGReport aggregates MeasureJPEGImpact for one (text, wrong) candidate pair.
type JPEGReport struct {
	Text, Wrong string
	Points      []JPEGPoint
}

// MeasureJPEGImpact renders text and a wrong candidate, block-averages both, then
// JPEG-round-trips the clean text mosaic at each quality and reports the drift
// (distance from the clean mosaic) and whether the compressed observation still
// resolves to the true candidate. It quantifies JPEG as a robustness cost on the
// known block values — not a sub-block information leak. qualities are processed
// in the given order (pass them high→low for a readable drift curve).
func MeasureJPEGImpact(r unpixel.Renderer, text, wrong string, block int, fontSize float64, qualities []int) (JPEGReport, error) {
	pix := pixelate.NewBlockAverage(block)
	tImg, _, err := r.Render(text, unpixel.Style{FontSize: fontSize})
	if err != nil {
		return JPEGReport{}, err
	}
	wImg, _, err := r.Render(wrong, unpixel.Style{FontSize: fontSize})
	if err != nil {
		return JPEGReport{}, err
	}
	clean := pix.Pixelate(tImg, 0, 0)
	wrongMosaic := pix.Pixelate(wImg, 0, 0)

	rep := JPEGReport{Text: text, Wrong: wrong, Points: make([]JPEGPoint, 0, len(qualities))}
	for _, q := range qualities {
		jpegd, err := JPEGRoundTrip(clean, q)
		if err != nil {
			return JPEGReport{}, err
		}
		toTrue := Separability(jpegd, clean)
		toWrong := Separability(jpegd, wrongMosaic)
		rep.Points = append(rep.Points, JPEGPoint{
			Quality:       q,
			Drift:         toTrue,
			TrueStillWins: toTrue < toWrong,
		})
	}
	return rep, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `scripts/gotest-caged.sh go test ./internal/infoleak/ -count=1` → PASS (all). Then `CGO_ENABLED=0 go build ./...` → clean.

> If `driftGrowsAsQualityDrops` is occasionally non-monotone at adjacent qualities (JPEG drift is mostly but not strictly monotone), widen the gap between the tested qualities (e.g. `{90, 40, 10}`) so the trend is unambiguous — do NOT remove the monotonicity assertion.

- [ ] **Step 5: Commit**

```bash
git add internal/infoleak/infoleak.go internal/infoleak/infoleak_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(infoleak): MeasureJPEGImpact — drift + true-still-wins per quality

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `//go:build infoleak` study runner + mise task

**Files:**
- Create: `internal/infoleak/study_test.go` (`//go:build infoleak`)
- Modify: `mise.toml`

**Interfaces:**
- Consumes: `MeasureAALeak`, `MeasureJPEGImpact` (Tasks 2-3); `fonts.All`; `render.NewXImageFromFonts`.

**Design note:** the heavy runner is out of the default test path (tag `infoleak`). It prints a report (via `t.Logf`) and asserts sanity invariants only — it is a study, not a pass/fail gate, but the invariants guard against a broken harness.

- [ ] **Step 1: Write the study runner**

Create `internal/infoleak/study_test.go`:

```go
//go:build infoleak

package infoleak

import (
	"testing"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/render"
)

// confusablePairs are near-equal-width OCR-confusable pairs where sub-pixel AA
// might add discriminability after block-averaging.
var confusablePairs = [][2]string{
	{"rn", "m"}, {"cl", "d"}, {"vv", "w"}, {"nn", "m"}, {"0", "O"}, {"8", "B"}, {"I", "l"},
}

const (
	studyBlock    = 6
	studyFontSize = 28
)

// TestInfoLeakStudy runs the full information-leak measurement over the bundled
// fonts and prints a report. Run it with: mise run infoleak
// (or: scripts/gotest-caged.sh go test -tags infoleak -run InfoLeak ./internal/infoleak/).
func TestInfoLeakStudy(t *testing.T) {
	for _, f := range fonts.All() {
		r, err := render.NewXImageFromFonts(f.Data, nil)
		if err != nil {
			t.Fatalf("renderer %s: %v", f.Name, err)
		}

		// --- AA leak ---
		aa, err := MeasureAALeak(r, f.Name, confusablePairs, studyBlock, studyFontSize)
		if err != nil {
			t.Fatalf("MeasureAALeak %s: %v", f.Name, err)
		}
		t.Logf("[AA] %-18s meanAASep=%.4f meanHardSep=%.4f meanGain=%+.4f",
			f.Name, aa.MeanAASep, aa.MeanHardSep, aa.MeanGain)
		for _, p := range aa.Pairs {
			t.Logf("[AA]   %-5s vs %-5s  AA=%.4f hard=%.4f gain=%+.4f", p.A, p.B, p.AASep, p.HardSep, p.Gain)
			// Invariant: identical-input separability would be 0; a real pair differs.
			if p.AASep < 0 || p.HardSep < 0 {
				t.Errorf("negative separability for %q/%q", p.A, p.B)
			}
		}

		// --- JPEG impact (one representative font is enough; do Liberation Sans only) ---
		if f.Name == fonts.All()[0].Name {
			jp, err := MeasureJPEGImpact(r, "the", "tho", studyBlock, studyFontSize, []int{95, 75, 50, 30, 10})
			if err != nil {
				t.Fatalf("MeasureJPEGImpact: %v", err)
			}
			t.Logf("[JPEG] text=%q wrong=%q", jp.Text, jp.Wrong)
			prevDrift := -1.0
			for _, pt := range jp.Points {
				t.Logf("[JPEG]   q=%-3d drift=%.4f trueStillWins=%v", pt.Quality, pt.Drift, pt.TrueStillWins)
				if pt.Drift+1e-9 < prevDrift {
					t.Errorf("drift decreased as quality dropped: q=%d drift=%.4f < prev %.4f", pt.Quality, pt.Drift, prevDrift)
				}
				prevDrift = pt.Drift
			}
		}
	}

	// --- multi-offset (idea 1) is already shipped via internal/multiframe (IBP);
	// it is the only real super-resolution lever. Documented, not re-measured here.
	t.Log("[multi-offset] already shipped: internal/multiframe IBP fusion (see DecodeMultiFrame)")
}
```

> Note the `prevDrift` check: qualities are listed high→low, so drift should be non-decreasing. If JPEG drift is non-monotone between adjacent close qualities, use a widely-spaced quality ladder (already `{95,75,50,30,10}`).

- [ ] **Step 2: Run the study (must compile + pass under the tag)**

Run: `scripts/gotest-caged.sh go test -tags infoleak -run InfoLeak ./internal/infoleak/ -v -count=1`
Expected: PASS — prints the AA + JPEG tables. CAPTURE this output; Task 5 transcribes the numbers into JOURNAL.
Run: `scripts/gotest-caged.sh go test ./internal/infoleak/ -count=1` (default, no tag) → still PASS (study file excluded).

- [ ] **Step 3: Add the mise task**

In `mise.toml`, add (near other test/study tasks; NAME must be `infoleak`, never `leak`):

```toml
[tasks.infoleak]
description = "Information-leak feasibility study (#8): AA / JPEG / multi-offset measurement, memory-caged"
run = "./scripts/gotest-caged.sh go test -tags infoleak -run InfoLeak -v ./internal/infoleak/"
```

> Confirm `scripts/gotest-caged.sh`'s invocation form matches the other tasks (some pass `go test …` as args; mirror `[tasks.leak]`/`[tasks.test]` exactly — grep `mise.toml` for `gotest-caged`). Run `mise run infoleak` to verify the task wires up.

- [ ] **Step 4: Verify**

Run: `mise run infoleak` → PASS (study prints tables). Then `CGO_ENABLED=0 go build ./...` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/infoleak/study_test.go mise.toml
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "feat(infoleak): //go:build infoleak study runner + mise infoleak task

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: JOURNAL writeup + full validation

**Files:**
- Modify: `docs/JOURNAL.md`

- [ ] **Step 1: Run the study and capture the numbers**

Run: `mise run infoleak 2>&1 | tee /tmp/infoleak-out.txt` — capture the AA per-font means + the JPEG drift table. These are the REAL measured numbers to record (do not invent them).

- [ ] **Step 2: Append the #8 section to `docs/JOURNAL.md`**

Append (do NOT edit/delete existing entries — JOURNAL is append-only) a section titled e.g. `## #8 — Information-leak feasibility study (2026-06-30)` containing:
- **What was measured & why** (one paragraph): block-average mosaic's only recoverable signal is block values (avalanche-effect, already exploited); this study quantifies whether AA / JPEG add anything.
- **AA result**: a small table or summary of `meanGain` per bundled font (from the captured run) + the conclusion (AA adds a small/large/no measurable edge; whichever the numbers show — report honestly).
- **JPEG result**: the drift + true-still-wins curve (from the captured run) + conclusion (JPEG = robustness cost on known block values, not a sub-block leak).
- **Multi-offset**: one line — the only real super-resolution lever, already shipped (`internal/multiframe` IBP).
- **Verdict**: whether any exploiter is justified (state the criterion: an unexpectedly large AA gain would warrant a non-block-constant forward model; otherwise the information wall stands and #8 closes as a documented boundary).

Keep it factual, cite the actual numbers from the run.

- [ ] **Step 3: Coverage gate**

Run: `mise run cover:check` → ≥ 85% (report %). The untagged unit tests (Tasks 1-3) cover the primitives + measurements. If short, add a `MeasureJPEGImpact`/`MeasureAALeak` table case to `infoleak_test.go`.

- [ ] **Step 4: Full CI gate**

Run: `scripts/gotest-caged.sh go test ./... -count=1` → PASS (incl. 17/17 panel; nothing in the default path changed).
Run: `mise run ci` → all green (lint + test + cgo:check + scans). (The `infoleak` study is excluded from the default path; CI stays fast.)

- [ ] **Step 5: Commit**

```bash
git add docs/JOURNAL.md
git restore --staged PROGRESS.md 2>/dev/null || true
# review, arm marker (separate call), then:
git commit -m "docs(journal): #8 information-leak study findings (AA/JPEG/multi-offset)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/superpowers/specs/2026-06-30-infoleak-study-design.md`):
- §3.1 primitives `Separability`/`JPEGRoundTrip`/`binarizeHardEdge` → Task 1. ✅
- §3.2 `MeasureAALeak` + AAReport/PairResult → Task 2. ✅
- §3.2 `MeasureJPEGImpact` + JPEGReport/JPEGPoint → Task 3. ✅
- §3.3 `//go:build infoleak` study runner + invariants → Task 4. ✅
- §3.6 `mise.toml` `[tasks.infoleak]` → Task 4. ✅
- §3.5 `docs/JOURNAL.md` writeup with real numbers → Task 5. ✅
- §3 idea-1 multi-offset documented-not-reimplemented → Task 4 runner log + Task 5 JOURNAL. ✅
- §2.1 primitive correctness tests → Task 1. §2.2 reproducible study + invariants → Task 4. §2.3 boundary documented → Task 5. §2.4 pure-Go/caged/in-memory/≥85% → Global Constraints + Task 5. ✅
- §8 no `leak` collision (tag+task `infoleak`) → Task 4. ✅

**2. Placeholder scan:** every code step has complete, compilable content. The `grep`-to-confirm notes (gotest-caged invocation form) are verification steps, not logic gaps. JOURNAL numbers are intentionally captured-at-runtime (a study records measured values, not invented ones).

**3. Type consistency:** `Separability(a,b *image.RGBA) float64`, `JPEGRoundTrip(*image.RGBA,int)(*image.RGBA,error)`, `binarizeHardEdge(*image.RGBA,int)*image.RGBA`, `PairResult`/`AAReport`/`MeasureAALeak`, `JPEGPoint`/`JPEGReport`/`MeasureJPEGImpact` are used identically across Tasks 1-4. `unpixel.Renderer`/`Style`, `pixelate.NewBlockAverage(int).Pixelate(img,0,0)`, `imutil.Lum601`/`ToRGBA` match the verified signatures.

> **Known note for the implementer:** the `infoleak` build tag and mise task must never be named `leak` (that's the goroutine-leak gate). The study runner is excluded from the default test path, so the untagged unit tests (Tasks 1-3) are what carry coverage — keep them substantive.
