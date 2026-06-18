package search_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/search"
)

// resultSignature renders results into a stable string so two runs can be
// compared for determinism regardless of slice identity.
func resultSignature(results []unpixel.Result) string {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "off(%d,%d) best=%q score=%.6f ncand=%d top=%d|",
			r.Offset.X, r.Offset.Y, r.BestGuess, r.BestScore, len(r.Candidates), len(r.TopN))
		for _, e := range r.Candidates {
			fmt.Fprintf(&b, "%s:%.6f,", e.Guess, e.Score)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestFanout_workersDoNotChangeResults verifies the deterministic merge: running
// the same search with Workers=1 (sequential) and Workers=8 (parallel) yields
// byte-identical Results. Covers both strategies.
func TestFanout_workersDoNotChangeResults(t *testing.T) {
	inner, base, _, _ := buildScorerFixture(t)
	base.Charset = "abcdefghijklmnopqrstuvwxyz "
	base.MaxLength = 3
	base.TopN = 5
	base.BeamWidth = 16
	redacted := inner.RedactedImage()

	strategies := map[string]unpixel.Strategy{
		"guided": search.NewGuidedStrategy(),
		"beam":   search.NewBeamStrategy(0),
	}
	for name, strategy := range strategies {
		t.Run(name, func(t *testing.T) {
			seqCfg := base
			seqCfg.Workers = 1
			_, seq := drainAndRun(t, strategy, redacted, seqCfg)

			parCfg := base
			parCfg.Workers = 8
			_, par := drainAndRun(t, strategy, redacted, parCfg)

			if got, want := resultSignature(par), resultSignature(seq); got != want {
				t.Errorf("parallel results differ from sequential:\nseq:\n%s\npar:\n%s", want, got)
			}
		})
	}
}

// TestFanout_parallelRunsAreStable verifies two parallel runs (Workers=8) produce
// identical results, i.e. scheduling never leaks into the output.
func TestFanout_parallelRunsAreStable(t *testing.T) {
	inner, cfg, _, _ := buildScorerFixture(t)
	cfg.Charset = "abcdefghijklmnopqrstuvwxyz "
	cfg.MaxLength = 3
	cfg.Workers = 8
	redacted := inner.RedactedImage()

	_, a := drainAndRun(t, search.NewGuidedStrategy(), redacted, cfg)
	_, b := drainAndRun(t, search.NewGuidedStrategy(), redacted, cfg)
	if resultSignature(a) != resultSignature(b) {
		t.Error("two parallel runs produced different results")
	}
}
