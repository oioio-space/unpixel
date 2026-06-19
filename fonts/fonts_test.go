package fonts_test

import (
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/fonts"
)

// TestAll checks every bundled font carries data and well-formed metadata.
func TestAll(t *testing.T) {
	all := fonts.All()
	if len(all) < 5 {
		t.Fatalf("All() returned %d fonts, want the full bundle", len(all))
	}
	seen := map[string]bool{}
	for _, f := range all {
		if f.Name == "" || f.File == "" {
			t.Errorf("font with empty Name/File: %+v", f)
		}
		if len(f.Data) == 0 {
			t.Errorf("font %q has no data", f.Name)
		}
		switch f.Style {
		case "sans", "serif", "mono":
		default:
			t.Errorf("font %q has unexpected Style %q", f.Name, f.Style)
		}
		if seen[f.File] {
			t.Errorf("duplicate font file %q", f.File)
		}
		seen[f.File] = true
	}
}

// TestRenderers builds every bundled font and renders with it — proving each
// embedded TTF/OTF (including the .otf CFF outlines of Source Code Pro) parses.
func TestRenderers(t *testing.T) {
	rs, err := fonts.Renderers()
	if err != nil {
		t.Fatalf("Renderers: %v", err)
	}
	if len(rs) != len(fonts.All()) {
		t.Fatalf("Renderers() = %d, want %d", len(rs), len(fonts.All()))
	}
	style := unpixel.Style{FontSize: 24, PaddingTop: 8, PaddingLeft: 8}
	for i, r := range rs {
		img, sentinelX, err := r.Render("hi", style)
		if err != nil {
			t.Errorf("font %d (%s): Render: %v", i, fonts.All()[i].Name, err)
			continue
		}
		if img == nil || sentinelX <= 0 {
			t.Errorf("font %d (%s): bad render (img=%v, sentinelX=%d)", i, fonts.All()[i].Name, img != nil, sentinelX)
		}
	}
}

// TestLicensesEmbedded verifies attribution ships with the fonts.
func TestLicensesEmbedded(t *testing.T) {
	for _, name := range []string{"NOTICE.md", "licenses/OFL-1.1.txt", "licenses/Apache-2.0.txt"} {
		b, err := fonts.Licenses.ReadFile(name)
		if err != nil || len(b) == 0 {
			t.Errorf("missing/empty embedded license %q: %v", name, err)
		}
	}
}
