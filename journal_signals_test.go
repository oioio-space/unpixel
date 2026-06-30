// Package unpixel_test contains unit tests for the journal aggregation signals
// added in journal_test.go (failure-mode histogram, mean confidence, mean
// fidelity, per-corpus duration). These tests carry no build tag so they run
// in the default suite on every `mise run test`.
package unpixel_test

import (
	"testing"
)

// syntheticRow builds a journalRow with the minimum fields needed by
// summariseCorpora. Both zero- and best-config attempts use the supplied
// attempt value; callers override individual fields as needed.
func syntheticRow(corpus, gt string, zero, best journalAttempt) journalRow {
	return journalRow{
		Corpus:      corpus,
		Name:        "img",
		Kind:        "mosaic",
		GroundTruth: gt,
		ZeroConfig:  zero,
		BestConfig:  best,
	}
}

// attempt is a convenience constructor for journalAttempt in test literals.
func attempt(status journalStatus, why string, score, conf, bestTotal, durMS float64) journalAttempt {
	return journalAttempt{
		Status:     status,
		Why:        why,
		Score:      score,
		Confidence: conf,
		BestTotal:  bestTotal,
		DurationMS: durMS,
	}
}

func TestSummariseCorporaSignals(t *testing.T) {
	// below-threshold why string matches classifyOutcome exactly.
	const whyBelow = "below-threshold / no confident candidate"
	const whyWrongLen = "wrong length (got 2 want 4)"
	const whyWrongGly = "wrong glyphs (font fidelity / params)"
	const whyTimeout = "timeout (no result in 30s)"

	cases := []struct {
		name string
		rows []journalRow
		// Expected values for the single corpus named "fix".
		wantBestOK         int
		wantBestSensical   int
		wantBestMeanScore  float64
		wantZeroMeanConf   float64
		wantBestMeanConf   float64
		wantZeroMeanFid    float64
		wantBestMeanFid    float64
		wantBestDurationMS float64
		wantFailModes      map[string]int
	}{
		{
			name: "two exact matches",
			rows: []journalRow{
				syntheticRow(
					"fix", "hello",
					attempt(statusOK, "", 100, 0.9, 0.05, 10),
					attempt(statusOK, "", 100, 0.95, 0.03, 20),
				),
				syntheticRow(
					"fix", "world",
					attempt(statusOK, "", 100, 0.8, 0.10, 15),
					attempt(statusOK, "", 100, 0.85, 0.08, 25),
				),
			},
			wantBestOK:         2,
			wantBestSensical:   2,
			wantBestMeanScore:  100,
			wantZeroMeanConf:   0.85,  // (0.9+0.8)/2
			wantBestMeanConf:   0.90,  // (0.95+0.85)/2
			wantZeroMeanFid:    0.925, // mean(1-0.05, 1-0.10) = mean(0.95,0.90)
			wantBestMeanFid:    0.945, // mean(1-0.03, 1-0.08) = mean(0.97,0.92)
			wantBestDurationMS: 45,    // 20+25
			wantFailModes:      map[string]int{"ok": 2},
		},
		{
			name: "mixed failures",
			rows: []journalRow{
				syntheticRow(
					"fix", "hello",
					attempt(statusFail, whyBelow, 0, 0.1, 0.9, 10),
					attempt(statusFail, whyBelow, 0, 0.2, 0.8, 30),
				),
				syntheticRow(
					"fix", "world",
					attempt(statusFail, whyWrongLen, 40, 0.5, 0.6, 12),
					attempt(statusFail, whyWrongLen, 40, 0.6, 0.5, 40),
				),
				syntheticRow(
					"fix", "go",
					attempt(statusFail, whyWrongGly, 60, 0.7, 0.4, 11),
					attempt(statusFail, whyWrongGly, 60, 0.75, 0.35, 50),
				),
				syntheticRow(
					"fix", "x",
					attempt(statusFail, whyTimeout, 0, 0.0, 1.0, 5),
					attempt(statusFail, whyTimeout, 0, 0.0, 1.0, 60),
				),
			},
			wantBestOK:         0,
			wantBestSensical:   0,
			wantBestMeanScore:  25,     // (0+40+60+0)/4
			wantZeroMeanConf:   0.325,  // (0.1+0.5+0.7+0.0)/4
			wantBestMeanConf:   0.3875, // (0.2+0.6+0.75+0.0)/4
			wantZeroMeanFid:    0.275,  // mean(1-0.9,1-0.6,1-0.4,1-1.0)=mean(0.1,0.4,0.6,0.0)
			wantBestMeanFid:    0.3375, // mean(1-0.8,1-0.5,1-0.35,1-1.0)=mean(0.2,0.5,0.65,0.0)
			wantBestDurationMS: 180,    // 30+40+50+60
			wantFailModes: map[string]int{
				"below-threshold": 1,
				"wrong-length":    1,
				"wrong-glyphs":    1,
				"timeout":         1,
			},
		},
		{
			name: "unknown ground truth excluded from histogram and means",
			rows: []journalRow{
				// Row with known GT — should be counted.
				syntheticRow(
					"fix", "hi",
					attempt(statusOK, "", 100, 0.9, 0.1, 10),
					attempt(statusOK, "", 100, 0.95, 0.05, 20),
				),
				// Row with unknown GT (wild-style "—"): excluded from means + histogram.
				{
					Corpus:      "fix",
					Name:        "wild1",
					Kind:        "mosaic",
					GroundTruth: "—",
					ZeroConfig:  attempt(statusUnknown, "no ground truth", -1, 0.5, 0.5, 5),
					BestConfig:  attempt(statusUnknown, "no ground truth", -1, 0.6, 0.4, 15),
				},
			},
			wantBestOK:         1,
			wantBestSensical:   1,
			wantBestMeanScore:  100,
			wantZeroMeanConf:   0.9,
			wantBestMeanConf:   0.95,
			wantZeroMeanFid:    0.9,  // 1-0.1
			wantBestMeanFid:    0.95, // 1-0.05
			wantBestDurationMS: 35,   // 20+15 (duration counted for all rows)
			wantFailModes:      map[string]int{"ok": 1},
		},
		{
			name: "fidelity clamped to [0,1] when BestTotal > 1",
			rows: []journalRow{
				syntheticRow(
					"fix", "go",
					attempt(statusFail, whyBelow, 0, 0.1, 1.5, 10),
					attempt(statusFail, whyBelow, 0, 0.2, 1.5, 20),
				),
			},
			wantBestOK:         0,
			wantBestSensical:   0,
			wantBestMeanScore:  0,
			wantZeroMeanConf:   0.1,
			wantBestMeanConf:   0.2,
			wantZeroMeanFid:    0, // max(0, 1-1.5) = 0
			wantBestMeanFid:    0,
			wantBestDurationMS: 20,
			wantFailModes:      map[string]int{"below-threshold": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summariseCorpora(tc.rows)

			// Find the "fix" corpus summary.
			var cs *journalCorpusSummary
			for i := range got {
				if got[i].Name == "fix" {
					cs = &got[i]
					break
				}
			}
			if cs == nil {
				t.Fatal("summariseCorpora: no summary for corpus \"fix\"")
				return // unreachable after Fatal; satisfies staticcheck SA5011
			}

			if got, want := cs.BestOK, tc.wantBestOK; got != want {
				t.Errorf("BestOK: got %d, want %d", got, want)
			}
			if got, want := cs.BestSensical, tc.wantBestSensical; got != want {
				t.Errorf("BestSensical: got %d, want %d", got, want)
			}
			if got, want := cs.BestMeanScore, tc.wantBestMeanScore; !approxEq(got, want, 1e-9) {
				t.Errorf("BestMeanScore: got %.6f, want %.6f", got, want)
			}
			if got, want := cs.ZeroMeanConf, tc.wantZeroMeanConf; !approxEq(got, want, 1e-9) {
				t.Errorf("ZeroMeanConf: got %.6f, want %.6f", got, want)
			}
			if got, want := cs.BestMeanConf, tc.wantBestMeanConf; !approxEq(got, want, 1e-9) {
				t.Errorf("BestMeanConf: got %.6f, want %.6f", got, want)
			}
			if got, want := cs.ZeroMeanFidelity, tc.wantZeroMeanFid; !approxEq(got, want, 1e-9) {
				t.Errorf("ZeroMeanFidelity: got %.6f, want %.6f", got, want)
			}
			if got, want := cs.BestMeanFidelity, tc.wantBestMeanFid; !approxEq(got, want, 1e-9) {
				t.Errorf("BestMeanFidelity: got %.6f, want %.6f", got, want)
			}
			if got, want := cs.BestDurationMS, tc.wantBestDurationMS; !approxEq(got, want, 1e-9) {
				t.Errorf("BestDurationMS: got %.1f, want %.1f", got, want)
			}
			for bucket, wantCount := range tc.wantFailModes {
				if gotCount := cs.BestFailModes[bucket]; gotCount != wantCount {
					t.Errorf("BestFailModes[%q]: got %d, want %d", bucket, gotCount, wantCount)
				}
			}
			// Check no unexpected non-zero buckets.
			for bucket, gotCount := range cs.BestFailModes {
				if gotCount > 0 {
					if _, expected := tc.wantFailModes[bucket]; !expected {
						t.Errorf("BestFailModes[%q]: got %d, want 0 (unexpected bucket)", bucket, gotCount)
					}
				}
			}
		})
	}
}

// TestWhyToBucket guards the why→bucket mapping exhaustively.
func TestWhyToBucket(t *testing.T) {
	cases := []struct {
		name       string
		status     journalStatus
		why        string
		wantBucket string
	}{
		{name: "ok status", status: statusOK, why: "", wantBucket: "ok"},
		{name: "error status", status: statusError, why: "error: something", wantBucket: "error"},
		{name: "below-threshold", status: statusFail, why: "below-threshold / no confident candidate", wantBucket: "below-threshold"},
		{name: "timeout", status: statusFail, why: "timeout (no result in 30s)", wantBucket: "timeout"},
		{name: "wrong-length", status: statusFail, why: "wrong length (got 2 want 4)", wantBucket: "wrong-length"},
		{name: "wrong-glyphs", status: statusFail, why: "wrong glyphs (font fidelity / params)", wantBucket: "wrong-glyphs"},
		{name: "unrecognised why", status: statusFail, why: "something completely unexpected", wantBucket: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := whyToBucket(tc.status, tc.why)
			if got != tc.wantBucket {
				t.Errorf("whyToBucket(%q, %q): got %q, want %q", tc.status, tc.why, got, tc.wantBucket)
			}
		})
	}
}

// approxEq returns true when |a-b| < eps.
func approxEq(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}
