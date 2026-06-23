// elision.go — French apostrophe-elision candidate generation for wordPool.
//
// # French orthographic elisions
//
// In French, certain monosyllabic words drop their final vowel before a word
// beginning with a vowel or mute-h, replacing it with an apostrophe and
// joining the two words without whitespace. Examples:
//
//	l'histoire (= le + histoire)
//	c'est      (= ce + est)
//	d'accord   (= de + accord)
//	n'est      (= ne + est)
//	j'ai       (= je + ai)
//
// The segmenter treats such a band as a single visual unit (no surrounding
// space gap), so the glued form "l'histoire" is not in the dictionary and
// would otherwise be unrecoverable.
//
// # Algorithm
//
// elisionCandidates splits the estimated rune count for a band into:
//
//	total = prefixRunes + wordRunes
//
// where prefixRunes ∈ {len(p) for p ∈ FrenchElisionPrefixes} (2 or 3) and
// wordRunes = total − prefixRunes (≥ 1).  It then fetches dictionary words at
// rune lengths wordRunes±1 (the same ±1 tolerance used by wordPool), pairs
// each with the prefix, and returns the glued strings ranked by prior.
//
// The candidate set is additive and small (|prefixes| × 3 tiers × k per tier,
// gated behind Elisions=true and a minimum band width), so it does not
// materially affect the Cartesian-product budget on the non-elision path.

package blinddecode

import (
	"cmp"
	"slices"

	"github.com/oioio-space/unpixel/internal/lang"
)

// FrenchElisionPrefixes is the exhaustive set of French elision prefixes
// (French orthography, RF 1990 reform included). Each prefix ends with an
// apostrophe; the prefix rune length includes the apostrophe character.
//
// Reference: Arrêté du 6 décembre 1990 — Rectifications de l'orthographe;
// classic French grammar (Grévisse, "Le Bon Usage", §§ 49–52).
//
//   - l'  (le / la before vowel or mute-h)
//   - d'  (de)
//   - j'  (je)
//   - m'  (me)
//   - t'  (te)
//   - s'  (se)
//   - n'  (ne)
//   - c'  (ce)
//   - qu' (que — three runes: q, u, ')
//
// Note: "lorsqu'", "puisqu'", "quoiqu'", "jusque'" are longer and rarer;
// they are omitted to keep the candidate budget bounded.
var FrenchElisionPrefixes = []string{
	"l'", "d'", "j'", "m'", "t'", "s'", "n'", "c'", "qu'",
}

// elisionCandidates generates candidate strings of the form
// <elisionPrefix><word> where the combined rune count matches nEst±1,
// word is drawn from dict, and each candidate is scored via prior.
//
// k is the per-rune-length cap (same budget as wordPool's regular tier).
// It returns at most len(FrenchElisionPrefixes) × 3 × k deduplicated strings,
// ranked descending by prior within each (prefix, wordLen) bucket.
// Returns nil when the band is too narrow to hold prefix+1 word character.
func elisionCandidates(
	nEst int,
	k int,
	d *lang.Dict,
	prior func(string) float64,
) []string {
	type scored struct {
		word  string
		score float64
	}

	seen := make(map[string]struct{})
	var out []string

	for _, prefix := range FrenchElisionPrefixes {
		prefixRunes := len([]rune(prefix))
		for delta := -1; delta <= 1; delta++ {
			total := nEst + delta
			wordRunes := total - prefixRunes
			if wordRunes < 1 {
				continue
			}
			tier := slices.Clone(d.ByRuneLen(wordRunes))
			if len(tier) == 0 {
				continue
			}
			pairs := make([]scored, len(tier))
			for i, w := range tier {
				glued := prefix + w
				pairs[i] = scored{word: glued, score: prior(glued)}
			}
			slices.SortFunc(pairs, func(a, b scored) int {
				return cmp.Compare(b.score, a.score) // descending
			})
			lim := min(k, len(pairs))
			for _, p := range pairs[:lim] {
				if _, dup := seen[p.word]; !dup {
					seen[p.word] = struct{}{}
					out = append(out, p.word)
				}
			}
		}
	}
	return out
}
