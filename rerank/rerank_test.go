package rerank_test

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/rerank"
)

// vd is a tiny constructor for test input.
func vd(text string, dist float64) unpixel.Verdict {
	return unpixel.Verdict{Text: text, Distance: dist, Match: dist < unpixel.VerifyMatchThreshold}
}

func TestLinguistic_weightZeroIsPhysicalOrder(t *testing.T) {
	// Input in arbitrary order; weight 0 must sort by ascending distance.
	in := []unpixel.Verdict{vd("b", 0.30), vd("a", 0.10), vd("c", 0.20)}
	got, err := rerank.Linguistic{}.Rerank(t.Context(), nil, in, func(string) float64 { return 0 }, 0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	want := []string{"a", "c", "b"}
	for i, w := range want {
		if got[i].Text != w {
			t.Errorf("pos %d = %q; want %q (physical order)", i, got[i].Text, w)
		}
	}
}

func TestLinguistic_lmRescuesPlausibleCandidate(t *testing.T) {
	// Physics marginally prefers the implausible "rn"; the LM strongly prefers "m".
	in := []unpixel.Verdict{vd("rn", 0.10), vd("m", 0.14)}
	lm := func(s string) float64 {
		if s == "m" {
			return 0.0 // plausible
		}
		return -5.0 // implausible
	}

	// weight 0: physics wins → "rn" first.
	phys, _ := rerank.Linguistic{}.Rerank(t.Context(), nil, in, lm, 0)
	if phys[0].Text != "rn" {
		t.Errorf("weight 0 top = %q; want rn (physics)", phys[0].Text)
	}

	// weight 0.02: blended("m") = 0.14 − 0.02·(0−0) = 0.14;
	// blended("rn") = 0.10 − 0.02·(−5−0) = 0.10 + 0.10 = 0.20 → "m" wins.
	fused, _ := rerank.Linguistic{}.Rerank(t.Context(), nil, in, lm, 0.02)
	if fused[0].Text != "m" {
		t.Errorf("weight 0.02 top = %q; want m (LM rescue)", fused[0].Text)
	}
}

func TestLinguistic_nilLMIsPhysicalOrder(t *testing.T) {
	in := []unpixel.Verdict{vd("b", 0.30), vd("a", 0.10)}
	got, _ := rerank.Linguistic{}.Rerank(t.Context(), nil, in, nil, 0.5)
	if got[0].Text != "a" {
		t.Errorf("nil LM top = %q; want a (physical)", got[0].Text)
	}
}

func TestLinguistic_empty(t *testing.T) {
	got, err := rerank.Linguistic{}.Rerank(t.Context(), nil, nil, nil, 0.1)
	if err != nil {
		t.Fatalf("Rerank(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Rerank(empty) len = %d; want 0", len(got))
	}
}

func TestLinguistic_singleCandidate(t *testing.T) {
	// Linguistic is the pure-Go reranker; verify it works regardless of build tag.
	got, err := rerank.Linguistic{}.Rerank(t.Context(), nil,
		[]unpixel.Verdict{vd("a", 0.1)}, func(string) float64 { return 0 }, 0)
	if err != nil {
		t.Fatalf("Linguistic{}.Rerank: %v", err)
	}
	if len(got) != 1 || got[0].Text != "a" {
		t.Errorf("Linguistic{}.Rerank = %+v; want single 'a'", got)
	}
}

func TestLinguistic_allNegativeLMScores(t *testing.T) {
	// When ALL LM scores are negative the i==0 seeding must initialise bestLM
	// from the first score (not 0.0), otherwise bestLM stays at its zero value
	// and the blended values are wrong.
	//
	// lm("m")=-1.0, lm("rn")=-5.0; bestLM must be -1.0.
	// blended("m")  = 0.14 − 0.02·(−1−(−1)) = 0.14
	// blended("rn") = 0.10 − 0.02·(−5−(−1)) = 0.10 + 0.08 = 0.18
	// → "m" wins despite worse physical distance.
	lm := func(s string) float64 {
		if s == "m" {
			return -1.0
		}
		return -5.0
	}
	in := []unpixel.Verdict{vd("rn", 0.10), vd("m", 0.14)}
	got, err := rerank.Linguistic{}.Rerank(t.Context(), nil, in, lm, 0.02)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if got[0].Text != "m" {
		t.Errorf("all-negative LM: top = %q; want %q (LM rescue over physics)", got[0].Text, "m")
	}
}

// Compile-time check that Linguistic satisfies Reranker.
var _ rerank.Reranker = rerank.Linguistic{}

// Compile-time check that image.Image is accepted (nil ok for Linguistic).
var _ image.Image = nil
