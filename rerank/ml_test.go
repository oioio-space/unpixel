//go:build ml

package rerank_test

import (
	"errors"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/rerank"
)

func TestCTCReranker_notBuiltYet(t *testing.T) {
	_, err := rerank.Default().Rerank(t.Context(), nil,
		[]unpixel.Verdict{{Text: "a", Distance: 0.1}}, func(string) float64 { return 0 }, 0.1)
	if !errors.Is(err, rerank.ErrCTCNotBuilt) {
		t.Errorf("ml Default().Rerank err = %v; want ErrCTCNotBuilt", err)
	}
}
