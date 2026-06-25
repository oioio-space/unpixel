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

func TestProjector_trueScoresLowWrongHigh(t *testing.T) {
	const rectW, rectH = 64, 48
	const photoW, photoH = 200, 170

	// The redaction content in rect space: a few large constant-colour blocks so
	// bilinear resampling barely perturbs it (the true candidate should match).
	trueRect := blockPattern(rectW, rectH, false)

	// Photograph it under perspective: warp the rect content into a tilted quad of
	// an otherwise-white photo. photoToRect maps photo→rect for inverse warping.
	quad := [4]Point{{20, 15}, {180, 30}, {170, 150}, {10, 140}}
	rectToPhoto, err := RectToQuad(rectW, rectH, quad)
	if err != nil {
		t.Fatalf("RectToQuad: %v", err)
	}
	photoToRect, err := rectToPhoto.Inverse()
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	photo := Warp(trueRect, photoToRect, photoW, photoH)

	p, err := NewProjector(photo, quad, rectW, rectH)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}

	dTrue := p.Distance(trueRect)
	dWrong := p.Distance(blockPattern(rectW, rectH, true)) // inverted colours

	if dTrue > 0.05 {
		t.Errorf("true candidate distance = %.4f, want ≤ 0.05 (forward model should match)", dTrue)
	}
	if dWrong <= dTrue+0.10 {
		t.Errorf("wrong candidate distance = %.4f must clearly exceed true %.4f", dWrong, dTrue)
	}
}

// TestPartialDistance verifies PartialDistance boundary conditions:
//   - xFrac ≤ 0 returns 1 (maximally different, no pixels compared)
//   - xFrac ≥ 1 delegates to Distance (same result)
//   - xFrac ∈ (0,1) compares only the left fraction
func TestPartialDistance(t *testing.T) {
	const rectW, rectH = 64, 48
	const photoW, photoH = 200, 170

	trueRect := blockPattern(rectW, rectH, false)
	quad := [4]Point{{20, 15}, {180, 30}, {170, 150}, {10, 140}}
	rectToPhoto, err := RectToQuad(rectW, rectH, quad)
	if err != nil {
		t.Fatalf("RectToQuad: %v", err)
	}
	photoToRect, err := rectToPhoto.Inverse()
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	photo := Warp(trueRect, photoToRect, photoW, photoH)

	proj, err := NewProjector(photo, quad, rectW, rectH)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}

	// xFrac ≤ 0 → always 1.
	got := proj.PartialDistance(trueRect, 0)
	if got != 1 {
		t.Errorf("PartialDistance(xFrac=0): got %.4f, want 1.0", got)
	}
	got = proj.PartialDistance(trueRect, -0.5)
	if got != 1 {
		t.Errorf("PartialDistance(xFrac=-0.5): got %.4f, want 1.0", got)
	}

	// xFrac ≥ 1 → delegates to Distance; results must be equal.
	wantFull := proj.Distance(trueRect)
	got = proj.PartialDistance(trueRect, 1.0)
	if got != wantFull {
		t.Errorf("PartialDistance(xFrac=1.0): got %.6f, want %.6f (same as Distance)", got, wantFull)
	}
	got = proj.PartialDistance(trueRect, 2.0)
	if got != wantFull {
		t.Errorf("PartialDistance(xFrac=2.0): got %.6f, want %.6f (same as Distance)", got, wantFull)
	}

	// xFrac = 0.5 → left half only; true candidate still matches.
	dHalf := proj.PartialDistance(trueRect, 0.5)
	if dHalf > 0.05 {
		t.Errorf("PartialDistance(xFrac=0.5, true candidate): got %.4f, want ≤ 0.05", dHalf)
	}
	// Wrong candidate (inverted) should score higher than true candidate.
	dHalfWrong := proj.PartialDistance(blockPattern(rectW, rectH, true), 0.5)
	if dHalfWrong <= dHalf+0.05 {
		t.Errorf("PartialDistance wrong > true: got dWrong=%.4f, dTrue=%.4f (want dWrong > dTrue+0.05)",
			dHalfWrong, dHalf)
	}
}

func TestNewProjector_errors(t *testing.T) {
	photo := patternImage(32, 32)
	quad := [4]Point{{0, 0}, {16, 0}, {16, 16}, {0, 16}}
	if _, err := NewProjector(photo, quad, 0, 16); err == nil {
		t.Error("NewProjector rectW=0: got nil error, want error")
	}
	// Degenerate quad (all corners identical) → singular homography.
	deg := [4]Point{{5, 5}, {5, 5}, {5, 5}, {5, 5}}
	if _, err := NewProjector(photo, deg, 16, 16); err == nil {
		t.Error("NewProjector degenerate quad: got nil error, want ErrSingular")
	}
}

// blockPattern builds a w×h RGBA of large constant-colour quadrants (low spatial
// frequency, so warp interpolation is near-lossless). When invert is true the
// colours are complemented, giving a clearly-different "wrong candidate".
func blockPattern(w, h int, invert bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			r, g, b := uint8(40), uint8(40), uint8(40)
			if x >= w/2 {
				r = 200
			}
			if y >= h/2 {
				g = 200
			}
			if x >= w/2 && y >= h/2 {
				b = 200
			}
			if invert {
				r, g, b = 255-r, 255-g, 255-b
			}
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = r, g, b, 255
		}
	}
	return img
}

func TestDetectQuad_recoversCorners(t *testing.T) {
	const photoW, photoH = 200, 170
	// A gray page with a white quad (the redaction region) filled in via the
	// homography membership test — exactly how a patch sits on a background.
	quad := [4]Point{{30, 20}, {175, 35}, {165, 150}, {15, 135}}
	const rw, rh = 120, 90
	p2r := mustPhotoToRect(t, rw, rh, quad)
	img := image.NewRGBA(image.Rect(0, 0, photoW, photoH))
	for y := range photoH {
		for x := range photoW {
			o := img.PixOffset(x, y)
			r := p2r.Apply(Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
			inside := r.X >= 0 && r.Y >= 0 && r.X < rw && r.Y < rh
			v := uint8(128) // gray background
			if inside {
				v = 255 // white region
			}
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = v, v, v, 255
		}
	}

	got, err := DetectQuad(img, 40)
	if err != nil {
		t.Fatalf("DetectQuad: %v", err)
	}
	// Edge-fit refinement recovers the vertices to within ~1.5px of a filled
	// convex quad (sub-pixel on the unforeshortened corner).
	for i := range got {
		ptClose(t, got[i], quad[i], 2.0, fmt.Sprintf("corner %d", i))
	}
}

func TestDetectQuad_noRegion(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = 255 // uniform white: no foreground
	}
	if _, err := DetectQuad(img, 40); err == nil {
		t.Error("DetectQuad on a uniform image: got nil error, want ErrNoRegion")
	}
}

func mustPhotoToRect(t *testing.T, rw, rh int, quad [4]Point) Matrix3 {
	t.Helper()
	r2p, err := RectToQuad(float64(rw), float64(rh), quad)
	if err != nil {
		t.Fatalf("RectToQuad: %v", err)
	}
	p2r, err := r2p.Inverse()
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	return p2r
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

func BenchmarkDetectQuad(b *testing.B) {
	const w, h = 200, 170
	quad := [4]Point{{30, 20}, {175, 35}, {165, 150}, {15, 135}}
	r2p, err := RectToQuad(120, 90, quad)
	if err != nil {
		b.Fatal(err)
	}
	p2r, err := r2p.Inverse()
	if err != nil {
		b.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			o := img.PixOffset(x, y)
			r := p2r.Apply(Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
			v := uint8(128)
			if r.X >= 0 && r.Y >= 0 && r.X < 120 && r.Y < 90 {
				v = 255
			}
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = v, v, v, 255
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		q, err := DetectQuad(img, 40)
		if err != nil {
			b.Fatal(err)
		}
		sinkQuad = q
	}
}

var sinkQuad [4]Point

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

var sinkFloat float64

func BenchmarkProjectorDistance(b *testing.B) {
	const rectW, rectH = 64, 48
	cand := blockPattern(rectW, rectH, false)
	quad := [4]Point{{20, 15}, {180, 30}, {170, 150}, {10, 140}}
	r2p, err := RectToQuad(rectW, rectH, quad)
	if err != nil {
		b.Fatal(err)
	}
	p2r, err := r2p.Inverse()
	if err != nil {
		b.Fatal(err)
	}
	photo := Warp(cand, p2r, 200, 170)
	proj, err := NewProjector(photo, quad, rectW, rectH)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		sinkFloat = proj.Distance(cand)
	}
}
