// Package unpixel_test defines classifyOutcome and its unit tests.
//
// classifyOutcome carries no build tag so it is compiled in the default test
// suite (guarding the classifier on every run) and is also visible to the
// journal harness (journal_test.go, build tag: journal).
package unpixel_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// outcome string constants used by classifyOutcome and the journal harness.
// They must match the journalStatus values in journal_test.go.
const (
	outcomeOK      = "ok"
	outcomeFail    = "fail"
	outcomeUnknown = "unknown"
)

// classifyOutcome is a pure function that maps recovery signals to a
// (status, why) pair.
//
// Rules (evaluated in order):
//  1. groundTruth == "" → ("unknown", "no ground truth").
//  2. guess == groundTruth (case-sensitive) → ("ok", "").
//  3. Otherwise → ("fail", <specific why>):
//     - recErr wraps context.DeadlineExceeded AND guess == "" →
//     "timeout (no result in <budget>)".
//     - guess == "" or belowThreshold → "below-threshold / no confident candidate".
//     - rune-length mismatch → "wrong length (got N want M)".
//     - same length, differs → "wrong glyphs (font fidelity / params)".
//
// A correct guess is never labelled timeout: the deadline is only surfaced
// when recovery returned no result at all due to the deadline.
func classifyOutcome(
	guess, groundTruth string,
	belowThreshold bool,
	recErr error,
	_ /* dur */, budget time.Duration,
) (status, why string) {
	if groundTruth == "" {
		return outcomeUnknown, "no ground truth"
	}
	if guess == groundTruth {
		return outcomeOK, ""
	}
	// Failed — determine specific reason.
	if guess == "" && recErr != nil && errors.Is(recErr, context.DeadlineExceeded) {
		return outcomeFail, fmt.Sprintf("timeout (no result in %s)", budget.Round(time.Second))
	}
	if guess == "" || belowThreshold {
		return outcomeFail, "below-threshold / no confident candidate"
	}
	gotLen := utf8.RuneCountInString(guess)
	wantLen := utf8.RuneCountInString(groundTruth)
	if gotLen != wantLen {
		return outcomeFail, fmt.Sprintf("wrong length (got %d want %d)", gotLen, wantLen)
	}
	return outcomeFail, "wrong glyphs (font fidelity / params)"
}

// TestClassifyOutcome guards classifyOutcome with table-driven cases.
// got before want throughout.
func TestClassifyOutcome(t *testing.T) {
	const budget = 30 * time.Second

	cases := []struct {
		name           string
		guess          string
		groundTruth    string
		belowThreshold bool
		recErr         error
		wantStatus     string
		wantWhyPrefix  string // empty means why must be empty
	}{
		{
			name:        "exact match → ok",
			guess:       "hello",
			groundTruth: "hello",
			wantStatus:  "ok",
		},
		{
			name:          "empty gt → unknown",
			guess:         "anything",
			groundTruth:   "",
			wantStatus:    "unknown",
			wantWhyPrefix: "no ground truth",
		},
		{
			name:          "empty guess + deadline → timeout fail",
			guess:         "",
			groundTruth:   "go",
			recErr:        context.DeadlineExceeded,
			wantStatus:    "fail",
			wantWhyPrefix: "timeout (no result in",
		},
		{
			name:          "empty guess + wrapped deadline → timeout fail",
			guess:         "",
			groundTruth:   "go",
			recErr:        fmt.Errorf("recovery: %w", context.DeadlineExceeded),
			wantStatus:    "fail",
			wantWhyPrefix: "timeout (no result in",
		},
		{
			name:        "correct guess beats belowThreshold → ok",
			guess:       "cat",
			groundTruth: "cat",
			// guess == groundTruth is checked before belowThreshold.
			belowThreshold: true,
			wantStatus:     "ok",
		},
		{
			name:           "below-threshold wrong guess → below-threshold fail",
			guess:          "caz",
			groundTruth:    "cat",
			belowThreshold: true,
			wantStatus:     "fail",
			wantWhyPrefix:  "below-threshold",
		},
		{
			name:          "wrong length → wrong length fail",
			guess:         "ab",
			groundTruth:   "1234",
			wantStatus:    "fail",
			wantWhyPrefix: "wrong length (got 2 want 4)",
		},
		{
			name:          "same length different chars → wrong glyphs fail",
			guess:         "hxllo",
			groundTruth:   "hello",
			wantStatus:    "fail",
			wantWhyPrefix: "wrong glyphs",
		},
		{
			name:          "case-sensitive mismatch → fail not ok",
			guess:         "Hello",
			groundTruth:   "hello",
			wantStatus:    "fail",
			wantWhyPrefix: "wrong glyphs",
		},
		{
			name:          "non-deadline error with wrong guess → wrong glyphs (error does not override result)",
			guess:         "hxllo",
			groundTruth:   "hello",
			recErr:        errors.New("some other error"),
			wantStatus:    "fail",
			wantWhyPrefix: "wrong glyphs",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotStatus, gotWhy := classifyOutcome(
				c.guess, c.groundTruth,
				c.belowThreshold, c.recErr,
				500*time.Millisecond, budget,
			)
			if gotStatus != c.wantStatus {
				t.Errorf("classifyOutcome(%q, %q): got status %q, want %q (why=%q)",
					c.guess, c.groundTruth, gotStatus, c.wantStatus, gotWhy)
			}
			switch {
			case c.wantWhyPrefix == "" && gotWhy != "":
				t.Errorf("classifyOutcome(%q, %q): got why %q, want empty",
					c.guess, c.groundTruth, gotWhy)
			case c.wantWhyPrefix != "" && !strings.HasPrefix(gotWhy, c.wantWhyPrefix):
				t.Errorf("classifyOutcome(%q, %q): got why %q, want prefix %q",
					c.guess, c.groundTruth, gotWhy, c.wantWhyPrefix)
			}
		})
	}
}
