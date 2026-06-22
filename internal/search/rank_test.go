package search_test

import (
	"math"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// approxEq reports whether a and b differ by less than eps.
func approxEq(a, b, eps float64) bool { return math.Abs(a-b) < eps }

func TestRankTopN_Basic(t *testing.T) {
	cands := []unpixel.Eval{
		{Guess: "b", Score: 0.3},
		{Guess: "a", Score: 0.1},
		{Guess: "c", Score: 0.2},
	}
	got := search.RankTopN(cands, 2)
	if len(got) != 2 {
		t.Fatalf("RankTopN(3 cands, 2): got len %d, want 2", len(got))
	}
	if got[0].Guess != "a" || got[0].Score != 0.1 {
		t.Errorf("RankTopN[0]: got %+v, want {Guess:a Score:0.1}", got[0])
	}
	if got[1].Guess != "c" || got[1].Score != 0.2 {
		t.Errorf("RankTopN[1]: got %+v, want {Guess:c Score:0.2}", got[1])
	}
}

func TestRankTopN_TieBreakByGuess(t *testing.T) {
	cands := []unpixel.Eval{
		{Guess: "z", Score: 0.1},
		{Guess: "a", Score: 0.1},
		{Guess: "m", Score: 0.1},
	}
	got := search.RankTopN(cands, 3)
	if len(got) != 3 {
		t.Fatalf("len: got %d, want 3", len(got))
	}
	// Tie broken by Guess string ascending: a < m < z.
	wantOrder := []string{"a", "m", "z"}
	for i, w := range wantOrder {
		if got[i].Guess != w {
			t.Errorf("RankTopN[%d].Guess: got %q, want %q", i, got[i].Guess, w)
		}
	}
}

func TestRankTopN_NGreaterThanLen(t *testing.T) {
	cands := []unpixel.Eval{
		{Guess: "x", Score: 0.05},
	}
	got := search.RankTopN(cands, 10)
	if len(got) != 1 {
		t.Errorf("n>len: got %d, want 1", len(got))
	}
}

func TestRankTopN_Empty(t *testing.T) {
	got := search.RankTopN(nil, 5)
	if got != nil {
		t.Errorf("empty cands: got %v, want nil", got)
	}
}

func TestRankTopN_NZeroOrNegative(t *testing.T) {
	cands := []unpixel.Eval{{Guess: "a", Score: 0.1}}
	if got := search.RankTopN(cands, 0); got != nil {
		t.Errorf("n=0: got %v, want nil", got)
	}
	if got := search.RankTopN(cands, -1); got != nil {
		t.Errorf("n=-1: got %v, want nil", got)
	}
}

func TestConfidence_Empty(t *testing.T) {
	conf, ambiguity := search.Confidence(nil)
	if conf != 0 || ambiguity != 0 {
		t.Errorf("empty: got conf=%v ambiguity=%v, want 0 0", conf, ambiguity)
	}
}

func TestConfidence_OneCandidate(t *testing.T) {
	top := []unpixel.Eval{{Guess: "a", Score: 0.1}}
	conf, ambiguity := search.Confidence(top)
	wantConf := 1 - 0.1
	if !approxEq(conf, wantConf, 1e-9) {
		t.Errorf("conf: got %v, want %v", conf, wantConf)
	}
	if ambiguity != 0 {
		t.Errorf("ambiguity (1 cand): got %v, want 0", ambiguity)
	}
}

func TestConfidence_TwoCandidates(t *testing.T) {
	top := []unpixel.Eval{
		{Guess: "a", Score: 0.1},
		{Guess: "b", Score: 0.3},
	}
	conf, ambiguity := search.Confidence(top)
	wantConf := 1 - 0.1
	wantAmb := 0.3 - 0.1
	if !approxEq(conf, wantConf, 1e-9) {
		t.Errorf("conf: got %v, want %v", conf, wantConf)
	}
	if !approxEq(ambiguity, wantAmb, 1e-9) {
		t.Errorf("ambiguity: got %v, want %v", ambiguity, wantAmb)
	}
}

// TestTrimEdgeSpaces verifies that phantom leading/trailing spaces produced by
// over-counted redaction grids are removed from finalized guesses, while
// interior spaces (e.g. "a b") are left intact.
func TestTrimEdgeSpaces(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "go ", want: "go"},
		{in: " x", want: "x"},
		{in: "  hello  ", want: "hello"},
		{in: "a b", want: "a b"},
		{in: "go run", want: "go run"},
		{in: "go", want: "go"},
		{in: " ", want: " "}, // all-space: leave as-is (Substantive filters these)
		{in: "", want: ""},
	}
	for _, tc := range tests {
		got := search.TrimEdgeSpaces(tc.in)
		if got != tc.want {
			t.Errorf("TrimEdgeSpaces(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRankTopN_TrimsEdgeSpaces verifies that RankTopN strips phantom leading/
// trailing spaces from each returned candidate's Guess.
func TestRankTopN_TrimsEdgeSpaces(t *testing.T) {
	cands := []unpixel.Eval{
		{Guess: "go ", Score: 0.05},
		{Guess: " x", Score: 0.10},
		{Guess: "a b", Score: 0.15},
	}
	got := search.RankTopN(cands, 3)
	wantGuesses := []string{"go", "x", "a b"}
	if len(got) != len(wantGuesses) {
		t.Fatalf("RankTopN: got %d results, want %d", len(got), len(wantGuesses))
	}
	for i, want := range wantGuesses {
		if got[i].Guess != want {
			t.Errorf("RankTopN[%d].Guess: got %q, want %q", i, got[i].Guess, want)
		}
	}
}
