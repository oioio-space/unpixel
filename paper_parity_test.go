//go:build journal

// TestPaperParity measures UnPixel's recovery quality against the benchmark
// numbers reported by Hill et al. (2016):
//
//   - SICK-corpus sentences (Table 4): ~92–96% character accuracy at matched
//     font/grid parameters.
//   - Check-number digit strings (§3.3): ~100% on digit-only text.
//
// The corpus is self-generated (render → mosaic via the faithful generator) so
// matched-parameter recovery is self-consistent. The test is purely
// observational (quality logged, never fails on score). Each fixture is run
// twice: once through the default engine (RecoverFile) and once through the
// kind-matched new decoder (DecodeTrainedHMM for digits, DecodeWindowHMM for
// SICK sentences). Two machine-readable PARITY lines are emitted so the gain
// from the specialised decoders is clearly visible.
//
// Drive it with:
//
//	mise run journal
//
// or directly:
//
//	scripts/gotest-caged.sh go test -tags journal -run TestPaperParity -v -timeout 60m .
package unpixel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/mosaictext"
)

// parityTimeoutBest is the per-fixture time budget for each decoder in the
// parity test. 60 s matches the journal's best-config budget and keeps the
// total test runtime bounded. Two passes are made per fixture (default engine
// + matched decoder), so total wall time is at most 2 × n × 60 s.
const parityTimeoutBest = 60 * time.Second

// parityResult holds the outcome of a single fixture × decoder combination.
type parityResult struct {
	name  string
	gt    string
	guess string
	score float64
}

// TestPaperParity runs best-config recovery over every fixture in
// testdata/sick/ and reports two summary lines:
//
//	PARITY default sick_mean=.. digit_mean=..
//	PARITY matched sick_mean=.. digit_mean=..   (paper sick~92-96 digit~100)
func TestPaperParity(t *testing.T) {
	const dir = "testdata/sick"
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Skipf("sick corpus not found (%v) — run `go generate ./...` first", err)
	}
	var entries []journalSickEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	// defSick/defDigit: default-engine results.
	// mchSick/mchDigit: kind-matched-decoder results.
	var defSick, defDigit, mchSick, mchDigit []parityResult

	for _, e := range entries {
		e := e
		t.Run(e.Name, func(t *testing.T) {
			imgPath := filepath.Join(dir, e.file())
			if _, statErr := os.Stat(imgPath); statErr != nil {
				t.Skipf("image missing: %v", statErr)
			}

			gtLen := utf8.RuneCountInString(e.Text)

			// ── pass 1: default engine ────────────────────────────────────────
			defScore, defGuess := func() (float64, string) {
				ctx, cancel := context.WithTimeout(t.Context(), parityTimeoutBest)
				defer cancel()
				res, recErr := unpixel.RecoverFile(
					ctx, imgPath,
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
				if recErr != nil && res.BestGuess == "" {
					t.Logf("%s default: error: %v", e.Name, recErr)
				}
				return recoveryScore(res.BestGuess, e.Text), res.BestGuess
			}()
			t.Logf("%s [%s] default: gt=%q guess=%q score=%.0f%%",
				e.Name, e.Kind, e.Text, defGuess, defScore)

			// ── pass 2: kind-matched decoder ──────────────────────────────────
			mchScore, mchGuess := parityRunMatched(t, e, imgPath)
			t.Logf("%s [%s] matched: gt=%q guess=%q score=%.0f%%",
				e.Name, e.Kind, e.Text, mchGuess, mchScore)

			defR := parityResult{name: e.Name, gt: e.Text, guess: defGuess, score: defScore}
			mchR := parityResult{name: e.Name, gt: e.Text, guess: mchGuess, score: mchScore}
			switch e.Kind {
			case "digits":
				defDigit = append(defDigit, defR)
				mchDigit = append(mchDigit, mchR)
			default: // "sick"
				defSick = append(defSick, defR)
				mchSick = append(mchSick, mchR)
			}
		})
	}

	// ── aggregate ─────────────────────────────────────────────────────────────

	mean := func(rs []parityResult) float64 {
		if len(rs) == 0 {
			return -1
		}
		var sum float64
		for _, r := range rs {
			sum += r.score
		}
		return sum / float64(len(rs))
	}

	defSickMean := mean(defSick)
	defDigitMean := mean(defDigit)
	mchSickMean := mean(mchSick)
	mchDigitMean := mean(mchDigit)

	t.Logf("─── paper-parity summary ────────────────────────────────────────")
	t.Logf("default  SICK sentences : n=%d  mean=%.1f%%  (paper ~92–96%%)", len(defSick), defSickMean)
	t.Logf("default  Digit strings  : n=%d  mean=%.1f%%  (paper ~100%%)", len(defDigit), defDigitMean)
	t.Logf("matched  SICK sentences : n=%d  mean=%.1f%%  (paper ~92–96%%)", len(mchSick), mchSickMean)
	t.Logf("matched  Digit strings  : n=%d  mean=%.1f%%  (paper ~100%%)", len(mchDigit), mchDigitMean)

	t.Logf("── default SICK breakdown ──")
	for _, r := range defSick {
		t.Logf("  %-28s  score=%5.1f%%  gt=%q  guess=%q", r.name, r.score, r.gt, r.guess)
	}
	t.Logf("── default digits breakdown ──")
	for _, r := range defDigit {
		t.Logf("  %-28s  score=%5.1f%%  gt=%q  guess=%q", r.name, r.score, r.gt, r.guess)
	}
	t.Logf("── matched SICK breakdown ──")
	for _, r := range mchSick {
		t.Logf("  %-28s  score=%5.1f%%  gt=%q  guess=%q", r.name, r.score, r.gt, r.guess)
	}
	t.Logf("── matched digits breakdown ──")
	for _, r := range mchDigit {
		t.Logf("  %-28s  score=%5.1f%%  gt=%q  guess=%q", r.name, r.score, r.gt, r.guess)
	}

	// Machine-readable parity lines for grepping in CI output. Purely
	// observational — no assertion ever fails here.
	fmt.Printf("PARITY default sick_mean=%.1f digit_mean=%.1f\n", defSickMean, defDigitMean)
	fmt.Printf("PARITY matched sick_mean=%.1f digit_mean=%.1f   (paper sick~92-96 digit~100)\n",
		mchSickMean, mchDigitMean)
}

// parityRunMatched runs the kind-matched decoder for the given fixture and
// returns (score, guess). On error or timeout, score is 0 and the test
// continues — this function never calls t.Fatal.
func parityRunMatched(t *testing.T, e journalSickEntry, imgPath string) (float64, string) {
	t.Helper()

	f, err := os.Open(imgPath)
	if err != nil {
		t.Logf("%s matched: open image: %v — score 0", e.Name, err)
		return 0, ""
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		t.Logf("%s matched: decode image: %v — score 0", e.Name, err)
		return 0, ""
	}

	ctx, cancel := context.WithTimeout(t.Context(), parityTimeoutBest)
	defer cancel()

	var guess string
	switch e.Kind {
	case "digits":
		res, decErr := mosaictext.DecodeTrainedHMM(ctx, img,
			mosaictext.WithTHMMCharset(e.Charset),
			mosaictext.WithTHMMFont(e.Font),
		)
		if decErr != nil {
			t.Logf("%s matched(trained-hmm): %v — score 0", e.Name, decErr)
			return 0, ""
		}
		guess = res.Text
	default: // "sick"
		res, decErr := mosaictext.DecodeWindowHMM(ctx, img,
			mosaictext.WithWHMMCharset(e.Charset),
			mosaictext.WithWHMMFont(e.Font),
		)
		if decErr != nil {
			t.Logf("%s matched(window-hmm): %v — score 0", e.Name, decErr)
			return 0, ""
		}
		guess = res.Text
	}

	return recoveryScore(guess, e.Text), guess
}
