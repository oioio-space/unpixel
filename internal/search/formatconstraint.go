package search

import "github.com/oioio-space/unpixel/internal/secrets"

// FormatConstraint adapts a [secrets.Format] into the [Constraint] interface,
// restricting each search position to the runes feasible for that format. It is
// stateless apart from its immutable fields, so a single value is safe for
// concurrent use by the parallel offset workers (like [PrefixConstraint]).
type FormatConstraint struct {
	format   secrets.Format
	totalLen int
}

// NewFormatConstraint returns a [Constraint] for format f. totalLen is the
// search's length upper bound (cfg.MaxLength); it enables the last-position
// checksum trick for fixed-length formats such as [secrets.FormatCreditCard].
// A [secrets.FormatNone] format yields a constraint whose AllowedAt always
// returns nil, so the search is byte-identical to unconstrained.
func NewFormatConstraint(f secrets.Format, totalLen int) *FormatConstraint {
	return &FormatConstraint{format: f, totalLen: totalLen}
}

// AllowedAt returns the runes feasible at pos given parent (the decoded prefix).
// It delegates directly to [secrets.AllowedRunesAt]; the returned slice must
// not be mutated.
func (c *FormatConstraint) AllowedAt(pos int, parent string) []rune {
	return secrets.AllowedRunesAt(c.format, pos, parent, c.totalLen)
}
