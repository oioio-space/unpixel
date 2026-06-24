package varfont

// optimizer_test.go — white-box unit tests for the nelderMead function.
// These live in package varfont (not varfont_test) to access the unexported
// nelderMead symbol directly.

import (
	"errors"
	"math"
	"testing"
)

// quadratic1D is a simple 1-axis bowl: f(x) = (x - 5.0)² with minimum at x=5.
func quadratic1D(v []float32) (float64, error) {
	x := float64(v[0])
	return (x - 5.0) * (x - 5.0), nil
}

// TestNelderMead_1D verifies convergence on a univariate bowl.
func TestNelderMead_1D(t *testing.T) {
	specs := []AxisSpec{{Tag: "x", Min: 0, Max: 10, Start: 1}}
	start := []float32{1.0}

	best, bestVal, evals, err := nelderMead(specs, start, 1.0, 200, quadratic1D)
	if err != nil {
		t.Fatalf("nelderMead: unexpected error: %v", err)
	}
	if evals == 0 {
		t.Error("nelderMead: evals = 0, want > 0")
	}
	if got, want := math.Abs(float64(best[0])-5.0), 0.1; got > want {
		t.Errorf("best[0]: got %.4f, want within %.2f of 5.0", best[0], want)
	}
	if bestVal > 0.01 {
		t.Errorf("bestVal: got %.6f, want <= 0.01 (near minimum)", bestVal)
	}
}

// TestNelderMead_2D verifies convergence on a coupled 2-axis bowl:
// f(x,y) = (x−3)² + (y−7)² + 0.5*(x−3)*(y−7)
// The cross-term couples the axes so coordinate descent stalls; Nelder-Mead
// should still converge. This exercises the multi-axis simplex path including
// expansion, contraction, and (probabilistically) the shrink step.
func TestNelderMead_2D(t *testing.T) {
	coupled := func(v []float32) (float64, error) {
		x, y := float64(v[0])-3.0, float64(v[1])-7.0
		return x*x + y*y + 0.5*x*y, nil
	}

	specs := []AxisSpec{
		{Tag: "x", Min: 0, Max: 10, Start: 0},
		{Tag: "y", Min: 0, Max: 10, Start: 0},
	}
	start := []float32{0.0, 0.0}

	best, bestVal, evals, err := nelderMead(specs, start, 2.0, 500, coupled)
	if err != nil {
		t.Fatalf("nelderMead 2D: unexpected error: %v", err)
	}
	if evals == 0 {
		t.Error("nelderMead 2D: evals = 0")
	}
	if got, want := math.Abs(float64(best[0])-3.0), 0.5; got > want {
		t.Errorf("best[0]: got %.4f, want within %.2f of 3.0", best[0], want)
	}
	if got, want := math.Abs(float64(best[1])-7.0), 0.5; got > want {
		t.Errorf("best[1]: got %.4f, want within %.2f of 7.0", best[1], want)
	}
	if bestVal > 0.5 {
		t.Errorf("bestVal: got %.6f, want <= 0.5", bestVal)
	}
}

// TestNelderMead_ErrorPropagates verifies that an error returned by f is
// propagated back to the caller without panic, covering the error-return path.
func TestNelderMead_ErrorPropagates(t *testing.T) {
	sentinel := errors.New("eval failed")
	calls := 0
	failAfter := func(v []float32) (float64, error) {
		calls++
		if calls > 3 {
			return 0, sentinel
		}
		return float64(v[0]) * float64(v[0]), nil
	}

	specs := []AxisSpec{{Tag: "x", Min: 0, Max: 10, Start: 5}}
	_, _, _, err := nelderMead(specs, []float32{5.0}, 1.0, 100, failAfter)
	if err == nil {
		t.Fatal("nelderMead: got nil error, want sentinel error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("nelderMead: got %v, want sentinel %v", err, sentinel)
	}
}

// TestNelderMead_ShrinkPath exercises the shrink-all branch by using a
// landscape where contractions always fail (contracted point ≥ worst vertex).
// A piecewise constant function (rounded) forces the simplex to shrink.
func TestNelderMead_ShrinkPath(t *testing.T) {
	// Piecewise constant: f(x) = floor(|x - 5|). Flat within each unit —
	// contraction moves land at the same cost as the worst vertex, triggering
	// the shrink step when the simplex straddles a step boundary.
	staircase := func(v []float32) (float64, error) {
		return math.Floor(math.Abs(float64(v[0]) - 5.0)), nil
	}

	specs := []AxisSpec{{Tag: "x", Min: 0, Max: 10, Start: 0.5}}
	best, _, evals, err := nelderMead(specs, []float32{0.5}, 3.0, 100, staircase)
	if err != nil {
		t.Fatalf("nelderMead staircase: %v", err)
	}
	if evals == 0 {
		t.Error("evals = 0")
	}
	// The minimum is 0 on [4,6]; best[0] should land near 5.
	if got, want := math.Abs(float64(best[0])-5.0), 2.0; got > want {
		t.Errorf("best[0]: got %.4f, want within %.1f of 5.0", best[0], want)
	}
}
