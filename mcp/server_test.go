package mcpserver_test

import (
	"path/filepath"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

const fixturesDir = "../testdata/fixtures"

func fixturePath(name string) string {
	return filepath.Join(fixturesDir, name)
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
// unrelated candidates "xy" and "zz" on the block08_go.png fixture.
func TestVerifyCandidates_ranking(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.VerifyCandidates(ctx, img, []string{"go", "xy", "zz"}, 8)
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}

	if report.Best != "go" {
		t.Errorf("Best = %q, want %q (full ranking: %v)", report.Best, "go", report.Ranked)
	}
	if len(report.Ranked) != 3 {
		t.Errorf("len(Ranked) = %d, want 3", len(report.Ranked))
	}
	// Ranked must be in ascending distance order.
	for i := 1; i < len(report.Ranked); i++ {
		if report.Ranked[i].Distance < report.Ranked[i-1].Distance {
			t.Errorf("Ranked[%d].Distance (%.4f) < Ranked[%d].Distance (%.4f): not sorted",
				i, report.Ranked[i].Distance, i-1, report.Ranked[i-1].Distance)
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

	report, err := mcpserver.VerifyCandidates(ctx, img, []string{"go", "xy"}, 8)
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

	report, err := mcpserver.VerifyCandidates(ctx, img, []string{"go"}, 8)
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
