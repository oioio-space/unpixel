// Package fonts bundles a small set of redistributable typefaces so UnPixel can
// recover a redaction zero-config — without the caller supplying any font file —
// by sweeping these candidates and keeping the best fit (see
// [github.com/oioio-space/unpixel.RecoverMultiFont]).
//
// The set spans the most common redaction faces; each is metric-compatible with
// a proprietary original, or a popular code font:
//
//   - Liberation Sans (≈ Arial), Liberation Serif (≈ Times New Roman),
//     Liberation Mono (≈ Courier New) — SIL OFL 1.1
//   - Carlito (≈ Calibri) — SIL OFL 1.1
//   - Caladea (≈ Cambria) — Apache 2.0
//   - Source Code Pro, JetBrains Mono, Adwaita Mono, Noto Sans Mono — code monospaces — SIL OFL 1.1 / Apache 2.0
//
// Usage:
//
//	rs, err := fonts.Renderers()
//	ranked, err := unpixel.RecoverMultiFont(ctx, img, rs, unpixel.WithBlockSize(5))
//
// Importing this package embeds ~2 MB of font data into the binary, so packages
// that don't need the bundle should not import it. Attribution and license texts
// ship in [Licenses] (NOTICE.md + licenses/); the fonts are unmodified.
package fonts

import (
	"embed"
	"fmt"
	"slices"
	"sync"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
)

//go:embed embed/*.ttf embed/*.otf
var files embed.FS

// Licenses holds the bundle's attribution and license texts (NOTICE.md and the
// licenses/ directory), so downstream tools can surface them.
//
//go:embed NOTICE.md licenses/*.txt
var Licenses embed.FS

// Font is one bundled typeface and its raw TrueType/OpenType data.
type Font struct {
	// Name is the human-readable font name, e.g. "Liberation Mono".
	Name string
	// Style is the broad category: "sans", "serif", or "mono".
	Style string
	// Approx is the proprietary/original face this one stands in for, e.g.
	// "Courier New"; empty for code fonts with no single equivalent.
	Approx string
	// File is the embedded file name under embed/.
	File string
	// Data is the font's TTF/OTF bytes. It is read-only; do not mutate it.
	Data []byte
}

// catalog lists the bundled fonts in sweep order (the embedded files are
// guaranteed present at build time by the //go:embed directive above).
var catalog = []Font{
	{Name: "Liberation Sans", Style: "sans", Approx: "Arial", File: "LiberationSans-Regular.ttf"},
	{Name: "Liberation Serif", Style: "serif", Approx: "Times New Roman", File: "LiberationSerif-Regular.ttf"},
	{Name: "Liberation Mono", Style: "mono", Approx: "Courier New", File: "LiberationMono-Regular.ttf"},
	{Name: "Carlito", Style: "sans", Approx: "Calibri", File: "Carlito-Regular.ttf"},
	{Name: "Caladea", Style: "serif", Approx: "Cambria", File: "Caladea-Regular.ttf"},
	{Name: "Source Code Pro", Style: "mono", Approx: "", File: "SourceCodePro-Regular.otf"},
	{Name: "JetBrains Mono", Style: "mono", Approx: "", File: "JetBrainsMono-Regular.ttf"},
	{Name: "Adwaita Mono", Style: "mono", Approx: "", File: "AdwaitaMono-Regular.ttf"},
	{Name: "Noto Sans Mono", Style: "mono", Approx: "", File: "NotoSansMono-Regular.ttf"},
}

// cachedCatalog reads every embedded font file once, on first use. embed.FS.ReadFile
// copies the ~2 MB bundle on each call, so caching avoids re-copying it on every
// All() (e.g. a per-request font lookup). The Data slices are read-only.
var cachedCatalog = sync.OnceValue(func() []Font {
	out := make([]Font, len(catalog))
	for i, f := range catalog {
		data, err := files.ReadFile("embed/" + f.File)
		if err != nil {
			// Unreachable: go:embed fails the build if a file is missing.
			panic("unpixel/fonts: embedded font not found: " + f.File)
		}
		f.Data = data
		out[i] = f
	}
	return out
})

// All returns the bundled fonts, in sweep order, each with its Data populated. The
// returned slice is a fresh copy the caller may reorder freely; the Data byte slices
// are shared across calls and must not be mutated (as documented on [Font.Data]).
func All() []Font {
	return slices.Clone(cachedCatalog())
}

// Renderers builds one renderer per bundled font, in [All] order, ready to pass
// to [github.com/oioio-space/unpixel.RecoverMultiFont]. It returns an error only
// if a bundled font fails to parse (which would indicate a corrupt build).
func Renderers() ([]unpixel.Renderer, error) {
	all := All()
	rs := make([]unpixel.Renderer, len(all))
	for i, f := range all {
		r, err := defaults.RendererFromFonts(f.Data, nil)
		if err != nil {
			return nil, fmt.Errorf("unpixel/fonts: build renderer for %s: %w", f.Name, err)
		}
		rs[i] = r
	}
	return rs, nil
}
