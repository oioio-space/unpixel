// Package linearml is a tiny, dependency-free linear softmax classifier used by
// the //go:build ml model seams (fontprior font-ID, rerank glyph emissions). It is
// deliberately minimal — a single dense layer trained by deterministic full-batch
// gradient descent — because the ML tier's job is a pure-Go forward pass with no
// CGO and no external framework; the renderer supplies unlimited labelled data, so
// a convex linear model trained on rich synthetic features is a strong, reproducible
// baseline. Callers own feature extraction and the class↔label mapping.
//
//	m := linearml.Train(X, y, nClass, linearml.Options{})
//	probs := m.Predict(feature) // len == nClass, sums to 1
package linearml

import "math"

// Options configures training. The zero value uses sensible defaults
// (Epochs=300, LR=0.3, L2=1e-4).
type Options struct {
	Epochs int     // gradient-descent passes; 0 → 300
	LR     float64 // learning rate; 0 → 0.3
	L2     float64 // L2 weight decay; 0 → 1e-4
}

func (o Options) withDefaults() Options {
	if o.Epochs <= 0 {
		o.Epochs = 300
	}
	if o.LR <= 0 {
		o.LR = 0.3
	}
	if o.L2 <= 0 {
		o.L2 = 1e-4
	}
	return o
}

// Softmax is a trained linear softmax classifier: Predict(x) = softmax(W·x + b).
// It is safe for concurrent Predict calls after Train returns.
type Softmax struct {
	W      [][]float64 // [nClass][nFeat]
	B      []float64   // [nClass]
	nClass int
	nFeat  int
}

// Train fits a softmax classifier on samples with integer labels y in [0, nClass)
// by deterministic full-batch gradient descent (zero initialisation), so the same
// data yields the same model. samples and y must have equal length; empty samples
// returns a zero-weight model.
func Train(samples [][]float64, y []int, nClass int, opts Options) *Softmax {
	opts = opts.withDefaults()
	nFeat := 0
	if len(samples) > 0 {
		nFeat = len(samples[0])
	}
	m := &Softmax{
		W:      make([][]float64, nClass),
		B:      make([]float64, nClass),
		nClass: nClass,
		nFeat:  nFeat,
	}
	for c := range m.W {
		m.W[c] = make([]float64, nFeat)
	}
	if len(samples) == 0 {
		return m
	}
	invN := 1.0 / float64(len(samples))
	gradW := make([][]float64, nClass)
	for c := range gradW {
		gradW[c] = make([]float64, nFeat)
	}
	gradB := make([]float64, nClass)
	for range opts.Epochs {
		for c := range gradW {
			clear(gradW[c])
			gradB[c] = 0
		}
		for i, x := range samples {
			p := m.Predict(x)
			for c := range nClass {
				d := p[c]
				if c == y[i] {
					d--
				}
				gradB[c] += d
				wc := gradW[c]
				for j, xj := range x {
					wc[j] += d * xj
				}
			}
		}
		for c := range nClass {
			m.B[c] -= opts.LR * gradB[c] * invN
			wc, gc := m.W[c], gradW[c]
			for j := range wc {
				wc[j] -= opts.LR * (gc[j]*invN + opts.L2*wc[j])
			}
		}
	}
	return m
}

// Predict returns the class-probability vector for feature x (length nClass, sums
// to 1). A feature shorter than nFeat is zero-padded; extra entries are ignored.
func (m *Softmax) Predict(x []float64) []float64 {
	logits := make([]float64, m.nClass)
	maxL := math.Inf(-1)
	for c := range m.nClass {
		s := m.B[c]
		wc := m.W[c]
		n := min(len(x), len(wc))
		for j := range n {
			s += wc[j] * x[j]
		}
		logits[c] = s
		if s > maxL {
			maxL = s
		}
	}
	var sum float64
	for c := range logits {
		logits[c] = math.Exp(logits[c] - maxL)
		sum += logits[c]
	}
	if sum == 0 {
		sum = 1
	}
	for c := range logits {
		logits[c] /= sum
	}
	return logits
}

// NumClasses reports the number of classes the model was trained for.
func (m *Softmax) NumClasses() int { return m.nClass }
