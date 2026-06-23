package windowhmm

import (
	"math"
	"math/rand/v2"
)

// KMeans runs K-means clustering on vectors using k-means++ initialisation.
// It returns K centroids (each of the same length as vectors[0]).
//
// seed controls the random initialisation; identical seeds give identical
// results, making the training deterministic.
//
// KMeans panics when K < 1 or len(vectors) < K.
func KMeans(vectors [][]float64, k int, seed uint64) [][]float64 {
	if k < 1 {
		panic("windowhmm: KMeans: K must be ≥ 1")
	}
	if len(vectors) < k {
		panic("windowhmm: KMeans: fewer vectors than K")
	}
	dim := len(vectors[0])

	rng := rand.New(rand.NewPCG(seed, seed^0xdeadbeef_cafebabe)) // #nosec G404 -- deterministic seed, not security

	// k-means++ initialisation.
	centroids := make([][]float64, 0, k)
	first := vectors[rng.IntN(len(vectors))]
	centroids = append(centroids, cloneVec(first))

	dist2 := make([]float64, len(vectors))
	for len(centroids) < k {
		var total float64
		for i, v := range vectors {
			d := minDist2(v, centroids)
			dist2[i] = d
			total += d
		}
		r := rng.Float64() * total
		var cum float64
		chosen := len(vectors) - 1
		for i, d := range dist2 {
			cum += d
			if cum >= r {
				chosen = i
				break
			}
		}
		centroids = append(centroids, cloneVec(vectors[chosen]))
	}

	// Lloyd iterations (max 300 or until stable).
	assign := make([]int, len(vectors))
	counts := make([]int, k)
	newCentroids := make([][]float64, k)
	for ci := range newCentroids {
		newCentroids[ci] = make([]float64, dim)
	}

	for iter := range 300 {
		changed := false

		// Assignment step.
		for i, v := range vectors {
			best, bestD := 0, math.Inf(1)
			for ci, c := range centroids {
				d := dist2Vecs(v, c)
				if d < bestD {
					bestD, best = d, ci
				}
			}
			if assign[i] != best {
				changed = true
				assign[i] = best
			}
		}
		if iter > 0 && !changed {
			break
		}

		// Update step.
		for ci := range newCentroids {
			clear(newCentroids[ci])
			counts[ci] = 0
		}
		for i, v := range vectors {
			ci := assign[i]
			counts[ci]++
			for d, x := range v {
				newCentroids[ci][d] += x
			}
		}
		for ci := range centroids {
			n := counts[ci]
			if n == 0 {
				// Dead cluster: reinitialise to a random vector.
				newCentroids[ci] = cloneVec(vectors[rng.IntN(len(vectors))])
			} else {
				for d := range newCentroids[ci] {
					newCentroids[ci][d] /= float64(n)
				}
			}
			copy(centroids[ci], newCentroids[ci])
		}
	}
	return centroids
}

// Quantize maps each vector to the index of its nearest centroid.
// The returned slice has the same length as vectors.
func Quantize(vectors [][]float64, centroids [][]float64) []int {
	ids := make([]int, len(vectors))
	for i, v := range vectors {
		best, bestD := 0, math.Inf(1)
		for ci, c := range centroids {
			d := dist2Vecs(v, c)
			if d < bestD {
				bestD, best = d, ci
			}
		}
		ids[i] = best
	}
	return ids
}

// NearestCentroid returns the index of the centroid nearest to v.
func NearestCentroid(v []float64, centroids [][]float64) int {
	best, bestD := 0, math.Inf(1)
	for ci, c := range centroids {
		d := dist2Vecs(v, c)
		if d < bestD {
			bestD, best = d, ci
		}
	}
	return best
}

func cloneVec(v []float64) []float64 {
	c := make([]float64, len(v))
	copy(c, v)
	return c
}

func dist2Vecs(a, b []float64) float64 {
	var s float64
	for i := range min(len(a), len(b)) {
		d := a[i] - b[i]
		s += d * d
	}
	return s
}

func minDist2(v []float64, centroids [][]float64) float64 {
	best := math.Inf(1)
	for _, c := range centroids {
		if d := dist2Vecs(v, c); d < best {
			best = d
		}
	}
	return best
}
