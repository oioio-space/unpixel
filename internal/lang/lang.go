// Package lang provides a tiny, pure-Go character bigram language model used as a
// prior over candidate strings. Image distance alone cannot separate visually
// near-identical candidates (especially behind heavy blur); a language prior
// breaks those ties toward plausible text, and flags implausible "recoveries".
//
// The model is a Laplace-smoothed character bigram trained at init from a small
// embedded corpus of English prose and code. It is deliberately lightweight (no
// CGO, no large model): it ranks, it does not generate.
package lang

import (
	_ "embed"
	"math"
	"strings"
	"unicode"
)

//go:embed corpus.txt
var corpus string

// Model is a character bigram language model. The zero value is not usable; use
// Default or New.
type Model struct {
	logProb map[[2]rune]float64 // log P(next | prev), Laplace-smoothed
	unigram map[rune]float64    // log P(rune), fallback for unseen contexts
	vocab   int
}

var defaultModel = New(corpus)

// Default returns the shared model trained on the embedded corpus.
func Default() *Model { return defaultModel }

// New trains a bigram model on text.
func New(text string) *Model {
	text = strings.ToLower(text)
	biCount := map[[2]rune]int{}
	uniCount := map[rune]int{}
	ctxTotal := map[rune]int{}
	vocab := map[rune]struct{}{}
	prev := ' '
	total := 0
	for _, r := range text {
		if r > unicode.MaxASCII {
			r = ' '
		}
		vocab[r] = struct{}{}
		uniCount[r]++
		biCount[[2]rune{prev, r}]++
		ctxTotal[prev]++
		total++
		prev = r
	}
	v := len(vocab)
	if v == 0 {
		v = 1
	}
	m := &Model{
		logProb: make(map[[2]rune]float64, len(biCount)),
		unigram: make(map[rune]float64, len(uniCount)),
		vocab:   v,
	}
	for bg, c := range biCount {
		// Laplace (add-1) smoothing over the vocabulary.
		m.logProb[bg] = math.Log(float64(c+1) / float64(ctxTotal[bg[0]]+v))
	}
	for r, c := range uniCount {
		m.unigram[r] = math.Log(float64(c+1) / float64(total+v))
	}
	return m
}

// unseen is the log-prob assigned to a bigram whose context was never observed
// (a Laplace prior of count 0 over the vocabulary): log(1/v) plus a penalty so
// genuinely unseen contexts rank below merely-rare observed ones.
func (m *Model) unseen() float64 { return math.Log(1.0/float64(m.vocab)) - 1 }

// TransitionLogProb returns log P(next|prev) under the bigram model, applying
// the same ASCII-clamp and smoothing/backoff that Score uses: the observed
// bigram log-prob if present, the unigram log-prob minus 1 (observed character,
// unseen context), or unseen() for a completely unseen character.
//
// It emits the exact per-edge factor summed by Score: for ASCII lowercase input
// with a ' ' start context, summing TransitionLogProb(' ', s[0]),
// TransitionLogProb(s[0], s[1]), … equals Score(s) × len(s).
func (m *Model) TransitionLogProb(prev, next rune) float64 {
	if prev > unicode.MaxASCII {
		prev = ' '
	}
	if next > unicode.MaxASCII {
		next = ' '
	}
	prev = unicode.ToLower(prev)
	next = unicode.ToLower(next)
	if lp, ok := m.logProb[[2]rune{prev, next}]; ok {
		return lp
	}
	if lp, ok := m.unigram[next]; ok {
		return lp - 1
	}
	return m.unseen()
}

// Score returns the mean per-character log-probability of s (higher = more
// plausible text). An empty string scores the unseen floor. Case is ignored.
func (m *Model) Score(s string) float64 {
	if s == "" {
		return m.unseen()
	}
	s = strings.ToLower(s)
	var sum float64
	n := 0
	prev := ' '
	for _, r := range s {
		if r > unicode.MaxASCII {
			r = ' '
		}
		if lp, ok := m.logProb[[2]rune{prev, r}]; ok {
			sum += lp
		} else if lp, ok := m.unigram[r]; ok {
			sum += lp - 1 // observed char, unseen context
		} else {
			sum += m.unseen()
		}
		prev = r
		n++
	}
	return sum / float64(n)
}
