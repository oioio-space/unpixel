package fixture

import (
	"bytes"
	"strings"
	"testing"
)

// TestMatrix_specsSelfConsistent checks every spec is internally valid: unique
// name, positive sizes, and a charset that actually contains the target text
// (otherwise the engine could never recover it).
func TestMatrix_specsSelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Matrix() {
		if s.Name == "" || seen[s.Name] {
			t.Errorf("empty or duplicate spec name %q", s.Name)
		}
		seen[s.Name] = true
		if s.Text == "" {
			t.Errorf("%s: empty text", s.Name)
		}
		if s.BlockSize <= 0 || s.FontSize <= 0 {
			t.Errorf("%s: non-positive block (%d) or font (%.0f)", s.Name, s.BlockSize, s.FontSize)
		}
		for _, r := range s.Text {
			if !strings.ContainsRune(s.Charset, r) {
				t.Errorf("%s: charset is missing %q from text %q", s.Name, r, s.Text)
			}
		}
	}
}

// TestRedact_deterministic ensures the generator is reproducible, so committed
// images stay in sync with `go generate`.
func TestRedact_deterministic(t *testing.T) {
	s := Matrix()[0]
	a, err := Redact(s)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	b, err := Redact(s)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if a.Bounds() != b.Bounds() || !bytes.Equal(a.Pix, b.Pix) {
		t.Error("Redact is not deterministic for the same spec")
	}
}

// TestRedact_allSpecsProduceImages confirms every spec renders a non-empty image.
func TestRedact_allSpecsProduceImages(t *testing.T) {
	for _, s := range Matrix() {
		img, err := Redact(s)
		if err != nil {
			t.Errorf("%s: Redact: %v", s.Name, err)
			continue
		}
		if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
			t.Errorf("%s: empty image %v", s.Name, img.Bounds())
		}
	}
}
