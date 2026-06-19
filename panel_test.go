//go:build panel

// Recovery panel: a quality+speed benchmark over the committed fixture set.
//
// Unlike matrix_test.go (a pass/fail correctness gate), the panel quantifies HOW
// WELL and HOW FAST the engine recovers each reference image, then diffs the
// whole panel against the previous recorded run so an improvement's effect — on
// decode quality AND decode speed — is visible at every step.
//
// It is gated behind the `panel` build tag so it never runs in the default
// `go test ./...` / coverage path; drive it via `mise run bench:panel`:
//
//	mise run bench:panel          # run, compare to benchmarks/quality-baseline.json
//	mise run bench:panel:record   # promote the latest run to the baseline
//
// TestPanel writes benchmarks/quality-latest.json when PANEL_OUT is set and
// fails on a quality regression vs the baseline when PANEL_STRICT=1.
// BenchmarkPanel gives benchstat-grade per-fixture speed numbers.
package unpixel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/fixture"
)

// baselinePath is the committed previous-version panel the current run is
// compared against; latestDefault is where a fresh run is recorded.
const (
	baselinePath  = "benchmarks/quality-baseline.json"
	latestDefault = "benchmarks/quality-latest.json"
)

// fixtureMetric is one fixture's recovery quality + speed.
type fixtureMetric struct {
	Name         string  `json:"name"`
	Text         string  `json:"text"`
	Exact        bool    `json:"exact"`         // BestGuess == Text
	CharAccuracy float64 `json:"char_accuracy"` // 1 − normalized Levenshtein
	Fidelity     float64 `json:"fidelity"`      // Result.Fidelity() (whole-image)
	Confidence   float64 `json:"confidence"`    // Result.Confidence
	Candidates   int     `json:"candidates"`    // candidates that passed the gate
	ElapsedMs    float64 `json:"elapsed_ms"`    // wall-clock for this recovery
}

// panelReport is the full panel: per-fixture metrics plus an aggregate.
type panelReport struct {
	Fixtures []fixtureMetric `json:"fixtures"`
	Summary  panelSummary    `json:"summary"`
}

// panelSummary aggregates the panel into headline numbers.
type panelSummary struct {
	N                int     `json:"n"`
	Exact            int     `json:"exact"`
	ExactRate        float64 `json:"exact_rate"`
	MeanCharAccuracy float64 `json:"mean_char_accuracy"`
	MeanFidelity     float64 `json:"mean_fidelity"`
	TotalElapsedMs   float64 `json:"total_elapsed_ms"`
}

// recoverOne runs the engine on one fixture with its known parameters and
// returns the result; identical option wiring to the recovery matrix.
func recoverOne(ctx context.Context, img image.Image, s fixture.Spec) (unpixel.Result, error) {
	return unpixel.Recover(ctx, img,
		unpixel.WithStyle(s.Style()),
		unpixel.WithBlockSize(s.BlockSize),
		unpixel.WithCharset(s.Charset),
		unpixel.WithMaxLength(utf8.RuneCountInString(s.Text)+1),
		unpixel.WithWorkers(2),
	)
}

// runPanel recovers every fixture once and assembles the report.
func runPanel(tb testing.TB) panelReport {
	tb.Helper()
	specs := panelManifest(tb)
	rep := panelReport{Fixtures: make([]fixtureMetric, 0, len(specs))}
	var sumAcc, sumFid float64
	for _, s := range specs {
		img := panelImage(tb, s.File())
		start := time.Now()
		res, err := recoverOne(context.Background(), img, s)
		elapsed := time.Since(start)
		if err != nil {
			tb.Fatalf("recover %s: %v", s.Name, err)
		}
		acc := charAccuracy(res.BestGuess, s.Text)
		m := fixtureMetric{
			Name:         s.Name,
			Text:         s.Text,
			Exact:        res.BestGuess == s.Text,
			CharAccuracy: acc,
			Fidelity:     res.Fidelity(),
			Confidence:   res.Confidence,
			Candidates:   len(res.Candidates),
			ElapsedMs:    float64(elapsed.Microseconds()) / 1000,
		}
		rep.Fixtures = append(rep.Fixtures, m)
		rep.Summary.TotalElapsedMs += m.ElapsedMs
		if m.Exact {
			rep.Summary.Exact++
		}
		sumAcc += acc
		sumFid += m.Fidelity
	}
	n := len(rep.Fixtures)
	rep.Summary.N = n
	if n > 0 {
		rep.Summary.ExactRate = float64(rep.Summary.Exact) / float64(n)
		rep.Summary.MeanCharAccuracy = sumAcc / float64(n)
		rep.Summary.MeanFidelity = sumFid / float64(n)
	}
	return rep
}

// TestPanel runs the recovery panel, prints it next to the previous recorded
// run, optionally records the fresh run (PANEL_OUT), and — with PANEL_STRICT=1 —
// fails on a decode-quality regression versus the baseline.
func TestPanel(t *testing.T) {
	rep := runPanel(t)
	t.Logf("\n%s", formatPanel(rep))

	base, ok := loadBaseline(t)
	if ok {
		t.Logf("\n%s", formatComparison(base.Summary, rep.Summary))
		if os.Getenv("PANEL_STRICT") == "1" {
			if rep.Summary.ExactRate < base.Summary.ExactRate {
				t.Errorf("quality regression: exact-match rate %.1f%% < baseline %.1f%%",
					100*rep.Summary.ExactRate, 100*base.Summary.ExactRate)
			}
			if rep.Summary.MeanCharAccuracy < base.Summary.MeanCharAccuracy-1e-9 {
				t.Errorf("quality regression: mean char accuracy %.4f < baseline %.4f",
					rep.Summary.MeanCharAccuracy, base.Summary.MeanCharAccuracy)
			}
		}
	} else {
		t.Logf("no baseline at %s — record one with `mise run bench:panel:record`", baselinePath)
	}

	if out := os.Getenv("PANEL_OUT"); out != "" {
		writeReport(t, out, rep)
		t.Logf("panel written to %s", out)
	}
}

// BenchmarkPanel reports per-fixture recovery speed (one sub-benchmark each) so
// benchstat can prove speed deltas across commits rigorously.
func BenchmarkPanel(b *testing.B) {
	specs := panelManifest(b)
	for _, s := range specs {
		img := panelImage(b, s.File())
		b.Run(s.Name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := recoverOne(context.Background(), img, s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- fixture loading (panel-local, testing.TB so test + benchmark share it) ---

func panelManifest(tb testing.TB) []fixture.Spec {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, "manifest.json"))
	if err != nil {
		tb.Fatalf("read manifest (run `go generate ./...`): %v", err)
	}
	var specs []fixture.Spec
	if err := json.Unmarshal(data, &specs); err != nil {
		tb.Fatalf("parse manifest: %v", err)
	}
	if len(specs) == 0 {
		tb.Fatal("manifest is empty")
	}
	return specs
}

func panelImage(tb testing.TB, file string) image.Image {
	tb.Helper()
	f, err := os.Open(filepath.Join(fixtureDir, file))
	if err != nil {
		tb.Fatalf("open %s: %v", file, err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		tb.Fatalf("decode %s: %v", file, err)
	}
	return img
}

// --- baseline I/O + reporting ---

func loadBaseline(t *testing.T) (panelReport, bool) {
	t.Helper()
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		return panelReport{}, false
	}
	var rep panelReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse baseline %s: %v", baselinePath, err)
	}
	return rep, true
}

func writeReport(t *testing.T, path string, rep panelReport) {
	t.Helper()
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("marshal panel: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// formatPanel renders the per-fixture table.
func formatPanel(rep panelReport) string {
	s := "Recovery panel (quality + speed)\n"
	s += fmt.Sprintf("%-16s %-7s %-6s %-8s %-8s %-6s %8s\n",
		"fixture", "exact", "acc", "fidelity", "conf", "cands", "ms")
	for _, m := range rep.Fixtures {
		mark := "·"
		if m.Exact {
			mark = "✓"
		}
		s += fmt.Sprintf("%-16s %-7s %-6.2f %-8.3f %-8.3f %-6d %8.1f\n",
			m.Name, mark, m.CharAccuracy, m.Fidelity, m.Confidence, m.Candidates, m.ElapsedMs)
	}
	su := rep.Summary
	s += fmt.Sprintf("→ exact %d/%d (%.1f%%)  meanAcc %.4f  meanFidelity %.3f  total %.1f ms\n",
		su.Exact, su.N, 100*su.ExactRate, su.MeanCharAccuracy, su.MeanFidelity, su.TotalElapsedMs)
	return s
}

// formatComparison renders the panel delta vs the previous recorded run.
func formatComparison(base, cur panelSummary) string {
	speed := "—"
	if cur.TotalElapsedMs > 0 && base.TotalElapsedMs > 0 {
		speed = fmt.Sprintf("%+.1f%%", 100*(cur.TotalElapsedMs-base.TotalElapsedMs)/base.TotalElapsedMs)
	}
	return "Δ vs previous recorded panel (baseline → now)\n" +
		fmt.Sprintf("  exact rate:    %.1f%% → %.1f%%  (%+.1f pt)\n",
			100*base.ExactRate, 100*cur.ExactRate, 100*(cur.ExactRate-base.ExactRate)) +
		fmt.Sprintf("  mean accuracy: %.4f → %.4f  (%+.4f)\n",
			base.MeanCharAccuracy, cur.MeanCharAccuracy, cur.MeanCharAccuracy-base.MeanCharAccuracy) +
		fmt.Sprintf("  mean fidelity: %.3f → %.3f  (%+.3f)\n",
			base.MeanFidelity, cur.MeanFidelity, cur.MeanFidelity-base.MeanFidelity) +
		fmt.Sprintf("  total time:    %.1f ms → %.1f ms  (%s)\n",
			base.TotalElapsedMs, cur.TotalElapsedMs, speed)
}

// charAccuracy is 1 − Levenshtein(got, want)/max(len). 1 means an exact match;
// it degrades gracefully so a near-miss scores higher than a total miss.
func charAccuracy(got, want string) float64 {
	a, b := []rune(got), []rune(want)
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	dist := levenshtein(a, b)
	denom := max(len(a), len(b))
	return 1 - float64(dist)/float64(denom)
}

// levenshtein is the classic two-row edit distance over runes.
func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(min(prev[j]+1, curr[j-1]+1), prev[j-1]+cost)
		}
		prev = curr
	}
	return prev[len(b)]
}
