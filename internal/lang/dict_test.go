package lang_test

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/lang"
)

// TestDictionary_membership verifies that known words are found and unknown
// strings are not.
func TestDictionary_membership(t *testing.T) {
	t.Parallel()
	d := lang.Dictionary()
	cases := []struct {
		word string
		want bool
	}{
		{"go", true},
		{"cat", true},
		{"hello", true},
		{"hi", true},
		{"yo", true},
		{"code", true},
		{"the", true},
		{"and", true},
		{"a", true},
		{"i", true},
		{"xqzj", false},
		{"bzzzt", false},
		{"HELLO", false}, // dict is lowercase-only; uppercase key misses
	}
	for _, tc := range cases {
		got := d.Contains(tc.word)
		if got != tc.want {
			t.Errorf("Dictionary.Contains(%q) = %v, want %v", tc.word, got, tc.want)
		}
	}
}

// TestDictionaryScore_ordering verifies that real-word strings score higher
// than nonsense of comparable length.
func TestDictionaryScore_ordering(t *testing.T) {
	t.Parallel()
	cases := []struct{ good, bad string }{
		{"hello", "xqzjv"},
		{"go cat", "go xqz"},
		{"the code", "xqz wkvb"},
		{"a", "q"},
		{"i", "z"},
	}
	for _, tc := range cases {
		g := lang.DictionaryScore(tc.good)
		b := lang.DictionaryScore(tc.bad)
		if g <= b {
			t.Errorf("DictionaryScore(%q)=%.3f should beat DictionaryScore(%q)=%.3f",
				tc.good, g, tc.bad, b)
		}
	}
}

// TestDictionaryScore_empty ensures an empty string returns exactly 0 and
// never panics.
func TestDictionaryScore_empty(t *testing.T) {
	t.Parallel()
	if s := lang.DictionaryScore(""); s != 0 {
		t.Errorf("DictionaryScore(\"\") = %v, want 0", s)
	}
}

// TestDictionaryScore_multiWord checks that a mostly-known phrase scores
// strictly higher than a mostly-unknown phrase of the same token count.
func TestDictionaryScore_multiWord(t *testing.T) {
	t.Parallel()
	// "go cat" — 2 known words; "go xqz" — 1 known + 1 garbage
	gGood := lang.DictionaryScore("go cat")
	gBad := lang.DictionaryScore("go xqz")
	if gGood <= gBad {
		t.Errorf("DictionaryScore(%q)=%.3f should beat DictionaryScore(%q)=%.3f",
			"go cat", gGood, "go xqz", gBad)
	}
}

// TestDictionaryPrior_composition ensures DictionaryPrior returns a non-nil
// function that agrees with DictionaryScore.
func TestDictionaryPrior_composition(t *testing.T) {
	t.Parallel()
	prior := lang.DictionaryPrior()
	if prior == nil {
		t.Fatal("DictionaryPrior() returned nil")
	}
	known := prior("hello")
	unknown := prior("xqzjv")
	if known <= unknown {
		t.Errorf("DictionaryPrior()(%q)=%.3f should beat prior(%q)=%.3f",
			"hello", known, "xqzjv", unknown)
	}
	if got := prior(""); got != 0 {
		t.Errorf("DictionaryPrior()(\"\") = %v, want 0", got)
	}
}

// TestDictionaryScore_noNegative ensures the scorer never returns negative
// values (it is a bonus-only prior).
func TestDictionaryScore_noNegative(t *testing.T) {
	t.Parallel()
	inputs := []string{"", "xqzjvwbk", "hello world", "the and or", "   "}
	for _, s := range inputs {
		if v := lang.DictionaryScore(s); v < 0 {
			t.Errorf("DictionaryScore(%q) = %v, want >= 0", s, v)
		}
	}
}
