package forensics

import "testing"

func TestSelect_securedRule(t *testing.T) {
	const dt, cm = 0.2, 0.25
	op := func(conf float64) Operator { return Operator{Conf: Conf{Kind: conf}} }
	tests := []struct {
		name     string
		cands    []Candidate
		wantOK   bool
		wantText string
	}{
		{
			name:     "none eligible (all above threshold)",
			cands:    []Candidate{{Op: op(.9), Text: "go", Dist: 0.5}, {Op: op(.8), Text: "ho", Dist: 0.6}},
			wantOK:   false,
			wantText: "",
		},
		{
			name:     "single eligible wins",
			cands:    []Candidate{{Op: op(.9), Text: "go", Dist: 0.05}, {Op: op(.8), Text: "ho", Dist: 0.6}},
			wantOK:   true,
			wantText: "go",
		},
		{
			name:     "agreement wins",
			cands:    []Candidate{{Op: op(.6), Text: "go", Dist: 0.05}, {Op: op(.55), Text: "go", Dist: 0.07}},
			wantOK:   true,
			wantText: "go",
		},
		{
			name:     "disagree, decisive coherence lead",
			cands:    []Candidate{{Op: op(.9), Text: "go", Dist: 0.05}, {Op: op(.5), Text: "ho", Dist: 0.07}},
			wantOK:   true,
			wantText: "go",
		},
		{
			name:     "disagree, indecisive lead → abstain",
			cands:    []Candidate{{Op: op(.6), Text: "go", Dist: 0.05}, {Op: op(.55), Text: "ho", Dist: 0.07}},
			wantOK:   false,
			wantText: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, ok := Select(tc.cands, dt, cm)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && sel.Text != tc.wantText {
				t.Errorf("Text = %q, want %q", sel.Text, tc.wantText)
			}
		})
	}
}

// TestSelect_coherenceTiebreakUsesGamma verifies that the step-5 tiebreak
// uses Conf.Kind + Conf.Gamma (combined coherence), not Conf.Kind alone.
// The primary case is sRGB-vs-linear mosaic ambiguity: both candidates share
// the same Kind, so Conf.Kind is identical — only Conf.Gamma differs.
func TestSelect_coherenceTiebreakUsesGamma(t *testing.T) {
	const dt = 0.2  // distThreshold
	const cm = 0.25 // coherenceMargin

	// opKG builds a mosaic Operator with the given Kind and Gamma confidences.
	opKG := func(kind, gamma float64) Operator {
		return Operator{
			Kind:  KindMosaic,
			Gamma: GammaSRGB,
			Conf:  Conf{Kind: kind, Gamma: gamma},
		}
	}

	tests := []struct {
		name     string
		cands    []Candidate
		wantOK   bool
		wantText string
	}{
		{
			// Same Conf.Kind, Conf.Gamma breaks the tie decisively.
			// coherence(winner)=0.66+0.69=1.35, coherence(loser)=0.66+0.07=0.73
			// gap=0.62 > cm=0.25 → pick the high-gamma candidate.
			name: "same Kind, gamma decisive",
			cands: []Candidate{
				{Op: opKG(0.66, 0.69), Text: "hello", Dist: 0.05},
				{Op: opKG(0.66, 0.07), Text: "world", Dist: 0.08},
			},
			wantOK:   true,
			wantText: "hello",
		},
		{
			// Same Conf.Kind, near-tie on Conf.Gamma: gap=0.03 < cm=0.25 → abstain.
			// coherence(a)=0.66+0.69=1.35, coherence(b)=0.66+0.66=1.32 → gap=0.03
			name: "same Kind, gamma near-tie → abstain",
			cands: []Candidate{
				{Op: opKG(0.66, 0.69), Text: "hello", Dist: 0.05},
				{Op: opKG(0.66, 0.66), Text: "world", Dist: 0.08},
			},
			wantOK:   false,
			wantText: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Select(tc.cands, dt, cm)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Text != tc.wantText {
				t.Errorf("Text = %q, want %q", got.Text, tc.wantText)
			}
		})
	}
}
