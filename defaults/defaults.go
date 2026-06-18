// Package defaults wires the standard internal components (XImage renderer,
// BlockAverage pixelator, Pixelmatch metric, GuidedDFS strategy) into an
// unpixel.Config. Import this package for its side-effect to enable
// Engine.Run with a zero-value Config.
//
// This package exists solely to break the import cycle between the root
// unpixel package and its internal implementations.
package defaults

import (
	"fmt"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

func init() {
	unpixel.DefaultComponents = Wire
}

// Wire fills nil component fields in cfg with the standard implementations.
// It returns an error if the renderer cannot be initialised (font parse failure).
func Wire(cfg *unpixel.Config) error {
	if cfg.Renderer == nil {
		r, err := render.NewXImage()
		if err != nil {
			return fmt.Errorf("init default renderer: %w", err)
		}
		cfg.Renderer = r
	}
	if cfg.Pixelator == nil {
		cfg.Pixelator = pixelate.NewBlockAverage(cfg.BlockSize)
	}
	if cfg.Metric == nil {
		// faithful: Jimp.diff uses threshold 0.02
		cfg.Metric = metric.NewPixelmatch(0.02)
	}
	if cfg.Strategy == nil {
		cfg.Strategy = search.NewGuidedStrategy()
	}
	return nil
}
