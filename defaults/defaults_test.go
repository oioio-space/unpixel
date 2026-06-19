package defaults_test

import (
	"context"
	"image"
	"image/color"
	"image/draw"
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

// TestDeblur sharpens a blurred image and verifies the wrapper handles a
// non-*image.RGBA input (the draw.Draw conversion path) and the no-op guards.
func TestDeblur(t *testing.T) {
	// Build a high-contrast image as a non-RGBA type (image.Image interface),
	// then blur it so deblurring has something to recover.
	const w, h = 32, 16
	base := image.NewNRGBA(image.Rect(0, 0, w, h)) // NRGBA, not RGBA → exercises conversion
	for y := range h {
		for x := range w {
			v := uint8(0)
			if x >= w/2 {
				v = 255
			}
			base.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}
	blurred := defaults.GaussianBlur(3).Pixelate(toRGBA(base), 0, 0)

	got := defaults.Deblur(blurred, 3, 10)
	if got == nil {
		t.Fatal("Deblur returned nil")
	}
	if got.Bounds() != blurred.Bounds() {
		t.Errorf("bounds: got %v, want %v", got.Bounds(), blurred.Bounds())
	}
	// Edge contrast (the half/half boundary) must sharpen: the deblurred middle
	// column gap should exceed the blurred one.
	sharpen := edgeGap(got) - edgeGap(blurred)
	if sharpen <= 0 {
		t.Errorf("Deblur did not sharpen the edge: delta=%d", sharpen)
	}

	// Guards: iterations<=0 and sigma<=0 return an unmodified copy.
	if d := defaults.Deblur(blurred, 3, 0); d.Pix[0] != blurred.Pix[0] || &d.Pix[0] == &blurred.Pix[0] {
		t.Error("Deblur(iterations=0) should return an unmodified copy")
	}
}

// toRGBA copies any image into an *image.RGBA for the test setup.
func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// edgeGap returns the absolute red-channel difference across the vertical mid-line
// on the middle row — a simple sharpness proxy for the half-black/half-white test.
func edgeGap(img *image.RGBA) int {
	y := img.Bounds().Dy() / 2
	left := int(img.RGBAAt(img.Bounds().Dx()/2-2, y).R)
	right := int(img.RGBAAt(img.Bounds().Dx()/2+2, y).R)
	d := right - left
	if d < 0 {
		d = -d
	}
	return d
}
