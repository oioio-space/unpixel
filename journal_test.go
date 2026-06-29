//go:build journal

// TestJournal is an evolving measurement harness that runs EVERY image in
// testdata (corpora: fixtures, blur, real, wild) through UnPixel twice:
//
//   - (A) ZERO-CONFIG: RecoverFile / RecoverBlurredFile with auto everything.
//   - (B) BEST-CONFIG: options from each manifest's known parameters.
//
// Results are appended as a timestamped, commit-stamped run to:
//   - docs/JOURNAL.md — human-readable evolving log (tracked by git).
//   - benchmarks/journal/run-<UTCstamp>-<commit>.json — machine-readable snapshot
//     (gitignored via .gitignore).
//
// The test never fails on recovery quality — it is purely observational.
// Drive it with:
//
//	mise run journal
//
// or directly:
//
//	scripts/gotest-caged.sh go test -tags journal -run TestJournal -v -timeout 60m .
package unpixel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	_ "image/jpeg" // register JPEG decoding for .jpg fixtures
	_ "image/png"  // register PNG decoding
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
)

// ─── corpus-specific manifest structs ────────────────────────────────────────

// journalFixtureEntry mirrors one entry in testdata/fixtures/manifest.json.
type journalFixtureEntry struct {
	Name        string  `json:"name"`
	Text        string  `json:"text"`
	Charset     string  `json:"charset"`
	FontSize    float64 `json:"font_size"`
	Bold        bool    `json:"bold"`
	BlockSize   int     `json:"block_size"`
	PaddingTop  int     `json:"padding_top"`
	PaddingLeft int     `json:"padding_left"`
	Secret      bool    `json:"secret,omitempty"`
}

func (e journalFixtureEntry) file() string { return e.Name + ".png" }

// journalBlurEntry mirrors one entry in testdata/blur/manifest.json.
type journalBlurEntry struct {
	Name     string  `json:"name"`
	File     string  `json:"file"`
	Text     string  `json:"text"`
	Charset  string  `json:"charset"`
	FontSize float64 `json:"font_size"`
	Sigma    float64 `json:"sigma"`
}

// journalRealEntry mirrors one entry in testdata/real/manifest.json.
type journalRealEntry struct {
	Name     string  `json:"name"`
	File     string  `json:"file"`
	Text     string  `json:"text"`
	Font     string  `json:"font"`
	FontSize float64 `json:"font_size"`
	XScale   float64 `json:"x_scale"`
	Block    int     `json:"block"`
	OffsetX  int     `json:"offset_x"`
	OffsetY  int     `json:"offset_y"`
	Linear   bool    `json:"linear"`
	Lines    int     `json:"lines"`
	Lang     string  `json:"lang"`
	Source   string  `json:"source"`
	Notes    string  `json:"notes"`
}

// journalWildEntry mirrors one entry in testdata/wild/manifest.json.
type journalWildEntry struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	Kind        string `json:"kind"` // "mosaic" or "blur"
	GroundTruth string `json:"ground_truth"`
	FontHint    string `json:"font_hint"`
	Notes       string `json:"notes"`
}

// journalSickEntry mirrors one entry in testdata/sick/manifest.json, which is
// the paper-parity (Hill-2016) corpus of SICK sentences and digit check-numbers.
type journalSickEntry struct {
	Name        string  `json:"name"`
	Text        string  `json:"text"`
	Charset     string  `json:"charset"`
	FontSize    float64 `json:"font_size"`
	Bold        bool    `json:"bold"`
	BlockSize   int     `json:"block_size"`
	PaddingTop  int     `json:"padding_top"`
	PaddingLeft int     `json:"padding_left"`
	Font        string  `json:"font"`
	Kind        string  `json:"kind"` // "sick" or "digits"
	Note        string  `json:"note,omitempty"`
}

func (e journalSickEntry) file() string { return e.Name + ".png" }

// journalRun is the full machine-readable run record written to benchmarks/journal/.
type journalRun struct {
	Timestamp      string                 `json:"timestamp"`
	Commit         string                 `json:"commit"`
	Version        string                 `json:"version"`
	GoVersion      string                 `json:"go_version"`
	GOOS           string                 `json:"goos"`
	GOARCH         string                 `json:"goarch"`
	DurationMS     float64                `json:"duration_ms"`
	Rows           []journalRow           `json:"rows"`
	Corpora        []journalCorpusSummary `json:"corpora"`
	DecoderRows    []decoderRow           `json:"decoder_rows,omitempty"`
	ContextDetails []contextDetail        `json:"context_details,omitempty"`
}

// ─── timeout / charset constants ─────────────────────────────────────────────

const (
	journalTimeoutZero = 30 * time.Second
	// 30s (was 90s): right-sized from measured data — every DECODABLE best-config
	// decode finishes ≤10.4s (blur) / ≤0.3s (fixtures), so 90s was pure wasted
	// compute on the undecodable real/wild/sick images (0/N at any budget). 30s
	// preserves all exact/≥70% counts with ~3× margin while cutting ~30% of journal
	// wall time. See PROGRESS.md perf section.
	journalTimeoutBest = 30 * time.Second

	// journalMaxLength is a conservative upper bound for unknown-text runs.
	journalMaxLength = 30
)

// journalWideCharset is a broad printable-ASCII set for unknown text.
var journalWideCharset = unpixel.CharsetAlnum + "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// ─── main test ───────────────────────────────────────────────────────────────

// TestJournal runs all four corpora through UnPixel in zero-config and
// best-config modes and appends a timestamped run to docs/JOURNAL.md and
// benchmarks/journal/run-<UTCstamp>-<commit>.json.
func TestJournal(t *testing.T) {
	start := time.Now()
	commit := gitShortCommit(t)
	version := gitVersion(t)
	timestamp := start.UTC().Format("2006-01-02T15:04:05Z")

	var rows []journalRow
	rows = append(rows, runFixturesCorpus(t)...)
	rows = append(rows, runBlurCorpus(t)...)
	rows = append(rows, runRealCorpus(t)...)
	rows = append(rows, runWildCorpus(t)...)
	rows = append(rows, runSickCorpus(t)...)

	decoderRows, ctxDetails := runDecoderMatrix(t)

	totalDuration := time.Since(start)
	corpora := summariseCorpora(rows)

	run := journalRun{
		Timestamp:      timestamp,
		Commit:         commit,
		Version:        version,
		GoVersion:      runtime.Version(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		DurationMS:     float64(totalDuration.Milliseconds()),
		Rows:           rows,
		Corpora:        corpora,
		DecoderRows:    decoderRows,
		ContextDetails: ctxDetails,
	}

	writeJournalJSON(t, run, timestamp, commit)
	appendJournalMD(t, run)

	// Print a compact summary to the test log.
	t.Logf("journal run complete: %s commit=%s go=%s", timestamp, commit, runtime.Version())
	for _, cs := range corpora {
		knowableZero := cs.Total - cs.ZeroUnknown
		knowableBest := cs.Total - cs.BestUnknown
		zeroMean, bestMean := "NA", "NA"
		if cs.ZeroMeanScore >= 0 {
			zeroMean = fmt.Sprintf("%.0f%%", cs.ZeroMeanScore)
		}
		if cs.BestMeanScore >= 0 {
			bestMean = fmt.Sprintf("%.0f%%", cs.BestMeanScore)
		}
		t.Logf("  %-8s  zero %d/%d ≥70%%=%d mean=%s  best %d/%d ≥70%%=%d mean=%s",
			cs.Name,
			cs.ZeroOK, knowableZero, cs.ZeroSensical, zeroMean,
			cs.BestOK, knowableBest, cs.BestSensical, bestMean)
	}
	t.Logf("  total duration: %.1f s", totalDuration.Seconds())
}

// ─── per-corpus runners ───────────────────────────────────────────────────────

func runFixturesCorpus(t *testing.T) []journalRow {
	t.Helper()
	const dir = "testdata/fixtures"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("fixtures: read manifest: %v (skipping corpus)", err)
		return nil
	}
	var entries []journalFixtureEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("fixtures: parse manifest: %v (skipping corpus)", err)
		return nil
	}

	rows := make([]journalRow, 0, len(entries))
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.file())
		if _, statErr := os.Stat(imgPath); statErr != nil {
			t.Logf("fixtures/%s: file missing, skipping", e.Name)
			rows = append(rows, skippedRow("fixtures", e.Name, "mosaic", e.Text))
			continue
		}

		gt := e.Text
		gtLen := utf8.RuneCountInString(gt)

		// Zero-config: no charset / block / font hints.
		zeroCtx, zeroCancel := context.WithTimeout(t.Context(), journalTimeoutZero)
		zeroStart := time.Now()
		zeroRes, zeroErr := unpixel.RecoverFile(
			zeroCtx, imgPath,
			unpixel.WithMaxLength(gtLen+4),
			unpixel.WithWorkers(2),
		)
		zeroDur := time.Since(zeroStart)
		zeroTimedOut := zeroCtx.Err() != nil
		zeroCancel()
		zeroAttempt := classifyAttempt(
			zeroRes, zeroErr, gt, zeroDur, zeroTimedOut,
			fmt.Sprintf("zero-config maxLen=%d", gtLen+4),
		)

		// Best-config: charset + block_size + style from manifest.
		bestCtx, bestCancel := context.WithTimeout(t.Context(), journalTimeoutBest)
		bestStart := time.Now()
		bestRes, bestErr := unpixel.RecoverFile(
			bestCtx, imgPath,
			unpixel.WithCharset(e.Charset),
			unpixel.WithBlockSize(e.BlockSize),
			unpixel.WithStyle(unpixel.Style{
				FontSize:    e.FontSize,
				Bold:        e.Bold,
				PaddingTop:  e.PaddingTop,
				PaddingLeft: e.PaddingLeft,
			}),
			unpixel.WithMaxLength(gtLen+1),
			unpixel.WithWorkers(2),
		)
		bestDur := time.Since(bestStart)
		bestTimedOut := bestCtx.Err() != nil
		bestCancel()
		bestAttempt := classifyAttempt(
			bestRes, bestErr, gt, bestDur, bestTimedOut,
			fmt.Sprintf("charset=%q block=%d font=%.0fpt bold=%v pad=%d,%d",
				e.Charset, e.BlockSize, e.FontSize, e.Bold, e.PaddingLeft, e.PaddingTop),
		)

		rows = append(rows, journalRow{
			Corpus:      "fixtures",
			Name:        e.Name,
			Kind:        "mosaic",
			GroundTruth: gt,
			ZeroConfig:  zeroAttempt,
			BestConfig:  bestAttempt,
		})
		t.Logf("fixtures/%-22s gt=%q  zero=%s best=%s", e.Name, gt, zeroAttempt.Status, bestAttempt.Status)
	}
	return rows
}

func runBlurCorpus(t *testing.T) []journalRow {
	t.Helper()
	const dir = "testdata/blur"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("blur: read manifest: %v (skipping corpus)", err)
		return nil
	}
	var entries []journalBlurEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("blur: parse manifest: %v (skipping corpus)", err)
		return nil
	}

	rows := make([]journalRow, 0, len(entries))
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.File)
		if _, statErr := os.Stat(imgPath); statErr != nil {
			t.Logf("blur/%s: file missing, skipping", e.Name)
			rows = append(rows, skippedRow("blur", e.Name, "blur", e.Text))
			continue
		}

		gt := e.Text
		gtLen := utf8.RuneCountInString(gt)

		// Zero-config blur: auto sigma estimation.
		zeroCtx, zeroCancel := context.WithTimeout(t.Context(), journalTimeoutZero)
		zeroStart := time.Now()
		zeroRes, zeroErr := unpixel.RecoverBlurredFile(
			zeroCtx, imgPath,
			unpixel.WithMaxLength(gtLen+4),
			unpixel.WithWorkers(2),
		)
		zeroDur := time.Since(zeroStart)
		zeroTimedOut := zeroCtx.Err() != nil
		zeroCancel()
		zeroAttempt := classifyAttempt(
			zeroRes, zeroErr, gt, zeroDur, zeroTimedOut,
			fmt.Sprintf("zero-config-blur maxLen=%d", gtLen+4),
		)

		// Best-config blur: tight charset + known font size; sigma is auto-estimated
		// since RecoverBlurred sweeps for the best sigma internally.
		bestCtx, bestCancel := context.WithTimeout(t.Context(), journalTimeoutBest)
		bestStart := time.Now()
		bestRes, bestErr := unpixel.RecoverBlurredFile(
			bestCtx, imgPath,
			unpixel.WithCharset(e.Charset),
			unpixel.WithStyle(unpixel.Style{FontSize: e.FontSize}),
			unpixel.WithMaxLength(gtLen+1),
			unpixel.WithWorkers(2),
		)
		bestDur := time.Since(bestStart)
		bestTimedOut := bestCtx.Err() != nil
		bestCancel()
		bestAttempt := classifyAttempt(
			bestRes, bestErr, gt, bestDur, bestTimedOut,
			fmt.Sprintf("charset=%q sigma=%.1f(auto) font=%.0fpt", e.Charset, e.Sigma, e.FontSize),
		)

		rows = append(rows, journalRow{
			Corpus:      "blur",
			Name:        e.Name,
			Kind:        "blur",
			GroundTruth: gt,
			ZeroConfig:  zeroAttempt,
			BestConfig:  bestAttempt,
		})
		t.Logf("blur/%-28s gt=%q  zero=%s best=%s", e.Name, gt, zeroAttempt.Status, bestAttempt.Status)
	}
	return rows
}

func runRealCorpus(t *testing.T) []journalRow {
	t.Helper()
	const dir = "testdata/real"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("real: read manifest: %v (skipping corpus)", err)
		return nil
	}
	var entries []journalRealEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("real: parse manifest: %v (skipping corpus)", err)
		return nil
	}

	rows := make([]journalRow, 0, len(entries))
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.File)
		if _, statErr := os.Stat(imgPath); statErr != nil {
			t.Logf("real/%s: file missing, skipping", e.Name)
			rows = append(rows, skippedRow("real", e.Name, "mosaic", e.Text))
			continue
		}

		gt := e.Text
		maxLen := utf8.RuneCountInString(gt)

		// Zero-config: no hints — rely entirely on auto-detect.
		zeroCtx, zeroCancel := context.WithTimeout(t.Context(), journalTimeoutZero)
		zeroStart := time.Now()
		zeroRes, zeroErr := unpixel.RecoverFile(
			zeroCtx, imgPath,
			unpixel.WithMaxLength(maxLen+4),
			unpixel.WithWorkers(2),
		)
		zeroDur := time.Since(zeroStart)
		zeroTimedOut := zeroCtx.Err() != nil
		zeroCancel()
		zeroAttempt := classifyAttempt(
			zeroRes, zeroErr, gt, zeroDur, zeroTimedOut,
			fmt.Sprintf("zero-config maxLen=%d", maxLen+4),
		)

		// Best-config: block + linear light pixelator + font size.
		// Use a broad Latin charset since real manifests don't provide a tight one.
		const realCharset = unpixel.CharsetAlnum + " !,.'-éàèùâêîôûäëïöüç"
		pixelatorDesc := "sRGB-block"
		bestOpts := []unpixel.Option{
			unpixel.WithCharset(realCharset),
			unpixel.WithBlockSize(e.Block),
			unpixel.WithStyle(unpixel.Style{FontSize: e.FontSize}),
			unpixel.WithMaxLength(maxLen + 2),
			unpixel.WithWorkers(2),
		}
		if e.Linear {
			pixelatorDesc = "linear-block"
			bestOpts = append(
				bestOpts,
				unpixel.WithPixelator(defaults.LinearBlockAverage(e.Block)),
			)
		}
		bestCtx, bestCancel := context.WithTimeout(t.Context(), journalTimeoutBest)
		bestStart := time.Now()
		bestRes, bestErr := unpixel.RecoverFile(bestCtx, imgPath, bestOpts...)
		bestDur := time.Since(bestStart)
		bestTimedOut := bestCtx.Err() != nil
		bestCancel()
		bestAttempt := classifyAttempt(
			bestRes, bestErr, gt, bestDur, bestTimedOut,
			fmt.Sprintf("block=%d font=%.0fpt %s font=%q", e.Block, e.FontSize, pixelatorDesc, e.Font),
		)

		rows = append(rows, journalRow{
			Corpus:      "real",
			Name:        e.Name,
			Kind:        "mosaic",
			GroundTruth: gt,
			ZeroConfig:  zeroAttempt,
			BestConfig:  bestAttempt,
		})
		t.Logf("real/%-20s gt=%q  zero=%s best=%s", e.Name, truncate(gt, 30), zeroAttempt.Status, bestAttempt.Status)
	}
	return rows
}

func runWildCorpus(t *testing.T) []journalRow {
	t.Helper()
	const dir = "testdata/wild"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("wild: read manifest: %v (skipping corpus)", err)
		return nil
	}
	var entries []journalWildEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("wild: parse manifest: %v (skipping corpus)", err)
		return nil
	}

	rows := make([]journalRow, 0, len(entries))
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.File)
		displayGT := e.GroundTruth
		if displayGT == "" {
			displayGT = "—"
		}
		if _, statErr := os.Stat(imgPath); statErr != nil {
			t.Logf("wild/%s: file missing (run scripts/fetch-wild-fixtures.sh), skipping", e.Name)
			rows = append(rows, skippedRow("wild", e.Name, e.Kind, displayGT))
			continue
		}

		gt := e.GroundTruth // may be empty = unknown
		maxLen := journalMaxLength
		if gt != "" {
			if n := utf8.RuneCountInString(gt) + 4; n < maxLen {
				maxLen = n
			}
		}

		// Zero-config: wide charset, kind-appropriate recovery.
		zeroCtx, zeroCancel := context.WithTimeout(t.Context(), journalTimeoutZero)
		zeroStart := time.Now()
		zeroRes, zeroErr := recoverWildEntry(
			zeroCtx, imgPath, e.Kind,
			unpixel.WithCharset(journalWideCharset),
			unpixel.WithMaxLength(maxLen),
			unpixel.WithWorkers(2),
		)
		zeroDur := time.Since(zeroStart)
		zeroTimedOut := zeroCtx.Err() != nil
		zeroCancel()
		zeroAttempt := classifyAttempt(zeroRes, zeroErr, gt, zeroDur, zeroTimedOut,
			fmt.Sprintf("zero-config kind=%s charset=wide maxLen=%d", e.Kind, maxLen))
		if gt == "" {
			zeroAttempt.Status = statusUnknown
			zeroAttempt.Why = ""
		}

		// Best-config: full ASCII charset (widest sensible set for real-world text).
		bestCtx, bestCancel := context.WithTimeout(t.Context(), journalTimeoutBest)
		bestStart := time.Now()
		bestRes, bestErr := recoverWildEntry(
			bestCtx, imgPath, e.Kind,
			unpixel.WithCharset(unpixel.CharsetASCII),
			unpixel.WithMaxLength(maxLen),
			unpixel.WithWorkers(2),
		)
		bestDur := time.Since(bestStart)
		bestTimedOut := bestCtx.Err() != nil
		bestCancel()
		bestAttempt := classifyAttempt(bestRes, bestErr, gt, bestDur, bestTimedOut,
			fmt.Sprintf("best-config kind=%s charset=ascii maxLen=%d", e.Kind, maxLen))
		if gt == "" {
			bestAttempt.Status = statusUnknown
			bestAttempt.Why = ""
		}

		rows = append(rows, journalRow{
			Corpus:      "wild",
			Name:        e.Name,
			Kind:        e.Kind,
			GroundTruth: displayGT,
			ZeroConfig:  zeroAttempt,
			BestConfig:  bestAttempt,
		})
		t.Logf("wild/%-5s gt=%q  zero=%s best=%s", e.Name, displayGT, zeroAttempt.Status, bestAttempt.Status)
	}
	return rows
}

// recoverWildEntry opens imgPath and runs either RecoverFile or
// RecoverBlurredFile depending on kind ("blur" vs anything else).
func recoverWildEntry(ctx context.Context, path, kind string, opts ...unpixel.Option) (unpixel.Result, error) {
	if kind == "blur" {
		return unpixel.RecoverBlurredFile(ctx, path, opts...)
	}
	return unpixel.RecoverFile(ctx, path, opts...)
}

// runSickCorpus runs the paper-parity Hill-2016 corpus (testdata/sick/) through
// UnPixel in zero-config and best-config modes. Best-config uses the manifest's
// charset, block size, font size, and padding — the fully matched-parameter
// self-consistent setup that corresponds to the paper's "matched font/grid" condition.
func runSickCorpus(t *testing.T) []journalRow {
	t.Helper()
	const dir = "testdata/sick"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("sick: read manifest: %v (skipping corpus)", err)
		return nil
	}
	var entries []journalSickEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("sick: parse manifest: %v (skipping corpus)", err)
		return nil
	}

	rows := make([]journalRow, 0, len(entries))
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.file())
		if _, statErr := os.Stat(imgPath); statErr != nil {
			t.Logf("sick/%s: file missing, skipping", e.Name)
			rows = append(rows, skippedRow("sick", e.Name, "mosaic", e.Text))
			continue
		}

		gt := e.Text
		gtLen := utf8.RuneCountInString(gt)

		// Zero-config: no charset / block / font hints.
		zeroCtx, zeroCancel := context.WithTimeout(t.Context(), journalTimeoutZero)
		zeroStart := time.Now()
		zeroRes, zeroErr := unpixel.RecoverFile(
			zeroCtx, imgPath,
			unpixel.WithMaxLength(gtLen+4),
			unpixel.WithWorkers(2),
		)
		zeroDur := time.Since(zeroStart)
		zeroTimedOut := zeroCtx.Err() != nil
		zeroCancel()
		zeroAttempt := classifyAttempt(
			zeroRes, zeroErr, gt, zeroDur, zeroTimedOut,
			fmt.Sprintf("zero-config maxLen=%d", gtLen+4),
		)

		// Best-config: fully matched parameters from manifest (charset + block +
		// font size + padding). This mirrors the paper's "matched font/grid" condition.
		bestCtx, bestCancel := context.WithTimeout(t.Context(), journalTimeoutBest)
		bestStart := time.Now()
		bestRes, bestErr := unpixel.RecoverFile(
			bestCtx, imgPath,
			unpixel.WithCharset(e.Charset),
			unpixel.WithBlockSize(e.BlockSize),
			unpixel.WithStyle(unpixel.Style{
				FontSize:    e.FontSize,
				Bold:        e.Bold,
				PaddingTop:  e.PaddingTop,
				PaddingLeft: e.PaddingLeft,
			}),
			unpixel.WithMaxLength(gtLen+1),
			unpixel.WithWorkers(2),
		)
		bestDur := time.Since(bestStart)
		bestTimedOut := bestCtx.Err() != nil
		bestCancel()
		bestAttempt := classifyAttempt(
			bestRes, bestErr, gt, bestDur, bestTimedOut,
			fmt.Sprintf("charset=%q block=%d font=%.0fpt bold=%v pad=%d,%d font=%q",
				e.Charset, e.BlockSize, e.FontSize, e.Bold, e.PaddingLeft, e.PaddingTop, e.Font),
		)

		rows = append(rows, journalRow{
			Corpus:      "sick",
			Name:        e.Name,
			Kind:        e.Kind,
			GroundTruth: gt,
			ZeroConfig:  zeroAttempt,
			BestConfig:  bestAttempt,
		})
		t.Logf("sick/%-28s gt=%q  zero=%s best=%s", e.Name, truncate(gt, 30), zeroAttempt.Status, bestAttempt.Status)
	}
	return rows
}

// skippedRow is a convenience constructor for missing-file rows.
func skippedRow(corpus, name, kind, gt string) journalRow {
	if gt == "" {
		gt = "—"
	}
	return journalRow{
		Corpus:      corpus,
		Name:        name,
		Kind:        kind,
		GroundTruth: gt,
		ZeroConfig:  journalAttempt{Status: statusSkipped, Params: "zero-config"},
		BestConfig:  journalAttempt{Status: statusSkipped, Params: "best-config"},
	}
}

// ─── scoring ──────────────────────────────────────────────────────────────────

// classifyAttempt builds a journalAttempt from the raw recovery output.
// timedOut must be captured from ctx.Err() BEFORE the context's cancel function
// is called, so that a normal cancellation (calling cancel() after the call
// returns) is not misclassified as a deadline exceeded.
func classifyAttempt(
	res unpixel.Result,
	err error,
	groundTruth string,
	dur time.Duration,
	timedOut bool,
	params string,
) journalAttempt {
	a := journalAttempt{
		Params:         params,
		Guess:          res.BestGuess,
		Confidence:     res.Confidence,
		BestTotal:      res.BestTotal,
		DurationMS:     float64(dur.Milliseconds()),
		BelowThreshold: res.BelowThreshold,
		BlurSigma:      res.BlurSigma,
	}

	// A hard (non-deadline) error that returned nothing is recorded directly.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && res.BestGuess == "" {
		a.Status = statusError
		a.Why = fmt.Sprintf("error: %v", err)
		return a
	}

	// Propagate the deadline error so classifyOutcome can distinguish
	// "timed out with no result" from "got result before deadline".
	var recErr error
	if timedOut && res.BestGuess == "" {
		recErr = context.DeadlineExceeded
	}

	// Determine the budget used for the timeout message.
	budget := journalTimeoutZero
	if strings.Contains(params, "best-config") || strings.Contains(params, "charset=") {
		budget = journalTimeoutBest
	}

	st, why := classifyOutcome(res.BestGuess, groundTruth, res.BelowThreshold, recErr, dur, budget)
	a.Status = journalStatus(st) // outcomeOK/outcomeFail/outcomeUnknown map 1-to-1 to journalStatus values.
	a.Why = why

	// ExactCI and partial-credit score: set on both ok and fail paths.
	if groundTruth != "" && groundTruth != "—" {
		a.ExactCI = strings.EqualFold(res.BestGuess, groundTruth)
		a.Score = recoveryScore(res.BestGuess, groundTruth)
	} else {
		a.Score = -1
	}

	return a
}

// ─── I/O ─────────────────────────────────────────────────────────────────────

// writeJournalJSON writes the machine-readable run to benchmarks/journal/.
func writeJournalJSON(t *testing.T, run journalRun, timestamp, commit string) {
	t.Helper()
	const dir = "benchmarks/journal"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("warning: create %s: %v", dir, err)
		return
	}
	// Sanitise timestamp for filename (colons → dashes).
	safe := strings.NewReplacer(":", "-").Replace(timestamp)
	path := filepath.Join(dir, fmt.Sprintf("run-%s-%s.json", safe, commit))
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Logf("warning: marshal run: %v", err)
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Logf("warning: write %s: %v", path, err)
		return
	}
	t.Logf("journal JSON written to %s", path)
}

// appendJournalMD appends a new evolution row and a new run section to
// docs/JOURNAL.md, creating the file with a header if it does not exist.
//
// Structure of docs/JOURNAL.md:
//
//	## Évolution
//	| date | commit | … |
//	|---|---|…|
//	| older row |
//	| NEW row |   ← appended inside the table
//
//	## Run <new timestamp> — <commit>   ← inserted before older runs
//	…
//
//	## Run <older timestamp> — <commit>
//	…
func appendJournalMD(t *testing.T, run journalRun) {
	t.Helper()
	const path = "docs/JOURNAL.md"

	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}

	newRow := buildEvolutionRow(run)
	newSection := buildRunSection(run)
	newDecoderRows := buildDecoderEvolutionRows(run, run.DecoderRows)

	// No existing file: create from scratch.
	if existing == "" {
		content := buildJournalHeader() + newRow + "\n\n" + newSection + "\n\n" +
			buildDecoderTableHeader() + newDecoderRows + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Logf("warning: write %s: %v", path, err)
		}
		t.Logf("docs/JOURNAL.md created")
		return
	}

	// Splice corpus row + run section into the corpus evolution table.
	updated := spliceJournalMD(existing, newRow, newSection)
	// Splice decoder rows into (or append) the decoder evolution table.
	updated = spliceDecoderTableMD(updated, newDecoderRows)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Logf("warning: write %s: %v", path, err)
		return
	}
	t.Logf("docs/JOURNAL.md updated")
}

// buildJournalHeader returns the initial file content including the static
// header and the evolution table header rows.
//
// Column layout (per corpus, zero then best):
//
//	exact | ≥70% | mean%
func buildJournalHeader() string {
	return `# UnPixel — Test Journal

This file is auto-generated by TestJournal (build tag: journal). Each run
appends one row to the evolution table and prepends a full run section. Re-read
this file over time to watch decode quality evolve.

Score columns: each corpus pair shows "exact/≥70%/mean%" for zero-config then best-config.

## Évolution

| Date (UTC) | Version | Commit | fix·zero | fix·best | blur·zero | blur·best | real·zero | real·best | wild·zero | wild·best | sick·zero | sick·best | ctx·C1a | Total | Dur (s) |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
`
}

// buildEvolutionRow builds one markdown table row for the Évolution table.
// Each corpus cell uses the compact format "exact/≥70%/mean%" where:
//   - exact  = number of exact matches out of knowable images
//   - ≥70%   = count scoring ≥70 (sensical recoveries per Hill et al.)
//   - mean%  = mean Levenshtein score across knowable images (or NA)
func buildEvolutionRow(run journalRun) string {
	byName := make(map[string]journalCorpusSummary, len(run.Corpora))
	for _, cs := range run.Corpora {
		byName[cs.Name] = cs
	}

	// cell returns "exact/≥70%/mean%" for the given corpus+mode.
	cell := func(name, mode string) string {
		cs, ok := byName[name]
		if !ok {
			return "—"
		}
		var exact, sensical, knowable int
		var mean float64
		if mode == "zero" {
			exact = cs.ZeroOK
			sensical = cs.ZeroSensical
			knowable = cs.Total - cs.ZeroUnknown
			mean = cs.ZeroMeanScore
		} else {
			exact = cs.BestOK
			sensical = cs.BestSensical
			knowable = cs.Total - cs.BestUnknown
			mean = cs.BestMeanScore
		}
		meanStr := "NA"
		if mean >= 0 {
			meanStr = fmt.Sprintf("%.0f%%", mean)
		}
		return fmt.Sprintf("%d/%d/%d/%s", exact, knowable, sensical, meanStr)
	}

	// ctxCell summarises the context corpus, which is calibration-only (no
	// zero/best RecoverFile path): the C1a calibrate-visible aggregate, in the
	// same "exact/knowable/≥70%/mean%" format. "—" when no context run happened.
	ctxCell := "—"
	for _, dr := range run.DecoderRows {
		if dr.Decoder == "calibrate-visible" && dr.Corpus == "context" {
			meanStr := "NA"
			if dr.MeanScore >= 0 {
				meanStr = fmt.Sprintf("%.0f%%", dr.MeanScore)
			}
			ctxCell = fmt.Sprintf("%d/%d/%d/%s", dr.ExactOK, dr.Knowable, dr.Sensical, meanStr)
			break
		}
	}

	total := 0
	for _, cs := range run.Corpora {
		total += cs.Total
	}
	durSec := run.DurationMS / 1000

	return fmt.Sprintf(
		"| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %d | %.0f |\n",
		run.Timestamp[:10],
		run.Version,
		run.Commit,
		cell("fixtures", "zero"), cell("fixtures", "best"),
		cell("blur", "zero"), cell("blur", "best"),
		cell("real", "zero"), cell("real", "best"),
		cell("wild", "zero"), cell("wild", "best"),
		cell("sick", "zero"), cell("sick", "best"),
		ctxCell,
		total, durSec,
	)
}

// buildContextSection renders the per-image C1a/C1b detail table for a run.
// It returns an empty string when run.ContextDetails is empty.
func buildContextSection(run journalRun) string {
	if len(run.ContextDetails) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "### context\n\n")
	fmt.Fprintf(&sb, "Context-assisted decode (C1a/C1b): the font is calibrated from a visible source\n")
	fmt.Fprintf(&sb, "(C1a: a sharp `visible_rect` in the same image; C1b: a separate font-sample PNG),\n")
	fmt.Fprintf(&sb, "then the redacted region is decoded blind. Calibration finds the font well\n")
	fmt.Fprintf(&sb, "(dist≈0 in unit tests), but blind recovery of the redacted secret stays weak —\n")
	fmt.Fprintf(&sb, "this section makes each fixture visible per-image rather than as a single 0/9 row.\n\n")
	fmt.Fprintf(&sb, "| image | secret | C1a visible: guess/score | C1b sample: guess/score | block |\n")
	fmt.Fprintf(&sb, "|---|---|---|---|---|\n")

	var c1aExact, c1aTotal int
	var c1aMeanSum float64
	var c1bExact, c1bTotal int

	for _, d := range run.ContextDetails {
		blockStr := "auto"
		if d.Block > 0 {
			blockStr = fmt.Sprintf("%d", d.Block)
		}

		c1aCell := fmt.Sprintf("`%s`/%.0f%%", d.VisibleGuess, d.VisibleScore)
		c1aTotal++
		c1aMeanSum += d.VisibleScore
		if d.VisibleGuess == d.Secret {
			c1aExact++
		}

		c1bCell := "—"
		if d.HasSample {
			c1bCell = fmt.Sprintf("`%s`/%.0f%%", d.SampleGuess, d.SampleScore)
			c1bTotal++
			if d.SampleGuess == d.Secret {
				c1bExact++
			}
		}

		fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s | %s |\n",
			d.Name, d.Secret, c1aCell, c1bCell, blockStr)
	}

	fmt.Fprintf(&sb, "\n")

	c1aMean := 0.0
	if c1aTotal > 0 {
		c1aMean = c1aMeanSum / float64(c1aTotal)
	}
	c1bStr := ""
	if c1bTotal > 0 {
		c1bStr = fmt.Sprintf(" C1b (calibrate-sample): %d/%d exact.", c1bExact, c1bTotal)
	}
	fmt.Fprintf(&sb, "C1a (calibrate-visible): %d/%d exact, mean %.0f%%.%s\n",
		c1aExact, c1aTotal, c1aMean, c1bStr)

	return sb.String()
}

// buildRunSection builds the detailed per-run markdown section.
func buildRunSection(run journalRun) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "## Run %s — %s\n\n", run.Timestamp, run.Commit)
	fmt.Fprintf(&sb, "**Environment:** Go %s · %s/%s · NumCPU=%d GOMAXPROCS=%d · total %.1f s\n\n",
		run.GoVersion, run.GOOS, run.GOARCH,
		runtime.NumCPU(), runtime.GOMAXPROCS(0),
		run.DurationMS/1000)

	// Résumé par corpus — compact summary table before per-image detail.
	byName := make(map[string]journalCorpusSummary, len(run.Corpora))
	for _, cs := range run.Corpora {
		byName[cs.Name] = cs
	}
	fmt.Fprintf(&sb, "### Résumé par corpus\n\n")
	fmt.Fprintf(&sb, "| Corpus | exact | ≥70%% | mean%% | mean-conf | mean-fidelity | dur(s) | échecs (top buckets) |\n")
	fmt.Fprintf(&sb, "|---|---|---|---|---|---|---|---|\n")
	for _, corpus := range []string{"fixtures", "blur", "real", "wild", "sick"} {
		cs, ok := byName[corpus]
		if !ok || cs.Total == 0 {
			continue
		}
		knowable := cs.Total - cs.BestUnknown
		meanStr := "NA"
		if cs.BestMeanScore >= 0 {
			meanStr = fmt.Sprintf("%.0f%%", cs.BestMeanScore)
		}
		confStr := "NA"
		if cs.BestMeanConf >= 0 {
			confStr = fmt.Sprintf("%.3f", cs.BestMeanConf)
		}
		fidStr := "NA"
		if cs.BestMeanFidelity >= 0 {
			fidStr = fmt.Sprintf("%.3f", cs.BestMeanFidelity)
		}
		durSec := cs.BestDurationMS / 1000
		fmt.Fprintf(
			&sb, "| %s | %d/%d | %d | %s | %s | %s | %.1f | %s |\n",
			corpus,
			cs.BestOK, knowable,
			cs.BestSensical,
			meanStr,
			confStr,
			fidStr,
			durSec,
			formatFailBuckets(cs.BestFailModes),
		)
	}
	fmt.Fprintf(&sb, "\n")

	// Group rows by corpus, preserving stable order.
	byCorpus := make(map[string][]journalRow)
	for _, row := range run.Rows {
		byCorpus[row.Corpus] = append(byCorpus[row.Corpus], row)
	}

	for _, corpus := range []string{"fixtures", "blur", "real", "wild", "sick"} {
		rows, ok := byCorpus[corpus]
		if !ok || len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "### %s\n\n", corpus)
		fmt.Fprintf(&sb, "| image | gt | zero: status/guess/score%%/conf/ms | best: status/guess/score%%/conf/ms | why |\n")
		fmt.Fprintf(&sb, "|---|---|---|---|---|\n")
		for _, row := range rows {
			gt := row.GroundTruth
			if gt == "" {
				gt = "—"
			}
			why := row.ZeroConfig.Why
			if why == "" {
				why = row.BestConfig.Why
			}
			if why == "" {
				why = "—"
			}
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s | %s |\n",
				row.Name, truncate(gt, 25),
				formatAttemptCell(row.ZeroConfig),
				formatAttemptCell(row.BestConfig),
				why)
		}
		fmt.Fprintf(&sb, "\n")
	}
	sb.WriteString(buildContextSection(run))
	return sb.String()
}

// formatFailBuckets renders the non-zero entries of a BestFailModes histogram
// as a compact string, e.g. "below-thr ×6, wrong-len ×2". Returns "—" when
// the map is empty or all counts are zero.
func formatFailBuckets(m map[string]int) string {
	// Canonical display order — most actionable buckets first.
	order := []string{"below-threshold", "wrong-length", "wrong-glyphs", "timeout", "error", "unknown", "ok"}
	// Short labels for the table column.
	short := map[string]string{
		"below-threshold": "below-thr",
		"wrong-length":    "wrong-len",
		"wrong-glyphs":    "wrong-gly",
		"timeout":         "timeout",
		"error":           "error",
		"unknown":         "unknown",
		"ok":              "ok",
	}
	var parts []string
	for _, k := range order {
		if n := m[k]; n > 0 && k != "ok" {
			parts = append(parts, fmt.Sprintf("%s ×%d", short[k], n))
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

// formatAttemptCell returns a compact markdown cell string for one attempt.
func formatAttemptCell(a journalAttempt) string {
	if a.Status == statusSkipped {
		return "skipped"
	}
	guess := truncate(a.Guess, 20)
	if guess == "" {
		guess = "(none)"
	}
	scoreStr := "NA"
	if a.Score >= 0 {
		scoreStr = fmt.Sprintf("%.0f%%", a.Score)
	}
	return fmt.Sprintf("%s/`%s`/%s/conf=%.2f/ms=%.0f",
		a.Status, guess, scoreStr, a.Confidence, a.DurationMS)
}

// ─── git / misc helpers ───────────────────────────────────────────────────────

// gitShortCommit returns the current git short commit hash, or "unknown".
func gitShortCommit(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output() // #nosec G204 -- test only
	if err != nil {
		t.Logf("git rev-parse: %v", err)
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// gitVersion returns the release label to show beside the commit in the
// evolution table. Precedence: the JOURNAL_VERSION env var (set at release time,
// mirroring PANEL_LABEL), then an exact tag on HEAD, then "<latest-tag>+dev" for
// an untagged work-in-progress commit, then "untagged".
func gitVersion(t *testing.T) string {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("JOURNAL_VERSION")); v != "" {
		return v
	}
	if out, err := exec.Command("git", "describe", "--tags", "--exact-match").Output(); err == nil { // #nosec G204 -- test only
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output(); err == nil { // #nosec G204 -- test only
		return strings.TrimSpace(string(out)) + "+dev"
	}
	return "untagged"
}

// truncate returns s truncated to at most n runes, appending "…" when shortened.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
