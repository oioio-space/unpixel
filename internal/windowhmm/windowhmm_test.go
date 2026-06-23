package windowhmm

import (
	"math"
	"testing"
)

// TestWindowVectorBasic verifies the basic three-channel normalisation for a
// 2-row × 3-col grid, extracting a window of width 2 starting at column 0.
func TestWindowVectorBasic(t *testing.T) {
	t.Parallel()
	// 2-row × 3-col grid. Row 0: [255,0,0], [0,255,0], [0,0,255].
	// Row 1: [128,128,128], [64,64,64], [0,0,0].
	grid := [][]BlockCell{
		{{R: 255, G: 0, B: 0}, {R: 0, G: 255, B: 0}, {R: 0, G: 0, B: 255}},
		{{R: 128, G: 128, B: 128}, {R: 64, G: 64, B: 64}, {R: 0, G: 0, B: 0}},
	}
	// Window [0, 2): columns 0 and 1. Length = 2 rows × 2 cols × 3 channels = 12.
	got := WindowVector(grid, 0, 2)
	if len(got) != 12 {
		t.Fatalf("vector length: got %d, want 12", len(got))
	}
	// Row 0, col 0: [1.0, 0.0, 0.0].
	const eps = 1e-9
	wantRow0Col0 := []float64{1.0, 0.0, 0.0}
	for i, w := range wantRow0Col0 {
		if math.Abs(got[i]-w) > eps {
			t.Errorf("v[%d]: got %.6f, want %.6f", i, got[i], w)
		}
	}
	// Row 0, col 1: [0.0, 1.0, 0.0].
	wantRow0Col1 := []float64{0.0, 1.0, 0.0}
	for i, w := range wantRow0Col1 {
		if math.Abs(got[3+i]-w) > eps {
			t.Errorf("v[%d]: got %.6f, want %.6f", 3+i, got[3+i], w)
		}
	}
}

// TestWindowVectorNilCases verifies that out-of-range or degenerate inputs
// return nil rather than panicking.
func TestWindowVectorNilCases(t *testing.T) {
	t.Parallel()
	twoCol := [][]BlockCell{
		{{R: 0, G: 0, B: 0}, {R: 0, G: 0, B: 0}},
	}
	cases := []struct {
		name            string
		grid            [][]BlockCell
		colStart, width int
	}{
		{"nil grid", nil, 0, 1},
		{"zero width", twoCol, 0, 0},
		{"negative start", twoCol, -1, 1},
		{"window past end", twoCol, 1, 2}, // 1+2 > 2
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WindowVector(tc.grid, tc.colStart, tc.width)
			if got != nil {
				t.Errorf("WindowVector: got %v, want nil", got)
			}
		})
	}
}
