package windowhmm

import (
	"math"
	"math/rand/v2"
	"testing"
)

// sinkPath is a package-level sink that defeats dead-code elimination in
// Viterbi benchmarks.
var sinkPath []int

// sinkCentroids is a package-level sink for KMeans benchmarks.
var sinkCentroids [][]float64

// buildBenchModel constructs a deterministic synthetic Model with numStates
// states, numClusters observation clusters, and sparseFrac fraction of
// non-zero transitions (clamped to [0,1]).
func buildBenchModel(numStates, numClusters int, sparseFrac float64, seed uint64) *Model {
	rng := rand.New(rand.NewPCG(seed, seed^0xcafe_babe))

	states := make([]string, numStates)
	stateID := make(map[string]int, numStates)
	for s := range numStates {
		// Build a unique state key from printable ASCII characters.
		key := string(rune('A'+s%26)) + string(rune('a'+(s/26)%26))
		states[s] = key
		stateID[key] = s
	}

	logPi := make([]float64, numStates)
	for s := range numStates {
		logPi[s] = math.Log(1.0 / float64(numStates))
	}

	logTrans := make([]map[int]float64, numStates)
	for prev := range numStates {
		m := make(map[int]float64)
		var totalW float64
		for s := range numStates {
			if rng.Float64() < sparseFrac {
				w := rng.Float64() + 0.01
				m[s] = w
				totalW += w
			}
		}
		if len(m) == 0 {
			// Guarantee at least one successor so the Viterbi path survives.
			s := rng.IntN(numStates)
			m[s] = 1.0
			totalW = 1.0
		}
		for s, w := range m {
			m[s] = math.Log(w / totalW)
		}
		logTrans[prev] = m
	}

	logB := make([][]float64, numStates)
	for s := range numStates {
		row := make([]float64, numClusters)
		var total float64
		for o := range numClusters {
			w := rng.Float64() + 0.01
			row[o] = w
			total += w
		}
		for o := range numClusters {
			row[o] = math.Log(row[o] / total)
		}
		logB[s] = row
	}

	return &Model{
		StateID:  stateID,
		States:   states,
		K:        numClusters,
		LogPi:    logPi,
		LogTrans: logTrans,
		LogB:     logB,
		W:        2,
	}
}

// buildBenchObs returns a deterministic random observation sequence of length
// seqLen with values in [0, numClusters).
func buildBenchObs(seqLen, numClusters int, seed uint64) []int {
	rng := rand.New(rand.NewPCG(seed, seed^0xdead_beef))
	obs := make([]int, seqLen)
	for i := range seqLen {
		obs[i] = rng.IntN(numClusters)
	}
	return obs
}

// BenchmarkViterbi exercises Viterbi across three model sizes and sparsity
// levels that span the realistic operating range of the window-HMM decoder.
//
//   - sparse05: 50 states, 5% density — small but typical W=1 model.
//   - sparse02: 200 states, 2% density — realistic W=2 sliding-window model.
//   - dense:    200 states, 100% density — worst-case fully connected graph.
func BenchmarkViterbi(b *testing.B) {
	cases := []struct {
		name       string
		S, K       int
		sparseFrac float64
		modelSeed  uint64
		obsSeed    uint64
	}{
		{"S50_T200_sparse05", 50, 16, 0.05, 1, 2},
		{"S200_T200_sparse02", 200, 32, 0.02, 3, 4},
		{"S200_T200_dense", 200, 32, 1.0, 5, 6},
	}

	const T = 200
	for _, tc := range cases {
		m := buildBenchModel(tc.S, tc.K, tc.sparseFrac, tc.modelSeed)
		obs := buildBenchObs(T, tc.K, tc.obsSeed)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(tc.S * T)) // transitions evaluated per call
			for b.Loop() {
				sinkPath = m.Viterbi(obs)
			}
		})
	}
}

// BenchmarkViterbiLM exercises ViterbiLM (with a zero-returning lmScore) on
// the same workloads as BenchmarkViterbi to isolate the overhead of the LM
// precomputation and context-tracking paths.
func BenchmarkViterbiLM(b *testing.B) {
	cases := []struct {
		name       string
		S, K       int
		sparseFrac float64
		modelSeed  uint64
		obsSeed    uint64
	}{
		{"S50_T200_sparse05", 50, 16, 0.05, 1, 2},
		{"S200_T200_sparse02", 200, 32, 0.02, 3, 4},
	}

	const T = 200
	lmScore := func(_, _ string) float64 { return 0.0 }
	for _, tc := range cases {
		m := buildBenchModel(tc.S, tc.K, tc.sparseFrac, tc.modelSeed)
		obs := buildBenchObs(T, tc.K, tc.obsSeed)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(tc.S * T))
			for b.Loop() {
				sinkPath = m.ViterbiLM(obs, 1.0, lmScore)
			}
		})
	}
}

// BenchmarkKMeans exercises KMeans clustering across vector-count / cluster-
// count combinations representative of real training workloads.
func BenchmarkKMeans(b *testing.B) {
	cases := []struct {
		name    string
		N, K, D int
	}{
		{"N1000_K16_D12", 1000, 16, 12},
		{"N5000_K32_D24", 5000, 32, 24},
	}

	for _, tc := range cases {
		rng := rand.New(rand.NewPCG(7, 8))
		vecs := make([][]float64, tc.N)
		for i := range vecs {
			v := make([]float64, tc.D)
			for d := range tc.D {
				v[d] = rng.Float64()
			}
			vecs[i] = v
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkCentroids = KMeans(vecs, tc.K, 42)
			}
		})
	}
}
