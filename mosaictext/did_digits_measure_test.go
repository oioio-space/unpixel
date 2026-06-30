//go:build didmeasure

package mosaictext_test

// TestDIDDigitsMeasure is an observational (never-fails) test that measures
// how well the DID column-trellis decoder handles long digit strings compared
// to the default monospace engine (Decode). It loads each digit fixture from
// testdata/sick/manifest.json, decodes it via both paths, and reports
// per-fixture accuracy side by side.
//
// Run with:
//
//	mise run didmeasure

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/mosaictext"
)

// sickEntry mirrors the relevant fields in testdata/sick/manifest.json.
type sickEntry struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	Charset   string `json:"charset"`
	BlockSize int    `json:"block_size"`
	Kind      string `json:"kind"`
}

// charAccuracy computes the fraction of positions (aligned from left) that
// match between got and want. Extra trailing characters in either string are
// treated as mismatches. If both are empty, accuracy is 1.0.
func charAccuracy(got, want string) float64 {
	if len(want) == 0 && len(got) == 0 {
		return 1.0
	}
	n := max(len([]rune(got)), len([]rune(want)))
	if n == 0 {
		return 1.0
	}
	gr := []rune(got)
	wr := []rune(want)
	correct := 0
	for i := range min(len(gr), len(wr)) {
		if gr[i] == wr[i] {
			correct++
		}
	}
	return float64(correct) / float64(n)
}

// TestDIDDigitsMeasure loads each digit fixture, decodes it via Decode (the
// default monospace engine) and via DecodeDID (column-trellis), and logs a
// comparison table. The test never fails — it is an observational measurement.
func TestDIDDigitsMeasure(t *testing.T) {
	const manifestPath = "../testdata/sick/manifest.json"
	const fixtureDir = "../testdata/sick"

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest %s: %v", manifestPath, err)
	}
	var all []sickEntry
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	// Filter to digit fixtures only.
	var cases []sickEntry
	for _, e := range all {
		if e.Kind == "digits" {
			cases = append(cases, e)
		}
	}
	if len(cases) == 0 {
		t.Fatal("no digit fixtures found in manifest")
	}

	ctx := t.Context()

	// Header.
	t.Logf("%-28s  %-12s  %-14s  %-6s  %-14s  %-6s",
		"fixture", "truth",
		"engine-text", "e-acc",
		"DID-text", "d-acc")
	t.Logf("%-28s  %-12s  %-14s  %-6s  %-14s  %-6s",
		"----------------------------", "------------",
		"--------------", "------",
		"--------------", "------")

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			imgPath := filepath.Join(fixtureDir, c.Name+".png")
			f, err := os.Open(imgPath) // #nosec G304 -- test reads controlled fixture path
			if err != nil {
				t.Logf("SKIP  %-24s  open: %v", c.Name, err)
				return
			}
			img, err := png.Decode(f)
			f.Close()
			if err != nil {
				t.Logf("SKIP  %-24s  png decode: %v", c.Name, err)
				return
			}

			// --- Default engine (Decode / monospace path) ---
			engText := "<err>"
			engDist := 0.0
			if res, err := mosaictext.Decode(ctx, img); err != nil {
				engText = fmt.Sprintf("<err:%v>", err)
			} else {
				engText = res.Text
				engDist = res.Distance
			}
			engAcc := charAccuracy(engText, c.Text)

			// --- DID trellis (DecodeDID) with digits charset ---
			// Pin Liberation Mono (the font used for all digit fixtures) and
			// supply the exact digit charset so the trellis is a fair structured
			// comparison: same information as the engine's expected_format=digits.
			// Also try WithDIDContext to model boundary blocks.
			didText := "<err>"
			didDist := 0.0
			didOpts := []mosaictext.DIDOption{
				mosaictext.WithDIDCharset(c.Charset),
				mosaictext.WithDIDFont("Liberation Mono"),
				mosaictext.WithDIDBlockSize(c.BlockSize),
				mosaictext.WithDIDLambda(0), // use default (image signal dominates for digits)
			}
			if res, err := mosaictext.DecodeDID(ctx, img, didOpts...); err != nil {
				didText = fmt.Sprintf("<err:%v>", err)
			} else {
				didText = res.Text
				didDist = res.Distance
			}
			didAcc := charAccuracy(didText, c.Text)

			// Context-aware variant.
			didCtxText := "<err>"
			didCtxDist := 0.0
			didCtxOpts := append(didOpts[:len(didOpts):len(didOpts)], mosaictext.WithDIDContext(true))
			if res, err := mosaictext.DecodeDID(ctx, img, didCtxOpts...); err != nil {
				didCtxText = fmt.Sprintf("<err:%v>", err)
			} else {
				didCtxText = res.Text
				didCtxDist = res.Distance
			}
			didCtxAcc := charAccuracy(didCtxText, c.Text)

			t.Logf("%-28s  %-12s  %-14s  %-6s  %-14s  %-6s  e-dist=%.4f  d-dist=%.4f  dctx=%-14s  dctx-acc=%-6s  dctx-dist=%.4f",
				c.Name, c.Text,
				engText, fmt.Sprintf("%.2f", engAcc),
				didText, fmt.Sprintf("%.2f", didAcc),
				engDist, didDist,
				didCtxText, fmt.Sprintf("%.2f", didCtxAcc),
				didCtxDist,
			)
		})
	}
}
