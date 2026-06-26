//go:build journal

// journal_decoder_test.go — opt-in decoder tracking table for TestJournal.
//
// runDecoderMatrix runs each (decoder, corpus) pair from the decoder matrix
// and returns a slice of decoderRows in long format (one row per run). The
// results are appended to the "## Évolution — décodeurs" section of
// docs/JOURNAL.md so each decoder's recovery quality is tracked
// version-over-version alongside the per-corpus table.
//
// The decoderRow type, decoderSlowCap constant, buildDecoderTableHeader, and
// spliceDecoderTableMD live in journal_decoder_md_test.go (no build tag) so
// the splice unit tests can exercise them without the journal tag.
package unpixel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/blind"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/mosaictext"
)

// decoderPerTimeout is the hard per-decode context timeout. A timed-out decode
// counts as a non-recovery (score 0) but is not a test failure.
const decoderPerTimeout = 45 * time.Second

// ─── main entry point ─────────────────────────────────────────────────────────

// runDecoderMatrix runs every (decoder, corpus) pair in the tracking matrix and
// returns the aggregate rows alongside per-image context details. It is called
// from TestJournal after the corpus runs.
func runDecoderMatrix(t *testing.T) ([]decoderRow, []contextDetail) {
	t.Helper()

	sickEntries := loadDecoderSickManifest(t)
	realEntries := loadDecoderRealManifest(t)
	contextSpecs := loadContextManifest(t)
	perspectiveSpecs := loadPerspectiveManifest(t)

	// C1a first: builds the per-image detail slice.
	c1aRow, ctxDetails := runCalibrateVisibleOnContext(t, contextSpecs)

	// C1b second: fills SampleGuess/SampleScore into the detail slice in-place.
	c1bRow := runCalibrateSampleOnContext(t, contextSpecs, ctxDetails)

	// Matrix: decoder → corpus. Order matches the table columns.
	rows := []decoderRow{
		// default → sick: baseline using unpixel.RecoverFile with best-config params,
		// included so the corpus table and this table are directly comparable.
		runDecoderOnSick(t, "default", sickEntries, false, func(ctx context.Context, img image.Image, e journalSickEntry) (string, error) {
			res, err := unpixel.RecoverFile(
				ctx, filepath.Join("testdata/sick", e.file()),
				unpixel.WithCharset(e.Charset),
				unpixel.WithBlockSize(e.BlockSize),
				unpixel.WithStyle(unpixel.Style{
					FontSize:    e.FontSize,
					Bold:        e.Bold,
					PaddingTop:  e.PaddingTop,
					PaddingLeft: e.PaddingLeft,
				}),
				unpixel.WithMaxLength(utf8.RuneCountInString(e.Text)+1),
				unpixel.WithWorkers(2),
			)
			return res.BestGuess, err
		}),

		// did → sick: Kopec-Chou trellis DP; capped (slow: O(charset×width) per image).
		runDecoderOnSick(t, "did", sickEntries, true, func(ctx context.Context, img image.Image, e journalSickEntry) (string, error) {
			res, err := mosaictext.DecodeDID(
				ctx, img,
				mosaictext.WithDIDCharset(e.Charset),
				mosaictext.WithDIDBlockSize(e.BlockSize),
				mosaictext.WithDIDFontSize(e.FontSize),
			)
			return res.Text, err
		}),

		// window-hmm → sick: sliding-window beam search.
		runDecoderOnSick(t, "window-hmm", sickEntries, false, func(ctx context.Context, img image.Image, e journalSickEntry) (string, error) {
			res, err := mosaictext.DecodeWindowHMM(
				ctx, img,
				mosaictext.WithWHMMCharset(e.Charset),
			)
			return res.Text, err
		}),

		// trained-hmm → sick: KMeans-trained HMM + Viterbi with language prior.
		// Capped: training is expensive (~20–40 s per image on the full charset).
		runDecoderOnSick(t, "trained-hmm", sickEntries, true, func(ctx context.Context, img image.Image, e journalSickEntry) (string, error) {
			res, err := mosaictext.DecodeTrainedHMM(
				ctx, img,
				mosaictext.WithTHMMCharset(e.Charset),
				mosaictext.WithTHMMLanguage(lang.English),
			)
			return res.Text, err
		}),

		// ref-match → sick: geometric per-cell reference match.
		runDecoderOnSick(t, "ref-match", sickEntries, false, func(ctx context.Context, img image.Image, e journalSickEntry) (string, error) {
			res, err := mosaictext.DecodeReference(
				ctx, img,
				mosaictext.WithRefCharset(e.Charset),
			)
			return res.Text, err
		}),

		// varfont → real: DecodeVarFont in blind mode (no axes supplied → immediate
		// error; result is honestly 0). Tracked so improvement shows when axes are added.
		runVarFontOnReal(t, realEntries),

		// blind → sick: blind.Recover zero-config.
		runBlindDecoderOnSick(t, sickEntries),

		// calibrate-visible → context (C1a): visible_rect crop from the same
		// PNG warm-starts axis fitting; redacted_rect is decoded blind.
		c1aRow,

		// calibrate-sample → context (C1b): a separate companion PNG provides
		// the calibration source; only fixtures with font_sample are included.
		c1bRow,

		// perspective → perspective: forward-model decode of a redaction
		// photographed at an angle, with manifest corners and with auto-detected
		// corners (rectify.DetectQuad, no corners supplied).
		runPerspectiveDecoder(t, perspectiveSpecs, false),
		runPerspectiveDecoder(t, perspectiveSpecs, true),
	}

	return rows, ctxDetails
}

// ─── per-decoder corpus runners ───────────────────────────────────────────────

// sickDecodeFunc is a per-image decode call for the sick corpus.
// img is pre-loaded; e is its manifest entry.
type sickDecodeFunc func(ctx context.Context, img image.Image, e journalSickEntry) (string, error)

// runDecoderOnSick runs fn over the sick corpus (or the first decoderSlowCap
// images when slow is true) and returns an aggregate decoderRow.
func runDecoderOnSick(t *testing.T, name string, entries []journalSickEntry, slow bool, fn sickDecodeFunc) decoderRow {
	t.Helper()

	subset := entries
	isCapped := slow && len(entries) > decoderSlowCap
	if isCapped {
		subset = entries[:decoderSlowCap]
	}

	start := time.Now()
	row := decoderRow{
		Decoder:   name,
		Corpus:    "sick",
		Subset:    isCapped,
		MeanScore: -1,
	}

	var scoreSum float64
	var scoreN int

	for _, e := range subset {
		img, err := loadDecoderImage(filepath.Join("testdata/sick", e.file()))
		if err != nil {
			t.Logf("decoder %s: load %s: %v (skipping)", name, e.Name, err)
			continue
		}
		row.Total++
		if e.Text != "" {
			row.Knowable++
		}

		ctx, cancel := context.WithTimeout(t.Context(), decoderPerTimeout)
		guess, decErr := fn(ctx, img, e)
		timedOut := ctx.Err() != nil
		cancel()

		switch {
		case timedOut && guess == "":
			t.Logf("decoder %s/%s: timeout (45 s)", name, e.Name)
		case decErr != nil && guess == "":
			t.Logf("decoder %s/%s: %v", name, e.Name, decErr)
		}

		if e.Text != "" {
			score := recoveryScore(guess, e.Text)
			scoreSum += score
			scoreN++
			if score >= 70 {
				row.Sensical++
			}
			if guess == e.Text {
				row.ExactOK++
			}
		}
		t.Logf("decoder %s/%-28s gt=%q guess=%q", name, e.Name, truncate(e.Text, 30), truncate(guess, 30))
	}

	if scoreN > 0 {
		row.MeanScore = scoreSum / float64(scoreN)
	}
	row.DurSec = time.Since(start).Seconds()
	return row
}

// runVarFontOnReal runs DecodeVarFont (blind mode) over the real corpus.
// No axes are supplied so it returns an error immediately — the result is
// honestly 0. Tracked so future axis-calibration improvements show up.
func runVarFontOnReal(t *testing.T, entries []journalRealEntry) decoderRow {
	t.Helper()

	start := time.Now()
	row := decoderRow{
		Decoder:   "varfont",
		Corpus:    "real",
		MeanScore: -1,
	}

	var scoreSum float64
	var scoreN int

	for _, e := range entries {
		img, err := loadDecoderImage(filepath.Join("testdata/real", e.File))
		if err != nil {
			t.Logf("decoder varfont: load %s: %v (skipping)", e.Name, err)
			continue
		}
		row.Total++
		gt := e.Text
		if gt != "" {
			row.Knowable++
		}

		ctx, cancel := context.WithTimeout(t.Context(), decoderPerTimeout)
		// Blind mode: no axes → DecodeVarFont returns an error immediately.
		res, decErr := mosaictext.DecodeVarFont(ctx, img)
		cancel()
		guess := res.Text

		if decErr != nil {
			t.Logf("decoder varfont/%s: %v (blind, no axes — expected)", e.Name, decErr)
		}

		if gt != "" {
			score := recoveryScore(guess, gt)
			scoreSum += score
			scoreN++
			if score >= 70 {
				row.Sensical++
			}
			if guess == gt {
				row.ExactOK++
			}
		}
	}

	if scoreN > 0 {
		row.MeanScore = scoreSum / float64(scoreN)
	}
	row.DurSec = time.Since(start).Seconds()
	return row
}

// runBlindDecoderOnSick runs blind.Recover zero-config over the sick corpus.
func runBlindDecoderOnSick(t *testing.T, entries []journalSickEntry) decoderRow {
	t.Helper()

	start := time.Now()
	row := decoderRow{
		Decoder:   "blind",
		Corpus:    "sick",
		MeanScore: -1,
	}

	var scoreSum float64
	var scoreN int

	for _, e := range entries {
		img, err := loadDecoderImage(filepath.Join("testdata/sick", e.file()))
		if err != nil {
			t.Logf("decoder blind: load %s: %v (skipping)", e.Name, err)
			continue
		}
		row.Total++
		if e.Text != "" {
			row.Knowable++
		}

		ctx, cancel := context.WithTimeout(t.Context(), decoderPerTimeout)
		res, decErr := blind.Recover(ctx, img)
		timedOut := ctx.Err() != nil
		cancel()

		// blind.Recover may return multi-line text; join lines for a flat comparison.
		guess := res.Text
		if len(res.Lines) > 1 {
			guess = strings.Join(res.Lines, " ")
		}

		switch {
		case timedOut && guess == "":
			t.Logf("decoder blind/%s: timeout (45 s)", e.Name)
		case decErr != nil && guess == "":
			t.Logf("decoder blind/%s: %v", e.Name, decErr)
		}

		if e.Text != "" {
			score := recoveryScore(guess, e.Text)
			scoreSum += score
			scoreN++
			if score >= 70 {
				row.Sensical++
			}
			if guess == e.Text {
				row.ExactOK++
			}
		}
		t.Logf("decoder blind/%-28s gt=%q guess=%q", e.Name, truncate(e.Text, 30), truncate(guess, 30))
	}

	if scoreN > 0 {
		row.MeanScore = scoreSum / float64(scoreN)
	}
	row.DurSec = time.Since(start).Seconds()
	return row
}

// ─── manifest loaders ─────────────────────────────────────────────────────────

// loadDecoderSickManifest reads testdata/sick/manifest.json and returns entries
// whose image file exists on disk.
func loadDecoderSickManifest(t *testing.T) []journalSickEntry {
	t.Helper()
	const dir = "testdata/sick"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("decoder matrix: sick manifest: %v (no sick rows)", err)
		return nil
	}
	var entries []journalSickEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("decoder matrix: sick manifest parse: %v (no sick rows)", err)
		return nil
	}
	valid := entries[:0]
	for _, e := range entries {
		if _, statErr := os.Stat(filepath.Join(dir, e.file())); statErr == nil {
			valid = append(valid, e)
		}
	}
	return valid
}

// loadDecoderRealManifest reads testdata/real/manifest.json and returns entries
// whose image file exists on disk.
func loadDecoderRealManifest(t *testing.T) []journalRealEntry {
	t.Helper()
	const dir = "testdata/real"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("decoder matrix: real manifest: %v (no real rows)", err)
		return nil
	}
	var entries []journalRealEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("decoder matrix: real manifest parse: %v (no real rows)", err)
		return nil
	}
	valid := entries[:0]
	for _, e := range entries {
		if _, statErr := os.Stat(filepath.Join(dir, e.File)); statErr == nil {
			valid = append(valid, e)
		}
	}
	return valid
}

// ─── image loader ─────────────────────────────────────────────────────────────

// loadDecoderImage opens a PNG image from path for decoder matrix runs.
// PNG is sufficient for the sick and real corpora (all .png files).
func loadDecoderImage(path string) (image.Image, error) {
	f, err := os.Open(path) // #nosec G304 -- test only, path from manifest
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}

// ─── markdown helpers ─────────────────────────────────────────────────────────

// buildDecoderEvolutionRows returns one markdown table row per decoderRow for
// the "## Évolution — décodeurs" table.
func buildDecoderEvolutionRows(run journalRun, drows []decoderRow) string {
	var sb strings.Builder
	for _, dr := range drows {
		meanStr := "NA"
		if dr.MeanScore >= 0 {
			meanStr = fmt.Sprintf("%.0f%%", dr.MeanScore)
		}
		subsetStr := ""
		if dr.Subset {
			subsetStr = fmt.Sprintf("first %d", decoderSlowCap)
		}
		fmt.Fprintf(
			&sb,
			"| %s | %s | %s | %s | %s | %d/%d/%d/%s | %.0f | %s |\n",
			run.Timestamp[:10],
			run.Version,
			run.Commit,
			dr.Decoder,
			dr.Corpus,
			dr.ExactOK, dr.Knowable, dr.Sensical, meanStr,
			dr.DurSec,
			subsetStr,
		)
	}
	return sb.String()
}
