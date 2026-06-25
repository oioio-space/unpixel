//go:build journal

// journal_decoder_perspective_test.go — perspective decode entries for the
// decoder evolution matrix. Tracks the forward-model perspective decoder over the
// testdata/perspective corpus, in two modes: manual quad (corners from the
// manifest) and auto (rectify.DetectQuad, no corners supplied), so both the
// decode quality and the auto-detect accuracy are followed over time.
package unpixel_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oioio-space/unpixel/internal/rectify"
	"github.com/oioio-space/unpixel/mosaictext"
)

const perspectiveCorpusDir = "testdata/perspective"

// perspectiveManifestEntry mirrors one entry in testdata/perspective/manifest.json.
type perspectiveManifestEntry struct {
	Name      string        `json:"name"`
	File      string        `json:"file"`
	Text      string        `json:"text"`
	Charset   string        `json:"charset"`
	FontSize  float64       `json:"font_size"`
	BlockSize int           `json:"block_size"`
	RectW     int           `json:"rect_w"`
	RectH     int           `json:"rect_h"`
	Quad      [4][2]float64 `json:"quad"`
}

func (e perspectiveManifestEntry) quad() [4]rectify.Point {
	return [4]rectify.Point{
		{X: e.Quad[0][0], Y: e.Quad[0][1]},
		{X: e.Quad[1][0], Y: e.Quad[1][1]},
		{X: e.Quad[2][0], Y: e.Quad[2][1]},
		{X: e.Quad[3][0], Y: e.Quad[3][1]},
	}
}

// loadPerspectiveManifest reads the perspective corpus manifest; a missing
// manifest is not fatal (returns nil so the caller emits no rows).
func loadPerspectiveManifest(t *testing.T) []perspectiveManifestEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(perspectiveCorpusDir, "manifest.json")) // #nosec G304
	if err != nil {
		t.Logf("decoder matrix: perspective manifest: %v (no perspective rows)", err)
		return nil
	}
	var specs []perspectiveManifestEntry
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Logf("decoder matrix: perspective manifest parse: %v (no perspective rows)", err)
		return nil
	}
	return specs
}

// runPerspectiveDecoder runs DecodePerspective over the perspective corpus. With
// auto=false it supplies the manifest quad + true rect size; with auto=true it
// supplies neither (WithPerspectiveAutoQuad → rectify.DetectQuad). Returns one
// aggregate decoderRow.
func runPerspectiveDecoder(t *testing.T, specs []perspectiveManifestEntry, auto bool) decoderRow {
	t.Helper()

	name := "perspective"
	if auto {
		name = "perspective-auto"
	}
	start := time.Now()
	row := decoderRow{Decoder: name, Corpus: "perspective", MeanScore: -1}

	var scoreSum float64
	var scoreN int
	for _, s := range specs {
		img, err := loadDecoderImage(filepath.Join(perspectiveCorpusDir, s.File))
		if err != nil {
			t.Logf("%s: load %s: %v (skipping)", name, s.Name, err)
			continue
		}
		row.Total++
		row.Knowable++

		opts := []mosaictext.PerspectiveOption{
			mosaictext.WithPerspectiveBlockSize(s.BlockSize),
			mosaictext.WithPerspectiveCharset(s.Charset),
			mosaictext.WithPerspectiveFontSize(s.FontSize),
		}
		if auto {
			opts = append(opts, mosaictext.WithPerspectiveAutoQuad(0))
		} else {
			opts = append(opts,
				mosaictext.WithPerspectiveQuad(s.quad()),
				mosaictext.WithPerspectiveRectSize(s.RectW, s.RectH),
			)
		}

		ctx, cancel := context.WithTimeout(t.Context(), decoderPerTimeout)
		res, decErr := mosaictext.DecodePerspective(ctx, img, opts...)
		cancel()
		if decErr != nil {
			t.Logf("%s/%s: %v", name, s.Name, decErr)
		}

		score := recoveryScore(res.Text, s.Text)
		scoreSum += score
		scoreN++
		if score >= 70 {
			row.Sensical++
		}
		if res.Text == s.Text {
			row.ExactOK++
		}
		t.Logf("%s/%-16s gt=%q guess=%q score=%.0f%%", name, s.Name, truncate(s.Text, 20), truncate(res.Text, 20), score)
	}

	if scoreN > 0 {
		row.MeanScore = scoreSum / float64(scoreN)
	}
	row.DurSec = time.Since(start).Seconds()
	return row
}
