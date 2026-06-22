package deblur

import (
	"math"
	"testing"
)

// naiveErode2D is a reference implementation used to cross-check the separable
// Erode. It computes the 2-D minimum over the full (2r+1)² neighbourhood in
// one pass, making it obviously correct but O(w·h·r²).
func naiveErode2D(lum []float64, w, h, r int) []float64 {
	out := make([]float64, w*h)
	for y := range h {
		for x := range w {
			v := math.MaxFloat64
			for ky := -r; ky <= r; ky++ {
				for kx := -r; kx <= r; kx++ {
					sy := max(0, min(y+ky, h-1))
					sx := max(0, min(x+kx, w-1))
					if lum[sy*w+sx] < v {
						v = lum[sy*w+sx]
					}
				}
			}
			out[y*w+x] = v
		}
	}
	return out
}

// naiveDilate2D is the reference dilation.
func naiveDilate2D(lum []float64, w, h, r int) []float64 {
	out := make([]float64, w*h)
	for y := range h {
		for x := range w {
			v := -math.MaxFloat64
			for ky := -r; ky <= r; ky++ {
				for kx := -r; kx <= r; kx++ {
					sy := max(0, min(y+ky, h-1))
					sx := max(0, min(x+kx, w-1))
					if lum[sy*w+sx] > v {
						v = lum[sy*w+sx]
					}
				}
			}
			out[y*w+x] = v
		}
	}
	return out
}

// closeTo reports whether a and b differ by at most eps.
func closeTo(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestErode_matchesNaive(t *testing.T) {
	// 5×5 grid with a spike in the centre.
	w, h := 5, 5
	lum := make([]float64, w*h)
	for i := range lum {
		lum[i] = 200
	}
	lum[2*w+2] = 10 // centre spike (low)

	for _, r := range []int{1, 2} {
		got := Erode(lum, w, h, r)
		want := naiveErode2D(lum, w, h, r)
		for i := range got {
			if !closeTo(got[i], want[i], 1e-9) {
				t.Errorf("r=%d i=%d: Erode=%v naïve=%v", r, i, got[i], want[i])
			}
		}
	}
}

func TestDilate_matchesNaive(t *testing.T) {
	// 5×5 grid with a spike in the centre.
	w, h := 5, 5
	lum := make([]float64, w*h)
	for i := range lum {
		lum[i] = 50
	}
	lum[2*w+2] = 250 // centre spike (high)

	for _, r := range []int{1, 2} {
		got := Dilate(lum, w, h, r)
		want := naiveDilate2D(lum, w, h, r)
		for i := range got {
			if !closeTo(got[i], want[i], 1e-9) {
				t.Errorf("r=%d i=%d: Dilate=%v naïve=%v", r, i, got[i], want[i])
			}
		}
	}
}

// TestOpen_removesThinStroke verifies that Open erases a 1-pixel-wide bright
// stroke on a dark background when the radius is > 0 (the stroke is narrower
// than 2·r+1 pixels so it erodes away completely, and the subsequent dilation
// brings the background back).
func TestOpen_removesThinStroke(t *testing.T) {
	// 9-pixel 1-D image: background=10, single bright pixel at position 4.
	w, h, r := 9, 1, 2
	lum := []float64{10, 10, 10, 10, 250, 10, 10, 10, 10}
	got := Open(lum, w, h, r)
	// After erosion with r=2, the peak at index 4 is replaced by the minimum of
	// indices [2..6], which is 10 (background). Dilation restores the background.
	for i, v := range got {
		if !closeTo(v, 10, 1.0) {
			t.Errorf("Open thin stroke: index %d = %.1f, want ≈10", i, v)
		}
	}
}

// TestOpen_preservesLargeBlock verifies that Open leaves a block wider than
// 2·r pixels mostly intact (the block centre survives both erosion and dilation).
func TestOpen_preservesLargeBlock(t *testing.T) {
	// 15-pixel 1-D image: background=10, bright block at [4,10].
	w, h, r := 15, 1, 2
	lum := []float64{10, 10, 10, 10, 250, 250, 250, 250, 250, 250, 250, 10, 10, 10, 10}
	got := Open(lum, w, h, r)
	// Centre of the block (indices 6,7) must remain clearly bright.
	centre := (got[6] + got[7]) / 2
	if centre < 200 {
		t.Errorf("Open: large block centre ≈ %.1f, want ≥ 200", centre)
	}
}

// TestErode_noModifySrc checks that Erode does not mutate its input.
func TestErode_noModifySrc(t *testing.T) {
	lum := []float64{100, 200, 50}
	orig := []float64{100, 200, 50}
	_ = Erode(lum, 3, 1, 1)
	for i := range lum {
		if lum[i] != orig[i] {
			t.Errorf("Erode mutated src[%d]: got %v, want %v", i, lum[i], orig[i])
		}
	}
}

// TestDilate_noModifySrc checks that Dilate does not mutate its input.
func TestDilate_noModifySrc(t *testing.T) {
	lum := []float64{100, 200, 50}
	orig := []float64{100, 200, 50}
	_ = Dilate(lum, 3, 1, 1)
	for i := range lum {
		if lum[i] != orig[i] {
			t.Errorf("Dilate mutated src[%d]: got %v, want %v", i, lum[i], orig[i])
		}
	}
}

// TestErode_zeroRadius returns a copy unchanged.
func TestErode_zeroRadius(t *testing.T) {
	lum := []float64{10, 20, 30}
	got := Erode(lum, 3, 1, 0)
	for i := range got {
		if got[i] != lum[i] {
			t.Errorf("Erode r=0 [%d]: got %v, want %v", i, got[i], lum[i])
		}
	}
}

// TestDilate_zeroRadius returns a copy unchanged.
func TestDilate_zeroRadius(t *testing.T) {
	lum := []float64{10, 20, 30}
	got := Dilate(lum, 3, 1, 0)
	for i := range got {
		if got[i] != lum[i] {
			t.Errorf("Dilate r=0 [%d]: got %v, want %v", i, got[i], lum[i])
		}
	}
}
