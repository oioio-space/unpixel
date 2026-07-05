package unpixel_test

import (
	"os"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
)

// TestVerify_SmallBlockPhaseAlignment guards the sub-block phase-alignment fix in
// alignedDist. At block=8 the phase step used to equal the block, so alignedDist
// only ever tried phase 0 and could not align a candidate whose ink fell on a
// sub-block phase — the block=8 sick digit fixtures digits_8d/digits_9d scored ~0.5
// (no match) while digits_7d/digits_10d (aligned at phase 0) scored 0.0. With the
// block/4 phase step every block gets genuine sub-block phase coverage, so all four
// recover. This pins that behaviour so the step cannot silently regress to the block.
func TestVerify_SmallBlockPhaseAlignment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping small-block phase-alignment recovery in -short mode")
	}

	var libMono []byte
	for _, f := range fonts.All() {
		if f.Name == "Liberation Mono" {
			libMono = f.Data
		}
	}
	r, err := defaults.RendererFromFonts(libMono, nil)
	if err != nil {
		t.Fatalf("build Liberation Mono renderer: %v", err)
	}

	// The two fixtures whose ink falls on a sub-block phase (the regression targets).
	cases := []struct{ file, truth string }{
		{"testdata/sick/digits_8d_98765432.png", "98765432"},
		{"testdata/sick/digits_9d_012345678.png", "012345678"},
	}
	for _, c := range cases {
		t.Run(c.truth, func(t *testing.T) {
			f, err := os.Open(c.file)
			if err != nil {
				t.Fatalf("open %s: %v", c.file, err)
			}
			defer func() { _ = f.Close() }()
			src, err := decodePNG(f)
			if err != nil {
				t.Fatalf("decode %s: %v", c.file, err)
			}

			vs, err := unpixel.Verify(t.Context(), src, []string{c.truth},
				unpixel.WithRenderer(r),
				unpixel.WithBlockSize(8),
				unpixel.WithStyle(unpixel.Style{FontSize: 32}))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			got := vs[0]
			t.Logf("truth %q: distance=%.4f match=%v", c.truth, got.Distance, got.Match)
			if !got.Match {
				t.Errorf("truth %q: Match=false (distance %.4f) — sub-block phase alignment regressed",
					c.truth, got.Distance)
			}
		})
	}
}
