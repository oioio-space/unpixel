package fixture

import (
	"os"
	"path/filepath"
	"runtime"
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

// TestSickFile_returnsPNGName verifies that SickFile() appends ".png" to
// the spec Name, matching the manifest filename convention.
func TestSickFile_returnsPNGName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want string
	}{
		{"sick01_hello", "sick01_hello.png"},
		{"digits_42", "digits_42.png"},
	}
	for _, tc := range cases {
		s := SickSpec{Spec: Spec{Name: tc.name}}
		if got := s.SickFile(); got != tc.want {
			t.Errorf("SickSpec{Name:%q}.SickFile() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRedactFont_withEmbeddedFont verifies that RedactFont produces a non-empty
// image when given real font bytes. The Liberation Sans TTF embedded in the
// render package is located via runtime.Caller so the test is cwd-independent.
func TestRedactFont_withEmbeddedFont(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	fontPath := filepath.Join(filepath.Dir(thisFile), "..", "render", "fonts", "LiberationSans-Regular.ttf")
	regularTTF, err := os.ReadFile(fontPath)
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	s := SickMatrix()[0].Spec
	img, err := RedactFont(s, regularTTF, nil)
	if err != nil {
		t.Fatalf("RedactFont: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Errorf("RedactFont returned empty image %v", img.Bounds())
	}
}
