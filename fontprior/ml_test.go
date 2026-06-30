//go:build ml

package fontprior_test

import (
	"errors"
	"image"
	"testing"

	"github.com/oioio-space/unpixel/fontprior"
	"github.com/oioio-space/unpixel/fonts"
)

func TestMLPrior_notBuiltYet(t *testing.T) {
	_, err := fontprior.Default().Rank(t.Context(), image.NewRGBA(image.Rect(0, 0, 8, 8)), 6, fonts.All())
	if !errors.Is(err, fontprior.ErrMLNotBuilt) {
		t.Errorf("ml Default().Rank err = %v; want ErrMLNotBuilt", err)
	}
}
