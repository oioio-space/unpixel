// bilingual.go — French support and unified PriorFor for package lang.
//
// This file adds French word-dictionary and character-model entry points
// alongside the existing English API, and provides PriorFor which fuses
// word-dictionary and variable-order character infini-gram scores.
//
// Design — fusion weights:
//
//	score = wDict * dictionaryScore + wChar * infini.Score(s)
//
// Where wDict=0.5 and wChar=1.0.  Rationale:
//   - infini.Score returns mean per-byte log-prob, typically in [-3, -1].
//     dictionaryScore (BonusWord=1.0, mean over tokens) sits in [0, 1].
//   - Without weighting, the char model dominates for long strings and the
//     dict bonus disappears below noise.  With wDict=0.5 the dict contributes
//     ~0.5 per fully in-vocabulary token — enough to separate "histoire" from
//     a shuffled form while keeping the char model's OOV discrimination.
//   - wChar=1.0 keeps the char floor visible even for OOV strings (accented
//     forms, proper nouns) so the prior never goes silent.
//   - Empirical verification: see TestPriorFor_frenchSentenceBeatsShuffled /
//     TestPriorFor_englishSentenceBeatsShuffledAndFrench.

package lang

import (
	_ "embed"
	"strings"
	"sync"
	"unicode/utf8"
)

// Language identifies a supported natural language.
type Language int

const (
	// English selects English word lists and character models.
	English Language = iota
	// French selects French word lists and character models (accent-aware).
	French
)

// String returns the ISO 639-1 two-letter code for the language ("en" / "fr").
func (l Language) String() string {
	switch l {
	case French:
		return "fr"
	default:
		return "en"
	}
}

// ParseLanguage parses a language name or code (case-insensitive).
// Recognised values: "en", "english" for English; "fr", "french", "français"
// for French. The second return value reports whether the input was recognised.
func ParseLanguage(s string) (Language, bool) {
	switch strings.ToLower(s) {
	case "en", "english":
		return English, true
	case "fr", "french", "français":
		return French, true
	default:
		return English, false
	}
}

// ---------------------------------------------------------------------------
// French dictionary
// ---------------------------------------------------------------------------

//go:embed words_fr.txt
var wordListFR string

var (
	frDictOnce    sync.Once
	defaultFRDict *Dict
)

// FrenchDictionary returns the shared Dict built from the embedded French word
// list. Words preserve correct accents ("connaît", "liberté", "égalité"…).
// It is initialised exactly once and is safe for concurrent use. Contains uses
// the word as given — the list is entirely lowercase, so callers wanting
// case-insensitive lookup should fold with strings.ToLower first.
func FrenchDictionary() *Dict {
	frDictOnce.Do(func() {
		defaultFRDict = buildDict(wordListFR)
	})
	return defaultFRDict
}

// DictionaryFor returns the appropriate Dict for l.
// DictionaryFor(English) == Dictionary(); DictionaryFor(French) == FrenchDictionary().
func DictionaryFor(l Language) *Dict {
	if l == French {
		return FrenchDictionary()
	}
	return Dictionary()
}

// ---------------------------------------------------------------------------
// Dict enumeration API
// ---------------------------------------------------------------------------

// All returns an iterator over every word in the dictionary in unspecified
// order. The iterator is safe to call multiple times; each call returns a
// fresh traversal.
func (d *Dict) All() func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for w := range d.words {
			if !yield(w) {
				return
			}
		}
	}
}

// lenIndex caches ByRuneLen results; protected by lenMu.
// The fields are initialised lazily on the first ByRuneLen call.
type lenIndex struct {
	mu    sync.Mutex
	index map[int][]string
}

// lenIndices maps each *Dict to its length index.  A simple package-level
// mutex is sufficient: index building is idempotent, and Dict is immutable
// after construction.
var (
	lenIdxMu sync.Mutex
	lenIdxs  = map[*Dict]*lenIndex{}
)

// ByRuneLen returns all words in the dictionary whose rune length is exactly n.
// The result slice is cached after the first call for a given n; subsequent
// calls for the same n return the identical slice.  An n that matches no words
// returns nil.
func (d *Dict) ByRuneLen(n int) []string {
	lenIdxMu.Lock()
	idx, ok := lenIdxs[d]
	if !ok {
		idx = &lenIndex{index: make(map[int][]string)}
		lenIdxs[d] = idx
	}
	lenIdxMu.Unlock()

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if words, ok := idx.index[n]; ok {
		return words
	}
	var words []string
	for w := range d.words {
		if utf8.RuneCountInString(w) == n {
			words = append(words, w)
		}
	}
	idx.index[n] = words
	return words
}

// ---------------------------------------------------------------------------
// English Infini model
// ---------------------------------------------------------------------------

var (
	defaultEnglishOnce  sync.Once
	defaultEnglishModel *Infini
)

// InfiniDefaultEnglish returns the shared Infini model trained on the embedded
// English corpus (corpus.txt). It is initialised exactly once. Not safe for
// concurrent Score calls — matches the Model and DefaultFrench contracts.
func InfiniDefaultEnglish() *Infini {
	defaultEnglishOnce.Do(func() {
		defaultEnglishModel = NewInfini(corpus)
	})
	return defaultEnglishModel
}

// InfiniFor returns the Infini model for the given language:
// InfiniFor(English) == InfiniDefaultEnglish();
// InfiniFor(French)  == DefaultFrench().
func InfiniFor(l Language) *Infini {
	if l == French {
		return DefaultFrench()
	}
	return InfiniDefaultEnglish()
}

// ---------------------------------------------------------------------------
// Fused prior
// ---------------------------------------------------------------------------

// Fusion weights — see the file-level doc for rationale.
const (
	wDict = 0.5 // weight on word-dictionary bonus (in [0, BonusWord])
	wChar = 1.0 // weight on infini-gram mean per-byte log-prob (typically [-3, -1])
)

// PriorFor returns a plausibility scorer for l: a fusion of the word-dictionary
// score and the variable-order character infini-gram, so in-vocabulary words are
// rewarded and out-of-vocabulary strings still get graceful char-level scoring.
// Higher is more plausible. The returned function is safe to pass directly as
// unpixel.WithLanguageModel (or composed via unpixel.WithPriors).
//
// Each call to PriorFor creates a dedicated Infini instance (not the shared
// singleton) so the returned closure can be called concurrently without races.
// The Infini cache inside the closure is private and is NOT shared.
//
// Fusion:
//
//	score(s) = wDict*dictionaryScore(s) + wChar*infini.Score(s)
//
// where wDict=0.5 and wChar=1.0. The dictionary term rewards known words; the
// char-gram term provides language discrimination for OOV strings and for
// distinguishing word-order permutations via sequence likelihood.
func PriorFor(l Language) func(string) float64 {
	dict := DictionaryFor(l)
	// Build a private Infini (not the shared singleton) so callers can invoke the
	// returned function from multiple goroutines without a data race on the cache.
	var corpusText string
	if l == French {
		corpusText = corpusFR
	} else {
		corpusText = corpus
	}
	infini := NewInfini(corpusText)

	return func(s string) float64 {
		return wDict*dict.Score(s) + wChar*infini.Score(s)
	}
}
