// Package search (constraint.go) provides a Constraint primitive that prunes
// the candidate charset at each DFS depth without altering the unconstrained path.
//
// When partial cleartext is known — a fixed prefix such as "https://" or a
// per-position format mask such as a date template — a Constraint narrows the
// characters tried at each depth from the full Charset to the subset that is
// still consistent with the known structure. Positions beyond the constraint
// fall back to the full Charset, so the constraint is purely additive.
//
// The integration point is GuidedDFSConstrained: it accepts an optional
// Constraint alongside the standard GuidedDFS parameters. When nil, it is
// byte-identical to GuidedDFS. When non-nil, each call to chooseEvalChildren
// is preceded by a charset narrowing step that replaces cfg.Charset with the
// intersection of the Constraint's AllowedAt result and the original Charset.
// This keeps the inner loops (evalChildrenFromChars, evalChildrenParCappedFromChars)
// entirely unchanged; only the chars slice they receive is smaller.
package search

import (
	"context"
	"slices"
	"unicode/utf8"

	"github.com/oioio-space/unpixel"
)

// Constraint narrows the candidate charset at a specific search depth.
// AllowedAt returns the runes permitted at position pos given the candidate
// string built so far (parent). A nil return means "no restriction at this
// position" — the full cfg.Charset is used, which keeps the unconstrained
// code path byte-identical to GuidedDFS.
//
// Implementations must be safe for concurrent use: GuidedDFSConstrained may
// call AllowedAt from multiple goroutines when intra-node parallelism is active.
type Constraint interface {
	AllowedAt(pos int, parent string) []rune
}

// PrefixConstraint locks the first len(prefix) characters of every candidate to
// the corresponding characters of prefix. Positions beyond the prefix length are
// unconstrained (AllowedAt returns nil).
//
// Example — lock the first seven characters to "https://":
//
//	c := search.NewPrefixConstraint("https://")
type PrefixConstraint struct {
	prefix []rune
}

// NewPrefixConstraint returns a Constraint that locks candidate positions
// 0..len(prefix)-1 to the corresponding rune in prefix. An empty prefix
// returns a constraint whose AllowedAt always returns nil, so the search is
// byte-identical to unconstrained.
func NewPrefixConstraint(prefix string) *PrefixConstraint {
	return &PrefixConstraint{prefix: []rune(prefix)}
}

// AllowedAt returns a single-element slice containing prefix[pos] when pos is
// within the prefix, or nil when pos is beyond it.
func (p *PrefixConstraint) AllowedAt(pos int, _ string) []rune {
	if pos >= len(p.prefix) {
		return nil
	}
	return []rune{p.prefix[pos]}
}

// TemplateConstraint restricts each position to a pre-defined allowed-rune set.
// Positions beyond the template length are unconstrained. Use it to enforce
// format templates such as date patterns (YYYY-MM-DD), UUIDs, or IBANs.
//
// Example — restrict position 0 to uppercase letters, positions 1–8 to digits:
//
//	digits := []rune("0123456789")
//	uppers := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
//	c := search.NewTemplateConstraint([][]rune{uppers, digits, digits, ...})
type TemplateConstraint struct {
	slots [][]rune
}

// NewTemplateConstraint returns a Constraint that restricts position i to the
// runes in slots[i]. A nil or empty entry at slots[i] is treated as
// unconstrained (AllowedAt returns nil for that position). Positions beyond
// len(slots) are always unconstrained.
func NewTemplateConstraint(slots [][]rune) *TemplateConstraint {
	return &TemplateConstraint{slots: slots}
}

// AllowedAt returns slots[pos] when pos is within the template and the slot is
// non-empty, or nil otherwise.
func (t *TemplateConstraint) AllowedAt(pos int, _ string) []rune {
	if pos >= len(t.slots) || len(t.slots[pos]) == 0 {
		return nil
	}
	return t.slots[pos]
}

// narrowCharset intersects allowed (from Constraint.AllowedAt) with the runes in
// charset, returning a string of the runes that appear in both. If allowed is nil
// the original charset is returned unchanged (no allocation). Order follows the
// original charset so callers stay deterministic.
func narrowCharset(charset string, allowed []rune) string {
	if allowed == nil {
		return charset
	}
	// Build a lookup set from allowed — typically a single rune (prefix case) or
	// a small per-position charset (template case), so a linear scan is fine.
	out := make([]rune, 0, len(allowed))
	for _, ch := range charset {
		if slices.Contains(allowed, ch) {
			out = append(out, ch)
		}
	}
	return string(out)
}

// GuidedDFSConstrained runs the same algorithm as GuidedDFS but applies c at
// each depth to narrow the candidate charset before child evaluation. When c is
// nil the function is byte-identical to GuidedDFS: the same candidates are
// explored in the same order and the same results are emitted.
//
// Use it when part of the redacted text is known:
//
//   - Fixed prefix: NewPrefixConstraint("https://") locks the first eight
//     characters, turning 8 levels from O(|charset|) to O(1) each.
//   - Format template: NewTemplateConstraint(slots) restricts each position
//     to its allowed charset (e.g. digits only for a date field).
//
// The constraint does not affect the scorer, thresholds, or any other search
// parameter — it only reduces the rune set considered at each depth. The
// unconstrained path (c == nil) shares all hot-path code with GuidedDFS.
func GuidedDFSConstrained(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	c Constraint,
	emit func(unpixel.Eval),
) {
	if c == nil {
		GuidedDFS(ctx, scorer, cfg, offset, emit)
		return
	}
	cfg = ensureThresholdFor(cfg)
	iw := intraNodeWorkers(cfg)
	seeds := constrainedEvalChildren(ctx, scorer, cfg, offset, "", c, iw)
	for _, s := range seeds {
		emit(unpixel.Eval{Guess: s.guess, Score: s.result.Score, TooBig: s.result.TooBig})
	}
	for _, s := range seeds {
		if ctx.Err() != nil {
			return
		}
		guessRecursiveConstrained(ctx, scorer, cfg, offset, s, c, emit, iw)
	}
}

// guessRecursiveConstrained is the constrained analogue of guessRecursive.
func guessRecursiveConstrained(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parent node,
	c Constraint,
	emit func(unpixel.Eval),
	iw int,
) {
	if ctx.Err() != nil {
		return
	}
	if parent.result.TooBig {
		return
	}
	if len(parent.guess) >= cfg.MaxLength {
		return
	}
	children := constrainedEvalChildren(ctx, scorer, cfg, offset, parent.guess, c, iw)
	for _, child := range children {
		emit(unpixel.Eval{Guess: child.guess, Score: child.result.Score, TooBig: child.result.TooBig})
	}
	for _, child := range children {
		guessRecursiveConstrained(ctx, scorer, cfg, offset, child, c, emit, iw)
	}
}

// constrainedEvalChildren narrows cfg.Charset via the Constraint at the current
// depth (len(parentGuess)) and delegates to chooseEvalChildren with the narrowed
// config. The original cfg is never mutated.
func constrainedEvalChildren(
	ctx context.Context,
	scorer Scorer,
	cfg unpixel.Config,
	offset unpixel.Offset,
	parentGuess string,
	c Constraint,
	iw int,
) []node {
	allowed := c.AllowedAt(utf8.RuneCountInString(parentGuess), parentGuess)
	narrow := narrowCharset(cfg.Charset, allowed)
	if narrow == "" {
		// No intersection: this branch is structurally impossible; return nothing.
		return nil
	}
	if narrow == cfg.Charset {
		// No narrowing: reuse the existing fast path unchanged.
		return chooseEvalChildren(ctx, scorer, cfg, offset, parentGuess, iw)
	}
	cfgNarrow := cfg
	cfgNarrow.Charset = narrow
	return chooseEvalChildren(ctx, scorer, cfgNarrow, offset, parentGuess, iw)
}
