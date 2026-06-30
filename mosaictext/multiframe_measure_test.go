//go:build mfmeasure

package mosaictext_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/mosaictext"
)

// multiframeManifest mirrors the Case/FrameEntry types in genmultiframe/main.go.
// Duplicated here to avoid importing an internal/fixture/genmultiframe command
// package from a test — the JSON schema is stable and small.
type mfManifest struct {
	Name      string       `json:"name"`
	Text      string       `json:"text"`
	BlockSize int          `json:"block_size"`
	Frames    []mfFrameRef `json:"frames"`
}

type mfFrameRef struct {
	File    string `json:"file"`
	OffsetX int    `json:"offset_x"`
	OffsetY int    `json:"offset_y"`
}

// TestMultiFrameMeasure is an observational (never-fails) test that loads the
// genuine multi-frame fixtures generated from a SHARP source and reports:
//   - single-frame Decode of frame 0,
//   - DecodeMultiFrameAuto over all 4 frames,
//   - the ground-truth text.
//
// The results are printed as a table to t.Log so they survive -v and appear
// in the mise run mfmeasure output. Run with:
//
//	mise run mfmeasure
func TestMultiFrameMeasure(t *testing.T) {
	const manifestPath = "../testdata/multiframe/manifest.json"

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest %s: %v", manifestPath, err)
	}
	var cases []mfManifest
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	dir := filepath.Dir(manifestPath)
	ctx := t.Context()

	// Header row.
	t.Logf("%-14s  %-7s  %-22s  %-22s  %-10s  %-10s  %s",
		"case", "truth",
		"single-frame", "multi-frame",
		"sf-dist", "mf-dist", "mf-better?")
	t.Logf("%-14s  %-7s  %-22s  %-22s  %-10s  %-10s  %s",
		"--------------", "-------",
		"----------------------", "----------------------",
		"----------", "----------", "----------")

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			imgs := loadMFFrames(t, dir, c.Frames)

			sfText, sfDist, sfErr := decodeOne(ctx, imgs[0])
			mfText, mfDist, mfErr := decodeMulti(ctx, imgs)

			better := "n/a"
			if sfErr == nil && mfErr == nil {
				if mfDist < sfDist {
					better = fmt.Sprintf("YES (%.1f%%)", (sfDist-mfDist)/sfDist*100)
				} else {
					better = fmt.Sprintf("no  (+%.1f%%)", (mfDist-sfDist)/sfDist*100)
				}
			}

			t.Logf("%-14s  %-7s  %-22s  %-22s  %-10s  %-10s  %s",
				c.Name, c.Text,
				fmtResult(sfText, sfErr), fmtResult(mfText, mfErr),
				fmtDist(sfDist, sfErr), fmtDist(mfDist, mfErr),
				better)
		})
	}
}

// loadMFFrames loads all frame PNGs for one manifest case.
func loadMFFrames(t *testing.T, dir string, refs []mfFrameRef) []image.Image {
	t.Helper()
	imgs := make([]image.Image, len(refs))
	for i, ref := range refs {
		p := filepath.Join(dir, ref.File)
		f, err := os.Open(p) // #nosec G304 -- test reads controlled fixture paths
		if err != nil {
			t.Fatalf("open frame %s: %v", p, err)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("decode PNG %s: %v", p, err)
		}
		imgs[i] = img
	}
	return imgs
}

// decodeOne runs mosaictext.Decode on a single image.
func decodeOne(ctx context.Context, img image.Image) (string, float64, error) {
	res, err := mosaictext.Decode(ctx, img)
	if err != nil {
		return "", 0, err
	}
	return res.Text, res.Distance, nil
}

// decodeMulti runs mosaictext.DecodeMultiFrameAuto over all frames.
func decodeMulti(ctx context.Context, imgs []image.Image) (string, float64, error) {
	res, err := mosaictext.DecodeMultiFrameAuto(ctx, imgs)
	if err != nil {
		return "", 0, err
	}
	return res.Text, res.Distance, nil
}

// fmtResult formats decode text/error as a compact display string.
func fmtResult(text string, err error) string {
	if err != nil {
		return fmt.Sprintf("ERR:%v", err)
	}
	if text == "" {
		return "<empty>"
	}
	return fmt.Sprintf("%q", text)
}

// fmtDist formats a distance value, or "ERR" on error.
func fmtDist(dist float64, err error) string {
	if err != nil {
		return "ERR"
	}
	return fmt.Sprintf("%.4f", dist)
}
