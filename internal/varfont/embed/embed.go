// Package embed exposes the bundled variable fonts for use by the varfont
// calibrate path. Font files live here (not in testdata/) so that non-test
// packages can embed them via //go:embed.
//
// All fonts are released under the SIL Open Font License 1.1; license texts
// for each font ship alongside them (OFL.txt, RobotoFlex-OFL.txt).
//
// These fonts are available ONLY to the varfont/calibrate path (opt-in).
// They are NOT included in the default font sweep used by
// [github.com/oioio-space/unpixel/fonts.All] — embedding them here keeps
// them isolated so the panel/matrix results remain unchanged.
package embed

import _ "embed"

// NunitoVFWght is the Nunito variable-weight font (wght axis 200–900).
//
//go:embed NunitoVF-wght.ttf
var NunitoVFWght []byte

// RobotoFlexVF is the Roboto Flex variable font.
//
// Design axes:
//   - opsz: 8–144 (optical size)
//   - wght: 100–1000 (weight)
//   - wdth: 25–151 (width)
//   - slnt: −10–0 (slant; 0 = upright, −10 = max slant)
//   - GRAD, XOPQ, XTRA, YOPQ, YTAS, YTDE, YTFI, YTLC, YTUC (parametric)
//
// Roboto Flex is copyright 2017 The Roboto Flex Project Authors
// (https://github.com/TypeNetwork/Roboto-Flex) and is licensed under the
// SIL Open Font License 1.1 (RobotoFlex-OFL.txt in this directory).
//
//go:embed RobotoFlex.ttf
var RobotoFlexVF []byte
