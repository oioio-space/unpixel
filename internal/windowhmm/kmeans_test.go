package windowhmm

import (
	"math"
	"testing"
)

// TestKMeansDeterminism verifies that identical seeds produce identical centroids.
func TestKMeansDeterminism(t *testing.T) {
	t.Parallel()
	vecs := make([][]float64, 20)
	for i := range vecs {
		vecs[i] = []float64{float64(i) / 20.0, 1.0 - float64(i)/20.0}
	}
	c1 := KMeans(vecs, 4, 42)
	c2 := KMeans(vecs, 4, 42)
	for i, a := range c1 {
		for j, v := range a {
			if math.Abs(v-c2[i][j]) > 1e-12 {
				t.Errorf("centroid[%d][%d]: got %v vs %v (seed mismatch)", i, j, v, c2[i][j])
			}
		}
	}
}

// TestKMeansDifferentSeeds verifies that different seeds (with a large spread
// of data) can produce different centroid orderings.
func TestKMeansDifferentSeeds(t *testing.T) {
	t.Parallel()
	// Well-separated clusters so K-means always finds them, but the seed
	// changes which cluster gets which ID.
	vecs := make([][]float64, 40)
	for i := range 10 {
		vecs[i] = []float64{0.0, 0.0}
		vecs[10+i] = []float64{10.0, 0.0}
		vecs[20+i] = []float64{0.0, 10.0}
		vecs[30+i] = []float64{10.0, 10.0}
	}
	c1 := KMeans(vecs, 4, 1)
	c2 := KMeans(vecs, 4, 999)
	// At least the centroids should all be near one of the four true cluster
	// centres regardless of seed.
	corners := [][]float64{{0, 0}, {10, 0}, {0, 10}, {10, 10}}
	for _, cs := range [][][]float64{c1, c2} {
		for _, c := range cs {
			best := math.Inf(1)
			for _, corner := range corners {
				best = min(best, dist2Vecs(c, corner))
			}
			if best > 1.0 {
				t.Errorf("centroid %v is far from all corners (min dist² = %.4f)", c, best)
			}
		}
	}
}

// TestKMeansK1 verifies that K=1 returns the mean of all vectors.
func TestKMeansK1(t *testing.T) {
	t.Parallel()
	vecs := [][]float64{{1, 2}, {3, 4}, {5, 6}}
	c := KMeans(vecs, 1, 0)
	if len(c) != 1 {
		t.Fatalf("K=1: want 1 centroid, got %d", len(c))
	}
	wantX, wantY := (1.0+3.0+5.0)/3.0, (2.0+4.0+6.0)/3.0
	const eps = 1e-9
	if math.Abs(c[0][0]-wantX) > eps || math.Abs(c[0][1]-wantY) > eps {
		t.Errorf("K=1 centroid: got %v, want [%.4f %.4f]", c[0], wantX, wantY)
	}
}

// TestQuantize verifies that Quantize assigns every vector to the nearest centroid.
func TestQuantize(t *testing.T) {
	t.Parallel()
	centroids := [][]float64{{0, 0}, {10, 0}, {0, 10}}
	cases := []struct {
		vec  []float64
		want int
	}{
		{[]float64{0.1, 0.1}, 0},
		{[]float64{9.9, 0.1}, 1},
		{[]float64{0.1, 9.9}, 2},
	}
	for _, tc := range cases {
		ids := Quantize([][]float64{tc.vec}, centroids)
		if ids[0] != tc.want {
			t.Errorf("Quantize(%v) = %d, want %d", tc.vec, ids[0], tc.want)
		}
	}
}

// TestNearestCentroid verifies NearestCentroid on a two-centroid case.
func TestNearestCentroid(t *testing.T) {
	t.Parallel()
	centroids := [][]float64{{0, 0}, {5, 5}}
	if got := NearestCentroid([]float64{1, 1}, centroids); got != 0 {
		t.Errorf("NearestCentroid([1,1]): got %d, want 0", got)
	}
	if got := NearestCentroid([]float64{4, 4}, centroids); got != 1 {
		t.Errorf("NearestCentroid([4,4]): got %d, want 1", got)
	}
}
