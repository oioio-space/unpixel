package capacity

// internal_test.go exercises unexported helpers that are not reachable through
// the exported API with the inputs required to cover specific branches.

import "testing"

// TestNormalise_zeroVector verifies that normalise leaves a zero vector
// unchanged (the sum==0 early-return path).
func TestNormalise_zeroVector(t *testing.T) {
	t.Parallel()
	sig := []float64{0, 0, 0}
	normalise(sig)
	for i, v := range sig {
		if v != 0 {
			t.Errorf("normalise(zero)[%d] = %v, want 0", i, v)
		}
	}
}

// TestNormalise_unitVector verifies that normalise scales a non-zero vector
// to unit L2 length.
func TestNormalise_unitVector(t *testing.T) {
	t.Parallel()
	sig := []float64{3, 4} // L2 = 5
	normalise(sig)
	// After normalisation: {0.6, 0.8}; L2 must be 1.
	var sumSq float64
	for _, v := range sig {
		sumSq += v * v
	}
	const tol = 1e-9
	if sumSq < 1-tol || sumSq > 1+tol {
		t.Errorf("normalise: L2² = %v, want 1.0", sumSq)
	}
}

// TestRecomputeCentroid_emptyMembers verifies the len(members)==0 early-return
// path: recomputeCentroid returns nil for an empty member list.
func TestRecomputeCentroid_emptyMembers(t *testing.T) {
	t.Parallel()
	got := recomputeCentroid(nil, map[rune]int{}, nil)
	if got != nil {
		t.Errorf("recomputeCentroid(nil, ...) = %v, want nil", got)
	}
}

// TestRecomputeCentroid_missingRuneInIndex verifies the !ok branch: when a
// member rune is absent from runeIdx the function skips it without panicking.
func TestRecomputeCentroid_missingRuneInIndex(t *testing.T) {
	t.Parallel()
	// 'a' maps to sig [1.0]; 'b' is absent from the index.
	members := []rune{'a', 'b'}
	runeIdx := map[rune]int{'a': 0}
	allSigs := [][]float64{{1.0}}
	got := recomputeCentroid(members, runeIdx, allSigs)
	// Only 'a' contributes; centroid = [1.0/2] = [0.5] (divided by len(members)=2).
	if len(got) != 1 {
		t.Fatalf("recomputeCentroid: got len=%d, want 1", len(got))
	}
	const want = 0.5
	if got[0] != want {
		t.Errorf("recomputeCentroid: got[0] = %v, want %v", got[0], want)
	}
}
