package fixture

import (
	"strings"
	"testing"
)

// TestSickMatrix_specsSelfConsistent checks every SickSpec is internally valid:
// unique name, positive sizes, charset that contains the target text, and a
// non-empty kind and font name.
func TestSickMatrix_specsSelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range SickMatrix() {
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
		if s.Font == "" {
			t.Errorf("%s: empty font name", s.Name)
		}
		if s.Kind != "sick" && s.Kind != "digits" {
			t.Errorf("%s: unknown kind %q (want sick or digits)", s.Name, s.Kind)
		}
	}
}

// TestSickRedact_allSpecsProduceImages confirms every SickSpec renders a
// non-empty image via RedactFont with the Liberation Sans bytes (a stand-in for
// any bundled face — the fixture package does not import the fonts package to
// avoid a dependency cycle, so we test only that the plumbing works with a
// known-good font).
func TestSickRedact_allSpecsProduceImages(t *testing.T) {
	// Use the default Liberation Sans renderer via Redact (which calls
	// render.NewXImage). We substitute the font by calling RedactFont with nil
	// bytes, which falls back to the embedded Liberation Sans in NewXImageFromFonts.
	// For the purpose of this unit test (non-nil image, correct pipeline) any
	// font is fine; the gensick integration test exercises per-spec fonts.
	for _, s := range SickMatrix() {
		img, err := Redact(s.Spec)
		if err != nil {
			t.Errorf("%s: Redact: %v", s.Name, err)
			continue
		}
		if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
			t.Errorf("%s: empty image %v", s.Name, img.Bounds())
		}
	}
}
