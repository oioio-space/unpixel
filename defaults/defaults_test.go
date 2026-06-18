package defaults_test

import (
	"context"
	"image"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
)

// TestWire_zeroConfig verifies that Wire populates all four component fields on
// a zero Config that has BlockSize set (required by NewBlockAverage).
func TestWire_zeroConfig(t *testing.T) {
	cfg := &unpixel.Config{BlockSize: 8}
	if err := defaults.Wire(cfg); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if cfg.Renderer == nil {
		t.Error("Wire: Renderer is nil, want non-nil")
	}
	if cfg.Pixelator == nil {
		t.Error("Wire: Pixelator is nil, want non-nil")
	}
	if cfg.Metric == nil {
		t.Error("Wire: Metric is nil, want non-nil")
	}
	if cfg.Strategy == nil {
		t.Error("Wire: Strategy is nil, want non-nil")
	}
}

// TestWire_preservesExistingComponents verifies that Wire does not overwrite
// component fields that are already set.
func TestWire_preservesExistingComponents(t *testing.T) {
	sentinel := &stubComponent{}
	cfg := &unpixel.Config{
		BlockSize: 8,
		Renderer:  sentinel,
		Pixelator: sentinel,
		Metric:    sentinel,
		Strategy:  sentinel,
	}
	if err := defaults.Wire(cfg); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if cfg.Renderer != sentinel {
		t.Error("Wire: overwrote Renderer, want original value")
	}
	if cfg.Pixelator != sentinel {
		t.Error("Wire: overwrote Pixelator, want original value")
	}
	if cfg.Metric != sentinel {
		t.Error("Wire: overwrote Metric, want original value")
	}
	if cfg.Strategy != sentinel {
		t.Error("Wire: overwrote Strategy, want original value")
	}
}

// TestInit_setsDefaultComponents verifies the init() side-effect: importing
// defaults wires unpixel.DefaultComponents so Engine.Run works without explicit
// component setup.
func TestInit_setsDefaultComponents(t *testing.T) {
	if unpixel.DefaultComponents == nil {
		t.Error("importing defaults should set unpixel.DefaultComponents; got nil")
	}
}

// TestStrategyConstructors verifies the exported strategy constructors return
// non-nil unpixel.Strategy values that can be assigned to Config.Strategy.
func TestStrategyConstructors(t *testing.T) {
	if defaults.GuidedStrategy() == nil {
		t.Error("GuidedStrategy() = nil, want non-nil")
	}
	for _, width := range []int{0, 1, 16} {
		if defaults.BeamStrategy(width) == nil {
			t.Errorf("BeamStrategy(%d) = nil, want non-nil", width)
		}
	}
}

// TestMetricConstructors verifies the exported metric constructors return
// non-nil unpixel.Metric values that can be assigned to Config.Metric.
func TestMetricConstructors(t *testing.T) {
	if defaults.PixelmatchMetric() == nil {
		t.Error("PixelmatchMetric() = nil, want non-nil")
	}
	for _, window := range []int{0, 4, 8} {
		if defaults.SSIMMetric(window) == nil {
			t.Errorf("SSIMMetric(%d) = nil, want non-nil", window)
		}
	}
}

// TestWire_preservesBeamStrategy verifies that Wire does not overwrite an
// explicitly chosen beam strategy with the default guided one.
func TestWire_preservesBeamStrategy(t *testing.T) {
	want := defaults.BeamStrategy(8)
	cfg := &unpixel.Config{BlockSize: 8, Strategy: want}
	if err := defaults.Wire(cfg); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if cfg.Strategy != want {
		t.Error("Wire overwrote an explicit BeamStrategy")
	}
}

// stubComponent satisfies all four component interfaces with no-op methods so
// we can pass a single value for all fields in TestWire_preservesExistingComponents.
type stubComponent struct{}

func (stubComponent) Render(_ string, _ unpixel.Style) (*image.RGBA, int, error) {
	return nil, 0, nil
}
func (stubComponent) Pixelate(img *image.RGBA, _, _ int) *image.RGBA { return img }
func (stubComponent) Compare(_, _ *image.RGBA) float64               { return 0 }
func (stubComponent) Search(_ context.Context, _ *image.RGBA, _ unpixel.Config, _ chan<- unpixel.Progress, _ chan<- unpixel.Result) {
}
