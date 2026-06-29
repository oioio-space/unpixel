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
		{"none eligible (all above threshold)", []Candidate{{op(.9), "go", 0.5}, {op(.8), "ho", 0.6}}, false, ""},
		{"single eligible wins", []Candidate{{op(.9), "go", 0.05}, {op(.8), "ho", 0.6}}, true, "go"},
		{"agreement wins", []Candidate{{op(.6), "go", 0.05}, {op(.55), "go", 0.07}}, true, "go"},
		{"disagree, decisive coherence lead", []Candidate{{op(.9), "go", 0.05}, {op(.5), "ho", 0.07}}, true, "go"},
		{"disagree, indecisive lead → abstain", []Candidate{{op(.6), "go", 0.05}, {op(.55), "ho", 0.07}}, false, ""},
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
