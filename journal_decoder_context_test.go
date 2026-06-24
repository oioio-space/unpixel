//go:build journal

// journal_decoder_context_test.go — C1a/C1b calibrate-from-visible entries for
// the decoder evolution matrix.
//
// C1a (calibrate-visible): each context fixture provides its own sharp
// visible_rect as the calibration source; the redacted_rect is decoded blind.
//
// C1b (calibrate-sample): only fixtures that carry a font_sample field are
// included. The separate companion PNG is the calibration source instead of the
// in-image visible region. The row's Total/Knowable counts reflect this subset.
//
// Both runners return one aggregate decoderRow for runDecoderMatrix.
package unpixel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/varfont"
	"github.com/oioio-space/unpixel/mosaictext"
)

// contextCorpusDir is the path to the context corpus relative to the module
// root. Journal tests run with the module root as working directory.
const contextCorpusDir = "testdata/context"

// contextCharset is the candidate charset for blind varfont decodes on the
// context corpus. It covers the full alphanumeric range plus the handful of
// special characters that appear in the fixture secrets.
var contextCharset = unpixel.CharsetAlnum + "!@#$%&*_-."

// runCalibrateVisibleOnContext runs C1a over the full context corpus.
// For each fixture it crops the sharp visible_rect from the same PNG, uses it
// as the calibration source via WithVarFontVisible, then blindly decodes the
// redacted_rect. Returns one aggregate decoderRow.
func runCalibrateVisibleOnContext(t *testing.T, specs []fixture.ContextSpec) decoderRow {
	t.Helper()

	start := time.Now()
	row := decoderRow{
		Decoder:   "calibrate-visible",
		Corpus:    "context",
		MeanScore: -1,
	}

	var scoreSum float64
	var scoreN int

	for _, s := range specs {
		img, err := loadContextImage(filepath.Join(contextCorpusDir, s.File()))
		if err != nil {
			t.Logf("C1a: load %s: %v (skipping)", s.Name, err)
			continue
		}
		row.Total++
		row.Knowable++ // every context fixture has a non-empty secret

		visibleCrop := cropContextRect(img, s.VisibleRect)
		redactCrop := cropContextRect(img, s.RedactedRect)

		ctx, cancel := context.WithTimeout(t.Context(), decoderPerTimeout)
		res, decErr := mosaictext.DecodeVarFont(ctx, redactCrop,
			mosaictext.WithVarFontAxes(contextAxes()),
			mosaictext.WithVarFontVisible(visibleCrop, s.VisibleText),
			mosaictext.WithVarFontCharset(contextCharset),
			mosaictext.WithVarFontBlockSize(s.BlockSize),
			mosaictext.WithVarFontLinear(s.Linear),
		)
		timedOut := ctx.Err() != nil
		cancel()

		guess := res.Text
		switch {
		case timedOut && guess == "":
			t.Logf("C1a/%-28s timeout", s.Name)
		case decErr != nil && guess == "":
			t.Logf("C1a/%-28s %v", s.Name, decErr)
		}

		score := recoveryScore(guess, s.Secret)
		scoreSum += score
		scoreN++
		if score >= 70 {
			row.Sensical++
		}
		if guess == s.Secret {
			row.ExactOK++
		}
		t.Logf("calibrate-visible/%-28s gt=%q guess=%q score=%.0f%%",
			s.Name, truncate(s.Secret, 30), truncate(guess, 30), score)
	}

	if scoreN > 0 {
		row.MeanScore = scoreSum / float64(scoreN)
	}
	row.DurSec = time.Since(start).Seconds()
	return row
}

// runCalibrateSampleOnContext runs C1b over the font_sample subset.
// Only fixtures with a non-nil FontSample are included; others are skipped.
// The calibration source is the separate companion PNG (FontSample.File()).
// Returns one aggregate decoderRow.
func runCalibrateSampleOnContext(t *testing.T, specs []fixture.ContextSpec) decoderRow {
	t.Helper()

	start := time.Now()
	row := decoderRow{
		Decoder:   "calibrate-sample",
		Corpus:    "context",
		MeanScore: -1,
		// Subset is false: the row covers all font_sample fixtures, not an
		// arbitrary cap. The table note column is intentionally left empty here;
		// the smaller Total count is self-documenting.
	}

	var scoreSum float64
	var scoreN int

	for _, s := range specs {
		if s.FontSample == nil {
			continue
		}

		sampleImg, err := loadContextImage(filepath.Join(contextCorpusDir, s.FontSample.File()))
		if err != nil {
			t.Logf("C1b: load sample %s: %v (skipping)", s.FontSample.Name, err)
			continue
		}

		// Crop the text region from the sample PNG. When the rect is zero (the
		// generator did not populate it), use the full image.
		var sampleCrop *image.RGBA
		sr := s.FontSample.SampleRect
		if sr.W > 0 && sr.H > 0 {
			sampleCrop = cropContextRect(sampleImg, sr)
		} else {
			sampleCrop = sampleImg
		}

		img, err := loadContextImage(filepath.Join(contextCorpusDir, s.File()))
		if err != nil {
			t.Logf("C1b: load redaction %s: %v (skipping)", s.Name, err)
			continue
		}
		row.Total++
		row.Knowable++

		redactCrop := cropContextRect(img, s.RedactedRect)

		ctx, cancel := context.WithTimeout(t.Context(), decoderPerTimeout)
		res, decErr := mosaictext.DecodeVarFont(ctx, redactCrop,
			mosaictext.WithVarFontAxes(contextAxes()),
			mosaictext.WithVarFontVisible(sampleCrop, s.FontSample.SampleText),
			mosaictext.WithVarFontCharset(contextCharset),
			mosaictext.WithVarFontBlockSize(s.BlockSize),
			mosaictext.WithVarFontLinear(s.Linear),
		)
		timedOut := ctx.Err() != nil
		cancel()

		guess := res.Text
		switch {
		case timedOut && guess == "":
			t.Logf("C1b/%-28s timeout", s.Name)
		case decErr != nil && guess == "":
			t.Logf("C1b/%-28s %v", s.Name, decErr)
		}

		score := recoveryScore(guess, s.Secret)
		scoreSum += score
		scoreN++
		if score >= 70 {
			row.Sensical++
		}
		if guess == s.Secret {
			row.ExactOK++
		}
		t.Logf("calibrate-sample/%-28s gt=%q guess=%q score=%.0f%%",
			s.Name, truncate(s.Secret, 30), truncate(guess, 30), score)
	}

	if scoreN > 0 {
		row.MeanScore = scoreSum / float64(scoreN)
	}
	row.DurSec = time.Since(start).Seconds()
	return row
}

// loadContextManifest reads testdata/context/manifest.json and returns specs
// whose PNG exists on disk. Missing image files are logged and excluded; a
// missing manifest is not fatal (returns nil so the caller emits zero rows).
func loadContextManifest(t *testing.T) []fixture.ContextSpec {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(contextCorpusDir, "manifest.json")) // #nosec G304
	if err != nil {
		t.Logf("decoder matrix: context manifest: %v (no context rows)", err)
		return nil
	}
	var specs []fixture.ContextSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Logf("decoder matrix: context manifest parse: %v (no context rows)", err)
		return nil
	}
	valid := specs[:0]
	for _, s := range specs {
		if _, statErr := os.Stat(filepath.Join(contextCorpusDir, s.File())); statErr == nil {
			valid = append(valid, s)
		}
	}
	return valid
}

// loadContextImage opens path as a PNG and returns it as *image.RGBA.
func loadContextImage(path string) (*image.RGBA, error) {
	f, err := os.Open(path) // #nosec G304 — test reads committed testdata
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return contextToRGBA(img), nil
}

// contextToRGBA returns img as *image.RGBA, drawing a fresh copy only when
// necessary.
func contextToRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// cropContextRect returns a fresh *image.RGBA holding the sub-rectangle r of
// img, clipped to img's bounds.
func cropContextRect(img *image.RGBA, r fixture.Rect) *image.RGBA {
	b := img.Bounds()
	x0 := max(b.Min.X, b.Min.X+r.X)
	y0 := max(b.Min.Y, b.Min.Y+r.Y)
	x1 := min(b.Max.X, x0+r.W)
	y1 := min(b.Max.Y, y0+r.H)
	dw := max(0, x1-x0)
	dh := max(0, y1-y0)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	if dw == 0 || dh == 0 {
		return dst
	}
	rowBytes := dw * 4
	for row := range dh {
		srcOff := img.PixOffset(x0, y0+row)
		copy(dst.Pix[row*dst.Stride:row*dst.Stride+rowBytes], img.Pix[srcOff:srcOff+rowBytes])
	}
	return dst
}

// contextAxes returns the wght AxisSpec used for all context corpus decodes.
// The full design-space range (200–900) is searched with a neutral start of 400.
func contextAxes() []varfont.AxisSpec {
	return []varfont.AxisSpec{
		{Tag: "wght", Min: 200, Max: 900, Start: 400},
	}
}
