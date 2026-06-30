//go:build infoleak

package infoleak

import (
	"testing"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/render"
)

// confusablePairs are near-equal-width OCR-confusable pairs where sub-pixel AA
// might add discriminability after block-averaging.
var confusablePairs = [][2]string{
	{"rn", "m"}, {"cl", "d"}, {"vv", "w"}, {"nn", "m"}, {"0", "O"}, {"8", "B"}, {"I", "l"},
}

const (
	studyBlock    = 6
	studyFontSize = 28
)

// TestInfoLeakStudy runs the full information-leak measurement over the bundled
// fonts and prints a report. Run it with: mise run infoleak
// (or: scripts/gotest-caged.sh go test -tags infoleak -run InfoLeak ./internal/infoleak/).
func TestInfoLeakStudy(t *testing.T) {
	for _, f := range fonts.All() {
		r, err := render.NewXImageFromFonts(f.Data, nil)
		if err != nil {
			t.Fatalf("renderer %s: %v", f.Name, err)
		}

		// --- AA leak ---
		aa, err := MeasureAALeak(r, f.Name, confusablePairs, studyBlock, studyFontSize)
		if err != nil {
			t.Fatalf("MeasureAALeak %s: %v", f.Name, err)
		}
		t.Logf("[AA] %-18s meanAASep=%.4f meanHardSep=%.4f meanGain=%+.4f",
			f.Name, aa.MeanAASep, aa.MeanHardSep, aa.MeanGain)
		for _, p := range aa.Pairs {
			t.Logf("[AA]   %-5s vs %-5s  AA=%.4f hard=%.4f gain=%+.4f", p.A, p.B, p.AASep, p.HardSep, p.Gain)
			// Invariant: identical-input separability would be 0; a real pair differs.
			if p.AASep < 0 || p.HardSep < 0 {
				t.Errorf("negative separability for %q/%q", p.A, p.B)
			}
		}

		// --- JPEG impact (one representative font is enough; do Liberation Sans only) ---
		if f.Name == fonts.All()[0].Name {
			jp, err := MeasureJPEGImpact(r, "the", "tho", studyBlock, studyFontSize, []int{95, 75, 50, 30, 10})
			if err != nil {
				t.Fatalf("MeasureJPEGImpact: %v", err)
			}
			t.Logf("[JPEG] text=%q wrong=%q", jp.Text, jp.Wrong)
			prevDrift := -1.0
			for _, pt := range jp.Points {
				t.Logf("[JPEG]   q=%-3d drift=%.4f trueStillWins=%v", pt.Quality, pt.Drift, pt.TrueStillWins)
				if pt.Drift+1e-9 < prevDrift {
					t.Errorf("drift decreased as quality dropped: q=%d drift=%.4f < prev %.4f", pt.Quality, pt.Drift, prevDrift)
				}
				prevDrift = pt.Drift
			}
		}
	}

	// --- multi-offset (idea 1) is already shipped via internal/multiframe (IBP);
	// it is the only real super-resolution lever. Documented, not re-measured here.
	t.Log("[multi-offset] already shipped: internal/multiframe IBP fusion (see DecodeMultiFrame)")
}
