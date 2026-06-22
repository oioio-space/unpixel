package blurfixture_test

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture/blurfixture"
)

// TestRedact_matrix renders+blurs every blur fixture spec and checks the output
// is a non-empty image, covering Matrix/ConnectSpecs/Style/Redact.
func TestRedact_matrix(t *testing.T) {
	specs := append(blurfixture.Matrix(), blurfixture.ConnectSpecs()...)
	if len(specs) == 0 {
		t.Fatal("no blur fixture specs")
	}
	for _, s := range specs {
		img, err := blurfixture.Redact(s)
		if err != nil {
			t.Errorf("Redact(%s): %v", s.Name, err)
			continue
		}
		if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
			t.Errorf("Redact(%s): empty image", s.Name)
		}
		if s.Style().FontSize <= 0 {
			t.Errorf("%s: Style().FontSize = %v, want > 0", s.Name, s.Style().FontSize)
		}
	}
}
