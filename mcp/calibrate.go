package mcpserver

// calibrate.go — unpixel_calibrate tool: fits variable-font axes to a crop of
// known visible (un-pixelated) text adjacent to a redaction, using
// varfont.CalibrateFromVisible. The fitted axis values can then be used as
// warm-start values for a subsequent unpixel_decode call with method=varfont.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"slices"
	"strconv"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
)

// bundledFont describes an embedded variable font available to unpixel_calibrate.
type bundledFont struct {
	name string   // identifier callers pass in the "font" field
	data []byte   // embedded font bytes
	axes []string // four-char OpenType tags supported by this font
}

// calibrateFonts is the catalogue of embedded variable fonts.
// Axis ranges are sourced from the font spec; tags not listed here will be
// rejected with a clear error rather than silently ignored.
var calibrateFonts = []bundledFont{
	{
		name: "nunito",
		data: vfembed.NunitoVFWght,
		axes: []string{"wght"},
	},
	{
		name: "robotoflex",
		data: vfembed.RobotoFlexVF,
		// Primary user-facing axes (parametric axes omitted for clarity).
		axes: []string{"wght", "wdth", "opsz", "slnt"},
	},
}

// axisDefaults maps axis tag → {min, max, start} for the two bundled fonts.
// Values are sourced from the font specs (RobotoFlex v3, Nunito v3).
var axisDefaults = map[string][3]float32{
	"wght": {100, 1000, 400},
	"wdth": {25, 151, 100},
	"opsz": {8, 144, 14},
	"slnt": {-10, 0, 0},
}

// toolCalibrate is the tool descriptor for unpixel_calibrate.
var toolCalibrate = &mcpsdk.Tool{
	Name: "unpixel_calibrate",
	Description: "Fits variable-font design axes (e.g. weight, optical size, slant) to a crop of " +
		"known visible text adjacent to a redaction, using varfont.CalibrateFromVisible. " +
		"Use this before unpixel_decode with method=varfont to supply precise axis warm-starts " +
		"when the redacted font is a variable font (e.g. Nunito for wght only; RobotoFlex for " +
		"wght+opsz+slnt+wdth). " +
		"Requires a sharp (un-pixelated) image crop of the known text and the cleartext string. " +
		"NOT needed for fixed-weight bundled fonts — use unpixel_rank_fonts instead. " +
		"Latency: fast (< 2 s per axis with coordinate-descent).",
}

// calibrateInput is the JSON input schema for unpixel_calibrate.
type calibrateInput struct {
	// ImagePath is the filesystem path of the sharp (un-pixelated) image crop.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the sharp un-pixelated image crop containing visible_text"`
	// VisibleText is the cleartext string that appears in the image crop.
	VisibleText string `json:"visible_text" jsonschema:"The cleartext string visible in the image crop (case-sensitive, exact match)"`
	// Region optionally crops the image before calibrating: 'x,y,w,h' in pixels.
	Region string `json:"region,omitzero" jsonschema:"Optional sub-region of the image: 'x,y,w,h' in pixels (omit to use the full image)"`
	// Font selects the embedded variable font to calibrate against.
	// Supported values: 'nunito' (wght only) or 'robotoflex' (wght, wdth, opsz, slnt).
	// Default: 'nunito'.
	Font string `json:"font,omitzero" jsonschema:"Embedded variable font: nunito (default, wght only) or robotoflex (wght/wdth/opsz/slnt)"`
	// Axes lists the OpenType axis tags to fit (e.g. ['wght','opsz']).
	// Omit to use the font's primary axis (wght for both bundled fonts).
	// Tags not present in the chosen font produce a clear error.
	Axes []string `json:"axes,omitzero" jsonschema:"OpenType axis tags to fit, e.g. ['wght','opsz','slnt'] — must be supported by the chosen font"`
}

// AxisResult is one fitted design-axis in CalibrateReport.
type AxisResult struct {
	// Tag is the four-character OpenType axis tag (e.g. "wght").
	Tag string `json:"tag"`
	// Value is the fitted design-space value.
	Value float32 `json:"value"`
}

// CalibrateReport is the output of unpixel_calibrate.
type CalibrateReport struct {
	// FittedAxes lists every axis that was optimised, with its fitted value.
	FittedAxes []AxisResult `json:"fitted_axes"`
	// Distance is the image-metric value at the best-fit axis configuration (lower is better).
	Distance float64 `json:"distance"`
	// Evals is the number of render+metric evaluations performed.
	Evals int `json:"evals"`
	// Notes carries supplementary information.
	Notes []string `json:"notes,omitzero"`
}

// CalibrateOptions carries optional parameters for [Calibrate].
type CalibrateOptions struct {
	// Region optionally crops the image before calibrating: "x,y,w,h" in pixels.
	Region string
	// Font selects the embedded variable font: "nunito" (default) or "robotoflex".
	Font string
	// Axes lists the OpenType tags to fit (e.g. ["wght", "opsz"]).
	// Empty defaults to ["wght"].
	Axes []string
}

// Calibrate fits variable-font axes to a sharp (un-pixelated) image crop of
// visibleText. The returned [CalibrateReport] carries fitted axis values that
// can be used as warm-start inputs to unpixel_decode with method=varfont.
//
// opts.Font selects the embedded variable font ("nunito" or "robotoflex").
// opts.Axes lists which axes to fit; defaults to ["wght"]. Axes not supported
// by the chosen font produce an error listing which tags are valid.
//
// Calibrate is the testable core of the unpixel_calibrate MCP tool.
func Calibrate(img image.Image, visibleText string, opts CalibrateOptions) (CalibrateReport, error) {
	if visibleText == "" {
		return CalibrateReport{}, fmt.Errorf("unpixel_calibrate: visible_text must not be empty")
	}

	// Resolve font.
	fontName := strings.ToLower(strings.TrimSpace(opts.Font))
	if fontName == "" {
		fontName = "nunito"
	}
	var bf *bundledFont
	for i := range calibrateFonts {
		if calibrateFonts[i].name == fontName {
			bf = &calibrateFonts[i]
			break
		}
	}
	if bf == nil {
		names := make([]string, len(calibrateFonts))
		for i, f := range calibrateFonts {
			names[i] = f.name
		}
		return CalibrateReport{}, fmt.Errorf("unpixel_calibrate: unknown font %q; supported: %s",
			fontName, strings.Join(names, ", "))
	}

	// Resolve axes.
	axisTags := opts.Axes
	if len(axisTags) == 0 {
		axisTags = []string{"wght"}
	}
	for _, tag := range axisTags {
		if !slices.Contains(bf.axes, tag) {
			return CalibrateReport{}, fmt.Errorf(
				"unpixel_calibrate: axis %q not supported by font %q; available axes: %s",
				tag, fontName, strings.Join(bf.axes, ", "))
		}
	}

	rgba := imutil.ToRGBA(img)
	if opts.Region != "" {
		crop, err := parseRegion(rgba, opts.Region)
		if err != nil {
			return CalibrateReport{}, fmt.Errorf("unpixel_calibrate: parse region: %w", err)
		}
		rgba = crop
	}

	vf, err := varfont.ParseFont(bytes.NewReader(bf.data))
	if err != nil {
		return CalibrateReport{}, fmt.Errorf("unpixel_calibrate: parse variable font %q: %w", fontName, err)
	}

	axisSpecs := make([]varfont.AxisSpec, len(axisTags))
	for i, tag := range axisTags {
		rng, ok := axisDefaults[tag]
		if !ok {
			// Fallback: use the full design-space range with mid-point start.
			rng = [3]float32{0, 1000, 500}
		}
		axisSpecs[i] = varfont.AxisSpec{Tag: tag, Min: rng[0], Max: rng[1], Start: rng[2]}
	}

	cfg := varfont.CalibrateConfig{
		Font:   vf,
		Text:   visibleText,
		Style:  unpixel.Style{FontSize: 28},
		Target: rgba,
		Metric: metric.NewPixelmatch(0.1),
		Axes:   axisSpecs,
	}
	res, err := varfont.CalibrateFromVisible(cfg)
	if err != nil {
		return CalibrateReport{}, fmt.Errorf("unpixel_calibrate: %w", err)
	}
	fitted := make([]AxisResult, len(res.Axes))
	for i, a := range res.Axes {
		fitted[i] = AxisResult{Tag: a.Tag, Value: a.Value}
	}
	return CalibrateReport{
		FittedAxes: fitted,
		Distance:   res.Distance,
		Evals:      res.Evals,
		Notes: []string{
			fmt.Sprintf("calibrated on embedded %s font", fontName),
			"pass fitted axis values as warm-starts to unpixel_decode method=varfont",
		},
	}, nil
}

// handleCalibrate is the tool handler for unpixel_calibrate.
func handleCalibrate(_ context.Context, _ *mcpsdk.CallToolRequest, in calibrateInput) (*mcpsdk.CallToolResult, CalibrateReport, error) {
	img, err := loadImage(in.ImagePath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_calibrate: load image: %w", err)), CalibrateReport{}, nil
	}
	report, err := Calibrate(img, in.VisibleText, CalibrateOptions{
		Region: in.Region,
		Font:   in.Font,
		Axes:   in.Axes,
	})
	if err != nil {
		return errResult(err), CalibrateReport{}, nil
	}
	return toolJSON(report)
}

// parseRegion interprets a "x,y,w,h" region string and returns the
// corresponding sub-image of src. It returns an error for malformed input or
// out-of-bounds rectangles.
func parseRegion(src *image.RGBA, region string) (*image.RGBA, error) {
	parts := strings.Split(region, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("region must be 'x,y,w,h', got %q", region)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("region component %d: %w", i, err)
		}
		vals[i] = v
	}
	x, y, w, h := vals[0], vals[1], vals[2], vals[3]
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("region width and height must be positive, got %dx%d", w, h)
	}
	b := src.Bounds()
	r := image.Rect(x, y, x+w, y+h)
	if !r.In(b) {
		return nil, fmt.Errorf("region %v is outside image bounds %v", r, b)
	}
	sub, ok := src.SubImage(r).(*image.RGBA)
	if !ok {
		// SubImage on *image.RGBA always returns *image.RGBA, but guard anyway.
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		imutil.FillWhite(dst)
		for dy := range h {
			for dx := range w {
				dst.SetRGBA(dx, dy, src.RGBAAt(x+dx, y+dy))
			}
		}
		return dst, nil
	}
	return sub, nil
}
