//go:build journal

package unpixel_test

// journal_proposeverify_test.go — the propose/verify (Verify-path) section of the
// test journal. The rest of the journal measures the BLIND RecoverFile/decoder
// path; this section measures the complementary LLM-propose / physical-verify loop
// (unpixel.Verify): given a small candidate set (truth + hard confusable decoys)
// and the redaction's calibrated config, does whole-string physical scoring rank
// the truth #1 and Match it? That is the differentiator this project ships for
// coarse-block redactions where per-character blind search is information-starved.
//
// It runs the sick corpus live (version-tracked) and renders an analysis block that
// records the full sick/context/real picture and the measured physical-tie frontier.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
)

// proposeVerifyRow is one image's propose/verify outcome.
type proposeVerifyRow struct {
	Name      string  `json:"name"`
	Truth     string  `json:"truth"`
	TruthDist float64 `json:"truth_dist"`
	Match     bool    `json:"match"`
	Rank      int     `json:"rank"` // 1 = truth is the lowest-distance candidate
	Decoys    int     `json:"decoys"`
	Win       bool    `json:"win"` // rank == 1 AND Match
}

// proposeVerifySummary aggregates the propose/verify sick-corpus run.
type proposeVerifySummary struct {
	Rows     []proposeVerifyRow `json:"rows"`
	Wins     int                `json:"wins"`
	Total    int                `json:"total"`
	Duration float64            `json:"duration_s"`
}

// pvDecoys builds a compact set of physically-plausible decoys (single confusable
// swaps + a length variant) around truth. It is intentionally smaller than the
// verifymeasure harness's set — the journal tracks a live discrimination signal,
// while `mise run verifymeasure` is the exhaustive hard-decoy spike.
func pvDecoys(truth string) []string {
	seen := map[string]bool{truth: true}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	swaps := [][2]string{
		{"o", "0"}, {"0", "o"}, {"l", "1"}, {"1", "l"}, {"O", "0"},
		{"e", "c"}, {"a", "o"}, {"5", "s"}, {"g", "9"}, {"i", "1"},
	}
	for _, sw := range swaps {
		add(strings.Replace(truth, sw[0], sw[1], 1))
	}
	if r := []rune(truth); len(r) > 1 {
		add(string(r[:len(r)-1]))
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// runProposeVerify measures the sick corpus through unpixel.Verify: for each image
// it scores truth + pvDecoys with the calibrated config and records whether the
// truth is rank-1 and Match. Live and version-tracked in the journal.
func runProposeVerify(t *testing.T) proposeVerifySummary {
	t.Helper()
	start := time.Now()
	const dir = "testdata/sick"

	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Logf("propose/verify: read sick manifest: %v (skipping)", err)
		return proposeVerifySummary{}
	}
	var entries []journalSickEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Logf("propose/verify: parse sick manifest: %v (skipping)", err)
		return proposeVerifySummary{}
	}

	all := fonts.All()
	sum := proposeVerifySummary{Total: len(entries)}
	for _, e := range entries {
		imgPath := filepath.Join(dir, e.file())
		img, decErr := loadDecoderImage(imgPath)
		if decErr != nil {
			t.Logf("propose/verify %s: load: %v", e.Name, decErr)
			sum.Total--
			continue
		}

		opts := []unpixel.Option{
			unpixel.WithBlockSize(e.BlockSize),
			unpixel.WithStyle(unpixel.Style{
				FontSize:    e.FontSize,
				Bold:        e.Bold,
				PaddingTop:  8,
				PaddingLeft: 8,
			}),
		}
		if r := pvRenderer(all, e.Font); r != nil {
			opts = append(opts, unpixel.WithRenderer(r))
		}

		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		verdicts, verr := unpixel.Verify(ctx, img, append([]string{e.Text}, pvDecoys(e.Text)...), opts...)
		cancel()
		if verr != nil || len(verdicts) == 0 {
			t.Logf("propose/verify %s: verify: %v", e.Name, verr)
			continue
		}

		var truthDist float64
		var match bool
		rank := 1
		for _, v := range verdicts {
			if v.Text == e.Text {
				truthDist, match = v.Distance, v.Match
			}
		}
		for _, v := range verdicts {
			if v.Text != e.Text && v.Distance < truthDist {
				rank++
			}
		}
		win := rank == 1 && match
		if win {
			sum.Wins++
		}
		sum.Rows = append(sum.Rows, proposeVerifyRow{
			Name: e.Name, Truth: e.Text, TruthDist: truthDist,
			Match: match, Rank: rank, Decoys: len(verdicts) - 1, Win: win,
		})
		t.Logf("propose/verify %-24s truth=%q dist=%.4f rank=%d win=%v", e.Name, truncate(e.Text, 30), truthDist, rank, win)
	}
	sum.Duration = time.Since(start).Seconds()
	return sum
}

// pvRenderer builds a renderer for the named bundled font, or nil (engine default)
// when the name is unknown.
func pvRenderer(all []fonts.Font, name string) unpixel.Renderer {
	for _, f := range all {
		if f.Name == name {
			r, err := defaults.RendererFromFonts(f.Data, nil)
			if err != nil {
				return nil
			}
			return r
		}
	}
	return nil
}

// buildProposeVerifySection renders the propose/verify section: a live sick-corpus
// discrimination table plus an analysis block on the full frontier.
func buildProposeVerifySection(pv *proposeVerifySummary) string {
	var sb strings.Builder
	sb.WriteString("### propose/verify (Verify path — LLM-propose / physical-verify)\n\n")
	sb.WriteString(
		"Complementary to the blind decoders above: `unpixel.Verify` scores a small candidate " +
			"set (truth + confusable decoys) by whole-string physical re-pixelation and reports whether " +
			"the truth is rank-1 and a confident Match. This is the recoverable path for coarse-block " +
			"redactions where per-character blind search is information-starved.\n\n")

	if pv == nil || pv.Total == 0 {
		sb.WriteString("_No propose/verify run this cycle._\n\n")
		return sb.String()
	}

	fmt.Fprintf(&sb, "**Sick corpus (live): %d/%d truth rank-1 AND Match** (%.1f s, compact decoy set; "+
		"the full hard-decoy spike is `mise run verifymeasure` → docs/VERIFY-SPIKE.md).\n\n",
		pv.Wins, pv.Total, pv.Duration)

	sb.WriteString("| image | truth | dist | rank | decoys | win |\n")
	sb.WriteString("|---|---|---|---|---|---|\n")
	for _, r := range pv.Rows {
		fmt.Fprintf(&sb, "| `%s` | `%s` | %.4f | %d | %d | %v |\n",
			r.Name, truncate(r.Truth, 30), r.TruthDist, r.Rank, r.Decoys, r.Win)
	}
	sb.WriteString("\n")

	sb.WriteString("**Analysis — what propose/verify recovers, and the frontier.**\n\n")
	sb.WriteString(
		"- **Real redactions are recoverable end-to-end.** `unpixel.Verify` + `unpixel.WithCrop` " +
			"confirms the truth of the real GIMP mosaic `real/hello-world.png` at distance 0.0000 " +
			"(decoy rejected), driven via the MCP `unpixel_verify_candidates` tool with hints from " +
			"analyze/rank_fonts/calibrate. The binding lever is a tight crop of the redaction band, " +
			"not model fidelity.\n" +
			"- **Sub-block alignment matters.** `alignedDist` now sweeps sub-block phases " +
			"(`block/4`) and refines position coarse-to-fine to single-pixel accuracy at coarse-sweep " +
			"cost — this took the sick digit fixtures to 10/10 and lifted context discrimination " +
			"(`mise run verifymeasure`: sick 10/10, context 6/9) with no perf regression.\n" +
			"- **The residual wall is information-theoretic, not engineering.** At correct rendering " +
			"(incl. the Nunito variable-font renderer), high-entropy secrets pixelated at coarse blocks " +
			"become physical homoglyph ties (confusable-glyph decoys within ~0.002). A global language " +
			"prior separates them inconsistently (helps where truth is more word-like than its decoy, " +
			"hurts otherwise). Breaking it needs learned per-character emissions (ML, `//go:build ml`) — " +
			"the documented ceiling.\n\n")
	return sb.String()
}
