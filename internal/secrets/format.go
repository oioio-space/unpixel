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
	for i := range len(s) {
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
	for i := range len(s) {
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
			if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	// Move the first four characters to the end, then compute mod 97 treating
	// letters as A=10 .. Z=35, processed digit by digit to avoid big integers.
	rearranged := s[4:] + s[:4]
	rem := 0
	for i := range len(rearranged) {
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
	set := []rune{}
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
	for i := range len(prefix) {
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
