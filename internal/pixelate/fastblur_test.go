package pixelate

// fastblur_test.go exercises unexported helpers in fastblur.go that are not
// reachable through the public API with the inputs needed to cover every branch.

import "testing"

// TestClampByteI_branches verifies all three branches of clampByteI:
// negative input → 0, interior value → uint8 cast, and ≥255 → 255.
func TestClampByteI_branches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want uint8
	}{
		{in: -10, want: 0},   // v ≤ 0 → 0
		{in: 0, want: 0},     // v ≤ 0 → 0
		{in: 1, want: 1},     // interior
		{in: 128, want: 128}, // interior
		{in: 254, want: 254}, // interior
		{in: 255, want: 255}, // v ≥ 255 → 255
		{in: 300, want: 255}, // v ≥ 255 → 255
	}
	for _, tc := range cases {
		if got := clampByteI(tc.in); got != tc.want {
			t.Errorf("clampByteI(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
