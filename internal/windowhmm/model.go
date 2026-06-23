package windowhmm

import (
	"fmt"
	"math"
	"strings"
)

// Model is a trained log-space HMM for column-anchored blind decoding.
//
// States are interned tuples of character runes covering a W-column window.
// Observations are KMeans cluster IDs quantised from window vectors.
// All probabilities are stored as natural logs; -∞ represents probability 0.
type Model struct {
	// StateID maps a canonical tuple string to its integer ID.
	StateID map[string]int
	// States lists each state's canonical tuple string (index = state ID).
	States []string
	// K is the number of observation clusters.
	K int
	// LogPi[s] is the log start probability of state s.
	LogPi []float64
	// LogTrans[s] maps successor state ID → log transition probability.
	// Absent entries represent transitions that were never observed during
	// training and carry an implicit -∞.
	LogTrans []map[int]float64
	// LogB[s][o] is the log emission probability of observation o in state s.
	LogB [][]float64
	// Centroids are the K cluster centroids used to quantise a window vector.
	Centroids [][]float64
	// W is the window width in block columns.
	W int
}

// Viterbi runs the Viterbi algorithm over the observation sequence obs and
// returns the most-likely state ID path.
//
// The path has the same length as obs. Each element is a state ID in [0,
// len(m.States)).
func (m *Model) Viterbi(obs []int) []int {
	return m.ViterbiLM(obs, 0, nil)
}

// ViterbiLM runs a language-model-fused Viterbi over the observation sequence
// obs and returns the most-likely state ID path.
//
// At each transition from state prev to state s the per-edge score becomes:
//
//	logA[prev][s] + beta * lmScore(prevContext, addedChars)
//
// where addedChars is the non-overlapping prefix of the prev tuple that is
// committed when the window advances to s (determined by the same maximal-overlap
// merge rule used by [Concatenate]), and prevContext is the string of all
// characters committed on the best path reaching prev at time t−1.
//
// LM accounting: the LM scores only the characters the transition actually
// commits, not the full state tuple, to avoid double-counting characters that
// remain in the window overlap and will be rescored on future transitions.
//
// When beta==0 or lmScore==nil the method is identical to [Viterbi] and
// produces a byte-identical result given the same model and observations.
func (m *Model) ViterbiLM(obs []int, beta float64, lmScore func(prevContext, addedChars string) float64) []int {
	T := len(obs)
	S := len(m.States)
	if T == 0 || S == 0 {
		return nil
	}

	useLM := beta != 0 && lmScore != nil

	// delta[t][s] = log P(best path to s at t, obs[0..t]) + beta*LM(path).
	// psi[t][s]   = predecessor state at t-1 on the best path to s at t.
	// ctx[t][s]   = committed text on the best path to s at t (only when useLM).
	delta := make([][]float64, T)
	psi := make([][]int, T)
	var ctx [][]string // ctx[t][s] = committed prefix text on best path to s at t
	for t := range T {
		delta[t] = make([]float64, S)
		psi[t] = make([]int, S)
	}
	if useLM {
		ctx = make([][]string, T)
		for t := range T {
			ctx[t] = make([]string, S)
		}
	}

	// Initialisation: no transition at t=0, so no LM term.
	for s := range S {
		delta[0][s] = m.LogPi[s] + m.logEmit(s, obs[0])
		// ctx[0][s] stays "" — no characters committed at initialisation.
	}

	// Precompute per-transition committed chars when useLM so we don't repeat
	// the overlap calculation inside the t-loop. The committed prefix for a
	// transition prev→s is the first (len(prev)-overlap(prev,s)) elements of
	// the prev tuple joined into a string.
	var transAdded [][]string // transAdded[prev][s] = chars committed by prev→s
	if useLM {
		transAdded = make([][]string, S)
		for prev := range S {
			transAdded[prev] = make([]string, S)
			prevTuple := parseTuple(m.States[prev])
			for s := range S {
				curTuple := parseTuple(m.States[s])
				ov := maxOverlap(prevTuple, curTuple)
				toCommit := len(prevTuple) - ov
				transAdded[prev][s] = strings.Join(prevTuple[:toCommit], "")
			}
		}
	}

	// Recursion.
	for t := 1; t < T; t++ {
		for s := range S {
			logE := m.logEmit(s, obs[t])
			best, bestPred := math.Inf(-1), 0
			bestCtx := ""
			for prev := range S {
				lTrans := math.Inf(-1)
				if m.LogTrans[prev] != nil {
					if v, ok := m.LogTrans[prev][s]; ok {
						lTrans = v
					}
				}
				lm := 0.0
				if useLM {
					added := transAdded[prev][s]
					if added != "" {
						lm = beta * lmScore(ctx[t-1][prev], added)
					}
				}
				val := delta[t-1][prev] + lTrans + logE + lm
				if val > best {
					best, bestPred = val, prev
					if useLM {
						bestCtx = ctx[t-1][prev] + transAdded[prev][s]
					}
				}
			}
			delta[t][s] = best
			psi[t][s] = bestPred
			if useLM {
				ctx[t][s] = bestCtx
			}
		}
	}

	// Backtrack.
	path := make([]int, T)
	best, bestS := math.Inf(-1), 0
	for s := range S {
		if delta[T-1][s] > best {
			best, bestS = delta[T-1][s], s
		}
	}
	path[T-1] = bestS
	for t := T - 2; t >= 0; t-- {
		path[t] = psi[t+1][path[t+1]]
	}
	return path
}

// parseTuple splits a canonical pipe-separated state key into individual
// character strings (the inverse of [TupleKey]).
func parseTuple(state string) []string {
	if state == "" {
		return nil
	}
	return strings.Split(state, "|")
}

// logEmit returns m.LogB[s][o], or -∞ when o is out of range.
func (m *Model) logEmit(s, o int) float64 {
	if s < 0 || s >= len(m.LogB) {
		return math.Inf(-1)
	}
	row := m.LogB[s]
	if o < 0 || o >= len(row) {
		return math.Inf(-1)
	}
	return row[o]
}

// Concatenate converts a Viterbi state-ID path to a string by the
// maximal-overlap merge rule: consecutive tuple states share their overlap, so
// the merged text is the longest string consistent with adjacent-state tuples.
//
// Each state ID maps to a canonical tuple string of the form "a|b|c" where the
// pipe separates the character runes (represented as their UTF-8 string). A
// single-element tuple is just the character string with no pipe.
//
// When two adjacent states disagree (which can happen when the Viterbi path
// transitions across a character boundary), the characters in the first state
// that are not in the overlap with the second state are committed, and the
// process continues.
func Concatenate(states []string, path []int) string {
	if len(path) == 0 || len(states) == 0 {
		return ""
	}

	// Parse each state's tuple (|‑separated character strings).
	tupleOf := func(id int) []string {
		if id < 0 || id >= len(states) {
			return nil
		}
		s := states[id]
		if s == "" {
			return nil
		}
		return strings.Split(s, "|")
	}

	// committed accumulates the final output runes.
	var committed []string

	prev := tupleOf(path[0])
	for _, sid := range path[1:] {
		cur := tupleOf(sid)

		// Find the maximal suffix of prev that matches a prefix of cur.
		overlap := maxOverlap(prev, cur)

		// The characters in prev that are NOT part of the overlap are done.
		toCommit := len(prev) - overlap
		committed = append(committed, prev[:toCommit]...)

		prev = cur
	}
	// Commit the remaining characters from the last tuple.
	committed = append(committed, prev...)

	// Trim edge spaces introduced by begin/end-of-line padding.
	result := strings.Join(committed, "")
	result = strings.TrimSpace(result)
	return result
}

// maxOverlap returns the length of the longest suffix of a that equals a
// prefix of b.
func maxOverlap(a, b []string) int {
	best := 0
	for n := 1; n <= min(len(a), len(b)); n++ {
		match := true
		for i := range n {
			if a[len(a)-n+i] != b[i] {
				match = false
				break
			}
		}
		if match {
			best = n
		}
	}
	return best
}

// TupleKey encodes a slice of rune strings as the canonical pipe-separated
// state key used by [Model.StateID] and [Model.States].
func TupleKey(chars []string) string {
	return strings.Join(chars, "|")
}

// BuildModel constructs a log-space Model from raw training counts.
//
// startCounts[s] is the number of training sequences where s was the first
// state. transCounts[s][t] is the number of s→t transitions observed.
// emitCounts[s][o] is the number of times observation o was emitted in state
// s. Laplace smoothing (add-1) is applied to all distributions so that unseen
// but feasible combinations have a small non-zero probability.
func BuildModel(
	states []string,
	stateID map[string]int,
	numClusters int,
	startCounts []float64,
	transCounts []map[int]float64,
	emitCounts [][]float64,
	centroids [][]float64,
	windowW int,
) *Model {
	S := len(states)
	m := &Model{
		StateID:   stateID,
		States:    states,
		K:         numClusters,
		LogPi:     make([]float64, S),
		LogTrans:  make([]map[int]float64, S),
		LogB:      make([][]float64, S),
		Centroids: centroids,
		W:         windowW,
	}

	// Start probabilities with Laplace smoothing.
	var startTotal float64
	for s := range S {
		startTotal += startCounts[s] + 1
	}
	for s := range S {
		m.LogPi[s] = math.Log((startCounts[s] + 1) / startTotal)
	}

	// Transition probabilities with Laplace smoothing over observed successors.
	// Unobserved transitions remain -∞ (sparse representation).
	for s := range S {
		row := transCounts[s]
		if len(row) == 0 {
			m.LogTrans[s] = nil
			continue
		}
		var total float64
		for _, cnt := range row {
			total += cnt + 1
		}
		m.LogTrans[s] = make(map[int]float64, len(row))
		for t, cnt := range row {
			m.LogTrans[s][t] = math.Log((cnt + 1) / total)
		}
	}

	// Emission probabilities with Laplace smoothing.
	for s := range S {
		row := emitCounts[s]
		var total float64
		for o := range numClusters {
			total += row[o] + 1
		}
		m.LogB[s] = make([]float64, numClusters)
		for o := range numClusters {
			m.LogB[s][o] = math.Log((row[o] + 1) / total)
		}
	}

	return m
}

// Describe returns a human-readable summary of the model for diagnostics.
func (m *Model) Describe() string {
	return fmt.Sprintf("Model{states=%d K=%d W=%d}", len(m.States), m.K, m.W)
}
