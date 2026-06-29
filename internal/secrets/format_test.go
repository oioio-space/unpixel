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
	if !TerminalLen(FormatCreditCard, 16) {
		t.Errorf("16 is a valid card length")
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
