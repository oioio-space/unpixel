// freq.go — frequency-weighted dictionary scoring for package lang.
//
// # Rank→weight model
//
// Both freq_en.txt and freq_fr.txt list the most-frequent words one per line,
// most-frequent first (rank 1 = line 1). Weights are Zipfian:
//
//	w(r) = (log(F+1) − log(r)) / log(F+1)
//
// where F is the total number of entries in the list and r is the 1-based rank.
// This maps rank 1 → 1.0 and rank F → log(F+1−log(F)) / log(F+1) ≈ 0 (a small
// positive floor). The division by log(F+1) normalises the range to [0, 1].
//
// Words that appear in the main dictionary (words.txt / words_fr.txt) but are
// absent from the frequency list receive a base weight of baseFreqWeight = 0.15.
// This rewards in-vocabulary words without inflating their score to that of a
// common function word. OOV words (not in either list) receive weight 0.
//
// # Rationale for baseFreqWeight = 0.15
//
// A word at the very bottom of the 10000-entry list has weight ≈ 0.09. Setting
// the base to 0.15 places dict-only words slightly above the rarest ranked words —
// acknowledging that a term appearing in a curated dictionary is at least mildly
// plausible even when we have no frequency evidence.

package lang

import (
	_ "embed"
	"math"
	"strings"
	"sync"
)

//go:embed freq_en.txt
var freqENText string

//go:embed freq_fr.txt
var freqFRText string

// baseFreqWeight is the weight given to a word that is in the dictionary but
// absent from the frequency list. See the file-level doc for rationale.
const baseFreqWeight = 0.15

// freqList holds a prebuilt rank→weight index for one language's frequency list.
// All fields are immutable after init and safe for concurrent reads.
type freqList struct {
	ranks  map[string]int // word → 1-based rank
	f      int            // total entries (list length F)
	logFp1 float64        // precomputed log(F+1)
}

// buildFreqList parses a newline-separated frequency list (most-frequent first)
// into a freqList. Duplicate words keep their first (highest-rank) occurrence.
func buildFreqList(text string) freqList {
	ranks := make(map[string]int)
	r := 0
	for line := range strings.Lines(text) {
		w := strings.TrimSpace(line)
		if w == "" {
			continue
		}
		r++
		if _, dup := ranks[w]; !dup {
			ranks[w] = r
		}
	}
	return freqList{
		ranks:  ranks,
		f:      r,
		logFp1: math.Log(float64(r) + 1),
	}
}

// weight returns the Zipfian normalised weight for word in the range [0, 1]:
//
//   - Ranked at position r: (log(F+1)−log(r)) / log(F+1)
//   - Not found: 0 (caller applies baseFreqWeight or OOV logic)
func (fl *freqList) weight(word string) (float64, bool) {
	r, ok := fl.ranks[word]
	if !ok {
		return 0, false
	}
	return (fl.logFp1 - math.Log(float64(r))) / fl.logFp1, true
}

// ---------------------------------------------------------------------------
// French frequency index
// ---------------------------------------------------------------------------

var (
	freqFROnce sync.Once
	freqFR     freqList
)

func initFreqFR() {
	freqFROnce.Do(func() { freqFR = buildFreqList(freqFRText) })
}

// FreqWeight returns the Zipfian frequency weight for word in the French
// frequency list (exact match, accents preserved, no case folding).
// The return value is in [0, 1]:
//
//   - Ranked word at position r: (log(F+1)−log(r)) / log(F+1)
//   - In-dict but unranked: baseFreqWeight (0.15)
//   - OOV (not in either list): 0
//
// FreqWeight is safe for concurrent use after the first call.
func FreqWeight(word string) float64 {
	initFreqFR()
	if w, ok := freqFR.weight(word); ok {
		return w
	}
	if FrenchDictionary().Contains(word) {
		return baseFreqWeight
	}
	return 0
}

// WeightedScoreFR scores s using the Zipfian frequency weight for each
// whitespace token. It returns the mean FreqWeight over all tokens (0 for
// empty input). It is the frequency-aware counterpart to [Dict.Score]: instead
// of a flat BonusWord=1.0 per known token, each token contributes its
// FreqWeight so common words outrank rare equal-length words.
func WeightedScoreFR(s string) float64 {
	return weightedScore(s, FreqWeight)
}

// ---------------------------------------------------------------------------
// English frequency index
// ---------------------------------------------------------------------------

var (
	freqENOnce sync.Once
	freqEN     freqList
)

func initFreqEN() {
	freqENOnce.Do(func() { freqEN = buildFreqList(freqENText) })
}

// FreqWeightEN returns the Zipfian frequency weight for word in the English
// frequency list (exact match, lowercase, no case folding). The return value
// is in [0, 1]:
//
//   - Ranked word at position r: (log(F+1)−log(r)) / log(F+1)
//   - In-dict but unranked: baseFreqWeight (0.15)
//   - OOV (not in either list): 0
//
// FreqWeightEN is safe for concurrent use after the first call.
func FreqWeightEN(word string) float64 {
	initFreqEN()
	if w, ok := freqEN.weight(word); ok {
		return w
	}
	if Dictionary().Contains(word) {
		return baseFreqWeight
	}
	return 0
}

// WeightedScoreEN scores s using the Zipfian frequency weight for each
// whitespace token. It returns the mean FreqWeightEN over all tokens (0 for
// empty input). Common words (e.g. "the", "and") outrank rare in-dict words.
func WeightedScoreEN(s string) float64 {
	return weightedScore(s, FreqWeightEN)
}

// ---------------------------------------------------------------------------
// Shared helper
// ---------------------------------------------------------------------------

// weightedScore is the shared implementation for WeightedScoreFR and
// WeightedScoreEN: it splits s on whitespace, lowercases each token, and
// returns the mean weight() over all tokens (0 for empty input).
func weightedScore(s string, weight func(string) float64) float64 {
	if s == "" {
		return 0
	}
	var sum float64
	n := 0
	for token := range strings.FieldsSeq(s) {
		n++
		sum += weight(strings.ToLower(token))
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
