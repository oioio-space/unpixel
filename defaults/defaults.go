// Package defaults wires the standard internal components into an unpixel.Config.
//
// The four standard components are:
//   - XImage renderer — rasterises text via the golang.org/x/image font stack.
//   - BlockAverage pixelator — replaces each block with its mean RGBA colour.
//   - Pixelmatch metric — measures pixel-level distance with a 0.02 threshold.
//   - GuidedDFS strategy — guided depth-first search over the candidate alphabet.
//
// Import this package for its side-effect alone to make Engine.Run work with a
// zero-value Config:
//
//	import _ "github.com/oioio-space/unpixel/defaults"
//
// This package exists solely to break the import cycle between the root unpixel
// package and its internal implementations. Applications that supply all four
// component fields in Config explicitly do not need to import it.
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

// Wire fills any nil component fields in cfg with the standard implementations.
// It is called automatically by Engine.Run via the DefaultComponents hook when
// this package is imported for its side-effect. It may also be called directly
// to pre-initialise a Config before passing it to New.
// Wire returns an error if the XImage renderer cannot be initialised, which
// indicates a font-parsing failure in the embedded font data.
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
