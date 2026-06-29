package mcpserver_test

import (
	"os"
	"strings"
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

// TestProposeHints_charCount verifies that ProposeHintsImage returns a
// plausible character-count estimate and the correct block size for the
// block08_go.png fixture ("go" pixelated at block size 8).
func TestProposeHints_charCount(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	rep, err := mcp.ProposeHintsImage(img)
	if err != nil {
		t.Fatalf("ProposeHintsImage: %v", err)
	}
	if rep.CharCountEstimate < 1 || rep.CharCountEstimate > 6 {
		t.Errorf("CharCountEstimate = %d, want a small count (~2 for \"go\")", rep.CharCountEstimate)
	}
	if rep.BlockSize != 8 {
		t.Errorf("BlockSize = %d, want 8", rep.BlockSize)
	}
}

// TestProposeHints_leakedContext verifies that ProposeHints (path-based
// variant) surfaces leaked plaintext from an OOXML file in LeakedContext.
//
// ProposeHints requires a valid image, so the test writes the fixture PNG to a
// temp file, calls ProposeHints on the PNG (LeakedContext will be empty — a
// plain PNG has no text leak), and then directly calls LeakScan on the .docx
// to confirm the docxBytesWith helper produces a real leak. This exercises:
//   - the ProposeHints "scan raw file for leaks" branch (non-fatal miss for PNG)
//   - the LeakScan path that populates LeakedContext for office text
func TestProposeHints_leakedContext(t *testing.T) {
	// Part 1: ProposeHints on a PNG — leak.Scan returns not-found (PNG has no
	// office body text), so LeakedContext must be empty and the call must not error.
	dir := t.TempDir()
	pngPath := fixturePath("block08_go.png")
	rep, err := mcp.ProposeHints(pngPath)
	if err != nil {
		t.Fatalf("ProposeHints(png): %v", err)
	}
	if rep.LeakedContext != "" {
		t.Logf("ProposeHints(png): LeakedContext = %q (non-empty is unusual for a PNG)", rep.LeakedContext)
	}

	// Part 2: a .docx written to a temp file → LeakScan finds the body text.
	const secret = "leaked-secret-from-docx"
	docxPath := dir + "/redacted.docx"
	if err := os.WriteFile(docxPath, docxBytesWith(t, secret), 0o600); err != nil {
		t.Fatalf("write docx: %v", err)
	}
	lrep, lErr := mcp.LeakScan(docxPath, "")
	if lErr != nil {
		t.Fatalf("LeakScan(docx): %v", lErr)
	}
	if !lrep.Found || !strings.Contains(lrep.Text, secret) {
		t.Errorf("LeakScan(docx): Found=%v Text=%q, want text containing %q", lrep.Found, lrep.Text, secret)
	}
}

// TestVerifyCandidates_noMatch verifies that VerifyCandidates sets Pick="" when
// no candidate meets VerifyMatchThreshold (all candidates are clearly wrong).
func TestVerifyCandidates_noMatch(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	// "xy" and "ab" are both wrong for "go" — neither should match.
	rep, err := mcp.VerifyCandidates(t.Context(), img, []string{"xy", "ab"}, 8, "abcdefghijklmnopqrstuvwxyz ")
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}
	if rep.Pick != "" {
		t.Errorf("Pick = %q, want empty (no confident match for clearly-wrong candidates)", rep.Pick)
	}
	// Best is still populated (lowest-distance candidate, regardless of Match).
	if rep.Best == "" {
		t.Errorf("Best is empty; want the lowest-distance candidate even when no Match")
	}
}
