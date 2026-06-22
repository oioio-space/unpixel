package lang_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel/internal/lang"
)

// ---------------------------------------------------------------------------
// Language type
// ---------------------------------------------------------------------------

// TestLanguage_String verifies String() returns the short ISO code.
func TestLanguage_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lang lang.Language
		want string
	}{
		{lang.English, "en"},
		{lang.French, "fr"},
	}
	for _, tc := range cases {
		if got := tc.lang.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.lang, got, tc.want)
		}
	}
}

// TestParseLanguage_roundTrip verifies all documented aliases parse correctly.
func TestParseLanguage_roundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  lang.Language
		ok    bool
	}{
		{"en", lang.English, true},
		{"EN", lang.English, true},
		{"english", lang.English, true},
		{"English", lang.English, true},
		{"fr", lang.French, true},
		{"FR", lang.French, true},
		{"french", lang.French, true},
		{"français", lang.French, true},
		{"FRANÇAIS", lang.French, true},
		{"de", lang.English, false}, // unknown → false
		{"", lang.English, false},
	}
	for _, tc := range cases {
		got, ok := lang.ParseLanguage(tc.input)
		if ok != tc.ok {
			t.Errorf("ParseLanguage(%q) ok=%v, want %v", tc.input, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("ParseLanguage(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// French dictionary
// ---------------------------------------------------------------------------

// TestFrenchDictionary_accents checks that accented words are present and
// retrieved without folding.
func TestFrenchDictionary_accents(t *testing.T) {
	t.Parallel()
	d := lang.FrenchDictionary()
	must := []string{
		"connaît", "liberté", "égalité", "été", "où", "déjà", "français",
		"histoire", "condamné", "revivre",
		// Common function words
		"le", "la", "les", "un", "une", "de", "des", "du", "et", "est",
		"qui", "ne", "pas", "pour",
	}
	for _, w := range must {
		if !d.Contains(w) {
			t.Errorf("FrenchDictionary().Contains(%q) = false, want true", w)
		}
	}
	// Non-French words must be absent.
	absent := []string{"the", "hello", "xqzjv", "LIBERTÉ"}
	for _, w := range absent {
		if d.Contains(w) {
			t.Errorf("FrenchDictionary().Contains(%q) = true, want false", w)
		}
	}
}

// TestDictionaryFor_dispatchesLanguage checks DictionaryFor delegates to the
// right dictionary for each language.
func TestDictionaryFor_dispatchesLanguage(t *testing.T) {
	t.Parallel()
	en := lang.DictionaryFor(lang.English)
	fr := lang.DictionaryFor(lang.French)

	if !en.Contains("hello") {
		t.Error("DictionaryFor(English) missing 'hello'")
	}
	if !fr.Contains("liberté") {
		t.Error("DictionaryFor(French) missing 'liberté'")
	}
	// Cross-language: English dict must not contain accented French words
	// and vice-versa.
	if en.Contains("connaît") {
		t.Error("DictionaryFor(English) should not contain 'connaît'")
	}
	if fr.Contains("hello") {
		t.Error("DictionaryFor(French) should not contain 'hello'")
	}
}

// ---------------------------------------------------------------------------
// Dict enumeration API
// ---------------------------------------------------------------------------

// TestDict_All verifies that All() returns a non-empty iterator over words.
func TestDict_All(t *testing.T) {
	t.Parallel()
	d := lang.FrenchDictionary()
	var count int
	for w := range d.All() {
		if w == "" {
			t.Error("All() yielded an empty string")
		}
		count++
	}
	if count < 100 {
		t.Errorf("All() yielded only %d words, want ≥ 100", count)
	}
}

// TestDict_All_earlyExit verifies that All() respects early termination: when
// yield returns false (the caller breaks from the range loop), the iterator
// stops without delivering additional words.
func TestDict_All_earlyExit(t *testing.T) {
	t.Parallel()
	d := lang.FrenchDictionary()
	var got int
	for range d.All() {
		got++
		break // yield returns false after the first word
	}
	if got != 1 {
		t.Errorf("All() early-exit: got %d words, want 1", got)
	}
}

// TestDict_ByRuneLen checks that ByRuneLen returns only words of the correct
// rune length and that the result is non-empty for common lengths.
func TestDict_ByRuneLen(t *testing.T) {
	t.Parallel()
	d := lang.FrenchDictionary()
	for _, n := range []int{2, 3, 4, 5} {
		words := d.ByRuneLen(n)
		if len(words) == 0 {
			t.Errorf("ByRuneLen(%d) returned 0 words", n)
			continue
		}
		for _, w := range words {
			if got := len([]rune(w)); got != n {
				t.Errorf("ByRuneLen(%d): word %q has rune len %d", n, w, got)
			}
		}
	}
}

// TestDict_ByRuneLen_cached verifies that two calls return equal slices (caching).
func TestDict_ByRuneLen_cached(t *testing.T) {
	t.Parallel()
	d := lang.FrenchDictionary()
	a := d.ByRuneLen(4)
	b := d.ByRuneLen(4)
	if !slices.Equal(a, b) {
		t.Error("ByRuneLen(4) returned different slices on second call")
	}
}

// TestDict_All_English ensures All() also works for the English dictionary.
func TestDict_All_English(t *testing.T) {
	t.Parallel()
	d := lang.Dictionary()
	var count int
	for range d.All() {
		count++
	}
	if count == 0 {
		t.Error("Dictionary().All() yielded 0 words")
	}
}

// ---------------------------------------------------------------------------
// Bilingual Infini char model
// ---------------------------------------------------------------------------

// TestInfiniDefaultEnglish_prefersEnglish verifies the English model scores
// English higher than French.
// Not parallel: Infini.Score is not goroutine-safe (shared cache).
func TestInfiniDefaultEnglish_prefersEnglish(t *testing.T) {
	m := lang.InfiniDefaultEnglish()
	enSentence := "the password is correct horse battery"
	frSentence := "celui qui ne connaît pas l'histoire"
	enScore := m.Score(enSentence)
	frScore := m.Score(frSentence)
	if enScore <= frScore {
		t.Errorf("DefaultEnglish: English sentence (%.4f) should beat French (%.4f)",
			enScore, frScore)
	}
}

// TestDefaultFrench_prefersFrench verifies the French model scores French
// higher than English.
// Not parallel: Infini.Score is not goroutine-safe (shared cache).
func TestDefaultFrench_prefersFrench(t *testing.T) {
	m := lang.DefaultFrench()
	enSentence := "the password is correct horse battery"
	frSentence := "celui qui ne connaît pas l'histoire"
	enScore := m.Score(enSentence)
	frScore := m.Score(frSentence)
	if frScore <= enScore {
		t.Errorf("DefaultFrench: French sentence (%.4f) should beat English (%.4f)",
			frScore, enScore)
	}
}

// TestInfiniFor_dispatches verifies InfiniFor routes to the correct model.
// Not parallel: Infini.Score is not goroutine-safe (shared cache).
func TestInfiniFor_dispatches(t *testing.T) {
	en := lang.InfiniFor(lang.English)
	fr := lang.InfiniFor(lang.French)

	enSentence := "the quick brown fox"
	frSentence := "les enfants jouent dans le jardin"

	if en.Score(enSentence) <= en.Score(frSentence) {
		t.Error("InfiniFor(English) should prefer English over French")
	}
	if fr.Score(frSentence) <= fr.Score(enSentence) {
		t.Error("InfiniFor(French) should prefer French over English")
	}
}

// ---------------------------------------------------------------------------
// PriorFor — fused prior
// ---------------------------------------------------------------------------

// TestPriorFor_frenchSentenceBeatsShuffled checks that the French prior ranks
// a real sentence above a shuffled (word-order scrambled) version and above an
// English sentence.
//
// Concrete numbers (pinned for documentation; subject to corpus/weight tuning):
//
//	PriorFor(French)("celui qui ne connaît pas l'histoire")   ≈ +0.65
//	PriorFor(French)(shuffled words same sentence)             ≈ +0.30
//	PriorFor(French)("the password is correct horse battery")  ≈ -0.50
func TestPriorFor_frenchSentenceBeatsShuffled(t *testing.T) {
	t.Parallel()
	prior := lang.PriorFor(lang.French)

	sentence := "celui qui ne connaît pas l'histoire"
	// Shuffle at word level (not character level) — same words, different order.
	shuffledWords := "connaît pas qui l'histoire ne celui"
	english := "the password is correct horse battery"

	sScore := prior(sentence)
	shScore := prior(shuffledWords)
	enScore := prior(english)

	if sScore <= shScore {
		t.Errorf("PriorFor(French): sentence (%.4f) should beat shuffled (%.4f)",
			sScore, shScore)
	}
	if sScore <= enScore {
		t.Errorf("PriorFor(French): French sentence (%.4f) should beat English (%.4f)",
			sScore, enScore)
	}
	t.Logf("PriorFor(French): sentence=%.4f, shuffled=%.4f, english=%.4f",
		sScore, shScore, enScore)
}

// TestPriorFor_englishSentenceBeatsShuffledAndFrench is the mirror test.
func TestPriorFor_englishSentenceBeatsShuffledAndFrench(t *testing.T) {
	t.Parallel()
	prior := lang.PriorFor(lang.English)

	sentence := "the password is correct horse battery"
	shuffledWords := "battery horse the correct is password"
	french := "celui qui ne connaît pas l'histoire"

	sScore := prior(sentence)
	shScore := prior(shuffledWords)
	frScore := prior(french)

	if sScore <= shScore {
		t.Errorf("PriorFor(English): sentence (%.4f) should beat shuffled (%.4f)",
			sScore, shScore)
	}
	if sScore <= frScore {
		t.Errorf("PriorFor(English): English sentence (%.4f) should beat French (%.4f)",
			sScore, frScore)
	}
	t.Logf("PriorFor(English): sentence=%.4f, shuffled=%.4f, french=%.4f",
		sScore, shScore, frScore)
}

// TestPriorFor_returnsNonNil checks the returned function is non-nil.
func TestPriorFor_returnsNonNil(t *testing.T) {
	t.Parallel()
	if lang.PriorFor(lang.English) == nil {
		t.Error("PriorFor(English) = nil")
	}
	if lang.PriorFor(lang.French) == nil {
		t.Error("PriorFor(French) = nil")
	}
}

// ---------------------------------------------------------------------------
// Backward-compatibility: existing English entry points must still work
// ---------------------------------------------------------------------------

// TestBackwardCompat_dictionary verifies that Dictionary() still returns
// English words unchanged.
func TestBackwardCompat_dictionary(t *testing.T) {
	t.Parallel()
	d := lang.Dictionary()
	if !d.Contains("hello") {
		t.Error("Dictionary().Contains('hello') = false; backward-compat broken")
	}
}

// TestBackwardCompat_dictionaryScore checks DictionaryScore is unchanged.
func TestBackwardCompat_dictionaryScore(t *testing.T) {
	t.Parallel()
	s := lang.DictionaryScore("hello world")
	if s <= 0 {
		t.Errorf("DictionaryScore('hello world') = %v, want > 0", s)
	}
}

// TestBackwardCompat_defaultModel verifies lang.Default() still scores English
// sensibly.
func TestBackwardCompat_defaultModel(t *testing.T) {
	t.Parallel()
	m := lang.Default()
	if m == nil {
		t.Fatal("Default() returned nil")
	}
	if m.Score("the code") <= m.Score("xqzj wkvb") {
		t.Error("Default() model no longer prefers English text")
	}
}

// TestBackwardCompat_frenchwWordList verifies the size of the French word list
// meets the minimum requirement (≥ 2000 words).
func TestBackwardCompat_frenchwWordList(t *testing.T) {
	t.Parallel()
	d := lang.FrenchDictionary()
	var count int
	for range d.All() {
		count++
	}
	if count < 2000 {
		t.Errorf("FrenchDictionary has %d words, want ≥ 2000", count)
	}
}

// TestPriorFor_OOV verifies that PriorFor still gives a meaningful score for
// out-of-vocabulary strings (char-level fallback must not panic or return NaN).
func TestPriorFor_OOV(t *testing.T) {
	t.Parallel()
	for _, l := range []lang.Language{lang.English, lang.French} {
		prior := lang.PriorFor(l)
		score := prior("xqzjvwbk")
		if score != score { // NaN
			t.Errorf("PriorFor(%v)(oov) = NaN", l)
		}
	}
}

// TestPriorFor_empty verifies that scoring an empty string does not panic and
// returns a finite value.
func TestPriorFor_empty(t *testing.T) {
	t.Parallel()
	for _, l := range []lang.Language{lang.English, lang.French} {
		prior := lang.PriorFor(l)
		score := prior("")
		if score != score {
			t.Errorf("PriorFor(%v)(\"\") = NaN", l)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmarks (hot-path rule)
// ---------------------------------------------------------------------------

// BenchmarkPriorFor_French measures the cost of scoring a French sentence with
// the fused prior. The model is built outside b.Loop() to isolate per-call cost.
func BenchmarkPriorFor_French(b *testing.B) {
	prior := lang.PriorFor(lang.French)
	sentence := "celui qui ne connaît pas l'histoire est condamné à la revivre"
	b.ReportAllocs()
	for b.Loop() {
		_ = prior(sentence)
	}
}

// BenchmarkPriorFor_English is the English mirror benchmark.
func BenchmarkPriorFor_English(b *testing.B) {
	prior := lang.PriorFor(lang.English)
	sentence := "the password is correct horse battery staple"
	b.ReportAllocs()
	for b.Loop() {
		_ = prior(sentence)
	}
}

// smallWordCheck is used by the ByRuneLen benchmark to avoid import cycle.
var _ = strings.ToLower // ensure strings import is used
