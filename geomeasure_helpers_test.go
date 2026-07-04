// Package unpixel_test — geometry measurement helpers.
//
// This file carries no build tag so its types and pure functions are always
// compiled and unit-tested in the normal test run. The observational harness
// that actually exercises images lives in geomeasure_test.go (tag: geomeasure).
package unpixel_test

import "testing"

// Stage-name constants used by stageVerdict and buildGeomMarkdown.
const (
	stageLocalize = "localize"
	stageGrid     = "grid"
	stageSegment  = "segment"
	stageFont     = "font"
	stageOK       = "ok"
	stageUnknown  = "unknown"
)

// geomResult holds the per-image geometry measurement for one run.
type geomResult struct {
	Corpus string `json:"corpus"`
	Image  string `json:"image"`

	// Localize stage.
	LocalizeOK bool `json:"localize_ok"`
	LocalizeW  int  `json:"localize_w"`
	LocalizeH  int  `json:"localize_h"`
	// ImageW is the full image width, set from img.Bounds().Dx() in measureImage.
	// It is used as a fallback when LocalizeW is zero.
	ImageW int `json:"image_w"`

	// Grid stage — InferBlockGrid.
	GridOK     bool    `json:"grid_ok"`
	GridSize   int     `json:"grid_size"`
	GridPhaseX int     `json:"grid_phase_x"`
	GridPhaseY int     `json:"grid_phase_y"`
	GridConf   float64 `json:"grid_conf"`

	// Grid stage — InferBlockSizeRobust.
	RobustSize    int     `json:"robust_size"`
	RobustSupport float64 `json:"robust_support"`

	// Ground-truth geometry (real corpus; zero for wild).
	GTBlock   int    `json:"gt_block"`
	GTOffsetX int    `json:"gt_offset_x"`
	GTOffsetY int    `json:"gt_offset_y"`
	GTLines   int    `json:"gt_lines"`
	GTFont    string `json:"gt_font"`

	// Errors vs GT (real corpus only; zero for wild).
	ErrSize   int `json:"err_size"`
	ErrPhaseX int `json:"err_phase_x"`
	ErrPhaseY int `json:"err_phase_y"`

	// Segment stage.
	SegLines   int  `json:"seg_lines"`
	LinesMatch bool `json:"lines_match"`

	// Font stage — top-3 names, sorted best-first.
	FontTop3     []string `json:"font_top3"`
	GTFontInTop3 bool     `json:"gt_font_in_top3"`

	// Wild plausibility for images with a known text length.
	KnownTextLen    int     `json:"known_text_len,omitzero"`
	ExpectedAdvance float64 `json:"expected_advance,omitzero"`
	// BlockPlausible is one of "ok", "sub-glyph", "sample-starved", or "".
	BlockPlausible string `json:"block_plausible,omitzero"`

	// FirstFailStage is the earliest failing geometry stage:
	// "localize" | "grid" | "segment" | "font" | "ok" | "unknown".
	FirstFailStage string `json:"first_fail_stage"`
}

// tickMark returns "✓" for true and "✗" for false.
func tickMark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

// stageVerdict returns the name of the first failing geometry stage for r.
//
// For the real corpus every stage is compared against ground truth. For the
// wild corpus only localize and grid can be evaluated — deeper stages return
// "unknown".
func stageVerdict(r geomResult) string {
	if !r.LocalizeOK {
		return stageLocalize
	}
	if !r.GridOK || r.GridConf < 0.5 {
		return stageGrid
	}
	if r.Corpus != "real" {
		// Wild: no ground truth for segment or font.
		return stageUnknown
	}
	if r.ErrSize > 2 {
		return stageGrid
	}
	if r.GTLines > 0 && !r.LinesMatch {
		return stageSegment
	}
	if r.GTFont != "" && !r.GTFontInTop3 {
		return stageFont
	}
	return stageOK
}

// ── unit tests ────────────────────────────────────────────────────────────────

// TestStageVerdict covers the stageVerdict decision table.
func TestStageVerdict(t *testing.T) {
	cases := []struct {
		name string
		r    geomResult
		want string
	}{
		{
			name: "no localize",
			r:    geomResult{LocalizeOK: false},
			want: "localize",
		},
		{
			name: "grid not ok",
			r:    geomResult{LocalizeOK: true, GridOK: false, GridConf: 0.9},
			want: "grid",
		},
		{
			name: "low grid confidence",
			r:    geomResult{LocalizeOK: true, GridOK: true, GridConf: 0.3},
			want: "grid",
		},
		{
			name: "wild no GT",
			r:    geomResult{Corpus: "wild", LocalizeOK: true, GridOK: true, GridConf: 0.9},
			want: "unknown",
		},
		{
			name: "real grid size error",
			r:    geomResult{Corpus: "real", LocalizeOK: true, GridOK: true, GridConf: 0.9, GTBlock: 32, ErrSize: 5},
			want: "grid",
		},
		{
			name: "real segment mismatch",
			r:    geomResult{Corpus: "real", LocalizeOK: true, GridOK: true, GridConf: 0.9, GTBlock: 32, ErrSize: 0, GTLines: 2, LinesMatch: false},
			want: "segment",
		},
		{
			name: "real font not in top3",
			r: geomResult{
				Corpus: "real", LocalizeOK: true, GridOK: true, GridConf: 0.9, GTBlock: 32,
				ErrSize: 0, GTLines: 1, LinesMatch: true,
				GTFont: "Noto Sans Mono", GTFontInTop3: false,
			},
			want: "font",
		},
		{
			name: "real all ok",
			r: geomResult{
				Corpus: "real", LocalizeOK: true, GridOK: true, GridConf: 0.9, GTBlock: 32,
				ErrSize: 0, GTLines: 1, LinesMatch: true,
				GTFont: "Noto Sans Mono", GTFontInTop3: true,
			},
			want: "ok",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stageVerdict(c.r)
			if got != c.want {
				t.Errorf("stageVerdict(%+v) = %q, want %q", c.r, got, c.want)
			}
		})
	}
}
