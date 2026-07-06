package mcpserver_test

import (
	"image"
	"testing"

	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestVerifyVarFontFit_DecodeCrossImg is a real walled-image decode: the context
// redaction ctx_crossimg_wght700 is Nunito variable-font text pixelated at block 8.
// At the calibrate-from-visible nominal weight (700) the truth "Secret7" LOSES to a
// confusable decoy ("Sccret7"/"Secnet7") — the weight is slightly off and coarse
// blocks are unforgiving. Fitting the weight axis (and size) per candidate drives the
// truth to distance 0.0000 (a perfect physical match); the residual homoglyph tie is
// then broken by the English language prior (rerank), which ranks the real word first.
// That full chain decodes the redaction to "Secret7".
func TestVerifyVarFontFit_DecodeCrossImg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping variable-font calibration decode in -short mode")
	}

	img, err := loadImageFile("../testdata/context/ctx_crossimg_wght700.png")
	if err != nil {
		t.Fatalf("load image: %v", err)
	}

	const truth = "Secret7"
	candidates := []string{truth, "Sccret7", "Secnet7", "Secret1", "5ecret7", "Secretz"}

	report, err := mcpserver.VerifyVarFontFit(t.Context(), img, candidates, mcpserver.VarFontFitHints{
		Crop:      image.Rect(107, 0, 107+128, 57), // analyze's redaction band
		BlockSize: 8,
		Linear:    true,
		FontData:  vfembed.NunitoVFWght,
		Axis:      "wght",
		WghtMin:   640, WghtMax: 760, WghtStep: 15,
		SizeMin: 28, SizeMax: 36, SizeStep: 1,
		RerankWeight: 0.1,
	})
	if err != nil {
		t.Fatalf("VerifyVarFontFit: %v", err)
	}

	for _, r := range report.Ranked {
		t.Logf("candidate %-10q distance=%.4f match=%v", r.Text, r.Distance, r.Match)
	}

	// The decode: the language-prior-blended ranking puts the truth first.
	if report.Best != truth {
		t.Errorf("Best = %q, want %q — the calibration+rerank chain should decode the redaction",
			report.Best, truth)
	}
	// The truth must be a confident physical match (calibration reached its minimum).
	byText := make(map[string]mcpserver.RankedCandidate, len(report.Ranked))
	for _, r := range report.Ranked {
		byText[r.Text] = r
	}
	if tr := byText[truth]; !tr.Match {
		t.Errorf("truth %q: Match=false (distance %.4f), want a confident physical match", truth, tr.Distance)
	}
}
