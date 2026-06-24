package fixture

// Layout describes how the visible and redacted regions are arranged in a
// context fixture image.
type Layout string

const (
	// LayoutSameLine places the visible cleartext and the redacted mosaic on the
	// same baseline, left-to-right (e.g. "User: " sharp, then the mosaic).
	LayoutSameLine Layout = "same-line"
	// LayoutLabelAbove places a visible label on the first line and the mosaic
	// region on the line below (e.g. "Password:" sharp, mosaic below it).
	LayoutLabelAbove Layout = "label-above"
)

// Rect is a pixel-space bounding rectangle.
// The JSON tags define the on-disk manifest schema.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// ContextSpec describes one context-corpus fixture: a PNG that contains both a
// VISIBLE cleartext region (sharp, for font calibration) and an ADJACENT REDACTED
// (mosaic) region of a secret string rendered with the same font and size.
//
// The JSON tags define the on-disk manifest schema for testdata/context/manifest.json.
type ContextSpec struct {
	// Name is the file stem; the PNG is Name + ".png".
	Name string `json:"name"`
	// Layout is the spatial arrangement of visible and redacted regions.
	Layout Layout `json:"layout"`
	// VisibleText is the sharp known cleartext (for calibration).
	VisibleText string `json:"visible_text"`
	// VisibleRect is the exact pixel rectangle of the visible cleartext in the PNG.
	VisibleRect Rect `json:"visible_rect"`
	// Secret is the ground truth of the redacted text.
	Secret string `json:"secret"`
	// RedactedRect is the exact pixel rectangle of the mosaic region in the PNG.
	RedactedRect Rect `json:"redacted_rect"`
	// Font is the human-readable name of the font used (e.g. "Liberation Sans").
	Font string `json:"font"`
	// FontSize is the font size in points at 72 DPI.
	FontSize float64 `json:"font_size"`
	// BlockSize is the mosaic block side length in pixels.
	BlockSize int `json:"block_size"`
	// Linear, when true, uses linear-light block averaging (GIMP/GEGL). When
	// false, averaging is in sRGB space (the faithful unredacter default).
	Linear bool `json:"linear"`
	// VarFont, when true, indicates the font is a variable-weight font (Nunito)
	// and VarWght holds the design-space wght axis value used.
	VarFont bool `json:"var_font,omitempty"`
	// VarWght is the wght axis value for variable-font fixtures.
	VarWght float32 `json:"var_wght,omitempty"`
	// FontSample, when non-nil, describes a SEPARATE companion image (a different
	// file) that provides the font calibration source. This is the C1b scenario:
	// the font weight is determined from an image OTHER than the redaction image.
	FontSample *FontSampleSpec `json:"font_sample,omitempty"`
	// Notes is a free-text annotation.
	Notes string `json:"notes,omitempty"`
}

// FontSampleSpec describes a stand-alone font-sample image used for C1b
// cross-image calibration. The PNG (Name + ".png") renders SampleText in the
// same variable font and weight as the parent ContextSpec's redaction, but as
// SHARP (un-pixelated) text — ideal for axis calibration.
type FontSampleSpec struct {
	// Name is the file stem of the sample PNG (e.g. "fontsample_wght700").
	Name string `json:"name"`
	// SampleText is the sharp cleartext rendered in the sample image.
	SampleText string `json:"sample_text"`
	// SampleRect is the pixel rectangle of the text within the sample PNG.
	// Populated by the generator; callers that load the whole image may leave
	// this zero and pass the full image to CalibrateFromVisible.
	SampleRect Rect `json:"sample_rect"`
}

// File returns the sample image's PNG filename.
func (f FontSampleSpec) File() string { return f.Name + ".png" }

// File returns the fixture's PNG filename.
func (s ContextSpec) File() string { return s.Name + ".png" }

// ContextMatrix returns the canonical context-corpus: fixtures that each
// contain a visible cleartext region adjacent to a pixelated redaction of a
// secret, rendered with the same font and size. The corpus spans three layout
// variants, three fonts (Liberation Sans, Liberation Mono, Nunito VF), and
// several realistic secrets.
//
// Coordinate fields (VisibleRect / RedactedRect) are populated by the generator
// (gencontext) and will be zero here — they are filled in from the computed
// image geometry at generation time.
func ContextMatrix() []ContextSpec {
	return []ContextSpec{
		// ── same-line layout (Liberation Sans) ────────────────────────────────
		{
			Name:        "ctx_sameline_user",
			Layout:      LayoutSameLine,
			VisibleText: "User: ",
			Secret:      "hunter2",
			Font:        "Liberation Sans",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			Notes:       "same-line; Liberation Sans; classic password test vector",
		},
		{
			Name:        "ctx_sameline_pin",
			Layout:      LayoutSameLine,
			VisibleText: "PIN: ",
			Secret:      "4892",
			Font:        "Liberation Sans",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			Notes:       "same-line; Liberation Sans; 4-digit PIN",
		},
		// ── same-line layout (Liberation Mono) ────────────────────────────────
		{
			Name:        "ctx_sameline_mono_token",
			Layout:      LayoutSameLine,
			VisibleText: "Token: ",
			Secret:      "a3f9b2",
			Font:        "Liberation Mono",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			Notes:       "same-line; Liberation Mono; hex token",
		},
		// ── label-above layout (Liberation Sans) ──────────────────────────────
		{
			Name:        "ctx_label_password",
			Layout:      LayoutLabelAbove,
			VisibleText: "Password:",
			Secret:      "Pa55w0rd!",
			Font:        "Liberation Sans",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			Notes:       "label-above; Liberation Sans; realistic password with symbols",
		},
		{
			Name:        "ctx_label_secret",
			Layout:      LayoutLabelAbove,
			VisibleText: "Secret key:",
			Secret:      "X7kQ9m",
			Font:        "Carlito",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			Notes:       "label-above; Carlito; alphanumeric secret key",
		},
		// ── variable font (Nunito, non-default weight) — C2 font reconstruction
		{
			Name:        "ctx_varfont_wght600",
			Layout:      LayoutSameLine,
			VisibleText: "Code: ",
			Secret:      "Tr0ub4dor",
			Font:        "Nunito",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			VarFont:     true,
			VarWght:     600,
			Notes:       "same-line; Nunito VF wght=600; font must be reconstructed/fitted (C2)",
		},
		{
			Name:        "ctx_varfont_wght750",
			Layout:      LayoutLabelAbove,
			VisibleText: "Access code:",
			Secret:      "G4te2024",
			Font:        "Nunito",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			VarFont:     true,
			VarWght:     750,
			Notes:       "label-above; Nunito VF wght=750; font must be reconstructed/fitted (C2)",
		},
		// ── same-line, larger block size ──────────────────────────────────────
		{
			Name:        "ctx_sameline_block10",
			Layout:      LayoutSameLine,
			VisibleText: "Key: ",
			Secret:      "r00t",
			Font:        "Liberation Sans",
			FontSize:    32,
			BlockSize:   10,
			Linear:      true,
			Notes:       "same-line; Liberation Sans; block_size=10 (non-power-of-2)",
		},
		// ── C1b: cross-image font sample (separate file, same font+weight) ────
		// The font calibration source lives in a DIFFERENT image (fontsample_wght700.png)
		// than the redaction target (ctx_crossimg_wght700.png). This proves that
		// WithVarFontVisible accepts a crop from any image, not only the redaction image.
		{
			Name:        "ctx_crossimg_wght700",
			Layout:      LayoutSameLine,
			VisibleText: "Label: ",
			Secret:      "Secret7",
			Font:        "Nunito",
			FontSize:    32,
			BlockSize:   8,
			Linear:      true,
			VarFont:     true,
			VarWght:     700,
			FontSample:  &FontSampleSpec{Name: "fontsample_wght700", SampleText: "Sample text"},
			Notes:       "C1b cross-image: font calibration from fontsample_wght700.png, redaction in ctx_crossimg_wght700.png",
		},
	}
}
