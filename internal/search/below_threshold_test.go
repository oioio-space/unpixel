package search

import (
	"context"
	"testing"

	"github.com/oioio-space/unpixel"
)

// fixedScorer returns a scripted EvalResult for specific guesses; unknown
// guesses score 1.0. It is defined here (rather than reusing the external
// search_test.mockScorer) because this file lives in package search (internal).
type fixedScorer struct{ scores map[string]EvalResult }

func (s *fixedScorer) Eval(_ context.Context, guess, _ string, _ unpixel.Offset) EvalResult {
	if r, ok := s.scores[guess]; ok {
		return r
	}
	return EvalResult{Score: 1.0}
}

// TestBelowThreshold_aboveThresholdUnchanged verifies that when candidates pass
// the gate, BelowThreshold is false and BestGuess/TopN/Confidence are
// byte-identical to previous behaviour.
func TestBelowThreshold_aboveThresholdUnchanged(t *testing.T) {
	scorer := &fixedScorer{scores: map[string]EvalResult{
		"a":  {Score: 0.1},
		"ab": {Score: 0.05},
	}}
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      3,
		BlockSize:      2,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		Workers:        1,
	}

	out := make(chan unpixel.Progress, 256)
	results := make(chan unpixel.Result, 16)
	var got []unpixel.Result
	resDone := make(chan struct{})
	go func() {
		for r := range results {
			got = append(got, r)
		}
		close(resDone)
	}()
	go func() {
		for range out { //nolint:revive // drain
		}
	}()

	searchOffsets(t.Context(), scorer, cfg, out, results, GuidedDFS)
	close(out)
	close(results)
	<-resDone

	if len(got) == 0 {
		t.Fatal("no results produced")
	}
	for _, r := range got {
		if r.BelowThreshold {
			t.Errorf("offset %v: BelowThreshold=true but candidates passed the gate", r.Offset)
		}
		if r.BestGuess == "" {
			t.Errorf("offset %v: BestGuess empty despite candidates passing the gate", r.Offset)
		}
	}
}

// TestBelowThreshold_noCandidatePassedGate verifies that when no candidate
// passes the acceptance threshold, BestGuess is still populated from the
// best-seen candidate, BelowThreshold=true, and Confidence is low.
func TestBelowThreshold_noCandidatePassedGate(t *testing.T) {
	// All single-char scores are above threshold (0.25).
	// "a" scores lower than "b", so "a" should be the promoted best-seen.
	scorer := &fixedScorer{scores: map[string]EvalResult{
		"a": {Score: 0.8},
		"b": {Score: 0.9},
	}}
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      3,
		BlockSize:      2,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		Workers:        1,
	}

	out := make(chan unpixel.Progress, 256)
	results := make(chan unpixel.Result, 16)
	var got []unpixel.Result
	resDone := make(chan struct{})
	go func() {
		for r := range results {
			got = append(got, r)
		}
		close(resDone)
	}()
	go func() {
		for range out { //nolint:revive // drain
		}
	}()

	searchOffsets(t.Context(), scorer, cfg, out, results, GuidedDFS)
	close(out)
	close(results)
	<-resDone

	if len(got) == 0 {
		t.Fatal("no results produced")
	}
	for _, r := range got {
		if !r.BelowThreshold {
			t.Errorf("offset %v: BelowThreshold=false but no candidate passed the gate", r.Offset)
		}
		if r.BestGuess == "" {
			t.Errorf("offset %v: BestGuess empty; expected best-seen candidate to be promoted", r.Offset)
		}
		if len(r.TopN) == 0 {
			t.Errorf("offset %v: TopN empty; expected at least one promoted entry", r.Offset)
		}
		// Confidence = 1 - TopN[0].Score; score is ~0.8, so confidence must be < 0.5.
		if r.Confidence >= 0.5 {
			t.Errorf("offset %v: Confidence=%.4f too high for a below-threshold guess", r.Offset, r.Confidence)
		}
	}
}

// TestBelowThreshold_noOffsetsSurviveDiscovery verifies that when
// DiscoverOffsets finds no surviving offsets, the terminal result still carries
// a non-empty BestGuess from the best-seen discovery candidate, with
// BelowThreshold=true.
func TestBelowThreshold_noOffsetsSurviveDiscovery(t *testing.T) {
	// All scores above threshold so no offset survives DiscoverOffsets.
	scorer := &fixedScorer{scores: map[string]EvalResult{
		"a": {Score: 0.8},
	}}
	cfg := unpixel.Config{
		Charset:        "a",
		MaxLength:      3,
		BlockSize:      2,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		TopN:           5,
		Workers:        1,
	}

	out := make(chan unpixel.Progress, 256)
	results := make(chan unpixel.Result, 16)
	var got []unpixel.Result
	resDone := make(chan struct{})
	go func() {
		for r := range results {
			got = append(got, r)
		}
		close(resDone)
	}()
	go func() {
		for range out { //nolint:revive // drain
		}
	}()

	searchOffsets(t.Context(), scorer, cfg, out, results, GuidedDFS)
	close(out)
	close(results)
	<-resDone

	if len(got) == 0 {
		t.Fatal("no result emitted; expected one with BelowThreshold promotion")
	}
	r := got[0]
	if !r.BelowThreshold {
		t.Errorf("BelowThreshold=false; expected true when no offsets survive discovery")
	}
	if r.BestGuess == "" {
		t.Errorf("BestGuess empty; expected best-seen candidate from discovery to be promoted")
	}
}
