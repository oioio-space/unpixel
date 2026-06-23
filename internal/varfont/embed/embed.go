// Package embed exposes the bundled Nunito variable font for use by
// production code. The font file lives here (not in testdata/) so that
// non-test packages can embed it via //go:embed.
//
// Nunito is released under the SIL Open Font License 1.1 (OFL.txt in this
// directory). The font is used solely as a default convenience for the
// varfont decoder; callers may supply any variable TrueType font instead.
package embed

import _ "embed"

// NunitoVFWght is the Nunito variable-weight font (wght axis 200–900).
//
//go:embed NunitoVF-wght.ttf
var NunitoVFWght []byte
