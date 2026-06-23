package fixture

// SickSpec extends Spec with paper-parity metadata for the Hill-2016 corpus.
// The JSON tags mirror the manifest schema written to testdata/sick/manifest.json.
type SickSpec struct {
	Spec
	// Font is the bundled font name used (e.g. "Liberation Sans").
	Font string `json:"font"`
	// Kind is "sick" for SICK-corpus sentences or "digits" for check-number strings.
	Kind string `json:"kind"`
	// Note is a free-text annotation (paper reference, font proxy note, …).
	Note string `json:"note,omitempty"`
}

// SickFile is the spec's reference image filename.
func (s SickSpec) SickFile() string { return s.Name + ".png" }

// csLowerSick is the full lowercase + space alphabet used by SICK sentence specs.
// The SICK corpus uses natural English prose, so the full lowercase charset is the
// faithful set that mirrors the paper's matched-font setup.
const csLowerSick = "abcdefghijklmnopqrstuvwxyz "

// csDigits is the digit-only charset used for the check-number (MICR proxy) specs.
const csDigits = "0123456789"

// SickMatrix returns the paper-parity corpus: 6 SICK-corpus sentences (Table 3
// of Hill et al. 2016, lowercased for charset tractability) and 4 check-number
// digit strings (§3.3 proxy using Liberation Mono instead of true MICR E-13B).
//
// Font/grid parameters are chosen to mirror the paper's "matched" setup:
// font_size 32 with block_size 8 (ratio 4:1), following Hill et al. §3.2.
// Fonts are varied across Liberation Sans (≈Arial), Liberation Mono (≈Courier New),
// and Carlito (≈Calibri) to exercise the font-diversity dimension.
func SickMatrix() []SickSpec {
	return []SickSpec{
		// ── SICK-corpus sentences (Table 3, Hill et al. 2016) ──────────────────
		// Lowercased + space-normalised to keep charset/search tractable.
		// Varied across the three bundled proportional/mono faces.
		{
			Spec: Spec{
				Name:        "sick_wrestling",
				Text:        "two dogs are wrestling and hugging",
				Charset:     csLowerSick,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Sans",
			Kind: "sick",
			Note: "SICK Table 3 row 1; Liberation Sans ≈ Arial (paper Fig 2)",
		},
		{
			Spec: Spec{
				Name:        "sick_boys_outdoors",
				Text:        "the young boys are playing outdoors",
				Charset:     csLowerSick,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Carlito",
			Kind: "sick",
			Note: "SICK Table 3 row 2; Carlito ≈ Calibri",
		},
		{
			Spec: Spec{
				Name:        "sick_water_safety",
				Text:        "nobody is practicing water safety",
				Charset:     csLowerSick,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Mono",
			Kind: "sick",
			Note: "SICK Table 3 row 3; Liberation Mono ≈ Courier New (monospaced variant)",
		},
		{
			Spec: Spec{
				Name:        "sick_man_playing",
				Text:        "a man is playing a guitar",
				Charset:     csLowerSick,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Sans",
			Kind: "sick",
			Note: "SICK Table 3 row 4; Liberation Sans ≈ Arial",
		},
		{
			Spec: Spec{
				Name:        "sick_children_playing",
				Text:        "two children are playing in the snow",
				Charset:     csLowerSick,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Carlito",
			Kind: "sick",
			Note: "SICK Table 3 row 5; Carlito ≈ Calibri",
		},
		{
			Spec: Spec{
				Name:        "sick_woman_singing",
				Text:        "a woman is singing a song",
				Charset:     csLowerSick,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Mono",
			Kind: "sick",
			Note: "SICK Table 3 row 6; Liberation Mono ≈ Courier New",
		},

		// ── Check-number digit strings (Hill et al. §3.3) ──────────────────────
		// True MICR E-13B is not bundled; Liberation Mono is used as a proxy.
		// Block size 8, font 32 (matched-param self-consistent setup).
		{
			Spec: Spec{
				Name:        "digits_7d_1234567",
				Text:        "1234567",
				Charset:     csDigits,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Mono",
			Kind: "digits",
			Note: "7-digit check proxy; true MICR E-13B not bundled; §3.3",
		},
		{
			Spec: Spec{
				Name:        "digits_8d_98765432",
				Text:        "98765432",
				Charset:     csDigits,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Mono",
			Kind: "digits",
			Note: "8-digit check proxy; §3.3",
		},
		{
			Spec: Spec{
				Name:        "digits_9d_012345678",
				Text:        "012345678",
				Charset:     csDigits,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Mono",
			Kind: "digits",
			Note: "9-digit check proxy; §3.3",
		},
		{
			Spec: Spec{
				Name:        "digits_10d_1029384756",
				Text:        "1029384756",
				Charset:     csDigits,
				FontSize:    32,
				BlockSize:   8,
				PaddingTop:  8,
				PaddingLeft: 8,
			},
			Font: "Liberation Mono",
			Kind: "digits",
			Note: "10-digit check proxy; §3.3",
		},
	}
}
