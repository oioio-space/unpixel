package lang

import (
	_ "embed"
	"strings"
	"sync"
)

// BonusWord is the plausibility bonus awarded per token that is a known
// dictionary word. The score returned by DictionaryScore is the mean of
// per-token bonuses (BonusWord when the token is in the dictionary, 0
// otherwise), so it lies in [0, BonusWord] and never penalises a candidate.
//
// The value 1.0 is chosen to be on the same order of magnitude as the
// character-bigram scores used by Model.Score, so that the two priors compose
// cleanly when summed via unpixel.WithPriors.
const BonusWord = 1.0

//go:embed words.txt
var wordList string

// Dict is a set of known English words used as a plausibility prior. The zero
// value is not usable; obtain one with Dictionary.
type Dict struct {
	words map[string]struct{}
}

var (
	dictOnce    sync.Once
	defaultDict *Dict
)

// Dictionary returns the shared Dict built from the embedded word list. It is
// initialised exactly once (sync.Once) and is safe for concurrent use.
func Dictionary() *Dict {
	dictOnce.Do(func() {
		defaultDict = buildDict(wordList)
	})
	return defaultDict
}

// buildDict parses a newline-separated word list into a Dict. It is a
// package-internal constructor used by Dictionary and exposed for testing.
func buildDict(text string) *Dict {
	d := &Dict{words: make(map[string]struct{})}
	for line := range strings.Lines(text) {
		w := strings.TrimSpace(line)
		if w != "" {
			d.words[w] = struct{}{}
		}
	}
	return d
}

// Contains reports whether word (as given, no case folding) is in the
// dictionary. The embedded word list is lowercase, so callers that want
// case-insensitive lookup should fold with strings.ToLower first.
func (d *Dict) Contains(word string) bool {
	_, ok := d.words[word]
	return ok
}

// Score scores s as a plausibility prior based on whole-word dictionary
// membership. It splits s on whitespace into tokens and returns the mean
// per-token bonus: BonusWord for each token that is a known dictionary word
// (after lowercasing), 0 otherwise. Single-character tokens that are valid
// words ("a", "i") are counted; other single-character tokens score 0.
//
// The return value is in [0, BonusWord]. An empty string or a string with no
// tokens returns 0, so the prior never penalises a candidate — it only rewards
// recognisable text.
func (d *Dict) Score(s string) float64 {
	if s == "" {
		return 0
	}
	var bonus float64
	n := 0
	for token := range strings.FieldsSeq(s) {
		n++
		if d.Contains(strings.ToLower(token)) {
			bonus += BonusWord
		}
	}
	if n == 0 {
		return 0
	}
	return bonus / float64(n)
}

// DictionaryScore scores s against the shared English dictionary. It is a
// convenience wrapper around Dictionary().Score(s); see [Dict.Score] for full
// semantics.
func DictionaryScore(s string) float64 {
	return Dictionary().Score(s)
}

// DictionaryPrior returns a plausibility scorer backed by the embedded English
// word list, ready for use with unpixel.WithPriors. The returned function is
// DictionaryScore expressed as a closure, making it trivial to compose with
// other priors:
//
//	res, _ := unpixel.Recover(ctx, img,
//	    unpixel.WithPriors(defaults.LanguageModel(), lang.DictionaryPrior()),
//	)
//
// The returned scorer awards BonusWord per known dictionary word (mean over
// tokens), returning 0 for empty input. It never returns a negative value.
func DictionaryPrior() func(string) float64 {
	return DictionaryScore
}
