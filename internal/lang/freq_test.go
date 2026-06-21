package lang_test

import (
	"math"
	"testing"

	"github.com/oioio-space/unpixel/internal/lang"
)

// ---------------------------------------------------------------------------
// freqWeight / WeightedScoreFR
// ---------------------------------------------------------------------------

// TestFreqWeight_ordering verifies the rank→weight contract:
//
//   - A top-ranked word (rank 1, e.g. "de") produces a weight near 1.0.
//   - A high-rank word ("communication", near the end of the list) produces
//     a lower weight but still > 0.
//   - An OOV word produces weight 0.
//   - An in-dict but unranked word produces the small base weight (0 < w < 1).
func TestFreqWeight_ordering(t *testing.T) {
	t.Parallel()

	topWord := "de"           // rank 1 in freq_fr.txt → weight ≈ 1.0
	highRank := "information" // near end of freq list → lower weight
	oov := "xqzjvwbk"         // not in dict, not in freq list → 0
	dictOnly := "algorithme"  // in words_fr.txt but not in freq_fr.txt → base weight

	wTop := lang.FreqWeight(topWord)
	wHigh := lang.FreqWeight(highRank)
	wOOV := lang.FreqWeight(oov)
	wDict := lang.FreqWeight(dictOnly)

	if wTop <= wHigh {
		t.Errorf("FreqWeight(%q)=%.4f should beat FreqWeight(%q)=%.4f", topWord, wTop, highRank, wHigh)
	}
	if wHigh <= 0 {
		t.Errorf("FreqWeight(%q)=%.4f, want > 0 (ranked word)", highRank, wHigh)
	}
	if wOOV != 0 {
		t.Errorf("FreqWeight(%q)=%.4f, want 0 (OOV)", oov, wOOV)
	}
	if wDict <= 0 || wDict >= wTop {
		t.Errorf("FreqWeight(%q)=%.4f, want in (0, FreqWeight(%q)=%.4f)", dictOnly, wDict, topWord, wTop)
	}
	t.Logf("FreqWeight: top=%q→%.4f  high-rank=%q→%.4f  oov=%q→%.4f  dict-only=%q→%.4f",
		topWord, wTop, highRank, wHigh, oov, wOOV, dictOnly, wDict)
}

// TestFreqWeight_accents verifies that accented words are found by their
// exact accent form, preserving the freq list's encoding.
func TestFreqWeight_accents(t *testing.T) {
	t.Parallel()
	// "connaît", "condamné", "liberté" appear in freq_fr.txt.
	for _, w := range []string{"connaît", "condamné", "liberté"} {
		if wt := lang.FreqWeight(w); wt <= 0 {
			t.Errorf("FreqWeight(%q)=%.4f, want > 0 (accented ranked word)", w, wt)
		}
	}
}

// TestWeightedScoreFR_empty checks that WeightedScoreFR on an empty string is 0
// and does not panic.
func TestWeightedScoreFR_empty(t *testing.T) {
	t.Parallel()
	if s := lang.WeightedScoreFR(""); s != 0 {
		t.Errorf("WeightedScoreFR(\"\") = %v, want 0", s)
	}
}

// TestWeightedScoreFR_mean checks that WeightedScoreFR is the mean of
// per-token FreqWeights.
//
// "de la" → both rank-1-ish words → high mean.
// "de xqzjv" → one ranked word + one OOV (weight=0) → half the mean.
func TestWeightedScoreFR_mean(t *testing.T) {
	t.Parallel()

	both := lang.WeightedScoreFR("de la")    // two ranked words
	half := lang.WeightedScoreFR("de xqzjv") // one ranked + one OOV

	if both <= half {
		t.Errorf("WeightedScoreFR(%q)=%.4f should beat WeightedScoreFR(%q)=%.4f",
			"de la", both, "de xqzjv", half)
	}
}

// ---------------------------------------------------------------------------
// Headline improvement test: frequency weighting disambiguates equal-uniform-
// weight pairs.
//
// Word pair chosen: "est" (copula "is", extremely common) vs "tes" (possessive
// "your [pl.]", much rarer). Both are 3-rune words present in words_fr.txt, so
// Dict.Score gives them EQUAL uniform weight (BonusWord=1.0 for each). The new
// WeightedScoreFR — and therefore PriorFor(French) — must rank "est" above "tes".
//
// The infini char model is the same for both sentences (same characters,
// reordered at word level in a one-word context), so only the frequency overlay
// produces any separation. This test pins that the overlay DOES separate them.
//
// Expected (verified with hardcoded constants in the comment):
//
//	uniform Dict.Score("il est là") == Dict.Score("il tes là") == 1.0  (all 3 tokens in dict)
//	PriorFor(French)("il est là") > PriorFor(French)("il tes là")
// ---------------------------------------------------------------------------

// TestFreqWeighting_disambiguatesEqualUniform is the headline improvement test.
func TestFreqWeighting_disambiguatesEqualUniform(t *testing.T) {
	t.Parallel()
	d := lang.FrenchDictionary()

	// Both sentences have 3 tokens all in the French dict → uniform Score == 1.0.
	sentCommon := "il est là"
	sentRare := "il tes là"

	uniformCommon := d.Score(sentCommon)
	uniformRare := d.Score(sentRare)
	if math.Abs(uniformCommon-uniformRare) > 1e-9 {
		t.Errorf("precondition failed: uniform scores differ: Score(%q)=%.6f  Score(%q)=%.6f "+
			"(should be equal; adjust word pair)",
			sentCommon, uniformCommon, sentRare, uniformRare)
	}
	t.Logf("uniform Score: %q=%.4f  %q=%.4f  (equal as expected)",
		sentCommon, uniformCommon, sentRare, uniformRare)

	// Weighted scores MUST differ: "est" has a much higher freq rank than "tes".
	wCommon := lang.WeightedScoreFR(sentCommon)
	wRare := lang.WeightedScoreFR(sentRare)
	if wCommon <= wRare {
		t.Errorf("WeightedScoreFR(%q)=%.6f should beat WeightedScoreFR(%q)=%.6f "+
			"(freq overlay should separate common 'est' from rare 'tes')",
			sentCommon, wCommon, sentRare, wRare)
	}
	t.Logf("weighted Score: %q=%.4f  %q=%.4f  (separated as required)",
		sentCommon, wCommon, sentRare, wRare)

	// The fused PriorFor(French) must also separate them.
	prior := lang.PriorFor(lang.French)
	priorCommon := prior(sentCommon)
	priorRare := prior(sentRare)
	if priorCommon <= priorRare {
		t.Errorf("PriorFor(French)(%q)=%.6f should beat PriorFor(French)(%q)=%.6f",
			sentCommon, priorCommon, sentRare, priorRare)
	}
	t.Logf("PriorFor(French): %q=%.4f  %q=%.4f",
		sentCommon, priorCommon, sentRare, priorRare)
}

// TestPriorFor_English_unchanged guards that the English prior is unaffected
// by the French frequency overlay — it must still rank a real English sentence
// above a shuffled version.
func TestPriorFor_English_unchanged(t *testing.T) {
	t.Parallel()
	prior := lang.PriorFor(lang.English)
	sentence := "the quick brown fox jumps over the lazy dog"
	shuffled := "dog lazy the over jumps fox brown quick the"
	sScore := prior(sentence)
	shScore := prior(shuffled)
	if sScore <= shScore {
		t.Errorf("PriorFor(English): real sentence (%.4f) should beat shuffled (%.4f)",
			sScore, shScore)
	}
	t.Logf("PriorFor(English): sentence=%.4f shuffled=%.4f", sScore, shScore)
}
