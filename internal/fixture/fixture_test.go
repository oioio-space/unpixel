package fixture

import (
	"bytes"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel/fonts"
)

// liberationSansBytes returns the bundled Liberation Sans TTF bytes for tests
// that need to drive the *Font redact paths with a caller-supplied font.
func liberationSansBytes(t *testing.T) []byte {
	t.Helper()
	for _, f := range fonts.All() {
		if f.Name == "Liberation Sans" {
			return f.Data
		}
	}
	t.Fatal("bundled font \"Liberation Sans\" not found")
	return nil
}

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

// TestRedactor_matchesRedact verifies the reused-renderer Redactor produces the
// same bytes as the one-shot Redact for the same spec (the only difference is
// that the font is parsed once), and that a second call reuses the renderer.
func TestRedactor_matchesRedact(t *testing.T) {
	s := Matrix()[0]
	want, err := Redact(s)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	rd, err := NewRedactor()
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	got, err := rd.Redact(s)
	if err != nil {
		t.Fatalf("Redactor.Redact: %v", err)
	}
	if got.Bounds() != want.Bounds() || !bytes.Equal(got.Pix, want.Pix) {
		t.Error("Redactor.Redact differs from Redact for the same spec")
	}
	// A second call on the reused renderer must still be correct.
	if again, err := rd.Redact(s); err != nil || !bytes.Equal(again.Pix, want.Pix) {
		t.Errorf("Redactor.Redact second call: err=%v, bytes-equal=%v", err, err == nil && bytes.Equal(again.Pix, want.Pix))
	}
}

// TestNewRedactorFont_rendersWithSuppliedFont verifies NewRedactorFont uses the
// supplied font bytes (the embedded Liberation Sans here) and redacts cleanly.
func TestNewRedactorFont_rendersWithSuppliedFont(t *testing.T) {
	rd, err := NewRedactorFont(liberationSansBytes(t), nil)
	if err != nil {
		t.Fatalf("NewRedactorFont: %v", err)
	}
	img, err := rd.Redact(Matrix()[0])
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if img.Bounds().Empty() {
		t.Error("NewRedactorFont produced an empty image")
	}
}

// TestFile_returnsPNGName verifies that File() appends ".png" to the spec Name.
func TestFile_returnsPNGName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"block08_go", "block08_go.png"},
		{"bold_go", "bold_go.png"},
	}
	for _, tc := range cases {
		s := Spec{Name: tc.name}
		if got := s.File(); got != tc.want {
			t.Errorf("Spec{Name:%q}.File() = %q, want %q", tc.name, got, tc.want)
		}
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
