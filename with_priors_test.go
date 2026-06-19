package unpixel_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
)

// doubled returns a prior that returns 2×the input string's length, used as a
// synthetic non-trivial prior in composition tests.
func doubled(s string) float64 { return float64(2 * len(s)) }

// tripled returns a prior that returns 3×the input string's length.
func tripled(s string) float64 { return float64(3 * len(s)) }

// TestWithPriors_SinglePrior verifies that a single non-nil prior is set directly.
func TestWithPriors_SinglePrior(t *testing.T) {
	t.Parallel()
	var cfg unpixel.Config
	unpixel.WithPriors(doubled)(&cfg)
	if cfg.LanguageModel == nil {
		t.Fatal("WithPriors(single): LanguageModel is nil, want non-nil")
	}
	if got, want := cfg.LanguageModel("hello"), doubled("hello"); got != want {
		t.Errorf("LanguageModel(%q) = %v, want %v", "hello", got, want)
	}
}

// TestWithPriors_SumOfTwo verifies that two priors are summed.
func TestWithPriors_SumOfTwo(t *testing.T) {
	t.Parallel()
	var cfg unpixel.Config
	unpixel.WithPriors(doubled, tripled)(&cfg)
	if cfg.LanguageModel == nil {
		t.Fatal("WithPriors(two): LanguageModel is nil, want non-nil")
	}
	s := "hi"
	got := cfg.LanguageModel(s)
	want := doubled(s) + tripled(s)
	if got != want {
		t.Errorf("LanguageModel(%q) = %v, want %v (sum of two priors)", s, got, want)
	}
}

// TestWithPriors_ComposesWithExistingLanguageModel verifies that WithPriors adds
// on top of a LanguageModel already present on the Config, regardless of order.
func TestWithPriors_ComposesWithExistingLanguageModel(t *testing.T) {
	t.Parallel()
	var cfg unpixel.Config
	// Set an existing language model first.
	unpixel.WithLanguageModel(doubled)(&cfg)
	// Then layer a second prior on top via WithPriors.
	unpixel.WithPriors(tripled)(&cfg)
	s := "abc"
	got := cfg.LanguageModel(s)
	want := doubled(s) + tripled(s)
	if got != want {
		t.Errorf("after WithLanguageModel then WithPriors: LanguageModel(%q) = %v, want %v", s, got, want)
	}
}

// TestWithPriors_ComposesReverseOrder verifies composition when WithPriors comes
// before WithLanguageModel (the "regardless of option order" guarantee).
func TestWithPriors_ComposesReverseOrder(t *testing.T) {
	t.Parallel()
	var cfg unpixel.Config
	// Apply WithPriors first, then WithLanguageModel — should still sum.
	unpixel.WithPriors(tripled)(&cfg)
	// WithLanguageModel replaces; then wrap again.
	unpixel.WithPriors(doubled)(&cfg)
	s := "abc"
	got := cfg.LanguageModel(s)
	want := tripled(s) + doubled(s)
	if got != want {
		t.Errorf("double WithPriors: LanguageModel(%q) = %v, want %v", s, got, want)
	}
}

// TestWithPriors_NilPriorsSkipped verifies that nil entries are ignored and do
// not cause a panic.
func TestWithPriors_NilPriorsSkipped(t *testing.T) {
	t.Parallel()
	var cfg unpixel.Config
	unpixel.WithPriors(nil, doubled, nil)(&cfg)
	if cfg.LanguageModel == nil {
		t.Fatal("WithPriors(nil, doubled, nil): LanguageModel is nil, want non-nil")
	}
	if got, want := cfg.LanguageModel("x"), doubled("x"); got != want {
		t.Errorf("LanguageModel(%q) = %v, want %v", "x", got, want)
	}
}

// TestWithPriors_AllNilNoOp verifies that all-nil priors leave LanguageModel
// unchanged.
func TestWithPriors_AllNilNoOp(t *testing.T) {
	t.Parallel()
	var cfg unpixel.Config
	unpixel.WithPriors(nil, nil)(&cfg)
	if cfg.LanguageModel != nil {
		t.Error("WithPriors(nil, nil): LanguageModel should remain nil")
	}
}

// TestWithPriors_EmptyNoOp verifies that WithPriors() with no arguments is a no-op.
func TestWithPriors_EmptyNoOp(t *testing.T) {
	t.Parallel()
	var cfg unpixel.Config
	unpixel.WithPriors()(&cfg)
	if cfg.LanguageModel != nil {
		t.Error("WithPriors(): LanguageModel should remain nil")
	}
}
