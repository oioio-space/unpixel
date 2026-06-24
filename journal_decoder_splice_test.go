// journal_decoder_splice_test.go — unit tests for spliceDecoderTableMD.
//
// No build tag: compiled in the default test suite so the splice logic is
// guarded on every run without waiting for a 30-minute journal run.
package unpixel_test

import (
	"strings"
	"testing"
)

// TestSpliceDecoderTableMD_insertsIntoExistingTable verifies that
// spliceDecoderTableMD appends new rows after the last "|" line of an
// already-existing "## Évolution — décodeurs" table.
func TestSpliceDecoderTableMD_insertsIntoExistingTable(t *testing.T) {
	existing := `# UnPixel — Test Journal

## Évolution

| Date (UTC) | Version | Commit |
|---|---|---|
| 2026-01-01 | v1 | abc1234 |

## Évolution — décodeurs

Some description.

| Date (UTC) | Version | Commit | Decoder | Corpus | exact/knowable/≥70%/mean% | Dur (s) | Subset |
|---|---|---|---|---|---|---|---|
| 2026-01-01 | v1 | abc1234 | default | sick | 0/10/0/10% | 30 |  |

## Run 2026-01-01T00:00:00Z — abc1234

detail
`

	newRows := "| 2026-06-24 | v2 | def5678 | did | sick | 1/4/1/25% | 45 | first 4 |\n"

	got := spliceDecoderTableMD(existing, newRows)

	// The new row must appear after the existing table row.
	oldRowIdx := strings.Index(got, "abc1234 | default")
	newRowIdx := strings.Index(got, "def5678 | did")
	if oldRowIdx < 0 {
		t.Error("spliceDecoderTableMD: existing row missing from output")
	}
	if newRowIdx < 0 {
		t.Error("spliceDecoderTableMD: new row missing from output")
	}
	if oldRowIdx >= 0 && newRowIdx >= 0 && newRowIdx <= oldRowIdx {
		t.Errorf("spliceDecoderTableMD: new row (idx=%d) must appear after existing row (idx=%d)", newRowIdx, oldRowIdx)
	}

	// The run section must survive untouched.
	if !strings.Contains(got, "## Run 2026-01-01") {
		t.Error("spliceDecoderTableMD: run section was lost")
	}
}

// TestSpliceDecoderTableMD_createsTableWhenAbsent verifies that
// spliceDecoderTableMD appends the full decoder-table header when the
// "## Évolution — décodeurs" section is absent from docs/JOURNAL.md.
func TestSpliceDecoderTableMD_createsTableWhenAbsent(t *testing.T) {
	existing := `# UnPixel — Test Journal

## Évolution

| Date (UTC) | Version | Commit |
|---|---|---|
| 2026-01-01 | v1 | abc1234 |

## Run 2026-01-01T00:00:00Z — abc1234

detail
`

	newRows := "| 2026-06-24 | v2 | def5678 | blind | sick | 0/10/0/5% | 120 |  |\n"

	got := spliceDecoderTableMD(existing, newRows)

	if !strings.Contains(got, "## Évolution — décodeurs") {
		t.Error("spliceDecoderTableMD: header not created when section absent")
	}
	if !strings.Contains(got, "def5678 | blind") {
		t.Error("spliceDecoderTableMD: new row not present after header creation")
	}
	// Existing corpus table must survive.
	if !strings.Contains(got, "abc1234") {
		t.Error("spliceDecoderTableMD: existing content was lost")
	}
}

// TestSpliceDecoderTableMD_multipleRunsAccumulate verifies that calling
// spliceDecoderTableMD twice produces two data rows in the table.
func TestSpliceDecoderTableMD_multipleRunsAccumulate(t *testing.T) {
	base := `# Journal

## Évolution — décodeurs

| Date (UTC) | Version | Commit | Decoder | Corpus | exact/knowable/≥70%/mean% | Dur (s) | Subset |
|---|---|---|---|---|---|---|---|
`

	row1 := "| 2026-01-01 | v1 | aaa | default | sick | 0/10/0/10% | 30 |  |\n"
	row2 := "| 2026-06-24 | v2 | bbb | default | sick | 1/10/1/15% | 31 |  |\n"

	after1 := spliceDecoderTableMD(base, row1)
	after2 := spliceDecoderTableMD(after1, row2)

	got1 := strings.Count(after2, "| default | sick |")
	want1 := 2
	if got1 != want1 {
		t.Errorf("spliceDecoderTableMD after two runs: got %d data rows, want %d\n--- output ---\n%s", got1, want1, after2)
	}

	// Row 2 must appear after row 1 (chronological order preserved).
	idx1 := strings.Index(after2, "| aaa |")
	idx2 := strings.Index(after2, "| bbb |")
	if idx1 < 0 || idx2 < 0 {
		t.Errorf("spliceDecoderTableMD: expected both commit hashes in output")
	} else if idx2 <= idx1 {
		t.Errorf("spliceDecoderTableMD: row2 (idx=%d) must follow row1 (idx=%d)", idx2, idx1)
	}
}
