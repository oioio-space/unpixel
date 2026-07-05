package defaults

import "testing"

// TestAlignPhaseStep pins the sub-block phase increment alignedDist sweeps. The
// block=32 case is the critical no-regression invariant: it must equal the original
// fixed step of 8 (phases 0,8,16,24) so real block-32 redactions (e.g. hello-world)
// score byte-identically. Small blocks must yield a step < block so alignedDist
// actually sweeps sub-block phases instead of collapsing to phase 0.
func TestAlignPhaseStep(t *testing.T) {
	tests := []struct {
		block, want int
	}{
		{block: 32, want: 8}, // no-regression invariant: identical to the old const
		{block: 16, want: 4},
		{block: 8, want: 2}, // was 8 (== block) → only phase 0; now sweeps 0,2,4,6
		{block: 5, want: 1},
		{block: 4, want: 1},
		{block: 2, want: 1}, // never 0: the loop increment must stay positive
		{block: 1, want: 1},
	}
	for _, tc := range tests {
		if got := alignPhaseStep(tc.block); got != tc.want {
			t.Errorf("alignPhaseStep(%d) = %d, want %d", tc.block, got, tc.want)
		}
		// Invariant: the step must divide the block into at least one full sweep and
		// never be zero (which would make the alignedDist phase loop spin forever).
		if got := alignPhaseStep(tc.block); got < 1 {
			t.Errorf("alignPhaseStep(%d) = %d, must be ≥ 1", tc.block, got)
		}
	}
}
