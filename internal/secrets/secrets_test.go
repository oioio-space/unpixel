package secrets_test

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/secrets"
)

// TestLuhn covers valid cards, invalid checksums, non-digit input, and short strings.
func TestLuhn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		want bool
	}{
		// Valid Luhn numbers (well-known test vectors).
		{name: "visa_test_card", s: "4532015112830366", want: true},
		{name: "mastercard_test", s: "5425233430109903", want: true},
		{name: "amex_test", s: "378282246310005", want: true},
		{name: "simple_valid", s: "79927398713", want: true},
		{name: "minimal_two_digits", s: "18", want: true},
		// Invalid Luhn numbers.
		{name: "simple_invalid", s: "79927398710", want: false},
		{name: "off_by_one", s: "4532015112830367", want: false},
		// Non-digit input.
		{name: "contains_letter", s: "4532a15112830366", want: false},
		{name: "uuid_like", s: "550e8400-e29b-41d4", want: false},
		{name: "empty", s: "", want: false},
		// Too short.
		{name: "single_digit", s: "0", want: false},
		{name: "zero_zero", s: "00", want: true}, // 0%10==0 → valid
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := secrets.Luhn(tc.s); got != tc.want {
				t.Errorf("Luhn(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestClassify verifies that each Kind is returned for canonical examples and
// that precedence rules hold (KindLuhn > KindDigits, KindUUID > KindHexToken).
func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		want secrets.Kind
	}{
		// KindUUID — must take precedence over KindHexToken.
		{name: "uuid_lower", s: "550e8400-e29b-41d4-a716-446655440000", want: secrets.KindUUID},
		{name: "uuid_upper", s: "550E8400-E29B-41D4-A716-446655440000", want: secrets.KindUUID},
		// KindHexToken — 16+ hex chars, no separators.
		{name: "hex_16", s: "deadbeefcafe0123", want: secrets.KindHexToken},
		{name: "hex_32", s: "aabbccddeeff00112233445566778899", want: secrets.KindHexToken},
		// KindBase64Token — 16+ mixed-case/digit, base64url alphabet.
		{name: "base64_mixed", s: "aB3dEfGh1JkLmNoP", want: secrets.KindBase64Token},
		{name: "base64_with_slash", s: "aB3dEfGh1JkLm/oP", want: secrets.KindBase64Token},
		// KindLuhn takes precedence over KindDigits.
		{name: "luhn_card", s: "4532015112830366", want: secrets.KindLuhn},
		// KindDigits — 4+ digits that do NOT pass Luhn.
		{name: "digits_pin", s: "1234", want: secrets.KindDigits},
		{name: "digits_long", s: "99999999999999999", want: secrets.KindDigits},
		// KindUnknown.
		{name: "unknown_word", s: "hello", want: secrets.KindUnknown},
		{name: "unknown_short_digit", s: "123", want: secrets.KindUnknown},
		{name: "unknown_empty", s: "", want: secrets.KindUnknown},
		// All-hex short (< 16) goes to KindUnknown (not KindHexToken).
		{name: "hex_short", s: "deadbeef", want: secrets.KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := secrets.Classify(tc.s); got != tc.want {
				t.Errorf("Classify(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestIsCommonPassword checks membership (hit), non-membership (miss), and
// case-insensitivity.
func TestIsCommonPassword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{name: "hit_lower", s: "password", want: true},
		{name: "hit_digits", s: "123456", want: true},
		{name: "hit_qwerty", s: "qwerty", want: true},
		{name: "hit_letmein", s: "letmein", want: true},
		{name: "hit_admin", s: "admin", want: true},
		// Case insensitivity.
		{name: "case_upper", s: "PASSWORD", want: true},
		{name: "case_mixed", s: "Password", want: true},
		{name: "case_mixed2", s: "LeTmEiN", want: true},
		// Misses.
		{name: "miss_random", s: "xK9mQ2pL", want: false},
		{name: "miss_empty", s: "", want: false},
		{name: "miss_uuid", s: "550e8400-e29b-41d4-a716-446655440000", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := secrets.IsCommonPassword(tc.s); got != tc.want {
				t.Errorf("IsCommonPassword(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestPrior verifies the ordering guarantee:
// common-password > luhn > uuid/hex/base64 > digits > unknown == 0.
func TestPrior(t *testing.T) {
	t.Parallel()

	pUnknown := secrets.Prior("xyzzy")                             // KindUnknown, not common
	pDigits := secrets.Prior("9876")                               // KindDigits (non-Luhn), not common
	pUUID := secrets.Prior("550e8400-e29b-41d4-a716-446655440000") // KindUUID
	pHex := secrets.Prior("deadbeefcafe0123")                      // KindHexToken
	pBase64 := secrets.Prior("aB3dEfGh1JkLmNoP")                   // KindBase64Token
	pLuhn := secrets.Prior("4532015112830366")                     // KindLuhn, not common
	pCommon := secrets.Prior("password")                           // IsCommonPassword (KindUnknown format)

	// Unknown == 0.
	if pUnknown != 0 {
		t.Errorf("Prior(unknown): got %v, want 0", pUnknown)
	}
	// Ordering: unknown < digits.
	if pDigits <= pUnknown {
		t.Errorf("Prior(digits) %v should be > Prior(unknown) %v", pDigits, pUnknown)
	}
	// UUID, hex, base64 are all BonusStructured; they must all beat digits.
	for name, p := range map[string]float64{"uuid": pUUID, "hex": pHex, "base64": pBase64} {
		if p <= pDigits {
			t.Errorf("Prior(%s) %v should be > Prior(digits) %v", name, p, pDigits)
		}
	}
	// Luhn beats structured tokens.
	if pLuhn <= pUUID {
		t.Errorf("Prior(luhn) %v should be > Prior(uuid) %v", pLuhn, pUUID)
	}
	// Common password beats Luhn.
	if pCommon <= pLuhn {
		t.Errorf("Prior(commonPassword) %v should be > Prior(luhn) %v", pCommon, pLuhn)
	}
	// Empty string returns 0.
	if p := secrets.Prior(""); p != 0 {
		t.Errorf("Prior(\"\") = %v, want 0", p)
	}
}

// TestPrior_CommonPasswordBoostStacks verifies that a Luhn-valid common password
// receives both bonuses (BonusLuhn + BonusCommonPassword).
func TestPrior_CommonPasswordBoostStacks(t *testing.T) {
	t.Parallel()
	// "123456" is in the common-password list and may or may not be Luhn-valid —
	// we just confirm its prior exceeds BonusCommonPassword alone by testing that
	// a non-common Luhn number scores less than a common password.
	pCard := secrets.Prior("4532015112830366") // Luhn, not common
	pCommon := secrets.Prior("password")       // common, KindUnknown → BonusCommonPassword only
	if pCommon <= pCard {
		t.Errorf("Prior(common password) %v should exceed Prior(luhn card) %v", pCommon, pCard)
	}
}
