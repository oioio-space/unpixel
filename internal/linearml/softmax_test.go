//go:build ml

package linearml_test

import (
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/linearml"
)

// TestSoftmax_learnsSeparable trains on a linearly-separable two-class problem and
// checks the model classifies both classes and returns a valid probability vector.
func TestSoftmax_learnsSeparable(t *testing.T) {
	samples := [][]float64{
		{0, 0}, {0.1, 0.1}, {0.2, 0}, {0, 0.2},
		{1, 1}, {0.9, 1}, {1, 0.9}, {0.8, 0.8},
	}
	labels := []int{0, 0, 0, 0, 1, 1, 1, 1}

	m := linearml.Train(samples, labels, 2, linearml.Options{})
	if m.NumClasses() != 2 {
		t.Fatalf("NumClasses = %d, want 2", m.NumClasses())
	}

	for i, x := range samples {
		p := m.Predict(x)
		if len(p) != 2 {
			t.Fatalf("Predict len = %d, want 2", len(p))
		}
		if sum := p[0] + p[1]; math.Abs(sum-1) > 1e-9 {
			t.Errorf("probs sum = %.6f, want 1", sum)
		}
		got := 0
		if p[1] > p[0] {
			got = 1
		}
		if got != labels[i] {
			t.Errorf("sample %v classified %d, want %d (p=%.3f)", x, got, labels[i], p)
		}
	}
}

// TestSoftmax_emptyIsZeroModel checks Train on no data yields a usable zero-weight
// model (uniform probabilities), not a panic.
func TestSoftmax_emptyIsZeroModel(t *testing.T) {
	m := linearml.Train(nil, nil, 3, linearml.Options{})
	p := m.Predict([]float64{1, 2, 3})
	if len(p) != 3 {
		t.Fatalf("Predict len = %d, want 3", len(p))
	}
	for _, v := range p {
		if math.Abs(v-1.0/3) > 1e-9 {
			t.Errorf("zero model prob = %.6f, want uniform 1/3", v)
		}
	}
}
