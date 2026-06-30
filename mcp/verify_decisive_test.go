package mcpserver_test

import (
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

// TestVerifyCandidates_decisivePick verifies that VerifyCandidates sets Pick to
// the true candidate ("go") when the faithful forward model scores it below
// VerifyMatchThreshold and all others above it.
func TestVerifyCandidates_decisivePick(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep, err := mcp.VerifyCandidates(t.Context(), img, []string{"go", "ab", "xy"}, 8, "go abcde", 0)
	if err != nil {
		t.Fatalf("VerifyCandidates: %v", err)
	}
	if rep.Pick != "go" {
		t.Errorf("Pick = %q, want %q (decisive physical match)", rep.Pick, "go")
	}
}
