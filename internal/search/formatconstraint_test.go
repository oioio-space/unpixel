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
