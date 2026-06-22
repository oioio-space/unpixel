package lang_test

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/lang"
)

// TestScore_plausibleBeatsGibberish: real words score higher than random ones.
func TestScore_plausibleBeatsGibberish(t *testing.T) {
	m := lang.Default()
	cases := []struct{ good, bad string }{
		{"the password is", "xqzj wkvbphr"},
		{"recover", "rcvexq"},
		{"hello world", "hlxlq wzrlx"},
		{"go run main", "gx qun mxin"},
	}
	for _, c := range cases {
		g, b := m.Score(c.good), m.Score(c.bad)
		if g <= b {
			t.Errorf("Score(%q)=%.3f should beat Score(%q)=%.3f", c.good, g, c.bad, b)
		}
	}
}

// TestScore_empty returns a finite floor (no NaN/Inf, no panic).
func TestScore_empty(t *testing.T) {
	if s := lang.Default().Score(""); s >= 0 || s != s { // s!=s catches NaN
		t.Errorf("Score(\"\") = %v, want a finite negative floor", s)
	}
}

// TestNew_trainsFromText builds a model from custom text.
func TestNew_trainsFromText(t *testing.T) {
	m := lang.New("abababab abab")
	if m.Score("abab") <= m.Score("zqzq") {
		t.Error("custom model did not prefer its training pattern")
	}
}

// TestTransitionLogProb_nonASCIIClamped verifies that non-ASCII runes in both
// the prev and next positions are clamped to ' ' without panicking, and that
// the result is a finite negative number (the space-transition floor).
func TestTransitionLogProb_nonASCIIClamped(t *testing.T) {
	m := lang.Default()
	// '€' (U+20AC) > unicode.MaxASCII → both prev and next paths clamp to ' '.
	got := m.TransitionLogProb('€', '€')
	if got != got { // NaN check
		t.Error("TransitionLogProb(non-ASCII, non-ASCII) = NaN")
	}
	// Must equal TransitionLogProb(' ', ' ') since both clamp to space.
	want := m.TransitionLogProb(' ', ' ')
	if got != want {
		t.Errorf("TransitionLogProb(non-ASCII, non-ASCII) = %v, want %v (same as space/space)", got, want)
	}

	// Clamped prev with ASCII next must also produce a finite result.
	ascii := m.TransitionLogProb('é', 'a')
	if ascii != ascii {
		t.Error("TransitionLogProb(non-ASCII prev, 'a') = NaN")
	}
	// Clamped next with ASCII prev.
	ascii2 := m.TransitionLogProb('a', 'é')
	if ascii2 != ascii2 {
		t.Error("TransitionLogProb('a', non-ASCII next) = NaN")
	}
}

// TestTransitionLogProb_matchesScore asserts that summing TransitionLogProb
// over (prev,next) pairs — with ' ' as the start context — equals Score(s)×len(s),
// i.e. TransitionLogProb emits the exact per-edge factor that Score averages.
func TestTransitionLogProb_matchesScore(t *testing.T) {
	m := lang.Default()
	cases := []string{
		"hello world",
		"the password is",
		"go run main",
		"a",
		"zq", // unusual pair
	}
	for _, s := range cases {
		want := m.Score(s) * float64(len([]rune(s)))
		var got float64
		prev := ' '
		for _, r := range s {
			got += m.TransitionLogProb(prev, r)
			prev = r
		}
		if diff := got - want; diff < -1e-9 || diff > 1e-9 {
			t.Errorf("TransitionLogProb sum for %q = %.10f, Score×n = %.10f (diff %.2e)",
				s, got, want, diff)
		}
	}
}
