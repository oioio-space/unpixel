# Checksum Pruning in the Trellis — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prune candidates that fail structured-secret format constraints *during* the guided search (not just as a post-hoc plausibility bonus), shrinking the search space and improving recovery of credit cards (Luhn), IBANs, dates, phones, and numeric IDs — strictly opt-in so free text is never affected.

**Architecture:** A new `secrets.Format` enum drives a per-position feasibility function (`AllowedRunesAt`) and a full-string validator (`Valid`). `internal/search` adapts these into the existing `Constraint` interface (`FormatConstraint`); the root `unpixel.Engine` gains a `WithExpectedFormat` option that installs the constraint through the existing `GuidedDFSConstrained` path, plus a leaf filter that drops complete-but-invalid candidates from the results competition without halting exploration. The MCP `unpixel_decode` engine path forwards an optional `expected_format` field.

**Tech Stack:** Pure Go (no CGO). Reuses `internal/search` `Constraint`/`GuidedDFSConstrained` and `internal/secrets` `Luhn`. Standard library only (`time` for date validation).

## Global Constraints

- **NO CGO, ever.** `CGO_ENABLED=0` is pinned; never add `import "C"` or a cgo-requiring dependency. Enforced by `mise run cgo:check`.
- **Pure-Go libraries only** (stdlib + existing deps). No new third-party dependency.
- **Opt-in / byte-identical default:** `WithExpectedFormat(FormatNone)` or the option absent ⇒ decoding is **byte-identical** to today. The decode core is untouched except additive wiring mirroring `WithPrefix`/`DefaultConstrainedStrategy`. The 17/17 synthetic fixture panel must remain 17/17 (it sets no format).
- **Caged tests only:** never run `go test` bare. Use `scripts/gotest-caged.sh` (memory-capped cgroup) for every test run.
- **In-memory fixtures only:** every test image is generated in code or committed; never load a gitignored or network-fetched fixture (CI runs on a fresh checkout).
- **Coverage gate:** `COVER_MIN=85`; keep coverage ≥ 85% (`mise run cover:check`).
- **Commit gate:** each commit goes through the pre-commit review gate. Arm the `/simplify` marker (`$GIT_DIR/claude-simplify-ok`) **only after** genuinely completing the review, and in a **separate bash call** from `git commit`. Run `git restore --staged PROGRESS.md` before arming (the post-commit hook re-stages it).
- **Commit trailer:** every commit message ends with
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** `feat/checksum-trellis` (create from `master`; do not commit on `master`).
- Follow `go-style-guide` and `use-modern-go` (e.g. `for range` / `min`/`max`, `any`, table tests).

## Import-graph facts (verified, do not re-litigate)

- `internal/secrets` imports neither root `unpixel` nor `internal/search` — it is a leaf. So:
  - root `unpixel` **may** import `internal/secrets` (for `secrets.Format` in `WithExpectedFormat`) — no cycle.
  - `internal/search` **may** import `internal/secrets` (for the constraint adapter) — no cycle.
- `search.Constraint` is `interface { AllowedAt(pos int, parent string) []rune }` (`internal/search/constraint.go:35`). `parent` is the prefix decoded so far. `nil` return = "no constraint at this position".
- `search.GuidedDFSConstrained(ctx, scorer, cfg, offset, c, emit)` delegates to `GuidedDFS` (byte-identical) when `c == nil`. `emit` reports each explored node; **recursion into children is independent of whether a node is emitted**, so suppressing an emit never prunes the subtree.
- `cfg.MaxLength` (`unpixel.Config.MaxLength`, default `DefaultMaxLength = 20`) is the only length signal the search has; `TooBig` (rendered wider than redaction) is the real terminator.

---

## File Structure

- **Create** `internal/secrets/format.go` — `Format` enum, `AllowedRunesAt`, `Valid`, `TerminalLen`, `ParseFormat`, and unexported validators (`luhnCheckDigit`, `ibanValid`, `dateValid`, phone validators). One responsibility: format feasibility + validation. Reuses existing `secrets.Luhn`.
- **Create** `internal/secrets/format_test.go` — unit tests for the above.
- **Create** `internal/search/formatconstraint.go` — `FormatConstraint` adapting a `secrets.Format` to `search.Constraint`. Stateless (immutable fields) ⇒ concurrency-safe.
- **Create** `internal/search/formatconstraint_test.go` — unit tests for the adapter.
- **Modify** `unpixel.go` — add `Config.expectedFormat`, `WithExpectedFormat`, the `DefaultFormatStrategy` hook var, and the wiring in `Engine.Run`.
- **Modify** `defaults/defaults.go` — wire `DefaultFormatStrategy` to a new `formatConstrainedStrategy` (mirrors `constrainedGuidedStrategy`) that installs the constraint + leaf filter.
- **Create/Modify** `defaults/formatconstraint_integration_test.go` — end-to-end recovery + node-count + no-regression tests with in-memory fixtures.
- **Modify** `mcp/decode.go` — add `expected_format` to `decodeInput`/`DecodeOptions`, forward on the engine path.
- **Modify** `mcp/decode_test.go` (or a new `mcp/decode_format_test.go`) — test the field forwards and is ignored off-engine.

---

## Task 1: `internal/secrets/format.go` — format model

**Files:**
- Create: `internal/secrets/format.go`
- Test: `internal/secrets/format_test.go`

**Interfaces:**
- Consumes: existing `secrets.Luhn(s string) bool` (in `internal/secrets/secrets.go`).
- Produces (relied on by Tasks 2–4):
  - `type Format uint8`
  - `const ( FormatNone Format = iota; FormatDigits; FormatCreditCard; FormatIBAN; FormatDate; FormatPhoneFR; FormatPhoneUS; FormatPhoneE164 )`
  - `func AllowedRunesAt(f Format, pos int, prefix string, totalLen int) []rune`
  - `func Valid(f Format, s string) bool`
  - `func TerminalLen(f Format, n int) bool`
  - `func ParseFormat(s string) (Format, bool)`

**Design notes (read before coding):**
- `AllowedRunesAt` returns the runes feasible at `pos` given `prefix` (the decoded-so-far string) and `totalLen` (the search's `cfg.MaxLength`; treat as an upper bound, not a guaranteed exact length). Return `nil` only for `FormatNone`. Feasibility is *per-position* — it cannot see the whole string, so checksums are enforced two ways: (a) the **last-position Luhn** trick when `totalLen` is a valid card length, and (b) the leaf `Valid` filter wired in Task 3.
- `Valid` validates a **complete** string for the format.
- `TerminalLen(f, n)` reports whether `n` runes is a length at which `f` could be complete. The Task-3 leaf filter only validates (and possibly drops) candidates whose length is terminal, so in-progress prefixes are never dropped.
- Phone formats accept both the national form and the international (`+CC`) form; `AllowedRunesAt` branches on whether `prefix` already starts with `+`. At `pos == 0` with an empty prefix it returns the **union** of feasible leading runes.

- [ ] **Step 1: Write the failing tests**

Create `internal/secrets/format_test.go`:

```go
package secrets

import (
	"slices"
	"testing"
)

func runesString(rs []rune) string { return string(rs) }

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{
		"":            FormatNone,
		"none":        FormatNone,
		"digits":      FormatDigits,
		"credit_card": FormatCreditCard,
		"card":        FormatCreditCard,
		"iban":        FormatIBAN,
		"date":        FormatDate,
		"phone_fr":    FormatPhoneFR,
		"phone_us":    FormatPhoneUS,
		"phone_e164":  FormatPhoneE164,
		"e164":        FormatPhoneE164,
	}
	for in, want := range cases {
		got, ok := ParseFormat(in)
		if !ok || got != want {
			t.Errorf("ParseFormat(%q) = %v,%v; want %v,true", in, got, ok, want)
		}
	}
	if _, ok := ParseFormat("nonsense"); ok {
		t.Errorf("ParseFormat(nonsense) ok = true; want false")
	}
}

func TestAllowedRunesAt_digits(t *testing.T) {
	got := AllowedRunesAt(FormatDigits, 0, "", 0)
	if runesString(got) != "0123456789" {
		t.Errorf("digits pos0 = %q; want all digits", runesString(got))
	}
}

func TestAllowedRunesAt_none(t *testing.T) {
	if AllowedRunesAt(FormatNone, 0, "", 0) != nil {
		t.Errorf("FormatNone must return nil (no constraint)")
	}
}

func TestAllowedRunesAt_creditCardLuhnLastPosition(t *testing.T) {
	// 16-digit card, first 15 known. Only the Luhn-correct last digit is allowed.
	prefix := "453201511283036" // 15 digits
	got := AllowedRunesAt(FormatCreditCard, 15, prefix, 16)
	if len(got) != 1 {
		t.Fatalf("last-position card constraint = %q; want exactly one check digit", runesString(got))
	}
	if !Luhn(prefix + string(got[0])) {
		t.Errorf("constrained check digit %q does not satisfy Luhn", string(got[0]))
	}
	// A non-last position is just digits.
	if runesString(AllowedRunesAt(FormatCreditCard, 0, "", 16)) != "0123456789" {
		t.Errorf("card pos0 should be all digits")
	}
}

func TestAllowedRunesAt_phoneFRleading(t *testing.T) {
	got := AllowedRunesAt(FormatPhoneFR, 0, "", 0)
	if !slices.Contains(got, '0') || !slices.Contains(got, '+') {
		t.Errorf("FR phone pos0 = %q; want union containing '0' and '+'", runesString(got))
	}
	// After national leading 0, second digit is 1-9 (no 0).
	got = AllowedRunesAt(FormatPhoneFR, 1, "0", 0)
	if slices.Contains(got, '0') || !slices.Contains(got, '1') || !slices.Contains(got, '9') {
		t.Errorf("FR phone pos1 after '0' = %q; want 1-9", runesString(got))
	}
}

func TestAllowedRunesAt_phoneUSleading(t *testing.T) {
	got := AllowedRunesAt(FormatPhoneUS, 0, "", 0)
	if slices.Contains(got, '0') || slices.Contains(got, '1') {
		t.Errorf("US phone pos0 = %q; NANP area code cannot start 0 or 1", runesString(got))
	}
	if !slices.Contains(got, '+') {
		t.Errorf("US phone pos0 = %q; want '+' as intl alternative", runesString(got))
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		f    Format
		s    string
		want bool
	}{
		{FormatNone, "anything", true},
		{FormatDigits, "12345", true},
		{FormatDigits, "12a45", false},
		{FormatCreditCard, "4532015112830366", true},  // valid Luhn-16
		{FormatCreditCard, "4532015112830367", false}, // bad Luhn
		{FormatCreditCard, "1234", false},             // not a card length
		{FormatIBAN, "GB82WEST12345698765432", true},  // valid mod-97
		{FormatIBAN, "GB82WEST12345698765433", false}, // bad mod-97
		{FormatDate, "2024-02-29", true},              // leap day, ISO
		{FormatDate, "2023-02-29", false},             // not a leap year
		{FormatDate, "29/02/2024", true},              // leap day, DMY
		{FormatDate, "2024-13-01", false},             // month 13
		{FormatPhoneFR, "0612345678", true},
		{FormatPhoneFR, "+33612345678", true},
		{FormatPhoneFR, "0012345678", false}, // second digit 0
		{FormatPhoneUS, "4155550123", true},
		{FormatPhoneUS, "+14155550123", true},
		{FormatPhoneUS, "1155550123", false}, // area code starts 1
		{FormatPhoneE164, "+33612345678", true},
		{FormatPhoneE164, "+0612345678", false}, // leading digit 0
		{FormatPhoneE164, "0612345678", false},  // no '+'
	}
	for _, c := range cases {
		if got := Valid(c.f, c.s); got != c.want {
			t.Errorf("Valid(%v, %q) = %v; want %v", c.f, c.s, got, c.want)
		}
	}
}

func TestTerminalLen(t *testing.T) {
	if !TerminalLen(FormatCreditCard, 16) || TerminalLen(FormatCreditCard, 15+1-2) {
		// 16 is a card length; 14 is not.
	}
	if TerminalLen(FormatCreditCard, 14) {
		t.Errorf("14 is not a card length")
	}
	if !TerminalLen(FormatDate, 10) || TerminalLen(FormatDate, 9) {
		t.Errorf("date terminal length is 10")
	}
	if !TerminalLen(FormatPhoneFR, 10) || !TerminalLen(FormatPhoneFR, 12) {
		t.Errorf("FR phone terminal lengths are 10 and 12")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `scripts/gotest-caged.sh ./internal/secrets/ -run 'Format|AllowedRunesAt|Valid|TerminalLen|ParseFormat' -v`
Expected: FAIL — `undefined: Format`, `undefined: AllowedRunesAt`, etc.

- [ ] **Step 3: Write the implementation**

Create `internal/secrets/format.go`:

```go
package secrets

import "time"

// Format enumerates the structured-secret formats the constraint system can
// prune for. The zero value FormatNone applies no constraint.
type Format uint8

const (
	// FormatNone applies no constraint (free text). AllowedRunesAt returns nil.
	FormatNone Format = iota
	// FormatDigits is a variable-length numeric string (PIN, numeric ID).
	FormatDigits
	// FormatCreditCard is a Luhn-checked card number of length 13, 15, 16, or 19.
	FormatCreditCard
	// FormatIBAN is a generic IBAN: 2 letters, 2 check digits, alphanumeric BBAN,
	// total length 15-34, validated by the ISO 7064 mod-97 checksum. No per-country
	// BBAN structure is enforced.
	FormatIBAN
	// FormatDate is a calendar date in one of two common layouts: YYYY-MM-DD or DD/MM/YYYY.
	FormatDate
	// FormatPhoneFR is a French phone number: national 0X######## (X in 1-9) or
	// international +33X######## (X in 1-9).
	FormatPhoneFR
	// FormatPhoneUS is a NANP phone number: national NXX-NXX-XXXX (area and exchange
	// first digit 2-9) or international +1 followed by the 10 NANP digits.
	FormatPhoneUS
	// FormatPhoneE164 is a generic E.164 number: '+' then 7-15 digits, first digit 1-9.
	FormatPhoneE164
)

// digitRunes is the shared 0-9 rune set returned for numeric positions.
// It is never mutated by callers.
var digitRunes = []rune("0123456789")

// upperRunes is A-Z, used for IBAN country-code positions.
var upperRunes = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")

// alnumUpperRunes is A-Z plus 0-9, used for IBAN BBAN positions.
var alnumUpperRunes = []rune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")

// digit2to9Runes is 2-9, used for NANP leading positions.
var digit2to9Runes = []rune("23456789")

// digit1to9Runes is 1-9, used for leading positions that forbid zero.
var digit1to9Runes = []rune("123456789")

// cardLens are the valid credit-card lengths.
var cardLens = [...]int{13, 15, 16, 19}

func isCardLen(n int) bool {
	for _, l := range cardLens {
		if n == l {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ParseFormat maps a lowercase format name to a Format. The empty string and
// "none" map to FormatNone. The bool is false for unrecognised names.
func ParseFormat(s string) (Format, bool) {
	switch s {
	case "", "none":
		return FormatNone, true
	case "digits":
		return FormatDigits, true
	case "credit_card", "card":
		return FormatCreditCard, true
	case "iban":
		return FormatIBAN, true
	case "date":
		return FormatDate, true
	case "phone_fr":
		return FormatPhoneFR, true
	case "phone_us":
		return FormatPhoneUS, true
	case "phone_e164", "e164":
		return FormatPhoneE164, true
	default:
		return FormatNone, false
	}
}

// AllowedRunesAt returns the runes feasible at position pos given prefix (the
// string decoded so far) and totalLen (the search's length upper bound; 0 if
// unknown). It returns nil when the format imposes no constraint at this
// position (always for FormatNone). The returned slice must not be mutated.
func AllowedRunesAt(f Format, pos int, prefix string, totalLen int) []rune {
	switch f {
	case FormatDigits:
		return digitRunes
	case FormatCreditCard:
		// Checksum-in-the-trellis: when the target length is a valid card length
		// and we are at the last position with a complete digit prefix, allow only
		// the Luhn-correct check digit.
		if isCardLen(totalLen) && pos == totalLen-1 && len(prefix) == totalLen-1 && allDigits(prefix) {
			if d, ok := luhnCheckDigit(prefix); ok {
				return []rune{d}
			}
		}
		return digitRunes
	case FormatIBAN:
		switch {
		case pos < 2:
			return upperRunes
		case pos < 4:
			return digitRunes
		default:
			return alnumUpperRunes
		}
	case FormatDate:
		return dateRunesAt(pos, prefix)
	case FormatPhoneFR:
		return phoneFRRunesAt(pos, prefix)
	case FormatPhoneUS:
		return phoneUSRunesAt(pos, prefix)
	case FormatPhoneE164:
		return e164RunesAt(pos)
	default: // FormatNone
		return nil
	}
}

// Valid reports whether s is a complete, well-formed value for format f.
func Valid(f Format, s string) bool {
	switch f {
	case FormatNone:
		return true
	case FormatDigits:
		return allDigits(s)
	case FormatCreditCard:
		return isCardLen(len(s)) && Luhn(s)
	case FormatIBAN:
		return ibanValid(s)
	case FormatDate:
		return dateValid(s)
	case FormatPhoneFR:
		return phoneFRValid(s)
	case FormatPhoneUS:
		return phoneUSValid(s)
	case FormatPhoneE164:
		return e164Valid(s)
	default:
		return false
	}
}

// TerminalLen reports whether a string of n runes is at a length where format f
// could be complete. The leaf filter validates only terminal-length candidates,
// so in-progress prefixes are never dropped.
func TerminalLen(f Format, n int) bool {
	switch f {
	case FormatDigits:
		return n >= 1
	case FormatCreditCard:
		return isCardLen(n)
	case FormatIBAN:
		return n >= 15 && n <= 34
	case FormatDate:
		return n == 10
	case FormatPhoneFR:
		return n == 10 || n == 12 // 0X######## or +33X########
	case FormatPhoneUS:
		return n == 10 || n == 12 // NXXNXXXXXX or +1 + 10
	case FormatPhoneE164:
		return n >= 8 && n <= 16 // '+' plus 7-15 digits
	default:
		return false
	}
}

// luhnCheckDigit returns the single digit that, appended to the all-digit
// prefix, makes the whole string pass Luhn. ok is false if prefix is not all
// digits.
func luhnCheckDigit(prefix string) (rune, bool) {
	if !allDigits(prefix) {
		return 0, false
	}
	// The check digit sits at the rightmost (un-doubled) position, so the
	// rightmost prefix digit is doubled, then alternating leftward.
	sum := 0
	double := true
	for i := len(prefix) - 1; i >= 0; i-- {
		d := int(prefix[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	check := (10 - sum%10) % 10
	return rune('0' + check), true
}

// ibanValid reports whether s is a structurally valid IBAN with a correct
// mod-97 checksum (ISO 7064). It does not validate per-country BBAN layout.
func ibanValid(s string) bool {
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case i < 2:
			if c < 'A' || c > 'Z' {
				return false
			}
		case i < 4:
			if c < '0' || c > '9' {
				return false
			}
		default:
			if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				return false
			}
		}
	}
	// Move the first four characters to the end, then compute mod 97 treating
	// letters as A=10 .. Z=35, processed digit by digit to avoid big integers.
	rearranged := s[4:] + s[:4]
	rem := 0
	for i := 0; i < len(rearranged); i++ {
		c := rearranged[i]
		if c >= '0' && c <= '9' {
			rem = (rem*10 + int(c-'0')) % 97
		} else {
			v := int(c-'A') + 10
			rem = (rem*100 + v) % 97
		}
	}
	return rem == 1
}

// dateValid reports whether s parses as a calendar date in one of the supported
// layouts. time.Parse enforces field ranges (month 1-12, valid day per month,
// leap years).
func dateValid(s string) bool {
	for _, layout := range []string{"2006-01-02", "02/01/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			// Reject zero-padding-loose matches by requiring round-trip equality.
			if t.Format(layout) == s {
				return true
			}
		}
	}
	return false
}

// dateMasks encodes the supported date layouts as a per-position mask: 'D' means
// a digit position, any other byte is a required literal separator.
var dateMasks = []string{
	"DDDD-DD-DD", // YYYY-MM-DD
	"DD/DD/DDDD", // DD/MM/YYYY
}

// dateRunesAt returns the union of feasible runes at pos across the date layouts
// still consistent with prefix.
func dateRunesAt(pos int, prefix string) []rune {
	var set []rune
	digitsAdded := false
	for _, mask := range dateMasks {
		if pos >= len(mask) || !datePrefixOK(mask, prefix) {
			continue
		}
		if mask[pos] == 'D' {
			if !digitsAdded {
				set = append(set, digitRunes...)
				digitsAdded = true
			}
			continue
		}
		set = appendUnique(set, rune(mask[pos]))
	}
	return set
}

// datePrefixOK reports whether prefix is consistent with mask up to its length:
// every separator position covered by prefix must match the mask literal, and
// every digit position must hold a digit.
func datePrefixOK(mask, prefix string) bool {
	if len(prefix) > len(mask) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if mask[i] == 'D' {
			if prefix[i] < '0' || prefix[i] > '9' {
				return false
			}
		} else if prefix[i] != mask[i] {
			return false
		}
	}
	return true
}

// phoneFRRunesAt returns feasible runes for a French phone number.
func phoneFRRunesAt(pos int, prefix string) []rune {
	if hasPlusPrefix(prefix) || (pos > 0 && prefix == "+") {
		// International form: +33 X ######## (X in 1-9).
		switch pos {
		case 0:
			return []rune{'+'}
		case 1, 2:
			return []rune{'3'}
		case 3:
			return digit1to9Runes
		default:
			return digitRunes
		}
	}
	if pos == 0 {
		// Union of national leading '0' and international '+'.
		return []rune{'0', '+'}
	}
	// National form: 0 X ######## (X in 1-9).
	if pos == 1 {
		return digit1to9Runes
	}
	return digitRunes
}

// phoneUSRunesAt returns feasible runes for a NANP phone number.
func phoneUSRunesAt(pos int, prefix string) []rune {
	if hasPlusPrefix(prefix) || (pos > 0 && prefix == "+") {
		// International form: +1 then 10 NANP digits.
		switch pos {
		case 0:
			return []rune{'+'}
		case 1:
			return []rune{'1'}
		case 2, 5: // area-code first digit, exchange first digit
			return digit2to9Runes
		default:
			return digitRunes
		}
	}
	if pos == 0 {
		// Union of national NANP leading digit (2-9) and international '+'.
		return append([]rune{'+'}, digit2to9Runes...)
	}
	// National form: positions 0 and 3 are 2-9, the rest digits.
	if pos == 3 {
		return digit2to9Runes
	}
	return digitRunes
}

// e164RunesAt returns feasible runes for a generic E.164 number.
func e164RunesAt(pos int) []rune {
	switch pos {
	case 0:
		return []rune{'+'}
	case 1:
		return digit1to9Runes
	default:
		return digitRunes
	}
}

func hasPlusPrefix(s string) bool { return len(s) > 0 && s[0] == '+' }

// appendUnique appends r to set only if absent.
func appendUnique(set []rune, r rune) []rune {
	for _, x := range set {
		if x == r {
			return set
		}
	}
	return append(set, r)
}

// phoneFRValid validates a complete French phone number.
func phoneFRValid(s string) bool {
	switch {
	case len(s) == 10 && s[0] == '0':
		return s[1] >= '1' && s[1] <= '9' && allDigits(s)
	case len(s) == 12 && s[:3] == "+33":
		rest := s[3:]
		return rest[0] >= '1' && rest[0] <= '9' && allDigits(rest)
	default:
		return false
	}
}

// phoneUSValid validates a complete NANP phone number.
func phoneUSValid(s string) bool {
	switch {
	case len(s) == 10:
		return nanpDigitsValid(s)
	case len(s) == 12 && s[:2] == "+1":
		return nanpDigitsValid(s[2:])
	default:
		return false
	}
}

// nanpDigitsValid validates exactly 10 NANP digits: area-code first digit and
// exchange first digit are 2-9, all are digits.
func nanpDigitsValid(s string) bool {
	if len(s) != 10 || !allDigits(s) {
		return false
	}
	return s[0] >= '2' && s[0] <= '9' && s[3] >= '2' && s[3] <= '9'
}

// e164Valid validates a generic E.164 number: '+' then 7-15 digits, first 1-9.
func e164Valid(s string) bool {
	if len(s) < 8 || len(s) > 16 || s[0] != '+' {
		return false
	}
	rest := s[1:]
	return rest[0] >= '1' && rest[0] <= '9' && allDigits(rest)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `scripts/gotest-caged.sh ./internal/secrets/ -run 'Format|AllowedRunesAt|Valid|TerminalLen|ParseFormat' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Run lint on the package**

Run: `mise run lint` (or `golangci-lint run ./internal/secrets/...`)
Expected: no findings.

- [ ] **Step 6: Commit**

```bash
git add internal/secrets/format.go internal/secrets/format_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# (review for /simplify, then arm the marker in a SEPARATE bash call)
git commit -m "feat(secrets): add Format model — per-position feasibility + checksum validation

Format enum (digits/card/IBAN/date/phone FR/US/E164), AllowedRunesAt for
trellis pruning (incl. Luhn last-position when length known), Valid (Luhn,
mod-97, date ranges, region phone plans), TerminalLen, ParseFormat.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `internal/search/formatconstraint.go` — Constraint adapter

**Files:**
- Create: `internal/search/formatconstraint.go`
- Test: `internal/search/formatconstraint_test.go`

**Interfaces:**
- Consumes: `secrets.Format`, `secrets.AllowedRunesAt` (Task 1); `search.Constraint` interface (`internal/search/constraint.go`).
- Produces (relied on by Task 3): `func NewFormatConstraint(f secrets.Format, totalLen int) *FormatConstraint` returning a `*FormatConstraint` whose `AllowedAt(pos int, parent string) []rune` satisfies `search.Constraint`.

- [ ] **Step 1: Write the failing test**

Create `internal/search/formatconstraint_test.go`:

```go
package search

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/secrets"
)

func TestFormatConstraint_satisfiesInterface(t *testing.T) {
	var _ Constraint = NewFormatConstraint(secrets.FormatDigits, 0)
}

func TestFormatConstraint_delegatesToSecrets(t *testing.T) {
	c := NewFormatConstraint(secrets.FormatDigits, 0)
	got := c.AllowedAt(0, "")
	if string(got) != "0123456789" {
		t.Errorf("digits AllowedAt(0) = %q; want all digits", string(got))
	}
}

func TestFormatConstraint_cardLuhnLastPosition(t *testing.T) {
	c := NewFormatConstraint(secrets.FormatCreditCard, 16)
	got := c.AllowedAt(15, "453201511283036")
	if len(got) != 1 {
		t.Fatalf("card last-position = %q; want one check digit", string(got))
	}
}

func TestFormatConstraint_noneReturnsNil(t *testing.T) {
	c := NewFormatConstraint(secrets.FormatNone, 0)
	if c.AllowedAt(0, "") != nil {
		t.Errorf("FormatNone constraint must return nil")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `scripts/gotest-caged.sh ./internal/search/ -run 'FormatConstraint' -v`
Expected: FAIL — `undefined: NewFormatConstraint`.

- [ ] **Step 3: Write the implementation**

Create `internal/search/formatconstraint.go`:

```go
package search

import "github.com/oioio-space/unpixel/internal/secrets"

// FormatConstraint adapts a secrets.Format into the Constraint interface,
// restricting each search position to the runes feasible for that format. It is
// stateless apart from its immutable fields, so a single value is safe for
// concurrent use by the parallel offset workers (like PrefixConstraint).
type FormatConstraint struct {
	format   secrets.Format
	totalLen int
}

// NewFormatConstraint returns a Constraint for format f. totalLen is the
// search's length upper bound (cfg.MaxLength); it enables the last-position
// checksum trick for fixed-length formats. A FormatNone format yields a
// constraint whose AllowedAt always returns nil, so the search is byte-identical
// to unconstrained.
func NewFormatConstraint(f secrets.Format, totalLen int) *FormatConstraint {
	return &FormatConstraint{format: f, totalLen: totalLen}
}

// AllowedAt returns the runes feasible at pos given parent (the decoded prefix).
func (c *FormatConstraint) AllowedAt(pos int, parent string) []rune {
	return secrets.AllowedRunesAt(c.format, pos, parent, c.totalLen)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/gotest-caged.sh ./internal/search/ -run 'FormatConstraint' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/search/formatconstraint.go internal/search/formatconstraint_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# (review for /simplify, then arm the marker in a SEPARATE bash call)
git commit -m "feat(search): add FormatConstraint adapter over secrets.Format

Stateless Constraint impl delegating to secrets.AllowedRunesAt; concurrency-safe
like PrefixConstraint. FormatNone returns nil (byte-identical search).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `WithExpectedFormat` option + strategy wiring + leaf filter

**Files:**
- Modify: `unpixel.go` (add `Config.expectedFormat`; `WithExpectedFormat`; `DefaultFormatStrategy` hook var; wiring in `Engine.Run` next to the existing `DefaultConstrainedStrategy` block around `unpixel.go:1597`).
- Modify: `defaults/defaults.go` (wire `unpixel.DefaultFormatStrategy` in `init()`; add `formatConstrainedStrategy` mirroring `constrainedGuidedStrategy` around `defaults/defaults.go:284`).
- Test: `defaults/formatconstraint_integration_test.go` (created in Task 5).

**Interfaces:**
- Consumes: `secrets.Format`, `secrets.TerminalLen`, `secrets.Valid` (Task 1); `search.NewFormatConstraint` (Task 2); existing `search.GuidedDFSConstrained`, `search.Offsets`, `search.NewCachingScorer`, `search.NewPipelineScorer`.
- Produces (relied on by Task 4 and tests):
  - `func WithExpectedFormat(f secrets.Format) Option`
  - `var DefaultFormatStrategy func(format secrets.Format) Strategy`

**Design notes:**
- Mirror the existing `WithPrefix` / `DefaultConstrainedStrategy` pattern exactly.
- In `Engine.Run`, install the format strategy only when **no prefix** is set (prefix wins; document this), the format is not `FormatNone`, and the hook is wired — so the byte-identical default is preserved.
- The leaf filter lives inside the strategy's `dfs` closure: wrap `emit` to drop a candidate whose rune-length is terminal for the format **and** fails `secrets.Valid`. Suppressing an emit does not stop child exploration (verified against `constraint.go`), so a 16-digit non-Luhn prefix is removed from the results race while its 19-digit continuations are still explored.

- [ ] **Step 1: Add the Config field, option, and hook var to `unpixel.go`**

First add the import. In the import block of `unpixel.go`, add:

```go
	"github.com/oioio-space/unpixel/internal/secrets"
```

Add the field to the `Config` struct (next to the existing `prefix` field — find it with `grep -n 'prefix ' unpixel.go`):

```go
	// expectedFormat, when not FormatNone, constrains the guided search to runes
	// feasible for the declared structured-secret format and drops complete-but-
	// invalid candidates. Set via WithExpectedFormat. Ignored when a prefix is set.
	expectedFormat secrets.Format
```

Add the option (next to `WithPrefix` at `unpixel.go:1889`):

```go
// WithExpectedFormat constrains the guided search to candidates feasible for a
// structured-secret format (credit card, IBAN, date, phone, numeric ID): each
// position is limited to the runes the format allows, the Luhn check digit is
// fixed at the last position of a fixed-length card, and complete candidates
// that fail the format's checksum/range rules are dropped before ranking.
//
// It is strictly opt-in: WithExpectedFormat(secrets.FormatNone) or omitting the
// option leaves decoding byte-identical to the unconstrained search. Declaring
// the wrong format will wrongly reject the true answer, so use it only when the
// redaction's format is known. Ignored when WithPrefix is also set (prefix wins).
func WithExpectedFormat(f secrets.Format) Option {
	return func(c *Config) { c.expectedFormat = f }
}
```

Add the hook var (next to `DefaultConstrainedStrategy` at `unpixel.go:1553`):

```go
// DefaultFormatStrategy is a hook populated by importing the defaults package
// for its side-effect. It returns a Strategy that runs GuidedDFS constrained to
// the given structured-secret format (per-position feasibility plus a leaf
// validity filter). A nil hook means WithExpectedFormat is a no-op, so callers
// that wire all components explicitly and do not use WithExpectedFormat need not
// import defaults.
var DefaultFormatStrategy func(format secrets.Format) Strategy
```

- [ ] **Step 2: Wire the strategy selection in `Engine.Run`**

Find the existing prefix block (`unpixel.go:1597`):

```go
	if e.cfg.prefix != "" && DefaultConstrainedStrategy != nil {
		e.cfg.Strategy = DefaultConstrainedStrategy(e.cfg.prefix)
	}
```

Add immediately after it:

```go
	if e.cfg.prefix == "" && e.cfg.expectedFormat != secrets.FormatNone && DefaultFormatStrategy != nil {
		e.cfg.Strategy = DefaultFormatStrategy(e.cfg.expectedFormat)
	}
```

- [ ] **Step 3: Wire the hook and strategy in `defaults/defaults.go`**

Add the import to the defaults import block (`defaults/defaults.go` around line 40):

```go
	"github.com/oioio-space/unpixel/internal/secrets"
```

In `init()` (after the `DefaultConstrainedStrategy` assignment, `defaults/defaults.go:60`):

```go
	unpixel.DefaultFormatStrategy = func(f secrets.Format) unpixel.Strategy {
		return formatConstrainedStrategy{format: f}
	}
```

Add the strategy type (after `constrainedGuidedStrategy`, `defaults/defaults.go:307`):

```go
// formatConstrainedStrategy implements unpixel.Strategy using
// GuidedDFSConstrained with a secrets.Format constraint, plus a leaf filter that
// drops complete-but-invalid candidates (failing Luhn/mod-97/date/phone rules)
// from the results competition. It is wired by the DefaultFormatStrategy hook
// when WithExpectedFormat is active.
type formatConstrainedStrategy struct {
	format secrets.Format
}

// Search runs offset discovery then, per surviving offset, GuidedDFSConstrained
// with the format constraint and a validity leaf filter. Dropping an emit only
// removes a candidate from the results race; child exploration is unaffected, so
// shorter invalid prefixes (e.g. a non-Luhn 16-digit card) do not prevent longer
// valid candidates (a 19-digit card) from being found.
func (s formatConstrainedStrategy) Search(
	ctx context.Context,
	redacted *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	scorer := search.NewCachingScorer(search.NewPipelineScorer(redacted, cfg), cfg.CacheSize)
	dfs := func(ctx context.Context, sc search.Scorer, cfg unpixel.Config, offset unpixel.Offset, emit func(unpixel.Eval)) {
		c := search.NewFormatConstraint(s.format, cfg.MaxLength)
		filtered := func(ev unpixel.Eval) {
			if secrets.TerminalLen(s.format, len([]rune(ev.Guess))) && !secrets.Valid(s.format, ev.Guess) {
				return
			}
			emit(ev)
		}
		search.GuidedDFSConstrained(ctx, sc, cfg, offset, c, filtered)
	}
	search.Offsets(ctx, scorer, cfg, out, results, dfs)
}
```

- [ ] **Step 4: Build to verify it compiles**

Run: `CGO_ENABLED=0 go build ./...`
Expected: builds clean (no import cycle, no undefined symbols).

- [ ] **Step 5: Run the existing suites to verify no regression**

Run: `scripts/gotest-caged.sh ./... -run 'Recover|Prefix|Strategy|Engine' -count=1`
Expected: PASS — existing prefix/engine behaviour unchanged.

- [ ] **Step 6: Commit**

```bash
git add unpixel.go defaults/defaults.go
git restore --staged PROGRESS.md 2>/dev/null || true
# (review for /simplify, then arm the marker in a SEPARATE bash call)
git commit -m "feat(unpixel): WithExpectedFormat — checksum pruning in the guided search

Opt-in format constraint wired like WithPrefix: per-position feasibility via
FormatConstraint through GuidedDFSConstrained + leaf filter dropping complete-
but-invalid candidates. FormatNone/absent and any WithPrefix use are byte-
identical (prefix wins).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: MCP `expected_format` field on the engine path

**Files:**
- Modify: `mcp/decode.go` (`decodeInput`, `DecodeOptions`, `Decode`, `handleDecode`, `decodeEngine`).
- Test: `mcp/decode_format_test.go` (created in Task 5).

**Interfaces:**
- Consumes: `secrets.ParseFormat`, `secrets.Format` (Task 1); `unpixel.WithExpectedFormat` (Task 3).
- Produces: a forwarded `expected_format` string on the `engine` decode path.

**Design notes:**
- `expected_format` only affects the **engine** path (`decodeEngine`), the only method that reaches `unpixel.Engine`. Document it as ignored elsewhere (consistent with how `prefix` is documented as a no-op).
- Import `internal/secrets` in `mcp/decode.go` (allowed — mcp already imports root `unpixel`; secrets is a leaf).

- [ ] **Step 1: Add the import**

In `mcp/decode.go` import block, add:

```go
	"github.com/oioio-space/unpixel/internal/secrets"
```

- [ ] **Step 2: Add the field to `decodeInput`**

After the `Prefix` field (`mcp/decode.go:110`):

```go
	// ExpectedFormat constrains the engine search to a structured-secret format
	// (digits|credit_card|iban|date|phone_fr|phone_us|phone_e164). Forwarded only
	// to the engine method via unpixel.WithExpectedFormat; ignored by all other
	// decoders. Declaring the wrong format will reject the true answer. Omit for
	// free text.
	ExpectedFormat string `json:"expected_format,omitzero" jsonschema:"Engine-only structured-secret format: digits|credit_card|iban|date|phone_fr|phone_us|phone_e164 (omit for free text)"`
```

- [ ] **Step 3: Add the field to `DecodeOptions` and thread it through `Decode`/`handleDecode`**

In `DecodeOptions` (after `Prefix`, `mcp/decode.go:218`):

```go
	// ExpectedFormat constrains the engine search to a structured-secret format.
	// Forwarded only to the engine method; ignored by other decoders.
	ExpectedFormat string
```

In `Decode`, add to the `in := decodeInput{...}` literal (after `Prefix: opts.Prefix,`):

```go
		ExpectedFormat:   opts.ExpectedFormat,
```

In `handleDecode`, add to the `opts := DecodeOptions{...}` literal (after `Prefix: in.Prefix,`):

```go
		ExpectedFormat:   in.ExpectedFormat,
```

- [ ] **Step 4: Forward it in `decodeEngine`**

In `decodeEngine`, after the charset option is appended to `opts` (after `mcp/decode.go:485`), add:

```go
	if f, ok := secrets.ParseFormat(in.ExpectedFormat); ok && f != secrets.FormatNone {
		opts = append(opts, unpixel.WithExpectedFormat(f))
	}
```

Also append a note when active (in the `notes` assembly near the end of `decodeEngine`):

```go
	if f, ok := secrets.ParseFormat(in.ExpectedFormat); ok && f != secrets.FormatNone {
		notes = append(notes, fmt.Sprintf("expected_format=%s applied", in.ExpectedFormat))
	}
```

- [ ] **Step 5: Build to verify it compiles**

Run: `CGO_ENABLED=0 go build ./mcp/...`
Expected: builds clean.

- [ ] **Step 6: Commit**

```bash
git add mcp/decode.go
git restore --staged PROGRESS.md 2>/dev/null || true
# (review for /simplify, then arm the marker in a SEPARATE bash call)
git commit -m "feat(mcp): forward expected_format to the engine decode path

Optional decode field parsed via secrets.ParseFormat and applied with
unpixel.WithExpectedFormat on the engine method only; documented as ignored
elsewhere (like prefix).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Integration validation, node-count assertion, coverage

**Files:**
- Create: `defaults/formatconstraint_integration_test.go`
- Create: `mcp/decode_format_test.go`

**Interfaces:**
- Consumes everything above: `unpixel.Recover` with `unpixel.WithExpectedFormat`, `unpixel.WithCharset`, `unpixel.WithBlockSize`, `unpixel.WithStyle`, `unpixel.WithMaxLength`; `mcpserver.Decode`; `defaults` wiring (import for side-effect).

**Design notes:**
- Build the fixture **in memory**: render a known digit string with the default renderer, pixelate it with the default block-average pixelator, then recover with and without the format. This mirrors how existing `defaults` tests construct fixtures — find the helper with `grep -rn "func.*[Ff]ixture\|pixelate\|BlockAverage\|render.NewXImage" defaults/*_test.go` and reuse it; if none fits, render+pixelate inline using `defaults.Wire` on a `unpixel.Config{BlockSize: N}`.
- The **node-count** assertion uses a counting `Metric` wrapper (every candidate evaluation calls the metric once per offset). Inject it via `unpixel.WithMetric` so both runs share identical config except the format, then assert constrained evaluations < unconstrained.

- [ ] **Step 1: Write the failing integration tests**

Create `defaults/formatconstraint_integration_test.go`:

```go
package defaults_test

import (
	"context"
	"image"
	"sync/atomic"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/secrets"
)

// renderPixelated renders text with the default components and pixelates it,
// returning an in-memory mosaic image plus the block size used. No file or
// network I/O.
func renderPixelated(t *testing.T, text string, block int, fontSize float64) image.Image {
	t.Helper()
	cfg := unpixel.Config{BlockSize: block, Style: unpixel.Style{FontSize: fontSize}}
	cfg = applyTestDefaults(t, cfg)
	rendered, _, err := cfg.Renderer.Render(text, cfg.Style)
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	return cfg.Pixelator.Pixelate(rendered)
}

// applyTestDefaults wires the standard components onto cfg for fixture building.
func applyTestDefaults(t *testing.T, cfg unpixel.Config) unpixel.Config {
	t.Helper()
	if err := defaults.Wire(&cfg); err != nil {
		t.Fatalf("wire defaults: %v", err)
	}
	return cfg
}

// countingMetric wraps a Metric and counts comparisons.
type countingMetric struct {
	inner unpixel.Metric
	count *int64
}

func (m countingMetric) Compare(a, b *image.RGBA) float64 {
	atomic.AddInt64(m.count, 1)
	return m.inner.Compare(a, b)
}

func TestExpectedFormat_digitsRecovers(t *testing.T) {
	const secret = "8675309"
	block := 6
	img := renderPixelated(t, secret, block, 18)

	res, err := unpixel.Recover(context.Background(), img,
		unpixel.WithCharset("0123456789"),
		unpixel.WithBlockSize(block),
		unpixel.WithStyle(unpixel.Style{FontSize: 18}),
		unpixel.WithMaxLength(len(secret)),
		unpixel.WithExpectedFormat(secrets.FormatDigits),
	)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if res.BestGuess != secret {
		t.Errorf("digits recovery = %q; want %q", res.BestGuess, secret)
	}
}

func TestExpectedFormat_creditCardPassesLuhn(t *testing.T) {
	const card = "4532015112830366" // valid Luhn-16
	block := 6
	img := renderPixelated(t, card, block, 18)

	res, err := unpixel.Recover(context.Background(), img,
		unpixel.WithCharset("0123456789"),
		unpixel.WithBlockSize(block),
		unpixel.WithStyle(unpixel.Style{FontSize: 18}),
		unpixel.WithMaxLength(len(card)),
		unpixel.WithExpectedFormat(secrets.FormatCreditCard),
	)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !secrets.Valid(secrets.FormatCreditCard, res.BestGuess) {
		t.Errorf("card recovery %q does not pass Luhn/length", res.BestGuess)
	}
}

func TestExpectedFormat_fewerNodesThanUnconstrained(t *testing.T) {
	const secret = "8675309"
	block := 6

	count := func(format secrets.Format) int64 {
		img := renderPixelated(t, secret, block, 18)
		var n int64
		base := unpixel.Config{}
		if err := defaults.Wire(&base); err != nil {
			t.Fatalf("wire: %v", err)
		}
		opts := []unpixel.Option{
			unpixel.WithCharset("0123456789"),
			unpixel.WithBlockSize(block),
			unpixel.WithStyle(unpixel.Style{FontSize: 18}),
			unpixel.WithMaxLength(len(secret)),
			unpixel.WithMetric(countingMetric{inner: base.Metric, count: &n}),
		}
		if format != secrets.FormatNone {
			opts = append(opts, unpixel.WithExpectedFormat(format))
		}
		if _, err := unpixel.Recover(context.Background(), img, opts...); err != nil {
			t.Fatalf("recover: %v", err)
		}
		return n
	}

	unconstrained := count(secrets.FormatNone)
	constrained := count(secrets.FormatDigits)
	if constrained >= unconstrained {
		t.Errorf("constrained evals = %d; want fewer than unconstrained %d", constrained, unconstrained)
	}
}

func TestExpectedFormat_noFormatByteIdentical(t *testing.T) {
	const secret = "go2"
	block := 6
	img := renderPixelated(t, secret, block, 18)

	opts := []unpixel.Option{
		unpixel.WithCharset(unpixel.CharsetAlnum),
		unpixel.WithBlockSize(block),
		unpixel.WithStyle(unpixel.Style{FontSize: 18}),
		unpixel.WithMaxLength(len(secret)),
	}
	a, err := unpixel.Recover(context.Background(), img, opts...)
	if err != nil {
		t.Fatalf("recover a: %v", err)
	}
	b, err := unpixel.Recover(context.Background(), img, append(opts, unpixel.WithExpectedFormat(secrets.FormatNone))...)
	if err != nil {
		t.Fatalf("recover b: %v", err)
	}
	if a.BestGuess != b.BestGuess {
		t.Errorf("FormatNone changed result: %q vs %q", a.BestGuess, b.BestGuess)
	}
}
```

> Before running, verify the exact method names on the wired components with
> `grep -n 'func.*Render(' internal/render/*.go`, `grep -n 'func.*Pixelate(' internal/pixelate/*.go`,
> and `grep -n 'Compare(' unpixel.go internal/metric/*.go`. Adjust the helper calls
> (`Render`, `Pixelate`, `Compare`) and `WithMetric`/`WithRenderer` option names to match. The
> `unpixel.Metric` interface method may be named `Distance`/`Compare` — use whatever the
> interface declares.

- [ ] **Step 2: Run to verify they fail (then pass after fixups)**

Run: `scripts/gotest-caged.sh ./defaults/ -run 'ExpectedFormat' -v -count=1`
Expected: initially FAIL if helper signatures differ; fix the helper to match the real component method names, then PASS. If a digit fixture does not recover at `block=6/fontSize=18`, adjust block/font so the unconstrained run also recovers (the point is constrained ≤ unconstrained and recovery succeeds, not specific geometry).

- [ ] **Step 3: Write the MCP forwarding test**

Create `mcp/decode_format_test.go`:

```go
package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel/internal/secrets"
)

func TestParseFormat_engineFieldNames(t *testing.T) {
	// The MCP field accepts the documented names.
	for _, name := range []string{"digits", "credit_card", "iban", "date", "phone_fr", "phone_us", "phone_e164"} {
		if _, ok := secrets.ParseFormat(name); !ok {
			t.Errorf("ParseFormat(%q) not recognised", name)
		}
	}
}

func TestDecodeEngine_expectedFormatRecoversDigits(t *testing.T) {
	const secret = "8675309"
	img := renderPixelatedMCP(t, secret, 6, 18)
	res, err := Decode(context.Background(), img, "engine", DecodeOptions{
		CharsetPreset:  "digits",
		BlockSize:      6,
		FontSize:       18,
		MaxLength:      len(secret),
		ExpectedFormat: "digits",
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Text != secret {
		t.Errorf("text = %q; want %q", res.Text, secret)
	}
	joined := strings.Join(res.Notes, " ")
	if !strings.Contains(joined, "expected_format=digits") {
		t.Errorf("notes = %v; want an expected_format note", res.Notes)
	}
}
```

> Provide `renderPixelatedMCP` in this test file mirroring the `defaults` helper
> (render with `defaults.Wire` + `Pixelate`). If the `mcp` package already has an
> in-memory fixture helper (`grep -n 'func render\|Pixelate\|fixture' mcp/*_test.go`),
> reuse it instead of duplicating.

- [ ] **Step 4: Run the MCP test**

Run: `scripts/gotest-caged.sh ./mcp/ -run 'ParseFormat_engineFieldNames|DecodeEngine_expectedFormat' -v -count=1`
Expected: PASS.

- [ ] **Step 5: Verify the panel invariant and full suite**

Run: `scripts/gotest-caged.sh ./... -count=1`
Expected: PASS, including the 17/17 panel paths (they set no format, so behaviour is unchanged).

- [ ] **Step 6: Coverage gate**

Run: `mise run cover:check`
Expected: total coverage ≥ 85%. If `internal/secrets` validators are under-covered, add table cases to `format_test.go` for the IBAN/date/phone branches (esp. the international `+CC` phone branches and `dateRunesAt` separator union).

- [ ] **Step 7: Full CI gate**

Run: `mise run ci`
Expected: lint + test + cgo:check + scans all pass.

- [ ] **Step 8: Commit**

```bash
git add defaults/formatconstraint_integration_test.go mcp/decode_format_test.go
git restore --staged PROGRESS.md 2>/dev/null || true
# (review for /simplify, then arm the marker in a SEPARATE bash call)
git commit -m "test(format): integration recovery + node-count + no-format regression

In-memory digit/card fixtures recover under WithExpectedFormat; constrained
search evaluates fewer nodes; FormatNone is byte-identical; MCP engine path
forwards expected_format. Coverage >= 85%.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against `docs/superpowers/specs/2026-06-29-checksum-trellis-pruning-design.md`):
- §3.1 Format model in `internal/secrets` (Format, AllowedRunesAt, Valid) → Task 1. ✅
- §3.1 reuse `Luhn`, add `ibanValid`/`dateValid`/`phoneValid(region)` → Task 1 (`ibanValid`, `dateValid`, `phoneFRValid`/`phoneUSValid`/`e164Valid`). ✅
- §3.2 `formatConstraint` implementing `search.Constraint`, stateless/concurrent-safe → Task 2. ✅
- §3.2 digits / card Luhn-last-position / IBAN positions / date bounds / phone region → Task 1 `AllowedRunesAt` + validators. ✅
- §3.3 leaf checksum filter → Task 3 `formatConstrainedStrategy` emit wrapper using `TerminalLen`+`Valid`. ✅
- §3.4 opt-in `WithExpectedFormat` via the constrained path / hook, non-set ⇒ unchanged → Task 3. ✅
- §2.1 byte-identical when absent/None → Task 3 wiring (guarded) + Task 5 `noFormatByteIdentical`. ✅
- §2.2 digits fixture recovers → Task 5 `digitsRecovers`. ✅
- §2.3 card passes Luhn + fewer nodes → Task 5 `creditCardPassesLuhn` + `fewerNodesThanUnconstrained`. ✅
- §2.4 pure-Go, caged, in-memory, coverage ≥85 → Global Constraints + Task 5 steps 6-7. ✅
- §4 documented limits (generic IBAN, common date layouts, region phones) → encoded in doc comments (Task 1) and option doc (Task 3). ✅
- §5 MCP `decode` gains `expected_format` (string → Format) → Task 4. ✅

**2. Placeholder scan:** No "TBD"/"handle edge cases"/"similar to Task N" — every code step shows complete code. The two `grep`-to-confirm notes in Task 5 are explicit verification steps (the real component method names), not placeholders for logic. ✅

**3. Type consistency:** `Format`/`FormatNone`/`AllowedRunesAt`/`Valid`/`TerminalLen`/`ParseFormat` (Task 1) are used with identical signatures in Tasks 2–4. `NewFormatConstraint(secrets.Format, int)` (Task 2) matches its use in Task 3. `DefaultFormatStrategy func(format secrets.Format) Strategy` and `WithExpectedFormat(secrets.Format) Option` (Task 3) match Task 4's use. The `Metric` interface method name in the Task 5 `countingMetric` is flagged for verification against the real interface (`Compare` vs `Distance`). ✅

> **Known open detail for the implementer:** the spec calls the type `formatConstraint` (lowercase); this plan exports it as `FormatConstraint` because Task 3 (`defaults` package) must construct it from outside `internal/search`. This is the correct visibility — note it in the Task 2 report.
