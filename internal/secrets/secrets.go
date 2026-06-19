// Package secrets scores candidate strings for their resemblance to real
// secrets and structured tokens (UUIDs, hex tokens, Luhn-valid card numbers,
// base64 tokens, common passwords). It is used as a composable prior for
// UnPixel's final-ranking step: strings that look like secrets are preferred
// when image distance alone cannot separate candidates.
package secrets

import (
	_ "embed"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// Kind classifies the structural format of a candidate string.
// The zero value is KindUnknown.
type Kind int

const (
	// KindUnknown means the string did not match any recognised format.
	KindUnknown Kind = iota
	// KindUUID is a 8-4-4-4-12 hex UUID, e.g. "550e8400-e29b-41d4-a716-446655440000".
	KindUUID
	// KindHexToken is a hex string of 16 or more characters, e.g. an API token or HMAC key.
	KindHexToken
	// KindBase64Token is a base64-ish string of 16 or more mixed-case/digit characters.
	KindBase64Token
	// KindDigits is a run of four or more decimal digits (e.g. a PIN or year).
	KindDigits
	// KindLuhn is a digit-only string that also passes the Luhn checksum
	// (credit-card, IMEI, etc.). It takes precedence over KindDigits.
	KindLuhn
)

// precompiled patterns for Classify. Package-level vars are initialised once.
var (
	// reUUID matches 8-4-4-4-12 hex segments, case-insensitive.
	reUUID = regexp.MustCompile(
		`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
	)
	// reHex matches 16 or more hex characters (full string, no separators).
	reHex = regexp.MustCompile(`(?i)^[0-9a-f]{16,}$`)
	// reBase64 matches 16 or more base64/base64url characters with mixed case.
	// We require at least one letter and one digit to avoid classifying all-digit
	// strings (already captured by KindDigits/KindLuhn) as base64.
	reBase64 = regexp.MustCompile(`^[A-Za-z0-9+/_-]{16,}$`)
	// reDigits matches four or more decimal digits.
	reDigits = regexp.MustCompile(`^[0-9]{4,}$`)
)

// base64HasMixedCase reports whether s contains at least one letter and one
// digit, distinguishing a base64 token from an all-digit or all-letter run.
func base64HasMixedCase(s string) bool {
	hasLetter, hasDigit := false, false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
		if hasLetter && hasDigit {
			return true
		}
	}
	return false
}

// Luhn reports whether s is a valid Luhn-checksum string. It returns false
// when s contains any non-digit character or has fewer than two digits.
func Luhn(s string) bool {
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	// Standard Luhn algorithm: double every second digit from the right.
	sum := 0
	odd := false // whether the current position (from right) is odd
	for i := len(s) - 1; i >= 0; i-- {
		d := int(s[i] - '0')
		if odd {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		odd = !odd
	}
	return sum%10 == 0
}

// Classify returns the Kind that best describes s. When multiple formats
// match, the most specific wins: KindLuhn takes precedence over KindDigits,
// and KindUUID takes precedence over KindHexToken.
func Classify(s string) Kind {
	switch {
	case reUUID.MatchString(s):
		return KindUUID
	case reDigits.MatchString(s):
		// Both KindLuhn and KindDigits require all-digit; Luhn is more specific.
		if Luhn(s) {
			return KindLuhn
		}
		return KindDigits
	case reHex.MatchString(s):
		return KindHexToken
	case reBase64.MatchString(s) && base64HasMixedCase(s):
		return KindBase64Token
	default:
		return KindUnknown
	}
}

//go:embed common_passwords.txt
var commonPasswordsRaw string

// passwordSet is the in-memory lookup set, built exactly once.
var (
	passwordSetOnce sync.Once
	passwordSet     map[string]struct{}
)

// initPasswordSet populates passwordSet from the embedded file.
func initPasswordSet() {
	passwordSetOnce.Do(func() {
		lines := strings.SplitSeq(commonPasswordsRaw, "\n")
		m := make(map[string]struct{}, 256)
		for line := range lines {
			if t := strings.TrimSpace(line); t != "" {
				m[strings.ToLower(t)] = struct{}{}
			}
		}
		passwordSet = m
	})
}

// IsCommonPassword reports whether s (case-insensitive) is in the embedded
// list of the most frequently used passwords. The lookup runs in O(1).
func IsCommonPassword(s string) bool {
	initPasswordSet()
	_, ok := passwordSet[strings.ToLower(s)]
	return ok
}

// Prior constants — all in log-space, intended to be added to other priors.
// The magnitudes are deliberately small so the structured-secret bonus breaks
// ties toward secret-shaped strings without dominating the image-distance score.
const (
	// BonusStructured is added for any recognised non-digit structural format
	// (UUID, hex token, base64 token). A structured token is more likely to be
	// a real secret than an arbitrary word. +1.0 is one log-unit of preference,
	// roughly equivalent to a 2.7× plausibility multiplier.
	BonusStructured = 1.0

	// BonusDigits is added when the string is a pure digit run (KindDigits).
	// Smaller than BonusStructured because digits alone are less diagnostic
	// of a real secret (they may be any number), but still worth a mild boost.
	BonusDigits = 0.5

	// BonusLuhn is added when the string is a Luhn-valid digit run
	// (KindLuhn, a superset of KindDigits). Luhn is a strong signal for
	// credit-card numbers or IMEIs, so the bonus is larger. +1.5 gives it
	// a higher prior than an unvalidated hex token.
	BonusLuhn = 1.5

	// BonusCommonPassword is added when the string matches the embedded
	// common-password list. A common password appearing in a redacted image
	// is likely the actual secret, so we give it a strong boost. +2.0 is the
	// highest bonus in the prior; it is still additive and therefore never
	// makes the image distance irrelevant.
	BonusCommonPassword = 2.0
)

// Prior returns a non-negative log-space bonus for s, suitable to ADD to other
// priors (higher = more plausible as a secret). It returns 0 for empty strings
// and strings of unknown format. The bonus is the sum of applicable terms:
//
//   - BonusCommonPassword when s is in the embedded common-password list.
//   - BonusLuhn (KindLuhn), BonusStructured (KindUUID/KindHexToken/KindBase64Token),
//     or BonusDigits (KindDigits) — at most one format term applies.
//
// The intent is to break ties toward secret-shaped strings, not to dominate
// the image-distance score that drives the search.
func Prior(s string) float64 {
	if s == "" {
		return 0
	}
	var v float64
	switch Classify(s) {
	case KindLuhn:
		v += BonusLuhn
	case KindUUID, KindHexToken, KindBase64Token:
		v += BonusStructured
	case KindDigits:
		v += BonusDigits
	}
	if IsCommonPassword(s) {
		v += BonusCommonPassword
	}
	return v
}
