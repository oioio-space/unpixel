// journal_decoder_md_test.go — shared types, constants, and markdown helpers
// for the decoder evolution table.
//
// No build tag: compiled in both the default and journal test suites so the
// splice unit tests (journal_decoder_splice_test.go) can exercise these
// functions without the journal tag, and the journal harness
// (journal_decoder_test.go) can reference decoderRow and decoderSlowCap.
package unpixel_test

import (
	"fmt"
	"strings"
)

// ─── shared types and constants ───────────────────────────────────────────────

// decoderRow is one (decoder, corpus) result row for the decoder evolution table.
type decoderRow struct {
	// Decoder is the short decoder name (e.g. "default", "did", "window-hmm").
	Decoder string `json:"decoder"`
	// Corpus is the corpus name the decoder was run on (e.g. "sick", "real").
	Corpus string `json:"corpus"`
	// Total is the number of images in the subset actually run.
	Total int `json:"total"`
	// Subset is true when only a cap-bounded subset of the corpus was used.
	Subset bool `json:"subset,omitzero"`
	// ExactOK is the count of exact (case-sensitive) matches.
	ExactOK int `json:"exact_ok"`
	// Knowable is the count of images with known ground truth.
	Knowable int `json:"knowable"`
	// Sensical is the count of images scoring ≥70%.
	Sensical int `json:"sensical"`
	// MeanScore is the mean recoveryScore across knowable images, or -1 if none.
	MeanScore float64 `json:"mean_score"`
	// DurSec is the total wall-clock time in seconds for this (decoder, corpus).
	DurSec float64 `json:"dur_sec"`
}

// decoderSlowCap is the maximum number of sick images run for slow decoders
// (DID, trained-hmm). These decoders take 30–60 s per image at full corpus
// size, which would blow the journal time budget. The first decoderSlowCap
// sick rows are a representative subset; "subset" is noted in the table.
const decoderSlowCap = 4

// buildDecoderTableHeader returns the initial "## Évolution — décodeurs" section,
// written once when the section is absent from docs/JOURNAL.md.
func buildDecoderTableHeader() string {
	return fmt.Sprintf(`## Évolution — décodeurs

Long format: one row per (decoder, corpus) per run, growing down. Read each
decoder vertically to track quality over time. Slow decoders (did, trained-hmm)
are capped to the first %d sick images (noted in the Subset column).

| Date (UTC) | Version | Commit | Decoder | Corpus | exact/knowable/≥70%%/mean%% | Dur (s) | Subset |
|---|---|---|---|---|---|---|---|
`, decoderSlowCap)
}

// spliceDecoderTableMD inserts newDecoderRows at the end of the
// "## Évolution — décodeurs" table in existing, or appends the full section
// header when the section is absent. The section is identified by the heading
// "## Évolution — décodeurs" (ASCII fallback: "## Evolution — decodeurs").
func spliceDecoderTableMD(existing, newDecoderRows string) string {
	lines := strings.Split(existing, "\n")

	tableEnd := -1
	inSection := false

	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "## Évolution — décodeurs") ||
			strings.HasPrefix(line, "## Evolution — decodeurs"):
			inSection = true
		case inSection && strings.HasPrefix(line, "|"):
			tableEnd = i
		case inSection && strings.HasPrefix(line, "## "):
			inSection = false
		}
	}

	if tableEnd < 0 {
		// Section absent — append header + rows at the end.
		return strings.TrimRight(existing, "\n") + "\n\n" +
			buildDecoderTableHeader() + newDecoderRows + "\n"
	}

	// Insert new rows after the last "|" row of the decoder table.
	var sb strings.Builder
	for i, line := range lines {
		sb.WriteString(line)
		if i < len(lines)-1 {
			sb.WriteByte('\n')
		}
		if i == tableEnd {
			sb.WriteString(newDecoderRows)
		}
	}
	return sb.String()
}
