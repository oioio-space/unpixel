package windowhmm

import (
	"math"
	"testing"
)

// --- Viterbi tests ---

// TestViterbiTinyBruteForce verifies Viterbi against an exhaustive brute-force
// path search on a 2-state, 2-observation, 3-step HMM where the optimal path
// is unambiguous.
//
// Model:
//
//	States:  s0="A", s1="B"
//	Obs:     0, 1
//	LogPi:   s0=log(0.8)  s1=log(0.2)
//	LogTrans: s0→s0=log(0.7) s0→s1=log(0.3)
//	          s1→s0=log(0.4) s1→s1=log(0.6)
//	LogB:    s0=[log(0.9), log(0.1)]
//	         s1=[log(0.2), log(0.8)]
func TestViterbiTinyBruteForce(t *testing.T) {
	t.Parallel()

	states := []string{"A", "B"}
	stateID := map[string]int{"A": 0, "B": 1}
	logPi := []float64{math.Log(0.8), math.Log(0.2)}
	logTrans := []map[int]float64{
		0: {0: math.Log(0.7), 1: math.Log(0.3)},
		1: {0: math.Log(0.4), 1: math.Log(0.6)},
	}
	logB := [][]float64{
		{math.Log(0.9), math.Log(0.1)},
		{math.Log(0.2), math.Log(0.8)},
	}

	m := &Model{
		StateID:  stateID,
		States:   states,
		K:        2,
		LogPi:    logPi,
		LogTrans: logTrans,
		LogB:     logB,
		W:        1,
	}

	obs := []int{0, 1, 0} // observation sequence

	// Brute-force: enumerate all 2³=8 paths and find the best.
	S := 2
	bestScore := math.Inf(-1)
	var bestPath []int
	for mask := range 1 << (S * len(obs)) {
		path := make([]int, len(obs))
		tmp := mask
		for step := range len(obs) {
			path[step] = tmp % S
			tmp /= S
		}
		score := logPi[path[0]] + logB[path[0]][obs[0]]
		for step := 1; step < len(obs); step++ {
			score += logTrans[path[step-1]][path[step]] + logB[path[step]][obs[step]]
		}
		if score > bestScore {
			bestScore = score
			bestPath = append([]int{}, path...)
		}
	}

	got := m.Viterbi(obs)

	if len(got) != len(bestPath) {
		t.Fatalf("Viterbi path length: got %d, want %d", len(got), len(bestPath))
	}
	for step, s := range got {
		if s != bestPath[step] {
			t.Errorf("Viterbi path[%d]: got %d, want %d (full path: got %v want %v)",
				step, s, bestPath[step], got, bestPath)
			break
		}
	}
}

// TestViterbiEmpty verifies that an empty observation sequence returns nil.
func TestViterbiEmpty(t *testing.T) {
	t.Parallel()
	m := &Model{
		States:   []string{"A"},
		StateID:  map[string]int{"A": 0},
		K:        1,
		LogPi:    []float64{0},
		LogTrans: []map[int]float64{nil},
		LogB:     [][]float64{{0}},
		W:        1,
	}
	if got := m.Viterbi(nil); got != nil {
		t.Errorf("Viterbi(nil): got %v, want nil", got)
	}
	if got := m.Viterbi([]int{}); got != nil {
		t.Errorf("Viterbi([]): got %v, want nil", got)
	}
}

// TestViterbiSingleObs verifies a single-observation sequence picks the state
// with the highest π·B.
func TestViterbiSingleObs(t *testing.T) {
	t.Parallel()
	m := &Model{
		States:   []string{"A", "B"},
		StateID:  map[string]int{"A": 0, "B": 1},
		K:        2,
		LogPi:    []float64{math.Log(0.6), math.Log(0.4)},
		LogTrans: []map[int]float64{nil, nil},
		LogB: [][]float64{
			{math.Log(0.1), math.Log(0.9)},
			{math.Log(0.8), math.Log(0.2)},
		},
		W: 1,
	}
	// obs=0: π(B)·B(B,0) = 0.4·0.8 = 0.32 > π(A)·B(A,0) = 0.6·0.1 = 0.06 → state B
	got := m.Viterbi([]int{0})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("Viterbi([0]): got %v, want [1]", got)
	}
}

// --- Concatenate tests ---

// TestConcatenateDigits verifies the overlap-merge rule on a simple digit path
// where each state is a single-character tuple.
//
// Single-char tuples fold consecutive identical states via the maximal-overlap
// rule: "3"→"3" has overlap 1 (the char matches), so the second "3" is not
// re-emitted. The path [3,3,1,4,4,1] therefore produces "3141", not "331441".
// This is the intended sliding-window behaviour: when the window stays on the
// same character column, the same state repeats but the output advances only
// once that character's ink clears the window.
func TestConcatenateDigits(t *testing.T) {
	t.Parallel()
	states := []string{"3", "1", "4"}
	path := []int{0, 0, 1, 2, 2, 1} // state sequence: 3,3,1,4,4,1
	// "3"→"3": overlap=1, commit nothing; "3"→"1": overlap=0, commit "3";
	// "1"→"4": overlap=0, commit "1"; "4"→"4": overlap=1, commit nothing;
	// "4"→"1": overlap=0, commit "4"; last "1" committed → "3141".
	got := Concatenate(states, path)
	want := "3141"
	if got != want {
		t.Errorf("Concatenate single-char: got %q, want %q", got, want)
	}
}

// TestConcatenateOverlap verifies the maximal-overlap merge on two-char tuples.
//
// Window W=2 over "3141":
//
//	t=0: cols 0–1 → state "3|1"
//	t=1: cols 1–2 → state "1|4"
//	t=2: cols 2–3 → state "4|1"
//
// The overlap between "3|1" and "1|4" is ["1"] (length 1), so "3" is
// committed. Then between "1|4" and "4|1" the overlap is ["4"] (length 1),
// so "1" is committed. The last state "4|1" contributes "4","1". Result: "3141".
func TestConcatenateOverlap(t *testing.T) {
	t.Parallel()
	states := []string{"3|1", "1|4", "4|1"}
	path := []int{0, 1, 2}
	got := Concatenate(states, path)
	want := "3141"
	if got != want {
		t.Errorf("Concatenate overlap: got %q, want %q", got, want)
	}
}

// TestConcatenateFallback verifies that when adjacent states share no overlap
// (the Viterbi path jumped discontinuously) both states contribute their full
// tuples rather than panicking.
func TestConcatenateFallback(t *testing.T) {
	t.Parallel()
	// "3|1" then "9|2": overlap is 0, so both are emitted in full.
	states := []string{"3|1", "9|2"}
	path := []int{0, 1}
	got := Concatenate(states, path)
	want := "3192"
	if got != want {
		t.Errorf("Concatenate fallback: got %q, want %q", got, want)
	}
}

// TestConcatenateEmpty verifies that an empty or nil path returns an empty string.
func TestConcatenateEmpty(t *testing.T) {
	t.Parallel()
	states := []string{"A", "B"}
	if got := Concatenate(states, nil); got != "" {
		t.Errorf("Concatenate(nil): got %q, want empty", got)
	}
	if got := Concatenate(states, []int{}); got != "" {
		t.Errorf("Concatenate([]): got %q, want empty", got)
	}
}

// TestConcatenateSpaceTrim verifies that leading/trailing spaces inserted by
// the begin/end-of-line padding are trimmed from the output.
func TestConcatenateSpaceTrim(t *testing.T) {
	t.Parallel()
	states := []string{" ", "A", "B", " "}
	path := []int{0, 1, 2, 3}
	got := Concatenate(states, path)
	want := "AB"
	if got != want {
		t.Errorf("Concatenate space trim: got %q, want %q", got, want)
	}
}

// --- TupleKey ---

// TestTupleKey verifies canonical encoding.
func TestTupleKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"3"}, "3"},
		{[]string{"3", "1"}, "3|1"},
		{[]string{"A", "B", "C"}, "A|B|C"},
	}
	for _, tc := range cases {
		if got := TupleKey(tc.in); got != tc.want {
			t.Errorf("TupleKey(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- BuildModel ---

// TestBuildModelLaplace verifies that BuildModel produces non-−∞ log
// probabilities even for state/observation pairs with zero training counts
// (Laplace add-1 smoothing).
func TestBuildModelLaplace(t *testing.T) {
	t.Parallel()
	states := []string{"A", "B"}
	stateID := map[string]int{"A": 0, "B": 1}
	// Zero start counts, zero transition counts, zero emit counts.
	startCounts := []float64{0, 0}
	transCounts := []map[int]float64{{0: 1, 1: 0}, {0: 0, 1: 0}}
	emitCounts := [][]float64{{0, 0}, {0, 0}}
	m := BuildModel(states, stateID, 2, startCounts, transCounts, emitCounts, nil, 1)

	for s := range len(states) {
		if math.IsInf(m.LogPi[s], -1) {
			t.Errorf("LogPi[%d] is -∞ after Laplace (want finite)", s)
		}
		for o := range 2 {
			if math.IsInf(m.LogB[s][o], -1) {
				t.Errorf("LogB[%d][%d] is -∞ after Laplace (want finite)", s, o)
			}
		}
	}
}
