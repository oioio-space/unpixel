//go:build mfmeasure

package mosaictext

// TestMultiFrameRankImprovement is a white-box measurement test that answers the
// recovery-relevant question: does multi-frame scoring make the TRUE text rank #1
// (argmin distance) among confusable candidates in cases where single-frame does NOT?
//
// For a matrix of block sizes × frame counts, it:
//  1. Renders the true text once to produce a sharp source.
//  2. Pixelates at distinct, phase-diverse grid positions to build N target frames.
//  3. Scores the true text and ~5–8 confusable wrong candidates using the decoder's
//     dist function under N=1, 2, and 4 frames.
//  4. Records the argmin rank of the true text in each regime and prints a table.
//
// The test never fails (it is observational). Its output is the deliverable.
// Run with:
//
//	CGO_ENABLED=0 go test -tags mfmeasure -run TestMultiFrameRankImprovement -v ./mosaictext/

import (
	"cmp"
	"fmt"
	"image"
	"slices"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// rankTestCase defines one (true text, confusables) test scenario.
type rankTestCase struct {
	trueText    string
	confusables []string
	// phases holds absolute (X, Y) grid phases for frames 0..3.
	// Frame 0 is always the anchor; frames 1..3 provide phase diversity.
	phases [4][2]int
}

// TestMultiFrameRankImprovement measures whether multi-frame scoring improves
// the rank of the true text among confusable candidates.
func TestMultiFrameRankImprovement(t *testing.T) {
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	const fs = 24.0

	scenarios := []rankTestCase{
		// Short 2-letter word — classic near-confusions.
		{
			trueText:    "be",
			confusables: []string{"he", "we", "me", "be", "ba", "bo", "bc", "le"},
			phases:      [4][2]int{{0, 0}, {3, 0}, {5, 0}, {7, 0}},
		},
		// 3-letter word — cat-like confusions.
		{
			trueText:    "cat",
			confusables: []string{"cot", "car", "can", "cit", "eat", "oat", "bat", "rat"},
			phases:      [4][2]int{{0, 0}, {4, 0}, {8, 0}, {2, 0}},
		},
		// 4-letter word — more signal per frame.
		{
			trueText:    "word",
			confusables: []string{"wore", "cord", "ward", "work", "ford", "lord", "bird", "warm"},
			phases:      [4][2]int{{0, 0}, {5, 0}, {9, 0}, {3, 0}},
		},
	}

	blocks := []int{8, 12, 16, 24}
	frameCounts := []int{1, 2, 4}

	// Header.
	t.Logf("%-8s  %-8s  %-6s  %-10s  %-10s  %-10s  %-12s",
		"scenario", "block", "true", "rank@N=1", "rank@N=2", "rank@N=4", "mf-improved?")
	t.Logf("%s", strings.Repeat("-", 80))

	for _, sc := range scenarios {
		// Remove the true text from confusables in case it was included accidentally,
		// then rebuild a clean candidate set: [trueText] + confusables (without dups).
		candidates := make([]string, 0, 1+len(sc.confusables))
		candidates = append(candidates, sc.trueText)
		for _, c := range sc.confusables {
			if c != sc.trueText {
				candidates = append(candidates, c)
			}
		}

		for _, block := range blocks {
			t.Run(fmt.Sprintf("%s/block%d", sc.trueText, block), func(t *testing.T) {
				// Build the sharp source from the true text.
				img, sx, rerr := r.Render(sc.trueText, unpixel.Style{FontSize: fs})
				if rerr != nil || sx <= 0 {
					t.Skipf("render: %v", rerr)
				}
				bb := inkBounds(img, sx)

				const pad = 4
				src := image.NewRGBA(image.Rect(0, 0, bb.Dx()+pad*2, bb.Dy()+pad*2))
				imutil.FillWhite(src)
				for y := range bb.Dy() {
					for x := range bb.Dx() {
						src.SetRGBA(pad+x, pad+y, img.RGBAAt(bb.Min.X+x, bb.Min.Y+y))
					}
				}

				pix := pixelate.NewBlockAverage(block)

				// Build 4 pixelated targets at distinct phases.
				mosaics := make([]*image.RGBA, 4)
				for fi := range 4 {
					ph := sc.phases[fi]
					mosaics[fi] = pix.Pixelate(src, ph[0], ph[1])
				}

				// Build the base decoder anchored to frame 0's mosaic.
				d := &decoder{
					r:        r,
					target:   mosaics[0],
					tW:       mosaics[0].Bounds().Dx(),
					tH:       mosaics[0].Bounds().Dy(),
					block:    block,
					pixelate: pix,
					cacheCap: minCacheEntries,
				}
				if _, _, _, ok := d.calibrate(); !ok {
					t.Skipf("calibrate failed at block=%d", block)
				}

				stretch := d.stretchForN(len([]rune(sc.trueText)))

				// For each N, populate d.frames and score all candidates.
				ranks := make([]int, len(frameCounts)) // rank (1-based) of true text at each N
				for ni, n := range frameCounts {
					d.cache = newRenderCache(minCacheEntries)
					if n == 1 {
						d.frames = nil
					} else {
						b := block
						sfs := make([]scoreFrame, n)
						for fi := range n {
							ph := sc.phases[fi]
							// Frame-0-relative delta, normalized to [0, block).
							rawDx := ph[0] - sc.phases[0][0]
							rawDy := ph[1] - sc.phases[0][1]
							normDx := ((rawDx % b) + b) % b
							normDy := ((rawDy % b) + b) % b
							sfs[fi] = scoreFrame{
								target:   mosaics[fi],
								pixelate: pix,
								pox:      normDx,
								poy:      normDy,
							}
						}
						d.frames = sfs
					}

					// Score every candidate.
					type scored struct {
						text string
						dist float64
					}
					cands := make([]scored, len(candidates))
					for ci, c := range candidates {
						cands[ci] = scored{text: c, dist: d.dist(c, fs, stretch, sc.phases[0][0])}
					}
					slices.SortFunc(cands, func(a, b scored) int {
						return cmp.Compare(a.dist, b.dist)
					})
					// Find the 1-based rank of the true text.
					rank := slices.IndexFunc(cands, func(s scored) bool { return s.text == sc.trueText }) + 1
					ranks[ni] = rank

					// Verbose: log all candidate distances.
					t.Logf("  N=%d block=%d %-6s: candidates by dist:", n, block, sc.trueText)
					for ri, cs := range cands {
						marker := ""
						if cs.text == sc.trueText {
							marker = " ← TRUE"
						}
						t.Logf("    rank%d  %-6s  %.4f%s", ri+1, cs.text, cs.dist, marker)
					}
				}

				// Determine if multi-frame ever improved (lowered) the true-text rank vs N=1.
				improved := ranks[1] < ranks[0] || ranks[2] < ranks[0]
				mfStr := "no"
				if improved {
					mfStr = "YES"
				}

				t.Logf("%-8s  %-8d  %-6s  %-10d  %-10d  %-10d  %-12s",
					sc.trueText, block, sc.trueText, ranks[0], ranks[1], ranks[2], mfStr)
			})
		}
	}
}
