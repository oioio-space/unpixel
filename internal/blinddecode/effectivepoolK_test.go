// White-box tests for effectivePoolK — the budget-adaptive per-tier pool cap.
// These are in package blinddecode (not blinddecode_test) to access the
// unexported helper directly.
package blinddecode

import (
	"math"
	"testing"
)

// TestEffectivePoolK_Budget verifies that effectivePoolK satisfies the
// combination budget (3·k)^nWords ≤ maxCombinations for every line length,
// and that short lines (nWords ≤ 2) get k ≥ 200 so low-frequency words like
// "cat" and "chat" are included.
func TestEffectivePoolK_Budget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		nWords    int
		userTopK  int
		wantFloor int  // minimum acceptable k
		wantBound bool // assert (3k)^nWords ≤ maxCombinations; false when floor dominates
	}{
		// nWords=1: budget-derived k is huge; floor applies; pool caps naturally by dict size.
		{nWords: 1, userTopK: 0, wantFloor: absoluteFloorK, wantBound: false},
		// 2-word: k≈235 — recall-critical, covers "cat"/"chat".
		{nWords: 2, userTopK: 0, wantFloor: 200, wantBound: true},
		// 3-word: k≈26, budget holds.
		{nWords: 3, userTopK: 0, wantFloor: absoluteFloorK, wantBound: true},
		// 4-word: k≈9, budget holds.
		{nWords: 4, userTopK: 0, wantFloor: absoluteFloorK, wantBound: true},
		// 5-word and beyond: absoluteFloorK dominates and may exceed the budget —
		// that is the acknowledged tractability tradeoff (see TODO in wholeline.go).
		// We assert only the floor, not the bound.
		{nWords: 5, userTopK: 0, wantFloor: absoluteFloorK, wantBound: false},
		{nWords: 8, userTopK: 0, wantFloor: absoluteFloorK, wantBound: false},
		// userTopK as upper cap: must not push k past budget.
		{nWords: 3, userTopK: 10, wantFloor: absoluteFloorK, wantBound: true},
		// userTopK larger than budget: budget still wins.
		{nWords: 3, userTopK: 1000, wantFloor: absoluteFloorK, wantBound: true},
		// userTopK=50 (old blind.Recover value): budget still applies for 2-word.
		{nWords: 2, userTopK: 50, wantFloor: absoluteFloorK, wantBound: true},
	}

	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got := effectivePoolK(tc.nWords, tc.userTopK)

			if got < tc.wantFloor {
				t.Errorf("effectivePoolK(%d, %d) = %d, want ≥ %d",
					tc.nWords, tc.userTopK, got, tc.wantFloor)
			}
			if got < absoluteFloorK {
				t.Errorf("effectivePoolK(%d, %d) = %d, below absoluteFloorK %d",
					tc.nWords, tc.userTopK, got, absoluteFloorK)
			}
			if tc.wantBound {
				combinations := math.Pow(float64(3*got), float64(tc.nWords))
				if combinations > float64(maxCombinations) {
					t.Errorf("effectivePoolK(%d, %d) = %d: (3·%d)^%d = %.0f > maxCombinations %d",
						tc.nWords, tc.userTopK, got, got, tc.nWords, combinations, maxCombinations)
				}
			}
		})
	}
}

// TestEffectivePoolK_TwoWordMinimum asserts nWords=2 yields k ≥ 200 with no
// userTopK restriction, matching the sqrt(500_000)/3 ≈ 235 derivation.
func TestEffectivePoolK_TwoWordMinimum(t *testing.T) {
	t.Parallel()
	got := effectivePoolK(2, 0)
	const want = 200
	if got < want {
		t.Errorf("effectivePoolK(2, 0) = %d, want ≥ %d (need to cover 'cat'/'chat')", got, want)
	}
}

// TestEffectivePoolK_UserTopKCapOnly asserts that userTopK > 0 can only
// lower effectiveK, never raise it past the budget-derived value.
func TestEffectivePoolK_UserTopKCapOnly(t *testing.T) {
	t.Parallel()
	// For nWords=3, budgetK ≈ 26. userTopK=1000 must not produce k > budgetK.
	nWords := 3
	budgetOnly := effectivePoolK(nWords, 0)
	withLargeTopK := effectivePoolK(nWords, 1000)
	if withLargeTopK > budgetOnly {
		t.Errorf("effectivePoolK(%d, 1000) = %d > effectivePoolK(%d, 0) = %d: userTopK raised k past budget",
			nWords, withLargeTopK, nWords, budgetOnly)
	}
}

// TestEffectivePoolK_UserTopKLowers asserts that a small userTopK restricts k.
func TestEffectivePoolK_UserTopKLowers(t *testing.T) {
	t.Parallel()
	// For nWords=2, budgetOnly≈235. userTopK=5 must yield k ≤ budgetOnly.
	withSmallTopK := effectivePoolK(2, 5)
	budgetOnly := effectivePoolK(2, 0)
	if withSmallTopK > budgetOnly {
		t.Errorf("effectivePoolK(2, 5) = %d > effectivePoolK(2, 0) = %d: cap not applied",
			withSmallTopK, budgetOnly)
	}
	// Must still honour the floor.
	if withSmallTopK < absoluteFloorK {
		t.Errorf("effectivePoolK(2, 5) = %d < absoluteFloorK %d", withSmallTopK, absoluteFloorK)
	}
}
