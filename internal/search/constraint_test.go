package search_test

import (
	"context"
	"slices"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// --- PrefixConstraint unit tests ---

// TestPrefixConstraint_allowedAt verifies that positions inside the prefix
// are locked to the single prefix rune, and positions beyond are unconstrained
// (AllowedAt returns nil).
func TestPrefixConstraint_allowedAt(t *testing.T) {
	c := search.NewPrefixConstraint("ab")

	// Position 0 → must return only 'a'.
	got := c.AllowedAt(0, "")
	if len(got) != 1 || got[0] != 'a' {
		t.Errorf("AllowedAt(0, \"\") = %v, want ['a']", got)
	}
	// Position 1 → must return only 'b'.
	got = c.AllowedAt(1, "a")
	if len(got) != 1 || got[0] != 'b' {
		t.Errorf("AllowedAt(1, \"a\") = %v, want ['b']", got)
	}
	// Position 2 (beyond prefix) → nil (unconstrained).
	got = c.AllowedAt(2, "ab")
	if got != nil {
		t.Errorf("AllowedAt(2, \"ab\") = %v, want nil (unconstrained)", got)
	}
}

// TestPrefixConstraint_nilWhenEmpty verifies that an empty-prefix constraint
// returns nil at every position, so the search path is identical to unconstrained.
func TestPrefixConstraint_nilWhenEmpty(t *testing.T) {
	c := search.NewPrefixConstraint("")
	for pos := range 5 {
		if got := c.AllowedAt(pos, ""); got != nil {
			t.Errorf("AllowedAt(%d, \"\") = %v, want nil for empty prefix", pos, got)
		}
	}
}

// --- TemplateConstraint unit tests ---

// TestTemplateConstraint_allowedAt verifies that each position returns the
// charset specified in the template, or nil for positions beyond the template.
func TestTemplateConstraint_allowedAt(t *testing.T) {
	// Template: pos 0 allows 'A','B'; pos 1 allows '0','1','2'.
	c := search.NewTemplateConstraint([][]rune{
		{'A', 'B'},
		{'0', '1', '2'},
	})

	got := c.AllowedAt(0, "")
	if !slices.Equal(got, []rune{'A', 'B'}) {
		t.Errorf("AllowedAt(0) = %v, want ['A','B']", got)
	}
	got = c.AllowedAt(1, "A")
	if !slices.Equal(got, []rune{'0', '1', '2'}) {
		t.Errorf("AllowedAt(1) = %v, want ['0','1','2']", got)
	}
	// Position beyond template → nil (unconstrained).
	got = c.AllowedAt(2, "A0")
	if got != nil {
		t.Errorf("AllowedAt(2) = %v, want nil (unconstrained)", got)
	}
}

// --- GuidedDFSConstrained integration tests ---

// constraintCountingScorer wraps a mockScorer and records each candidate
// that is evaluated so we can assert that forbidden candidates are never tried.
type constraintCountingScorer struct {
	inner     *mockScorer
	evaluated []string
}

func (s *constraintCountingScorer) Eval(ctx context.Context, guess, prevGuess string, offset unpixel.Offset) search.EvalResult {
	s.evaluated = append(s.evaluated, guess)
	return s.inner.Eval(ctx, guess, prevGuess, offset)
}

// TestGuidedDFSConstrained_prefixLocksFirstChars verifies that with a prefix
// constraint "ab", the first two positions are locked: only candidates starting
// with "ab" are ever evaluated, and the correct answer "abc" is found.
func TestGuidedDFSConstrained_prefixLocksFirstChars(t *testing.T) {
	scorer := &constraintCountingScorer{
		inner: &mockScorer{scores: map[string]search.EvalResult{
			"a":   {Score: 0.1},
			"ab":  {Score: 0.05},
			"abc": {Score: 0.02},
		}},
	}
	cfg := unpixel.Config{
		Charset:        "abcxyz",
		MaxLength:      5,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{}

	var found []string
	search.GuidedDFSConstrained(
		t.Context(), scorer, cfg, offset,
		search.NewPrefixConstraint("ab"),
		func(e unpixel.Eval) { found = append(found, e.Guess) },
	)

	// "abc" must be found.
	if !slices.Contains(found, "abc") {
		t.Errorf("GuidedDFSConstrained did not find 'abc'; found: %v", found)
	}

	// No candidate starting with 'x', 'y', or 'z' at position 0 should be evaluated.
	for _, g := range scorer.evaluated {
		r := []rune(g)
		if r[0] == 'x' || r[0] == 'y' || r[0] == 'z' {
			t.Errorf("forbidden prefix candidate %q was evaluated", g)
		}
	}
}

// TestGuidedDFSConstrained_nilIsIdentical verifies that passing a nil
// Constraint produces the exact same set of candidates as GuidedDFS.
func TestGuidedDFSConstrained_nilIsIdentical(t *testing.T) {
	scorer := &mockScorer{scores: map[string]search.EvalResult{
		"a":  {Score: 0.1},
		"ab": {Score: 0.05},
		"b":  {Score: 0.2},
	}}
	cfg := unpixel.Config{
		Charset:        "abc",
		MaxLength:      3,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{}

	var base, constrained []string
	search.GuidedDFS(t.Context(), scorer, cfg, offset, func(e unpixel.Eval) {
		base = append(base, e.Guess)
	})
	search.GuidedDFSConstrained(t.Context(), scorer, cfg, offset, nil, func(e unpixel.Eval) {
		constrained = append(constrained, e.Guess)
	})

	slices.Sort(base)
	slices.Sort(constrained)
	if !slices.Equal(base, constrained) {
		t.Errorf("GuidedDFSConstrained(nil) differs from GuidedDFS:\n  base:        %v\n  constrained: %v", base, constrained)
	}
}

// TestGuidedDFSConstrained_templateReducesSpace verifies that a template
// constraint restricts each position to its allowed charset, so candidates
// outside the template are never explored.
func TestGuidedDFSConstrained_templateReducesSpace(t *testing.T) {
	// Template: pos 0 only 'a' or 'b', pos 1 only '1' or '2'.
	tmpl := search.NewTemplateConstraint([][]rune{
		{'a', 'b'},
		{'1', '2'},
	})
	scorer := &constraintCountingScorer{
		inner: &mockScorer{scores: map[string]search.EvalResult{
			"a":  {Score: 0.1},
			"b":  {Score: 0.1},
			"a1": {Score: 0.05},
			"a2": {Score: 0.05},
			"b1": {Score: 0.05},
			"b2": {Score: 0.05},
		}},
	}
	cfg := unpixel.Config{
		Charset:        "ab12xyz",
		MaxLength:      2,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{}

	search.GuidedDFSConstrained(t.Context(), scorer, cfg, offset, tmpl, func(unpixel.Eval) {})

	// Only 'a' and 'b' should appear at position 0.
	for _, g := range scorer.evaluated {
		if len([]rune(g)) >= 1 {
			r := []rune(g)[0]
			if r != 'a' && r != 'b' {
				t.Errorf("position-0 forbidden rune %c evaluated in %q", r, g)
			}
		}
		if len([]rune(g)) >= 2 {
			r := []rune(g)[1]
			if r != '1' && r != '2' {
				t.Errorf("position-1 forbidden rune %c evaluated in %q", r, g)
			}
		}
	}
}

// --- Benchmark ---

// BenchmarkGuidedDFSConstrained_prefixSpeedup measures the search-space reduction
// from a prefix constraint. The sub-benchmark "no_constraint" is the unconstrained
// baseline; "with_prefix" locks the first two characters, turning the branching
// factor at depth 0 and 1 from |charset| to 1.
func BenchmarkGuidedDFSConstrained_prefixSpeedup(b *testing.B) {
	// All charset chars score below threshold so the DFS explores the full tree,
	// making the search-space reduction from the prefix clearly visible.
	const charset = "abcdefghijklmnop" // 16 chars → 16^3 = 4096 nodes at MaxLength=3
	scores := make(map[rune]float64, len(charset))
	for _, ch := range charset {
		scores[ch] = 0.05 // always passes threshold=0.25
	}
	scorer := &scriptedScorer{charScore: scores}
	cfg := unpixel.Config{
		Charset:        charset,
		MaxLength:      3,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
	}
	offset := unpixel.Offset{}
	prefix := search.NewPrefixConstraint("ab") // locks 2 of 3 levels → 1×1×16 = 16 nodes

	b.Run("no_constraint", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var evals []unpixel.Eval
			search.GuidedDFSConstrained(context.Background(), scorer, cfg, offset, nil, func(e unpixel.Eval) {
				evals = append(evals, e)
			})
			sinkEvals = evals
		}
	})
	b.Run("with_prefix", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var evals []unpixel.Eval
			search.GuidedDFSConstrained(context.Background(), scorer, cfg, offset, prefix, func(e unpixel.Eval) {
				evals = append(evals, e)
			})
			sinkEvals = evals
		}
	})
}
