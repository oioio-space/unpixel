package forensics

import (
	"cmp"
	"slices"
)

// Candidate is one decoded hypothesis from the engine: an operator and the
// text it recovered with its image distance (lower = better fit).
type Candidate struct {
	Op   Operator
	Text string
	Dist float64
}

// Selection is the meta-strategy verdict: the winning operator and the text
// it recovered.
type Selection struct {
	Op   Operator
	Text string
}

// Select applies the secured selection rule over cands:
//  1. eligible = candidates with Dist < distThreshold.
//  2. None eligible → abstain (ok=false).
//  3. Exactly one eligible → it wins.
//  4. ≥2 eligible and all share the same Text → that text wins; Op is taken
//     from the eligible candidate with the lowest Dist.
//  5. Eligible disagree on Text → winner is the highest-Conf.Kind eligible
//     IFF its lead over the runner-up exceeds coherenceMargin; else abstain.
//
// ok=false means the caller must fall back (no confident answer).
func Select(cands []Candidate, distThreshold, coherenceMargin float64) (Selection, bool) {
	// Step 1: filter eligible candidates.
	eligible := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if c.Dist < distThreshold {
			eligible = append(eligible, c)
		}
	}

	// Step 2: none eligible → abstain.
	if len(eligible) == 0 {
		return Selection{}, false
	}

	// Step 3: exactly one eligible → it wins.
	if len(eligible) == 1 {
		return Selection{Op: eligible[0].Op, Text: eligible[0].Text}, true
	}

	// Step 4: ≥2 eligible — check for text agreement.
	allAgree := true
	for _, c := range eligible[1:] {
		if c.Text != eligible[0].Text {
			allAgree = false
			break
		}
	}
	if allAgree {
		// Pick the lowest-Dist candidate for Op.
		best := slices.MinFunc(eligible, func(a, b Candidate) int {
			return cmp.Compare(a.Dist, b.Dist)
		})
		return Selection{Op: best.Op, Text: best.Text}, true
	}

	// Step 5: disagreement — coherence-margin tiebreak.
	// Sort by Conf.Kind descending.
	slices.SortFunc(eligible, func(a, b Candidate) int {
		return cmp.Compare(b.Op.Conf.Kind, a.Op.Conf.Kind)
	})
	if eligible[0].Op.Conf.Kind-eligible[1].Op.Conf.Kind > coherenceMargin {
		return Selection{Op: eligible[0].Op, Text: eligible[0].Text}, true
	}
	return Selection{}, false
}
