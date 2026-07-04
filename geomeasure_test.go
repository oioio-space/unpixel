//go:build geomeasure

// TestGeomeasure is an observational (never-fails) diagnostic that isolates
// per-stage geometry accuracy on the real and wild corpora. For each image it
// runs — and records — four stages:
//
//   - Localize: LocateRedaction (bounding-box detection).
//   - Grid: InferBlockGrid (size, phase, confidence) + InferBlockSizeRobust.
//   - Segment: internal/segment.Lines count vs GT.
//   - Font: internal/fontrank.RankFontsAt top-3 vs GT font.
//
// For the real corpus the manifest provides rich geometry ground truth
// (block, offset_x, offset_y, lines, font); errors are computed per stage.
// For the wild corpus only raw inferred values are recorded; images with a
// known text length (m4/m5) also get a block-plausibility flag.
//
// Outputs (relative to the module root):
//
//	benchmarks/geometry/run-<stamp>.json   machine-readable
//	docs/GEOMETRY.md                        human-readable (overwritten snapshot)
//
// Run with:
//
//	mise run geomeasure
package unpixel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	unpixel "github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fontrank"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/segment"
)

// ── manifest structs ──────────────────────────────────────────────────────────

// realEntry mirrors the relevant fields in testdata/real/manifest.json.
type realEntry struct {
	Name     string  `json:"name"`
	File     string  `json:"file"`
	Text     string  `json:"text"`
	Font     string  `json:"font"`
	FontSize float64 `json:"font_size"`
	XScale   float64 `json:"x_scale"`
	Block    int     `json:"block"`
	OffsetX  int     `json:"offset_x"`
	OffsetY  int     `json:"offset_y"`
	Lines    int     `json:"lines"`
}

// geomWildEntry mirrors the relevant fields in testdata/wild/manifest.json.
type geomWildEntry struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	Kind        string `json:"kind"`
	GroundTruth string `json:"ground_truth"`
}

// ── JSON run record ───────────────────────────────────────────────────────────

// geomRun is the top-level JSON record written to benchmarks/geometry/.
type geomRun struct {
	Timestamp string       `json:"timestamp"`
	Results   []geomResult `json:"results"`
}

// ── test entry point ──────────────────────────────────────────────────────────

// TestGeomeasure measures per-stage geometry accuracy on real and wild corpora.
// It never fails — it is purely observational.
func TestGeomeasure(t *testing.T) {
	ctx := t.Context()
	stamp := time.Now().UTC().Format("20060102T150405")

	// Convert bundled fonts to the fontrank.NamedFont slice once.
	allFonts := fonts.All()
	named := make([]fontrank.NamedFont, len(allFonts))
	for i, f := range allFonts {
		named[i] = fontrank.NamedFont{Name: f.Name, Data: f.Data}
	}

	var results []geomResult
	results = append(results, runRealCorpus(t, ctx, named)...)
	results = append(results, runWildCorpus(t, ctx, named)...)

	writeGeomJSON(t, stamp, results)
	writeGeomMarkdown(t, stamp, results)
}

// ── corpus runners ────────────────────────────────────────────────────────────

func runRealCorpus(t *testing.T, ctx context.Context, named []fontrank.NamedFont) []geomResult {
	t.Helper()
	const (
		manifestPath = "testdata/real/manifest.json"
		dir          = "testdata/real"
	)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	var entries []realEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parse %s: %v", manifestPath, err)
	}

	// Header.
	t.Logf("%-30s  %-5s  %4s  %4s  %4s  %5s  %5s  %5s  %5s  %5s  %5s  %-3s  %-36s  %-16s  %-3s  %s",
		"image", "loc", "sz", "phX", "phY", "conf",
		"errSz", "erPhX", "erPhY", "segL", "gtL", "L✓",
		"top-3", "gt-font", "F✓", "verdict")

	results := make([]geomResult, 0, len(entries))
	for _, e := range entries {
		r := measureImage(t, ctx, named, "real", filepath.Join(dir, e.File))
		// Apply ground truth.
		r.GTBlock = e.Block
		r.GTOffsetX = e.OffsetX
		r.GTOffsetY = e.OffsetY
		r.GTLines = e.Lines
		r.GTFont = e.Font
		// Compute per-stage errors.
		sz := r.GridSize - e.Block
		r.ErrSize = max(sz, -sz)
		px := r.GridPhaseX - e.OffsetX
		r.ErrPhaseX = max(px, -px)
		py := r.GridPhaseY - e.OffsetY
		r.ErrPhaseY = max(py, -py)
		r.LinesMatch = r.SegLines == e.Lines
		r.GTFontInTop3 = slices.Contains(r.FontTop3, e.Font)
		r.FirstFailStage = stageVerdict(r)

		t.Logf("%-30s  %-5s  %4d  %4d  %4d  %5.2f  %5d  %5d  %5d  %5d  %5d  %-3s  %-36s  %-16s  %-3s  %s",
			r.Image, tickMark(r.LocalizeOK),
			r.GridSize, r.GridPhaseX, r.GridPhaseY, r.GridConf,
			r.ErrSize, r.ErrPhaseX, r.ErrPhaseY,
			r.SegLines, r.GTLines, tickMark(r.LinesMatch),
			strings.Join(r.FontTop3, ", "), r.GTFont, tickMark(r.GTFontInTop3),
			r.FirstFailStage)

		results = append(results, r)
	}
	return results
}

func runWildCorpus(t *testing.T, ctx context.Context, named []fontrank.NamedFont) []geomResult {
	t.Helper()
	const (
		manifestPath = "testdata/wild/manifest.json"
		dir          = "testdata/wild"
	)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	var entries []geomWildEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parse %s: %v", manifestPath, err)
	}

	// Header.
	t.Logf("%-34s  %-5s  %4s  %4s  %4s  %5s  %4s  %6s  %5s  %6s  %8s  %-10s  %-36s  %s",
		"image", "loc", "sz", "phX", "phY", "conf",
		"robSz", "robSup", "segL", "knLen", "expAdv", "plaus",
		"top-3", "verdict")

	results := make([]geomResult, 0, len(entries))
	for _, e := range entries {
		if e.Kind != "mosaic" {
			continue // blur fixtures are outside the mosaic-geometry scope
		}
		r := measureImage(t, ctx, named, "wild", filepath.Join(dir, e.File))

		// Plausibility check for images with a known text length.
		if n := len([]rune(e.GroundTruth)); n > 0 {
			imgW := float64(r.LocalizeW)
			if imgW == 0 {
				// Fall back to full image width when localization failed.
				imgW = float64(r.ImageW)
			}
			if imgW > 0 {
				expAdv := imgW / float64(n)
				r.KnownTextLen = n
				r.ExpectedAdvance = expAdv
				switch {
				case r.GridSize == 0:
					r.BlockPlausible = "unknown"
				case float64(r.GridSize) <= expAdv:
					r.BlockPlausible = "sub-glyph"
				default:
					r.BlockPlausible = "sample-starved"
				}
			}
		}

		r.FirstFailStage = stageVerdict(r)

		t.Logf("%-34s  %-5s  %4d  %4d  %4d  %5.2f  %4d  %6.2f  %5d  %6d  %8.2f  %-10s  %-36s  %s",
			r.Image, tickMark(r.LocalizeOK),
			r.GridSize, r.GridPhaseX, r.GridPhaseY, r.GridConf,
			r.RobustSize, r.RobustSupport,
			r.SegLines, r.KnownTextLen, r.ExpectedAdvance, r.BlockPlausible,
			strings.Join(r.FontTop3, ", "),
			r.FirstFailStage)

		results = append(results, r)
	}
	return results
}

// ── per-image measurement ─────────────────────────────────────────────────────

// measureImage runs all four geometry stages on a single image file and
// returns a partially-filled geomResult (GT fields are filled by the caller).
func measureImage(t *testing.T, ctx context.Context, named []fontrank.NamedFont, corpus, imgPath string) geomResult {
	t.Helper()
	r := geomResult{
		Corpus:   corpus,
		Image:    filepath.Base(imgPath),
		FontTop3: []string{},
	}

	f, err := os.Open(imgPath) // #nosec G304 -- test reads controlled fixture path
	if err != nil {
		t.Logf("SKIP %s: open: %v", imgPath, err)
		return r
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Logf("SKIP %s: decode: %v", imgPath, err)
		return r
	}

	r.ImageW = img.Bounds().Dx()
	rgba := imutil.ToRGBA(img)

	// Stage 1 — Localize.
	rect, locOK := unpixel.LocateRedaction(img)
	r.LocalizeOK = locOK
	if locOK {
		r.LocalizeW = rect.Dx()
		r.LocalizeH = rect.Dy()
	}

	// Stage 2 — Grid.
	grid, gridOK := unpixel.InferBlockGrid(img)
	r.GridOK = gridOK
	r.GridSize = grid.Size
	r.GridPhaseX = grid.PhaseX
	r.GridPhaseY = grid.PhaseY
	r.GridConf = grid.Confidence

	robSize, robSup := unpixel.InferBlockSizeRobust(img)
	r.RobustSize = robSize
	r.RobustSupport = robSup

	// Stage 3 — Segment.
	r.SegLines = len(segment.Lines(rgba))

	// Stage 4 — Font (blockSize=0 → auto-detect inside RankFontsAt).
	scores, ferr := fontrank.RankFontsAt(ctx, img, named, grid.Size)
	if ferr != nil {
		t.Logf("fontrank %s: %v", imgPath, ferr)
	} else {
		top := min(3, len(scores))
		r.FontTop3 = make([]string, top)
		for i := range top {
			r.FontTop3[i] = scores[i].Name
		}
	}

	return r
}

// ── output writers ────────────────────────────────────────────────────────────

func writeGeomJSON(t *testing.T, stamp string, results []geomResult) {
	t.Helper()
	outDir := filepath.Join("benchmarks", "geometry")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	run := geomRun{Timestamp: stamp, Results: results}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	path := filepath.Join(outDir, "run-"+stamp+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil { // #nosec G306
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("JSON written → %s", path)
}

func writeGeomMarkdown(t *testing.T, stamp string, results []geomResult) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(geomMarkdownHeader)
	buildGeomMarkdown(&sb, stamp, results)

	path := filepath.Join("docs", "GEOMETRY.md")
	// #nosec G304 -- writing to a fixed, source-controlled docs path
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("Markdown written → %s", path)
}

// geomMarkdownHeader is the fixed preamble of the regenerated snapshot. The
// harness OVERWRITES docs/GEOMETRY.md each run (it is a reproducible diagnostic
// snapshot, not an append-only journal), so the latest numbers always stand alone.
const geomMarkdownHeader = `# Geometry isolation diagnostic (P2)

Regenerated by ` + "`mise run geomeasure`" + ` (harness ` + "`geomeasure_test.go`" + `, build tag
` + "`geomeasure`" + `). For every real and wild image it runs each pipeline stage
(localize → grid → segment → font) in isolation and records where the FIRST break is,
before any decode — so decoder work targets the actual failing stage. Machine-readable
counterpart: ` + "`benchmarks/geometry/run-*.json`" + `.
`

// buildGeomMarkdown writes one run section into sb.
func buildGeomMarkdown(sb *strings.Builder, stamp string, results []geomResult) {
	fmt.Fprintf(sb, "\n## Geometry run %s\n\n", stamp)

	// ── real ──────────────────────────────────────────────────────────────────
	realRows := slices.DeleteFunc(slices.Clone(results), func(r geomResult) bool {
		return r.Corpus != "real"
	})
	if len(realRows) > 0 {
		fmt.Fprintf(sb, "### Real corpus\n\n")
		fmt.Fprintln(sb, "| image | loc | sz | phX | phY | conf | errSz | errPhX | errPhY | segL | gtL | L✓ | top-3 fonts | gt-font | F✓ | verdict |")
		fmt.Fprintln(sb, "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
		for _, r := range realRows {
			top3 := strings.Join(r.FontTop3, "<br>")
			fmt.Fprintf(sb,
				"| %s | %s | %d | %d | %d | %.2f | %d | %d | %d | %d | %d | %s | %s | %s | %s | **%s** |\n",
				r.Image, tickMark(r.LocalizeOK),
				r.GridSize, r.GridPhaseX, r.GridPhaseY, r.GridConf,
				r.ErrSize, r.ErrPhaseX, r.ErrPhaseY,
				r.SegLines, r.GTLines, tickMark(r.LinesMatch),
				top3, r.GTFont, tickMark(r.GTFontInTop3),
				r.FirstFailStage)
		}
		fmt.Fprintln(sb)
	}

	// ── wild ──────────────────────────────────────────────────────────────────
	wildRows := slices.DeleteFunc(slices.Clone(results), func(r geomResult) bool {
		return r.Corpus != "wild"
	})
	if len(wildRows) > 0 {
		fmt.Fprintf(sb, "### Wild corpus (mosaic)\n\n")
		fmt.Fprintln(sb, "| image | loc | sz | phX | phY | conf | robSz | robSup | segL | knLen | expAdv | plaus | top-3 fonts | verdict |")
		fmt.Fprintln(sb, "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
		for _, r := range wildRows {
			top3 := strings.Join(r.FontTop3, "<br>")
			fmt.Fprintf(sb,
				"| %s | %s | %d | %d | %d | %.2f | %d | %.2f | %d | %d | %.2f | %s | %s | **%s** |\n",
				r.Image, tickMark(r.LocalizeOK),
				r.GridSize, r.GridPhaseX, r.GridPhaseY, r.GridConf,
				r.RobustSize, r.RobustSupport,
				r.SegLines, r.KnownTextLen, r.ExpectedAdvance,
				r.BlockPlausible, top3, r.FirstFailStage)
		}
		fmt.Fprintln(sb)
	}

	// ── conclusions ───────────────────────────────────────────────────────────
	fmt.Fprintf(sb, "### Conclusions\n\n")
	for _, corpus := range []string{"real", "wild"} {
		var stages []string
		for _, r := range results {
			if r.Corpus == corpus {
				stages = append(stages, r.FirstFailStage)
			}
		}
		if len(stages) == 0 {
			continue
		}
		// Count per stage; compute mode with first-occurrence tie-break.
		counts := make(map[string]int, len(stages))
		for _, s := range stages {
			counts[s]++
		}
		modal, modalN := "", 0
		for _, s := range stages {
			if counts[s] > modalN {
				modal, modalN = s, counts[s]
			}
		}
		fmt.Fprintf(sb, "**%s** (%d images): modal failing stage = **%s**. Per-stage counts: ", corpus, len(stages), modal)
		// Emit in a fixed order for reproducibility.
		order := []string{stageLocalize, stageGrid, stageSegment, stageFont, stageOK, stageUnknown}
		parts := make([]string, 0, len(order))
		for _, s := range order {
			if n := counts[s]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", s, n))
			}
		}
		fmt.Fprintf(sb, "%s.\n\n", strings.Join(parts, ", "))
	}
}
