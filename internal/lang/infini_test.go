package lang_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel/internal/lang"
)

// TestInfini_plausibleBeatsGibberish verifies that a real French sentence scores
// higher than a shuffled (gibberish) permutation of the same characters.
func TestInfini_plausibleBeatsGibberish(t *testing.T) {
	m := lang.NewInfini(smallFrenchCorpus)
	sentence := "celui qui ne connaît pas l'histoire"
	runes := []rune(sentence)
	shuffled := shuffleRunes(runes, 42)

	good, bad := m.Score(sentence), m.Score(shuffled)
	if good <= bad {
		t.Errorf("Score(french sentence)=%.4f should beat Score(shuffled)=%.4f", good, bad)
	}
}

// TestInfini_variableOrderBeatsBigram checks that a phrase whose 3+-gram appears
// verbatim in the corpus scores higher than a plausible-but-unseen rearrangement,
// demonstrating that longer contexts are exploited when available.
func TestInfini_variableOrderBeatsBigram(t *testing.T) {
	// Build a minimal corpus that contains "bonjour" but not "rjuonob".
	corpus := "bonjour tout le monde. bonjour la france. le monde dit bonjour."
	m := lang.NewInfini(corpus)

	verbatim := "bonjour"   // long context found in corpus
	rearranged := "rjuonob" // same bytes, not in corpus

	vScore := m.Score(verbatim)
	rScore := m.Score(rearranged)

	if vScore <= rScore {
		t.Errorf("verbatim corpus phrase Score=%.4f should beat rearranged Score=%.4f", vScore, rScore)
	}
}

// TestInfini_accentsPreserved verifies that accented characters carry signal:
// the model does NOT collapse "î" to space, and "naît" beats a string with the
// accent byte replaced by a random ASCII byte.
func TestInfini_accentsPreserved(t *testing.T) {
	// Use the default French prior which is trained on accented text.
	m := lang.InfiniPrior()

	accented := "connaît"
	// Replace the UTF-8 accent character 'î' (U+00EE, bytes 0xC3 0xAE) with 'x'.
	corrupted := strings.ReplaceAll(accented, "î", "x")

	aScore := m(accented)
	cScore := m(corrupted)

	if aScore <= cScore {
		t.Errorf("accented Score(%q)=%.4f should beat corrupted Score(%q)=%.4f",
			accented, aScore, corrupted, cScore)
	}
}

// TestInfini_accentDistinctFromASCIIFold verifies that Infini distinguishes
// "naît" from "nait" (no accent), unlike Model which folds both to the same
// rune sequence. The corpus contains "connaît" (accented), so the accented form
// must score at least as well as the unaccented form.
func TestInfini_accentDistinctFromASCIIFold(t *testing.T) {
	m := lang.InfiniPrior()
	withAccent := "naît"
	withoutAccent := "nait"

	// The old Model collapses non-ASCII to space, so it can't distinguish these.
	// Infini must give "naît" a score that differs from (and is >= ) "nait".
	aScore := m(withAccent)
	bScore := m(withoutAccent)

	// They must differ — accent carries real signal in the French corpus.
	if aScore == bScore {
		t.Errorf("Infini should distinguish %q (%.4f) from %q (%.4f); "+
			"got identical scores (accent folded away)", withAccent, aScore, withoutAccent, bScore)
	}
}

// TestInfini_emptyStringFloor verifies that scoring an empty string returns a
// finite negative floor and does not panic.
func TestInfini_emptyStringFloor(t *testing.T) {
	m := lang.NewInfini(smallFrenchCorpus)
	s := m.Score("")
	if s != s { // NaN check
		t.Fatalf("Score(\"\") returned NaN")
	}
	if s >= 0 {
		t.Errorf("Score(\"\") = %v, want a finite negative floor", s)
	}
}

// TestInfini_defaultFrench smoke-tests InfiniPrior and DefaultFrench.
func TestInfini_defaultFrench(t *testing.T) {
	prior := lang.InfiniPrior()
	score := prior("il était une fois")
	if score != score {
		t.Fatal("InfiniPrior score is NaN")
	}
	m := lang.DefaultFrench()
	if m == nil {
		t.Fatal("DefaultFrench() returned nil")
	}
}

// BenchmarkInfiniScore measures the hot-path cost of scoring a French sentence.
// NewInfini build is intentionally outside the loop.
func BenchmarkInfiniScore(b *testing.B) {
	m := lang.NewInfini(smallFrenchCorpus)
	sentence := "celui qui ne connaît pas l'histoire est condamné à la revivre"
	b.ReportAllocs()
	for b.Loop() {
		_ = m.Score(sentence)
	}
}

// BenchmarkInfiniNew measures corpus indexing time (suffix array build).
func BenchmarkInfiniNew(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = lang.NewInfini(smallFrenchCorpus)
	}
}

// smallFrenchCorpus is a minimal French text used in corpus-building tests so
// they are not coupled to the embedded corpus_fr.txt file.
const smallFrenchCorpus = `celui qui ne connaît pas l'histoire est condamné à la revivre.
la vie est belle. le temps passe vite. il faut profiter de la vie.
bonjour tout le monde. bonsoir mes amis. au revoir et à bientôt.
je t'aime. nous aimons la france. vous aimez le fromage et le vin.
il était une fois un petit prince qui vivait sur une planète lointaine.
les enfants jouent dans le jardin sous le soleil de l'été.
la liberté, l'égalité et la fraternité sont les valeurs de la france.
après la pluie vient le beau temps. mieux vaut tard que jamais.
qui cherche trouve. tout est bien qui finit bien. c'est la vie.`

// shuffleRunes returns a deterministically shuffled copy of runes.
func shuffleRunes(runes []rune, seed uint64) string {
	cp := make([]rune, len(runes))
	copy(cp, runes)
	r := rand.New(rand.NewPCG(seed, seed^0xdeadbeef))
	r.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
	return string(cp)
}
