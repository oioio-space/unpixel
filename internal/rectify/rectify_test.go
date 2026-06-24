package rectify

import (
	"fmt"
	"image"
	"math"
	"testing"
)

const eps = 1e-9

func ptClose(t *testing.T, got, want Point, tol float64, ctx string) {
	t.Helper()
	if math.Abs(got.X-want.X) > tol || math.Abs(got.Y-want.Y) > tol {
		t.Errorf("%s: got (%.6f,%.6f), want (%.6f,%.6f)", ctx, got.X, got.Y, want.X, want.Y)
	}
}

// unitSquare is the canonical source quad (top-left, top-right, bottom-right,
// bottom-left) reused across tests.
var unitSquare = [4]Point{{0, 0}, {1, 0}, {1, 1}, {0, 1}}

func TestSolveDLT_identity(t *testing.T) {
	m, err := SolveDLT(unitSquare, unitSquare)
	if err != nil {
		t.Fatalf("SolveDLT identity: unexpected error %v", err)
	}
	for _, p := range []Point{{0.25, 0.75}, {0.5, 0.5}, {0.9, 0.1}} {
		ptClose(t, m.Apply(p), p, eps, fmt.Sprintf("identity Apply %v", p))
	}
}

func TestSolveDLT_roundTripCorners(t *testing.T) {
	// A non-affine quad (trapezoid) so the projective terms h6,h7 are exercised.
	dst := [4]Point{{10, 20}, {110, 5}, {120, 95}, {5, 90}}
	m, err := SolveDLT(unitSquare, dst)
	if err != nil {
		t.Fatalf("SolveDLT: unexpected error %v", err)
	}
	for i, src := range unitSquare {
		ptClose(t, m.Apply(src), dst[i], 1e-6, fmt.Sprintf("corner %d", i))
	}
}

func TestSolveDLT_collinearSingular(t *testing.T) {
	// Three collinear source points → degenerate, no unique homography.
	src := [4]Point{{0, 0}, {1, 1}, {2, 2}, {0, 1}}
	if _, err := SolveDLT(src, unitSquare); err == nil {
		t.Error("SolveDLT collinear: got nil error, want ErrSingular")
	}
}

func TestInverse_roundTrip(t *testing.T) {
	dst := [4]Point{{10, 20}, {110, 5}, {120, 95}, {5, 90}}
	m, err := SolveDLT(unitSquare, dst)
	if err != nil {
		t.Fatalf("SolveDLT: %v", err)
	}
	inv, err := m.Inverse()
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	for _, p := range []Point{{0.2, 0.3}, {0.7, 0.8}, {0.5, 0.5}} {
		back := inv.Apply(m.Apply(p))
		ptClose(t, back, p, 1e-6, fmt.Sprintf("inverse∘forward %v", p))
	}
}

func TestInverse_singular(t *testing.T) {
	// A rank-deficient matrix (all zeros) has determinant 0.
	var m Matrix3
	if _, err := m.Inverse(); err == nil {
		t.Error("Inverse of zero matrix: got nil error, want ErrSingular")
	}
}

func TestMul_identity(t *testing.T) {
	id := Matrix3{1, 0, 0, 0, 1, 0, 0, 0, 1}
	m := Matrix3{2, 0, 3, 0, 2, 4, 0, 0, 1}
	got := m.Mul(id)
	for i := range got {
		if math.Abs(got[i]-m[i]) > eps {
			t.Fatalf("m·I != m at index %d: got %v want %v", i, got[i], m[i])
		}
	}
}

func TestRectToQuad(t *testing.T) {
	dst := [4]Point{{10, 20}, {110, 5}, {120, 95}, {5, 90}}
	m, err := RectToQuad(100, 80, dst)
	if err != nil {
		t.Fatalf("RectToQuad: %v", err)
	}
	rect := [4]Point{{0, 0}, {100, 0}, {100, 80}, {0, 80}}
	for i, r := range rect {
		ptClose(t, m.Apply(r), dst[i], 1e-6, fmt.Sprintf("RectToQuad corner %d", i))
	}
}

func TestWarp_identityCopiesImage(t *testing.T) {
	src := patternImage(8, 8)
	id := Matrix3{1, 0, 0, 0, 1, 0, 0, 0, 1}
	got := Warp(src, id, 8, 8)
	for y := range 8 {
		for x := range 8 {
			so, do := src.PixOffset(x, y), got.PixOffset(x, y)
			for c := range 4 {
				if src.Pix[so+c] != got.Pix[do+c] {
					t.Fatalf("identity warp changed pixel (%d,%d) chan %d: got %d want %d",
						x, y, c, got.Pix[do+c], src.Pix[so+c])
				}
			}
		}
	}
}

func TestWarp_integerTranslationIsExact(t *testing.T) {
	src := patternImage(16, 16)
	// dst→src map shifting sampling by +3 in x: got(x,y) samples src(x+3,y).
	// An integer offset lands on pixel centres, so bilinear is exact.
	shift := Matrix3{1, 0, 3, 0, 1, 0, 0, 0, 1}
	got := Warp(src, shift, 16, 16)
	for _, p := range []image.Point{{X: 1, Y: 2}, {X: 5, Y: 9}, {X: 10, Y: 0}} {
		so, do := src.PixOffset(p.X+3, p.Y), got.PixOffset(p.X, p.Y)
		for c := range 4 {
			if got.Pix[do+c] != src.Pix[so+c] {
				t.Errorf("translated warp (%d,%d) chan %d: got %d want %d (= src(%d,%d))",
					p.X, p.Y, c, got.Pix[do+c], src.Pix[so+c], p.X+3, p.Y)
			}
		}
	}
}

func TestWarp_outOfBoundsIsWhite(t *testing.T) {
	src := patternImage(4, 4)
	shift := Matrix3{1, 0, 1000, 0, 1, 1000, 0, 0, 1} // sample far outside src
	got := Warp(src, shift, 4, 4)
	o := got.PixOffset(2, 2)
	if got.Pix[o] != 255 || got.Pix[o+1] != 255 || got.Pix[o+2] != 255 || got.Pix[o+3] != 255 {
		t.Errorf("out-of-bounds sample: got RGBA(%d,%d,%d,%d), want opaque white",
			got.Pix[o], got.Pix[o+1], got.Pix[o+2], got.Pix[o+3])
	}
}

// patternImage builds a w×h RGBA with a smooth deterministic gradient so warp
// interpolation has structure to preserve.
func patternImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			o := img.PixOffset(x, y)
			img.Pix[o] = uint8((x * 255) / max(1, w-1))
			img.Pix[o+1] = uint8((y * 255) / max(1, h-1))
			img.Pix[o+2] = 128
			img.Pix[o+3] = 255
		}
	}
	return img
}

var (
	sinkMatrix Matrix3
	sinkImage  *image.RGBA
)

func BenchmarkSolveDLT(b *testing.B) {
	dst := [4]Point{{10, 20}, {110, 5}, {120, 95}, {5, 90}}
	b.ReportAllocs()
	for b.Loop() {
		m, err := SolveDLT(unitSquare, dst)
		if err != nil {
			b.Fatal(err)
		}
		sinkMatrix = m
	}
}

func BenchmarkWarp(b *testing.B) {
	src := patternImage(128, 64)
	dst := [4]Point{{4, 6}, {120, 2}, {118, 60}, {2, 58}}
	m, err := RectToQuad(128, 64, dst)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		sinkImage = Warp(src, m, 128, 64)
	}
}
