//go:build wild

// In-the-wild fixture harness: measures UnPixel's decode quality against a set
// of real-world pixelated and blurred images downloaded from the internet.
//
// Unlike the default test suite (a pass/fail correctness gate) or the panel
// (committed fixtures whose ground truth is known), this harness operates on
// ephemeral images that are gitignored and must be fetched first:
//
//	scripts/fetch-wild-fixtures.sh
//	go test -tags wild -v -timeout 10m .
//
// Individual entries are skipped via t.Skip when their file is absent so the
// harness degrades gracefully — the caller need not fetch every image.
// The test never fails on decode quality; it is a measurement record, not a gate.
package unpixel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoding for .jpg fixtures
	_ "image/png"  // register PNG decoding
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire default components
	"github.com/oioio-space/unpixel/mosaictext"
)

// wildEntry mirrors one entry in testdata/wild/manifest.json.
type wildEntry struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	GTFile      string `json:"gt_file"`
	URL         string `json:"url"`
	GTUrl       string `json:"gt_url"`
	Kind        string `json:"kind"` // "mosaic" or "blur"
	GroundTruth string `json:"ground_truth"`
	FontHint    string `json:"font_hint"`
	Notes       string `json:"notes"`
}

// wildResult is the per-entry measurement recorded by the harness.
type wildResult struct {
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	BestGuess      string  `json:"best_guess"`
	GroundTruth    string  `json:"ground_truth,omitzero"`
	ExactMatch     bool    `json:"exact_match,omitzero"`
	BestTotal      float64 `json:"best_total"`
	Confidence     float64 `json:"confidence"`
	BelowThreshold bool    `json:"below_threshold"`
	ElapsedMs      float64 `json:"elapsed_ms"`
	// HMMGuess and HMMExact are populated for kind=="mosaic" entries only.
	// HMMGuess is the text returned by mosaictext.DecodeHMM (empty on error).
	// HMMExact is true when HMMGuess matches GroundTruth case-insensitively.
	HMMGuess string `json:"hmm_guess,omitzero"`
	HMMExact bool   `json:"hmm_exact,omitzero"`
}

const (
	wildManifest = "testdata/wild/manifest.json"
	wildDir      = "testdata/wild"

	// wildMaxLength is a conservative bound — real-world images may contain long
	// text but we want the harness to finish in a bounded time.
	wildMaxLength = 30
	// wildTimeout is per-entry; the test-level -timeout flag governs the total.
	wildTimeout = 90 * time.Second
)

// wildCharset is a broad printable-ASCII set suitable for unknown real-world text.
var wildCharset = unpixel.CharsetAlnum + "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// TestWild runs the wild-fixture harness over all manifest entries that are
// locally present. It skips missing files and records a measurement table in
// the test log. It never fails on decode quality — it is purely observational.
func TestWild(t *testing.T) {
	entries := loadWildManifest(t)

	results := make([]wildResult, 0, len(entries))

	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			imgPath := filepath.Join(wildDir, e.File)
			if _, err := os.Stat(imgPath); err != nil {
				t.Skipf("fixture not present (%s); run scripts/fetch-wild-fixtures.sh", e.File)
			}

			wr := measureEntry(t, e, imgPath)

			// For mosaic entries, also run the HMM decoder and record its output.
			// This is purely observational — never fatal on decode quality.
			if e.Kind == "mosaic" {
				hmmGuess, hmmFont, hmmDist := measureHMM(t, imgPath)
				wr.HMMGuess = hmmGuess
				if e.GroundTruth != "" && hmmGuess != "" {
					wr.HMMExact = strings.EqualFold(hmmGuess, e.GroundTruth)
				}
				t.Logf("hmm_guess=%q  hmm_exact=%v  hmm_font=%s  hmm_dist=%.2f",
					hmmGuess, wr.HMMExact, hmmFont, hmmDist)
			}

			results = append(results, wr)

			t.Logf("kind=%-6s  best_guess=%q  total=%.4f  conf=%.4f  below_thresh=%v  elapsed=%.0fms",
				e.Kind, wr.BestGuess, wr.BestTotal, wr.Confidence, wr.BelowThreshold, wr.ElapsedMs)
			if e.GroundTruth != "" {
				t.Logf("  ground_truth=%q  exact=%v", e.GroundTruth, wr.ExactMatch)
			}
		})
	}

	if len(results) > 0 {
		t.Logf("\n%s", formatWildTable(results))
	}
}

// measureEntry runs the appropriate recovery for one manifest entry and returns
// the measurement. It uses a per-entry context with wildTimeout so a slow image
// does not starve the rest of the run.
func measureEntry(t *testing.T, e wildEntry, imgPath string) wildResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), wildTimeout)
	defer cancel()

	opts := []unpixel.Option{
		unpixel.WithCharset(wildCharset),
		unpixel.WithMaxLength(wildMaxLength),
		unpixel.WithWorkers(2), // leave headroom for other entries
	}

	// If ground truth is known, tighten MaxLength to just above it so the search
	// is cheaper — this is still a meaningful measurement.
	if gt := e.GroundTruth; gt != "" {
		n := utf8.RuneCountInString(gt)
		if n+4 < wildMaxLength {
			opts = append(opts, unpixel.WithMaxLength(n+4))
		}
	}

	start := time.Now()

	var (
		res unpixel.Result
		err error
	)
	switch e.Kind {
	case "blur":
		res, err = recoverWildFile(ctx, imgPath, true, opts)
	default: // "mosaic" and anything unrecognised
		res, err = recoverWildFile(ctx, imgPath, false, opts)
	}

	elapsed := time.Since(start)

	if err != nil {
		t.Logf("recover error (non-fatal): %v", err)
	}

	wr := wildResult{
		Name:           e.Name,
		Kind:           e.Kind,
		BestGuess:      res.BestGuess,
		GroundTruth:    e.GroundTruth,
		BestTotal:      res.BestTotal,
		Confidence:     res.Confidence,
		BelowThreshold: res.BelowThreshold,
		ElapsedMs:      float64(elapsed.Milliseconds()),
	}
	if e.GroundTruth != "" {
		wr.ExactMatch = strings.EqualFold(res.BestGuess, e.GroundTruth)
	}
	return wr
}

// recoverWildFile opens imgPath (PNG or JPEG) and runs either Recover or
// RecoverBlurred depending on blur.
func recoverWildFile(ctx context.Context, path string, blur bool, opts []unpixel.Option) (unpixel.Result, error) {
	f, err := os.Open(path) // #nosec G304 -- test harness: path comes from manifest
	if err != nil {
		return unpixel.Result{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if blur {
		return unpixel.RecoverBlurredReader(ctx, f, opts...)
	}
	return unpixel.RecoverReader(ctx, f, opts...)
}

// measureHMM opens imgPath and runs mosaictext.DecodeHMM bounded by wildTimeout.
// It returns the decoded text, winning font name, and distance. All errors are
// logged (non-fatal) and result in empty text — the caller must not t.Fatal.
func measureHMM(t *testing.T, imgPath string) (text, fontName string, dist float64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wildTimeout)
	defer cancel()

	f, err := os.Open(imgPath) // #nosec G304 -- test harness: path comes from manifest
	if err != nil {
		t.Logf("hmm: open %s: %v", imgPath, err)
		return "", "", 0
	}
	defer func() { _ = f.Close() }()

	img, _, err := image.Decode(f)
	if err != nil {
		t.Logf("hmm: decode image %s: %v", imgPath, err)
		return "", "", 0
	}

	res, err := mosaictext.DecodeHMM(
		ctx, img,
		mosaictext.WithCharset(mosaictext.DefaultHMMCharset),
	)
	if err != nil {
		t.Logf("hmm: DecodeHMM %s: %v", imgPath, err)
		return "", "", 0
	}
	return res.Text, res.Font, res.Distance
}

// loadWildManifest reads and parses testdata/wild/manifest.json, failing the
// test if the file cannot be parsed.
func loadWildManifest(t *testing.T) []wildEntry {
	t.Helper()
	data, err := os.ReadFile(wildManifest)
	if err != nil {
		t.Fatalf("read manifest %s: %v", wildManifest, err)
	}
	var entries []wildEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parse manifest %s: %v", wildManifest, err)
	}
	return entries
}

// formatWildTable renders results as a readable ASCII table for the test log.
// Mosaic entries include an additional hmm_guess/hmm_exact column pair.
func formatWildTable(results []wildResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-4s  %-6s  %-28s  %-28s  %6s  %6s  %5s  %7s  %-28s  %8s\n",
		"name", "kind", "best_guess", "ground_truth", "total", "conf", "below", "ms", "hmm_guess", "hmm_exact")
	fmt.Fprintf(&sb, "%s\n", strings.Repeat("-", 140))
	for _, r := range results {
		gt := r.GroundTruth
		if gt == "" {
			gt = "(unknown)"
		}
		guess := r.BestGuess
		if guess == "" {
			guess = "(none)"
		}
		hmmGuess := r.HMMGuess
		if hmmGuess == "" && r.Kind == "mosaic" {
			hmmGuess = "(none)"
		}
		fmt.Fprintf(&sb, "%-4s  %-6s  %-28s  %-28s  %6.4f  %6.4f  %5v  %7.0f  %-28s  %8v\n",
			r.Name, r.Kind, guess, gt, r.BestTotal, r.Confidence, r.BelowThreshold, r.ElapsedMs,
			hmmGuess, r.HMMExact)
	}
	return sb.String()
}
