package mosaictext_test

// score_test.go — discrimination tests for ScoreCandidates.
//
// Each test asserts that the correct candidate scores strictly lower (better)
// than all decoys, and that the margin is positive. The "digits" cases are the
// key regression: before the per-candidate-stretch fix, all same-length strings
// scored bit-identically because wider renders were clipped before comparison.

import (
	"testing"

	"github.com/oioio-space/unpixel/mosaictext"
)

// TestScoreCandidates_discrimination asserts that ScoreCandidates produces
// strictly different distances for strings with different glyph content, and
// that the correct candidate ranks first.
//
// Regression for the per-candidate-stretch bug: before the fix, all
// candidates with len > nRef (the calibrated char count) were rendered wider
// than the target canvas and silently clipped, yielding identical scores.
func TestScoreCandidates_discrimination(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		correct string
		decoys  []string
	}{
		{
			// 2-char fixture: original working case, guards against regression.
			name:    "block08_go",
			fixture: "../testdata/fixtures/block08_go.png",
			correct: "go",
			decoys:  []string{"qq", "zz"},
		},
		{
			// 7-digit fixture: key regression — "0000000" vs "9999999" scored
			// identically before the fix (all clipped to white canvas).
			name:    "digits_7d_1234567",
			fixture: "../testdata/sick/digits_7d_1234567.png",
			correct: "1234567",
			decoys:  []string{"7654321", "0000000"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img := loadPNG(t, tc.fixture)
			ctx := t.Context()

			all := append([]string{tc.correct}, tc.decoys...)
			dists, err := mosaictext.ScoreCandidates(ctx, img, all)
			if err != nil {
				t.Fatalf("ScoreCandidates: %v", err)
			}
			if len(dists) != len(all) {
				t.Fatalf("got %d distances, want %d", len(dists), len(all))
			}

			// Find the minimum distance across all candidates.
			minDist := dists[0]
			minIdx := 0
			for i, d := range dists[1:] {
				if d < minDist {
					minDist = d
					minIdx = i + 1
				}
			}

			// The correct candidate must have the lowest distance.
			if all[minIdx] != tc.correct {
				t.Errorf("best candidate = %q (dist=%.6f), want %q (dist=%.6f); all dists: %v",
					all[minIdx], dists[minIdx], tc.correct, dists[0], dists)
			}

			// The margin (gap from best to second-best) must be positive:
			// different strings must not score identically.
			secondMin := 0.0
			first := true
			for i, d := range dists {
				if i == minIdx {
					continue
				}
				if first || d < secondMin {
					secondMin = d
					first = false
				}
			}
			margin := secondMin - minDist
			if margin <= 0 {
				t.Errorf("margin = %.6f, want > 0 (no discrimination); dists: %v", margin, dists)
			}
		})
	}
}
