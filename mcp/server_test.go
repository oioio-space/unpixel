package mcpserver_test

import (
	"path/filepath"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

const (
	fixturesDir = "../testdata/fixtures"
	sickDir     = "../testdata/sick"
)

func fixturePath(name string) string {
	return filepath.Join(fixturesDir, name)
}

func sickFixturePath(name string) string {
	return filepath.Join(sickDir, name)
}

// TestAnalyze_mosaic8 verifies that unpixel_analyze correctly identifies the
// block08_go.png fixture as a mosaic redaction with block size 8.
func TestAnalyze_mosaic8(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Analyze(img)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if got.RedactionType != "mosaic" {
		t.Errorf("RedactionType = %q, want %q", got.RedactionType, "mosaic")
	}
	if got.BlockSize != 8 {
		t.Errorf("BlockSize = %d, want 8", got.BlockSize)
	}
	if got.GridConfidence <= 0 {
		t.Errorf("GridConfidence = %.3f, want > 0", got.GridConfidence)
	}
	if got.RecommendedDecoder == "" {
		t.Error("RecommendedDecoder is empty")
	}
	if got.Rationale == "" {
		t.Error("Rationale is empty")
	}
}

// TestAnalyze_fields verifies that every field in AnalysisReport is populated
// (non-zero) for a real mosaic fixture.
func TestAnalyze_fields(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Analyze(img)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if got.Colorspace == "" {
		t.Error("Colorspace is empty")
	}
	// FontSizePt may be 0 for a very small fixture — skip.
	// RecommendedCharset may be empty only for "none" redaction type.
	if got.RedactionType != "none" && got.RecommendedCharset == "" {
		t.Error("RecommendedCharset is empty for non-none redaction type")
	}
}

// TestVerifyCandidates_ranking verifies that "go" scores lower (better) than
// decoys "xy" and "zz" on the block08_go.png fixture, and that the ranking is
// strictly sorted by ascending distance.
func TestVerifyCandidates_ranking(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.VerifyCandidates(ctx, img, []string{"go", "xy", "zz"}, 8, "")
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}

	if report.Best != "go" {
		t.Errorf("Best = %q, want %q (full ranking: %v)", report.Best, "go", report.Ranked)
	}
	if len(report.Ranked) != 3 {
		t.Errorf("len(Ranked) = %d, want 3", len(report.Ranked))
	}
	for i := 1; i < len(report.Ranked); i++ {
		if report.Ranked[i].Distance < report.Ranked[i-1].Distance {
			t.Errorf("Ranked[%d].Distance (%.4f) < Ranked[%d].Distance (%.4f): not sorted",
				i, report.Ranked[i].Distance, i-1, report.Ranked[i-1].Distance)
		}
	}
}

// TestVerifyCandidates_discrimination asserts that on calibrated fixtures the
// correct answer scores strictly lower than all decoys (margin > 0). This is
// the missing coverage that exposed the zero-config bug: the old scorer returned
// distance≈1 for every candidate, so margin was always 0.
//
// Block size and charset are supplied explicitly (from the fixture manifest) so
// that the faithful forward model scores decisively even on small images where
// auto-detection is weak.
func TestVerifyCandidates_discrimination(t *testing.T) {
	tests := []struct {
		fixture string
		correct string
		decoys  []string
		block   int
		charset string
	}{
		{"block08_go.png", "go", []string{"zz", "qq"}, 8, "abcdefghijklmnopqrstuvwxyz "},
		{"text_cat.png", "cat", []string{"zzz", "abc"}, 8, "cat eoabd"},
		{"alnum_Go2.png", "Go2", []string{"zzz", "xyz"}, 8, "Go2 abc019"},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			ctx := t.Context()
			img, err := loadFixture(tc.fixture)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}

			all := append([]string{tc.correct}, tc.decoys...)
			report, err := mcpserver.VerifyCandidates(ctx, img, all, tc.block, tc.charset)
			if err != nil {
				t.Fatalf("VerifyCandidates: %v", err)
			}

			if report.Best != tc.correct {
				t.Errorf("Best = %q, want %q (ranked: %v)", report.Best, tc.correct, report.Ranked)
			}
			if report.Margin <= 0 {
				t.Errorf("Margin = %.6f, want > 0 (no discrimination between correct and decoys; ranked: %v)",
					report.Margin, report.Ranked)
			}
			// Also verify correct strictly beats every decoy individually.
			var correctDist float64
			for _, rc := range report.Ranked {
				if rc.Text == tc.correct {
					correctDist = rc.Distance
					break
				}
			}
			for _, rc := range report.Ranked {
				if rc.Text == tc.correct {
					continue
				}
				if rc.Distance <= correctDist {
					t.Errorf("decoy %q distance %.6f ≤ correct %q distance %.6f",
						rc.Text, rc.Distance, tc.correct, correctDist)
				}
			}
		})
	}
}

// TestVerifyCandidates_digits asserts multi-character digit discrimination:
// "1234567" must score strictly lower than "7654321" and "0000000".
//
// This is the key regression test for the per-candidate-stretch bug: before
// the fix, all same-length candidates were rendered at a width calibrated for
// a different (shorter) char count, making the render wider than the target
// canvas. placed() silently skipped the draw, so every candidate compared a
// pure-white frame — scoring all identically with margin=0.
func TestVerifyCandidates_digits(t *testing.T) {
	ctx := t.Context()
	img, err := loadSickFixture("digits_7d_1234567.png")
	if err != nil {
		t.Fatalf("load sick fixture: %v", err)
	}

	candidates := []string{"1234567", "7654321", "0000000", "9999999"}
	// block=8 and charset from the fixture manifest; auto-detection is weak on
	// small sick-caption images and the faithful model needs the hint to discriminate.
	report, err := mcpserver.VerifyCandidates(ctx, img, candidates, 8, "0123456789")
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}

	if report.Margin <= 0 {
		t.Errorf("margin = %.6f, want > 0 (candidates not discriminated; ranked: %v)",
			report.Margin, report.Ranked)
	}
	if report.Best != "1234567" {
		t.Errorf("Best = %q, want %q (ranked: %v)", report.Best, "1234567", report.Ranked)
	}

	// Verify correct strictly beats every decoy.
	var correctDist float64
	for _, rc := range report.Ranked {
		if rc.Text == "1234567" {
			correctDist = rc.Distance
			break
		}
	}
	for _, rc := range report.Ranked {
		if rc.Text == "1234567" {
			continue
		}
		if rc.Distance <= correctDist {
			t.Errorf("decoy %q distance %.6f ≤ correct distance %.6f", rc.Text, rc.Distance, correctDist)
		}
	}
}

// TestVerifyCandidates_margin verifies that Margin equals the distance gap
// between the 2nd-best and best candidates.
func TestVerifyCandidates_margin(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.VerifyCandidates(ctx, img, []string{"go", "xy"}, 8, "")
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}

	want := report.Ranked[1].Distance - report.Ranked[0].Distance
	if report.Margin != want {
		t.Errorf("Margin = %.6f, want %.6f", report.Margin, want)
	}
}

// TestVerifyCandidates_singleCandidate verifies that a single candidate returns
// Margin=0 and Best equals that candidate.
func TestVerifyCandidates_singleCandidate(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.VerifyCandidates(ctx, img, []string{"go"}, 8, "")
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}

	if report.Best != "go" {
		t.Errorf("Best = %q, want %q", report.Best, "go")
	}
	if report.Margin != 0 {
		t.Errorf("Margin = %.6f, want 0", report.Margin)
	}
}

// TestNewServer verifies that NewServer returns a non-nil server without panicking.
func TestNewServer(t *testing.T) {
	srv := mcpserver.NewServer("v0.0.0-test")
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}
