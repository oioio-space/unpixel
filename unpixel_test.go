package unpixel_test

import (
	"errors"
	"testing"

	"github.com/oioio-space/unpixel"
)

func TestNew_nilImageReturnsError(t *testing.T) {
	_, err := unpixel.New(nil, unpixel.Config{})
	if !errors.Is(err, unpixel.ErrNilImage) {
		t.Errorf("New(nil, ...) error = %v, want ErrNilImage", err)
	}
}

func TestConfig_defaultConstants(t *testing.T) {
	if unpixel.DefaultCharset != "abcdefghijklmnopqrstuvwxyz " {
		t.Errorf("DefaultCharset = %q, want a–z + space", unpixel.DefaultCharset)
	}
	if unpixel.DefaultMaxLength != 20 {
		t.Errorf("DefaultMaxLength = %d, want 20", unpixel.DefaultMaxLength)
	}
	if unpixel.DefaultBlockSize != 8 {
		t.Errorf("DefaultBlockSize = %d, want 8", unpixel.DefaultBlockSize)
	}
	if unpixel.DefaultThreshold != 0.25 {
		t.Errorf("DefaultThreshold = %v, want 0.25", unpixel.DefaultThreshold)
	}
	if unpixel.DefaultSpaceThreshold != 0.5 {
		t.Errorf("DefaultSpaceThreshold = %v, want 0.5", unpixel.DefaultSpaceThreshold)
	}
}

func TestWithFontPriorOptions(t *testing.T) {
	var c unpixel.Config
	unpixel.WithFontPrior()(&c)
	if !c.FontPrior {
		t.Errorf("WithFontPrior did not set FontPrior")
	}
	unpixel.WithFontPriorTopK(3)(&c)
	if c.FontPriorTopK != 3 {
		t.Errorf("FontPriorTopK = %d; want 3", c.FontPriorTopK)
	}

	// WithFontPriorTopK alone must imply the font prior (per its doc).
	var d unpixel.Config
	unpixel.WithFontPriorTopK(2)(&d)
	if !d.FontPrior {
		t.Errorf("WithFontPriorTopK did not imply FontPrior")
	}
}

func TestEventKind_consts(t *testing.T) {
	// Verify iota ordering so Progress consumers can rely on numeric values.
	if unpixel.EventCandidate != 0 {
		t.Errorf("EventCandidate = %d, want 0", unpixel.EventCandidate)
	}
	if unpixel.EventOffsetProbed != 1 {
		t.Errorf("EventOffsetProbed = %d, want 1", unpixel.EventOffsetProbed)
	}
	if unpixel.EventNewBest != 2 {
		t.Errorf("EventNewBest = %d, want 2", unpixel.EventNewBest)
	}
	if unpixel.EventDone != 3 {
		t.Errorf("EventDone = %d, want 3", unpixel.EventDone)
	}
}

func TestWithRerankWeight(t *testing.T) {
	var c unpixel.Config
	unpixel.WithRerankWeight(0.1)(&c)
	if c.RerankWeight != 0.1 {
		t.Errorf("RerankWeight = %v; want 0.1", c.RerankWeight)
	}
}
