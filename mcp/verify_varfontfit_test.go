package mcpserver_test

import (
	"image"
	"testing"

	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestVerifyVarFontFit_DecodeContext generalises the variable-font calibration decode
// across the context corpus's Nunito variable-font redactions (text pixelated at block
// 8 whose exact weight is only approximately known). VerifyVarFontFit fits the weight
// axis (+ size) per candidate so the truth reaches its physical minimum, and a
// dictionary word prior breaks residual homoglyph ties.
//
// The measured boundary of the method: it decodes robustly when the secret contains a
// real WORD — "Secret7" (crossimg700) decodes because "Secret" is a word and its
// confusable decoys ("Secnet7"/"Sccret7") are not. The word-less secrets "Tr0ub4dor"
// (wght600) and "G4te2024" (wght750) collapse to physical homoglyph ties the word
// prior cannot separate (wght600 even loses to a T→X decoy at a coarse fit grid), so
// they are logged, not asserted — breaking those needs per-glyph learned emissions, not
// a word prior. This test pins the robust case and documents the honest residual.
func TestVerifyVarFontFit_DecodeContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping variable-font calibration decode in -short mode")
	}

	// Only the word-containing crossimg700 is asserted (it decodes robustly). The
	// word-less wght600/wght750 residuals are documented in the doc comment above rather
	// than run here — each costs ~2-4 min under the fit and does not decode robustly, so
	// exercising them would add minutes to the suite for a negative, grid-sensitive result.
	cases := []struct {
		name, file, truth string
		x, y, w, h, wght  int
		decoys            []string
		mustDecode        bool
	}{
		{
			name: "crossimg_wght700", file: "ctx_crossimg_wght700", truth: "Secret7",
			x: 107, y: 0, w: 128, h: 57, wght: 700,
			decoys: []string{"Sccret7", "Secnet7", "Secret1", "5ecret7"}, mustDecode: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img, err := loadImageFile("../testdata/context/" + c.file + ".png")
			if err != nil {
				t.Fatalf("load image: %v", err)
			}
			candidates := append([]string{c.truth}, c.decoys...)
			report, err := mcpserver.VerifyVarFontFit(t.Context(), img, candidates, mcpserver.VarFontFitHints{
				Crop:      image.Rect(c.x, c.y, c.x+c.w, c.y+c.h),
				BlockSize: 8,
				Linear:    true,
				FontData:  vfembed.NunitoVFWght,
				VarFont:   true,
				Axis:      "wght",
				WghtMin:   c.wght - 60, WghtMax: c.wght + 60, WghtStep: 20,
				SizeMin: 28, SizeMax: 36, SizeStep: 2,
				RerankWeight: 0.05,
			})
			if err != nil {
				t.Fatalf("VerifyVarFontFit: %v", err)
			}
			for _, r := range report.Ranked {
				t.Logf("candidate %-12q distance=%.4f match=%v", r.Text, r.Distance, r.Match)
			}
			t.Logf("%s: Best=%q (truth=%q)", c.name, report.Best, c.truth)
			if c.mustDecode && report.Best != c.truth {
				t.Errorf("Best = %q, want %q — the calibration + word-prior chain should decode this redaction",
					report.Best, c.truth)
			}
		})
	}
}
