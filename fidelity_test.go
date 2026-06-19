package unpixel_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
)

// TestResult_Fidelity maps BestTotal to a [0,1] whole-image confidence, 0 for
// an empty guess.
func TestResult_Fidelity(t *testing.T) {
	cases := []struct {
		guess string
		total float64
		want  float64
	}{
		{"go", 0.0, 1.0},
		{"go", 0.25, 0.75},
		{"go", 1.0, 0.0},
		{"", 0.0, 0.0},   // empty guess → no confidence despite total 0
		{"go", 2.0, 0.0}, // clamped
	}
	for _, c := range cases {
		got := unpixel.Result{BestGuess: c.guess, BestTotal: c.total}.Fidelity()
		if got != c.want {
			t.Errorf("Fidelity(guess=%q total=%v) = %v, want %v", c.guess, c.total, got, c.want)
		}
	}
}
