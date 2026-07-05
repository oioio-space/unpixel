//go:build verifymeasure

// TestVerifySpike is an observational (never-fails) diagnostic that spikes the
// propose/verify loop on the sick and context corpora — the decisive experiment
// the journal flagged: "does the semantic prior decolle the 0 exact on sick/context".
//
// For each image it builds a candidate set (ground truth + ~6–12 physically-hard
// decoys: confusable-glyph swaps, length variants, word substitutions) and calls
// unpixel.Verify with the image's calibrated config. It records:
//
//   - truth's Distance + Match
//   - truth's rank among candidates by distance (1 = best)
//   - best decoy's distance and the margin (bestDecoy − truth)
//   - win = truth is rank 1 AND Match=true
//
// Outputs:
//
//	benchmarks/verify/run-<stamp>.json   machine-readable
//	docs/VERIFY-SPIKE.md                 human-readable (overwritten snapshot)
//
// Run with:
//
//	mise run verifymeasure
package unpixel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	unpixel "github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults" // wire DefaultComponents (side-effect) + named use
	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// ── manifest structs ──────────────────────────────────────────────────────────

// sickEntry mirrors the fields in testdata/sick/manifest.json.
type sickEntry struct {
	Name        string `json:"name"`
	Text        string `json:"text"`
	Charset     string `json:"charset"`
	FontSize    int    `json:"font_size"`
	Bold        bool   `json:"bold"`
	BlockSize   int    `json:"block_size"`
	PaddingTop  int    `json:"padding_top"`
	PaddingLeft int    `json:"padding_left"`
	Font        string `json:"font"`
	Kind        string `json:"kind"`
}

// ctxRect is a rectangle in the context manifest.
type ctxRect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// contextEntry mirrors the relevant fields in testdata/context/manifest.json.
type contextEntry struct {
	Name         string  `json:"name"`
	Secret       string  `json:"secret"`
	RedactedRect ctxRect `json:"redacted_rect"`
	Font         string  `json:"font"`
	FontSize     int     `json:"font_size"`
	BlockSize    int     `json:"block_size"`
	Linear       bool    `json:"linear"`
}

// ── JSON run record ───────────────────────────────────────────────────────────

// verifyRecord holds the per-image result for one run.
type verifyRecord struct {
	Corpus        string  `json:"corpus"`
	Image         string  `json:"image"`
	Truth         string  `json:"truth"`
	TruthDist     float64 `json:"truth_dist"`
	TruthMatch    bool    `json:"truth_match"`
	TruthRank     int     `json:"truth_rank"`
	BestDecoyDist float64 `json:"best_decoy_dist"`
	Margin        float64 `json:"margin"` // bestDecoy − truth (positive = truth wins)
	Win           bool    `json:"win"`    // truth rank 1 AND Match=true
	Note          string  `json:"note,omitzero"`
}

// verifyRun is the top-level JSON record written to benchmarks/verify/.
type verifyRun struct {
	Timestamp string         `json:"timestamp"`
	Results   []verifyRecord `json:"results"`
}

// ── test entry point ──────────────────────────────────────────────────────────

// TestVerifySpike measures whether propose/verify discriminates the truth from
// plausible decoys on the sick and context corpora. It never fails — it is
// purely observational.
func TestVerifySpike(t *testing.T) {
	ctx := t.Context()
	stamp := time.Now().UTC().Format("20060102T150405")

	allFonts := fonts.All()

	var results []verifyRecord
	results = append(results, runSickVerify(t, ctx, allFonts)...)
	results = append(results, runContextVerify(t, ctx, allFonts)...)

	writeVerifyJSON(t, stamp, results)
	writeVerifyMarkdown(t, stamp, results)
}

// ── corpus runners ────────────────────────────────────────────────────────────

func runSickVerify(t *testing.T, ctx context.Context, allFonts []fonts.Font) []verifyRecord {
	t.Helper()
	const (
		manifestPath = "testdata/sick/manifest.json"
		dir          = "testdata/sick"
	)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	var entries []sickEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parse %s: %v", manifestPath, err)
	}

	// Header.
	t.Logf("%-30s  %-38s  %6s  %5s  %4s  %6s  %6s  %4s  %s",
		"image", "truth", "tDist", "Match", "rank", "dDist", "margin", "win", "note")

	results := make([]verifyRecord, 0, len(entries))
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.Name+".png")
		img := loadImageFile(t, imgPath)
		if img == nil {
			continue
		}

		// Build options: explicit block size + style; renderer from named font.
		opts := buildSickOpts(t, allFonts, e)

		// Build candidates: truth + hard decoys.
		candidates := buildCandidates(e.Text, e.Kind)

		r := measureVerify(t, ctx, img, "sick", e.Name, e.Text, candidates, opts)
		t.Logf("%-30s  %-38s  %6.4f  %5v  %4d  %6.4f  %6.4f  %4v  %s",
			r.Image, r.Truth, r.TruthDist, r.TruthMatch, r.TruthRank,
			r.BestDecoyDist, r.Margin, r.Win, r.Note)
		results = append(results, r)
	}
	return results
}

func runContextVerify(t *testing.T, ctx context.Context, allFonts []fonts.Font) []verifyRecord {
	t.Helper()
	const (
		manifestPath = "testdata/context/manifest.json"
		dir          = "testdata/context"
	)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	var entries []contextEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parse %s: %v", manifestPath, err)
	}

	// Header.
	t.Logf("%-30s  %-16s  %6s  %5s  %4s  %6s  %6s  %4s  %s",
		"image", "truth", "tDist", "Match", "rank", "dDist", "margin", "win", "note")

	results := make([]verifyRecord, 0, len(entries))
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.Name+".png")
		img := loadImageFile(t, imgPath)
		if img == nil {
			continue
		}

		// Crop to the redacted region.
		rgba := imutil.ToRGBA(img)
		r := e.RedactedRect
		cropped := imutil.Crop(rgba, r.X, r.Y, r.W, r.H)

		// Build options: block size + style + pixelator (linear) + renderer.
		opts, note := buildContextOpts(t, allFonts, e)

		// Build candidates: truth (secret) + hard decoys.
		candidates := buildCandidates(e.Secret, "context")

		rec := measureVerify(t, ctx, cropped, "context", e.Name, e.Secret, candidates, opts)
		if note != "" {
			rec.Note = note
		}
		t.Logf("%-30s  %-16s  %6.4f  %5v  %4d  %6.4f  %6.4f  %4v  %s",
			rec.Image, rec.Truth, rec.TruthDist, rec.TruthMatch, rec.TruthRank,
			rec.BestDecoyDist, rec.Margin, rec.Win, rec.Note)
		results = append(results, rec)
	}
	return results
}

// ── option builders ───────────────────────────────────────────────────────────

// buildSickOpts constructs Verify options for a sick-corpus entry.
func buildSickOpts(t *testing.T, allFonts []fonts.Font, e sickEntry) []unpixel.Option {
	t.Helper()
	blockSize := e.BlockSize
	if blockSize <= 0 {
		blockSize = unpixel.DefaultBlockSize
	}
	fontSize := float64(e.FontSize)
	if fontSize <= 0 {
		fontSize = 32
	}
	paddingTop := e.PaddingTop
	if paddingTop <= 0 {
		paddingTop = 8
	}
	paddingLeft := e.PaddingLeft
	if paddingLeft <= 0 {
		paddingLeft = 8
	}
	opts := []unpixel.Option{
		unpixel.WithBlockSize(blockSize),
		unpixel.WithStyle(unpixel.Style{
			FontSize:    fontSize,
			Bold:        e.Bold,
			PaddingTop:  paddingTop,
			PaddingLeft: paddingLeft,
		}),
		// Sick images use sRGB block-average (linear=false not in manifest).
		unpixel.WithPixelator(defaults.BlockAverage(blockSize)),
	}
	if r := rendererForFont(t, allFonts, e.Font); r != nil {
		opts = append(opts, unpixel.WithRenderer(r))
	}
	return opts
}

// buildContextOpts constructs Verify options for a context-corpus entry. It
// returns an optional note (e.g. when the named font is not bundled and a
// fallback is used).
func buildContextOpts(t *testing.T, allFonts []fonts.Font, e contextEntry) ([]unpixel.Option, string) {
	t.Helper()
	blockSize := e.BlockSize
	if blockSize <= 0 {
		blockSize = unpixel.DefaultBlockSize
	}
	fontSize := float64(e.FontSize)
	if fontSize <= 0 {
		fontSize = 32
	}

	var px unpixel.Pixelator
	if e.Linear {
		px = defaults.LinearBlockAverage(blockSize)
	} else {
		px = defaults.BlockAverage(blockSize)
	}

	opts := []unpixel.Option{
		unpixel.WithBlockSize(blockSize),
		unpixel.WithStyle(unpixel.Style{
			FontSize:    fontSize,
			PaddingTop:  8,
			PaddingLeft: 8,
		}),
		unpixel.WithPixelator(px),
	}

	note := ""
	fontName := e.Font
	r := rendererForFont(t, allFonts, fontName)
	if r == nil {
		// Font not bundled (e.g. Nunito). Fall back to Liberation Sans.
		r = rendererForFont(t, allFonts, "Liberation Sans")
		note = fmt.Sprintf("font %q not bundled → Liberation Sans fallback", fontName)
	}
	if r != nil {
		opts = append(opts, unpixel.WithRenderer(r))
	}
	return opts, note
}

// rendererForFont finds the named font in allFonts and builds a renderer from
// its bytes. It returns nil when the font is not found.
func rendererForFont(t *testing.T, allFonts []fonts.Font, name string) unpixel.Renderer {
	t.Helper()
	for _, f := range allFonts {
		if f.Name == name {
			r, err := defaults.RendererFromFonts(f.Data, nil)
			if err != nil {
				t.Logf("build renderer for %q: %v", name, err)
				return nil
			}
			return r
		}
	}
	return nil
}

// ── decoy generation ──────────────────────────────────────────────────────────

// confusableTable maps characters (or bigrams) to their visually similar
// alternatives under block-8 mosaic pixelation.
var confusableTable = [][2]string{
	{"e", "c"},
	{"c", "e"},
	{"o", "e"},
	{"e", "o"},
	{"l", "I"},
	{"I", "l"},
	{"a", "o"},
	{"o", "a"},
	{"u", "n"},
	{"n", "u"},
	{"d", "b"},
	{"b", "d"},
	{"p", "q"},
	{"0", "o"},
	{"o", "0"},
	{"1", "l"},
	{"l", "1"},
	{"i", "l"},
	{"rn", "m"},
	{"m", "rn"},
	{"W", "N"},
	{"N", "W"},
}

// wordSwaps provides plausible substitutions for common words in the sick corpus.
var wordSwaps = map[string]string{
	"dogs":       "cats",
	"cats":       "dogs",
	"boys":       "girls",
	"girls":      "boys",
	"man":        "woman",
	"woman":      "man",
	"children":   "students",
	"playing":    "singing",
	"singing":    "playing",
	"wrestling":  "running",
	"hugging":    "laughing",
	"outdoors":   "outside",
	"water":      "road",
	"safety":     "rules",
	"guitar":     "piano",
	"snow":       "rain",
	"song":       "tune",
	"young":      "small",
	"nobody":     "someone",
	"practicing": "learning",
}

// buildCandidates returns the truth followed by a set of hard decoys.
// kind distinguishes "sick" (sentence), "digits", and "context" (short secret).
func buildCandidates(truth, kind string) []string {
	seen := map[string]bool{truth: true}
	add := func(candidates *[]string, s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			*candidates = append(*candidates, s)
		}
	}

	decoys := make([]string, 0, 12)

	switch kind {
	case "sick":
		// (a) confusable-glyph swaps.
		for _, pair := range confusableTable {
			from, to := pair[0], pair[1]
			if swapped := strings.Replace(truth, from, to, 1); swapped != truth {
				add(&decoys, swapped)
			}
			if swapped := replaceNth(truth, from, to, 2); swapped != truth {
				add(&decoys, swapped)
			}
		}
		// (b) length variants: drop last word, add a word.
		words := strings.Fields(truth)
		if len(words) > 2 {
			add(&decoys, strings.Join(words[:len(words)-1], " "))
		}
		add(&decoys, truth+" now")
		// (c) word swaps.
		for from, to := range wordSwaps {
			if swapped := strings.Replace(truth, from, to, 1); swapped != truth {
				add(&decoys, swapped)
			}
		}

	case "digits":
		// Rotate, reverse, swap pairs, add/remove digit.
		runes := []rune(truth)
		// Rotate left.
		if len(runes) > 1 {
			add(&decoys, string(append(runes[1:], runes[0])))
		}
		// Rotate right.
		if len(runes) > 1 {
			add(&decoys, string(append(runes[len(runes)-1:], runes[:len(runes)-1]...)))
		}
		// Swap adjacent pairs at position 0 and 1.
		if len(runes) >= 2 {
			swapped := slices.Clone(runes)
			swapped[0], swapped[1] = swapped[1], swapped[0]
			add(&decoys, string(swapped))
		}
		if len(runes) >= 4 {
			swapped := slices.Clone(runes)
			swapped[2], swapped[3] = swapped[3], swapped[2]
			add(&decoys, string(swapped))
		}
		// Replace each digit with its visual neighbour.
		for i, r := range runes {
			for _, sub := range digitNeighbours(r) {
				cp := slices.Clone(runes)
				cp[i] = sub
				add(&decoys, string(cp))
			}
		}
		// Length variants.
		if len(runes) > 1 {
			add(&decoys, string(runes[:len(runes)-1]))
		}
		add(&decoys, truth+"0")

	default: // "context" — short alphanumeric secret
		// (a) confusable-glyph swaps.
		for _, pair := range confusableTable {
			from, to := pair[0], pair[1]
			if swapped := strings.Replace(truth, from, to, 1); swapped != truth {
				add(&decoys, swapped)
			}
		}
		// Replace each character with a visually close alternative.
		runes := []rune(truth)
		for i, r := range runes {
			for _, sub := range charNeighbours(r) {
				cp := slices.Clone(runes)
				cp[i] = sub
				add(&decoys, string(cp))
			}
		}
		// (b) length variants.
		if len(runes) > 1 {
			add(&decoys, string(runes[:len(runes)-1]))
		}
		add(&decoys, truth+"x")
		// (c) case flips at first char.
		if len(runes) > 0 {
			cp := slices.Clone(runes)
			if cp[0] >= 'a' && cp[0] <= 'z' {
				cp[0] = cp[0] - 32
				add(&decoys, string(cp))
			} else if cp[0] >= 'A' && cp[0] <= 'Z' {
				cp[0] = cp[0] + 32
				add(&decoys, string(cp))
			}
		}
	}

	// Cap decoy set: keep at most 11 decoys → total ≤ 12 candidates.
	if len(decoys) > 11 {
		decoys = decoys[:11]
	}

	return append([]string{truth}, decoys...)
}

// replaceNth replaces the nth occurrence (1-based) of old with new in s.
func replaceNth(s, old, new string, n int) string {
	count := 0
	idx := 0
	for {
		pos := strings.Index(s[idx:], old)
		if pos < 0 {
			return s
		}
		count++
		absPos := idx + pos
		if count == n {
			return s[:absPos] + new + s[absPos+len(old):]
		}
		idx = absPos + len(old)
	}
}

// digitNeighbours returns digit runes that look similar to r under mosaic.
func digitNeighbours(r rune) []rune {
	// Under block-8 pixelation, digits that share similar gross shape:
	// 0↔8, 1↔7, 3↔8, 4↔9, 6↔8.
	neighbours := map[rune][]rune{
		'0': {'8', 'o'},
		'1': {'7', 'l'},
		'2': {'3'},
		'3': {'8', '2'},
		'4': {'9'},
		'5': {'6'},
		'6': {'8', '5'},
		'7': {'1'},
		'8': {'0', '3'},
		'9': {'4'},
	}
	return neighbours[r]
}

// charNeighbours returns characters visually similar to r under mosaic.
func charNeighbours(r rune) []rune {
	neighbours := map[rune][]rune{
		'e': {'c', 'o'},
		'c': {'e', 'o'},
		'o': {'e', 'c', '0'},
		'0': {'o', '8'},
		'l': {'1', 'I', 'i'},
		'1': {'l', 'I'},
		'i': {'l', '1'},
		'I': {'l', '1'},
		'a': {'o', 'e'},
		'u': {'n', 'v'},
		'n': {'u', 'm'},
		'd': {'b', 'c'},
		'b': {'d'},
		'p': {'q'},
		'q': {'p', 'g'},
		'g': {'q'},
		'r': {'n'},
		'4': {'9'},
		'9': {'4'},
		'!': {'1'},
		'P': {'R', 'F'},
		'R': {'P'},
		'G': {'C', 'O'},
		'S': {'5'},
		'5': {'S'},
		'T': {'7'},
		'7': {'T', '1'},
		'A': {'4'},
		'w': {'v', 'u'},
		's': {'5'},
	}
	return neighbours[r]
}

// ── verify measurement ────────────────────────────────────────────────────────

// measureVerify calls unpixel.Verify on img with the given candidates and opts
// and returns a populated verifyRecord. It is purely observational.
func measureVerify(t *testing.T, ctx context.Context, img image.Image, corpus, name, truth string, candidates []string, opts []unpixel.Option) verifyRecord {
	t.Helper()
	rec := verifyRecord{
		Corpus: corpus,
		Image:  name,
		Truth:  truth,
	}

	verdicts, err := unpixel.Verify(ctx, img, candidates, opts...)
	if err != nil {
		t.Logf("Verify %s/%s: %v", corpus, name, err)
		rec.Note = "Verify error: " + err.Error()
		return rec
	}
	if len(verdicts) == 0 {
		rec.Note = "no verdicts returned"
		return rec
	}

	// Find the truth verdict and the best decoy verdict.
	var truthVerdict *unpixel.Verdict
	bestDecoyDist := 1.0
	for i := range verdicts {
		v := &verdicts[i]
		if v.Text == truth {
			truthVerdict = v
		} else if v.Distance < bestDecoyDist {
			bestDecoyDist = v.Distance
		}
	}
	if truthVerdict == nil {
		rec.Note = "truth not in verdicts"
		return rec
	}

	rec.TruthDist = truthVerdict.Distance
	rec.TruthMatch = truthVerdict.Match
	rec.BestDecoyDist = bestDecoyDist
	rec.Margin = bestDecoyDist - truthVerdict.Distance

	// Rank: count how many verdicts have a strictly lower distance than truth.
	rank := 1
	for _, v := range verdicts {
		if v.Text != truth && v.Distance < truthVerdict.Distance {
			rank++
		}
	}
	rec.TruthRank = rank
	rec.Win = rank == 1 && truthVerdict.Match

	return rec
}

// ── image loader ──────────────────────────────────────────────────────────────

// loadImageFile opens and decodes the image at path. Returns nil (logging a
// skip) on any error so that a single missing fixture does not abort the run.
func loadImageFile(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test reads controlled fixture path
	if err != nil {
		t.Logf("SKIP %s: open: %v", path, err)
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Logf("SKIP %s: decode: %v", path, err)
		return nil
	}
	return img
}

// ── output writers ────────────────────────────────────────────────────────────

func writeVerifyJSON(t *testing.T, stamp string, results []verifyRecord) {
	t.Helper()
	outDir := filepath.Join("benchmarks", "verify")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	run := verifyRun{Timestamp: stamp, Results: results}
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

func writeVerifyMarkdown(t *testing.T, stamp string, results []verifyRecord) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(verifyMarkdownHeader)
	buildVerifyMarkdown(&sb, stamp, results)

	path := filepath.Join("docs", "VERIFY-SPIKE.md")
	// #nosec G304 -- writing to a fixed, source-controlled docs path
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("Markdown written → %s", path)
}

// verifyMarkdownHeader is the fixed preamble of the regenerated snapshot. The
// harness OVERWRITES docs/VERIFY-SPIKE.md each run (it is a reproducible
// diagnostic snapshot, not an append-only journal).
const verifyMarkdownHeader = `# Verify spike (P1)

Regenerated by ` + "`mise run verifymeasure`" + ` (harness ` + "`verifymeasure_test.go`" + `, build tag
` + "`verifymeasure`" + `). For each sick and context image it builds a candidate set
(ground truth + ~6–12 hard decoys: confusable-glyph swaps, length variants, word
substitutions) and calls ` + "`unpixel.Verify`" + ` with the image's calibrated config.
A **win** = truth is rank 1 AND Match=true. Machine-readable counterpart:
` + "`benchmarks/verify/run-*.json`" + `.
`

// buildVerifyMarkdown writes one run section into sb.
func buildVerifyMarkdown(sb *strings.Builder, stamp string, results []verifyRecord) {
	fmt.Fprintf(sb, "\n## Verify spike run %s\n\n", stamp)

	for _, corpus := range []string{"sick", "context"} {
		rows := slices.DeleteFunc(slices.Clone(results), func(r verifyRecord) bool {
			return r.Corpus != corpus
		})
		if len(rows) == 0 {
			continue
		}
		wins := 0
		for _, r := range rows {
			if r.Win {
				wins++
			}
		}
		fmt.Fprintf(sb, "### %s corpus\n\n", corpus)
		fmt.Fprintf(sb, "Win rate: **%d / %d** (truth rank-1 AND Match=true)\n\n", wins, len(rows))
		fmt.Fprintln(sb, "| image | truth | truthDist | Match | rank | bestDecoyDist | margin | win | note |")
		fmt.Fprintln(sb, "|---|---|---|---|---|---|---|---|---|")
		for _, r := range rows {
			fmt.Fprintf(sb,
				"| %s | `%s` | %.4f | %v | %d | %.4f | %.4f | **%v** | %s |\n",
				r.Image, r.Truth, r.TruthDist, r.TruthMatch, r.TruthRank,
				r.BestDecoyDist, r.Margin, r.Win, r.Note)
		}
		fmt.Fprintln(sb)
	}

	// Per-corpus win-rate conclusion.
	fmt.Fprintf(sb, "### Conclusions\n\n")
	for _, corpus := range []string{"sick", "context"} {
		rows := slices.DeleteFunc(slices.Clone(results), func(r verifyRecord) bool {
			return r.Corpus != corpus
		})
		if len(rows) == 0 {
			continue
		}
		wins := 0
		for _, r := range rows {
			if r.Win {
				wins++
			}
		}
		verdict := "propose/verify CANNOT discriminate (coarse-block information wall)"
		if wins > 0 {
			verdict = fmt.Sprintf("propose/verify CAN discriminate on %d/%d images", wins, len(rows))
		}
		if wins == len(rows) {
			verdict = "propose/verify CAN discriminate on ALL images"
		}
		fmt.Fprintf(sb, "**%s** (%d images): win-rate = %d/%d → %s.\n\n",
			corpus, len(rows), wins, len(rows), verdict)
	}
}
