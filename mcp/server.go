// Package mcpserver wires UnPixel's decode and analysis capabilities as a
// Model Context Protocol server. The following tools are exposed:
//
//   - unpixel_analyze — non-destructive analysis of a pixelated image: block grid
//     detection, colorspace, blur estimation, font size, redaction bounding box, and
//     a heuristic recommended decoder.
//   - unpixel_verify_candidates — scores a list of known candidate strings against
//     an observed mosaic and returns them ranked by image distance (lowest = best).
//   - unpixel_decode — recovers hidden text from a pixelated or blurred redaction
//     using one of thirteen decoder methods (auto, mosaic, blurred, mono-hmm,
//     window-hmm, trained-hmm, did, varfont, perspective, reference, blind,
//     ensemble, multi-frame).
//   - unpixel_render — rasterises a candidate string as a PNG image returned as
//     MCP image content for visual inspection by a multimodal LLM.
//   - unpixel_rank_fonts — ranks bundled fonts by glyph-metric match against a
//     redaction; use as a cheap pre-filter before a full decode.
//   - unpixel_calibrate — fits variable-font design axes to a crop of known
//     visible text; use warm-start values with unpixel_decode method=varfont.
//
// Four read-only resources are also registered:
//
//   - unpixel://fonts            — JSON catalogue of all bundled fonts.
//   - unpixel://charsets         — charset presets (lower/alnum/ascii).
//   - unpixel://methods          — decode method catalogue with "use when" notes.
//   - unpixel://operating-envelope — honest note on real-world recovery rates.
//
// Build a server with [NewServer], then run it on a transport:
//
//	srv := mcpserver.NewServer("v0.1.0")
//	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
//	    log.Fatal(err)
//	}
package mcpserver

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoding
	_ "image/png"  // register PNG decoding
	"math"
	"os"
	"slices"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire standard components
	"github.com/oioio-space/unpixel/internal/forensics"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/rectify"
)

// NewServer builds an MCP server with all UnPixel tools and resources
// registered and ready to accept connections. version is embedded in the
// implementation metadata (e.g. "v0.1.0").
func NewServer(version string) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "unpixel",
		Version: version,
	}, nil)

	// Tools.
	mcpsdk.AddTool(srv, toolAnalyze, handleAnalyze)
	mcpsdk.AddTool(srv, toolVerify, handleVerify)
	mcpsdk.AddTool(srv, toolDecode, handleDecode)
	mcpsdk.AddTool(srv, toolJobResult, handleJobResult)
	mcpsdk.AddTool(srv, toolJobCancel, handleJobCancel)
	mcpsdk.AddTool(srv, toolRender, handleRender)
	mcpsdk.AddTool(srv, toolRankFonts, handleRankFonts)
	mcpsdk.AddTool(srv, toolCalibrate, handleCalibrate)
	mcpsdk.AddTool(srv, toolLeakScan, handleLeakScan)

	// Resources.
	registerResources(srv)

	return srv
}

// toolAnalyze is the tool descriptor for unpixel_analyze.
var toolAnalyze = &mcpsdk.Tool{
	Name: "unpixel_analyze",
	Description: "Analyzes a pixelated image and returns its technical characteristics: " +
		"block grid, colorspace, blur sigma, font size, redaction bounding box, " +
		"and a heuristic recommendation for which UnPixel decoder to use. " +
		"When perspective distortion is detected (non-axis-aligned mosaic grid) the " +
		"recommended_decoder is set to 'perspective' and, when the redaction is on a " +
		"roughly-uniform background, a suggested_quad is populated so the LLM can " +
		"pass it directly to unpixel_decode without manual annotation.",
}

// toolVerify is the tool descriptor for unpixel_verify_candidates.
var toolVerify = &mcpsdk.Tool{
	Name: "unpixel_verify_candidates",
	Description: "Scores a list of candidate strings against a mosaic-pixelated image " +
		"by rendering each candidate, re-pixelating it, and measuring image distance. " +
		"Returns the candidates ranked from best (lowest distance) to worst, plus a margin.",
}

// analyzeInput is the JSON-decoded input for unpixel_analyze.
type analyzeInput struct {
	// ImagePath is the absolute (or cwd-relative) filesystem path of the PNG or JPEG image.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the pixelated PNG or JPEG image"`
}

// AnalysisReport is the output of unpixel_analyze.
type AnalysisReport struct {
	// RedactionType is "mosaic", "blur", or "none".
	RedactionType string `json:"redaction_type"`
	// BlockSize is the detected mosaic block side length in pixels (0 if not detected).
	BlockSize int `json:"block_size"`
	// GridConfidence is in [0,1]: 1 means a perfect axis-aligned mosaic grid.
	GridConfidence float64 `json:"grid_confidence"`
	// BlurSigma is the estimated Gaussian-blur standard deviation (0 if no blur).
	BlurSigma float64 `json:"blur_sigma"`
	// Colorspace is "linear" (GIMP/GEGL) or "srgb".
	Colorspace string `json:"colorspace"`
	// DarkBackground is true when the dominant background is dark.
	DarkBackground bool `json:"dark_background"`
	// RedactionBbox is [x0, y0, x1, y1] of the detected redaction rectangle, or nil.
	RedactionBbox []int `json:"redaction_bbox,omitzero"`
	// FontSizePt is the estimated source text font size in points.
	FontSizePt float64 `json:"font_size_pt"`
	// ImpulseNoise is the estimated impulse-noise ratio in [0,1].
	ImpulseNoise float64 `json:"impulse_noise"`
	// PerspectiveDistortion is true when the image is likely a photo of a
	// mosaic redaction taken at an angle (non-axis-aligned grid). When true,
	// RecommendedDecoder is "perspective".
	PerspectiveDistortion bool `json:"perspective_distortion,omitzero"`
	// SuggestedQuad is the auto-detected redaction quad in "x0,y0 x1,y1
	// x2,y2 x3,y3" format (top-left, top-right, bottom-right, bottom-left),
	// populated when PerspectiveDistortion is true and the redaction region is
	// detectable against the background. Pass directly to unpixel_decode
	// quad field. Empty when auto-detection fails (supply corners manually).
	SuggestedQuad string `json:"suggested_quad,omitzero"`
	// RecommendedDecoder suggests which decoder to use: "engine", "blurred", "perspective", or "none".
	RecommendedDecoder string `json:"recommended_decoder"`
	// RecommendedCharset suggests a starter charset string.
	RecommendedCharset string `json:"recommended_charset"`
	// Rationale explains why the decoder was chosen.
	Rationale string `json:"rationale"`
	// Caveats lists optional warnings about the analysis.
	Caveats []string `json:"caveats,omitzero"`
	// ForwardOperator is the detected redaction operator (mosaic vs. blur,
	// colorspace, kernel family, estimated sigma, and tool heuristic).
	ForwardOperator DetectedOperator `json:"forward_operator,omitzero"`
}

// DetectedOperator carries the forensics.Fingerprint result in a flat,
// JSON-serializable form suitable for MCP tool output.
type DetectedOperator struct {
	// Kind is the operator family: "mosaic", "blur", or "unknown".
	Kind string `json:"kind"`
	// Gamma is the colorspace of mosaic averaging: "sRGB", "linear", or "unknown".
	Gamma string `json:"gamma,omitzero"`
	// Kernel is the blur kernel family: "true-gauss", "box3", or "unknown".
	Kernel string `json:"kernel,omitzero"`
	// Sigma is the estimated Gaussian blur standard deviation (blur only; 0 otherwise).
	Sigma float64 `json:"sigma,omitzero"`
	// Tool is a best-effort informative label for the likely redaction tool (e.g. "Photoshop/GIMP").
	Tool string `json:"tool,omitzero"`
	// Confidence is the detection confidence for Kind in [0, 1].
	Confidence float64 `json:"confidence,omitzero"`
}

// handleAnalyze is the tool handler for unpixel_analyze.
func handleAnalyze(_ context.Context, _ *mcpsdk.CallToolRequest, in analyzeInput) (*mcpsdk.CallToolResult, AnalysisReport, error) {
	img, err := loadImage(in.ImagePath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_analyze: load image: %w", err)), AnalysisReport{}, nil
	}

	report, err := Analyze(img)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_analyze: %w", err)), AnalysisReport{}, nil
	}
	return toolJSON(report)
}

// Analyze inspects img and returns an AnalysisReport without modifying the
// image. It calls the exported inference helpers in the unpixel package and
// derives a heuristic recommended decoder.
func Analyze(img image.Image) (AnalysisReport, error) {
	var r AnalysisReport

	// Block grid detection.
	grid, hasGrid := unpixel.InferBlockGrid(img)
	if hasGrid {
		r.BlockSize = grid.Size
		r.GridConfidence = grid.Confidence
	}

	// Blur sigma.
	r.BlurSigma = unpixel.InferBlurSigma(img)

	// Colorspace: DetectColorspace requires *image.RGBA and block ≥ 1.
	rgba := imutil.ToRGBA(img)
	blockForCS := max(1, r.BlockSize) // DetectColorspace panics on block=0
	linear, _ := pixelate.DetectColorspace(rgba, blockForCS)
	if linear {
		r.Colorspace = "linear"
	} else {
		r.Colorspace = "srgb"
	}

	// Forward-operator fingerprint: classify mosaic vs. blur, colorspace, kernel,
	// and estimated sigma. Block hint uses the inferred block size (0 = unknown).
	op := forensics.Fingerprint(rgba, forensics.Hint{Block: r.BlockSize})
	r.ForwardOperator = DetectedOperator{
		Kind:       op.Kind.String(),
		Gamma:      op.Gamma.String(),
		Kernel:     op.Kernel.String(),
		Sigma:      op.Sigma,
		Tool:       op.Tool,
		Confidence: op.Conf.Kind,
	}

	// Dark background.
	r.DarkBackground = unpixel.InferDarkBackground(img)

	// Redaction bounding box.
	bbox, hasBbox := unpixel.LocateRedaction(img)
	if hasBbox && !bbox.Empty() {
		r.RedactionBbox = []int{bbox.Min.X, bbox.Min.Y, bbox.Max.X, bbox.Max.Y}
	}

	// Font size estimate.
	r.FontSizePt = unpixel.InferFontSize(img)

	// Impulse noise estimate.
	r.ImpulseNoise = unpixel.InferImpulseNoise(img)

	// Robust block size support score (used by the heuristic).
	_, support := unpixel.InferBlockSizeRobust(img)

	r.RedactionType, r.RecommendedDecoder, r.RecommendedCharset, r.Rationale, r.Caveats = heuristic(r.BlurSigma, hasGrid, grid.Confidence, support, r.BlockSize)

	// Perspective detection: when the axis-aligned grid heuristic finds no
	// mosaic but the image appears to be a photo (non-uniform background with
	// a distinct foreground region), attempt a cheap DetectQuad. A successful
	// quad detection on an image that passed no grid test is strong evidence of
	// a photographed / perspective-distorted mosaic.
	if r.RecommendedDecoder == "none" || r.RecommendedDecoder == "" {
		// DetectQuad finds a foreground region in almost any text-on-background
		// image — including upright redactions and clean (un-redacted) text —
		// so its mere success is NOT evidence of perspective. Only flag
		// perspective when the quad is genuinely TILTED (its corners deviate
		// from an axis-aligned rectangle). Otherwise an upright redaction the
		// grid detector simply missed (e.g. a short label) would be misrouted
		// to the perspective decoder.
		if quad, qErr := rectify.DetectQuad(rgba, 40); qErr == nil && quadTilted(quad) {
			r.PerspectiveDistortion = true
			r.RecommendedDecoder = "perspective"
			r.RedactionType = "mosaic"
			r.RecommendedCharset = unpixel.CharsetAlnum
			r.Rationale = "a tilted foreground quad was found (non-axis-aligned); likely a photographed mosaic"
			r.SuggestedQuad = fmt.Sprintf("%.0f,%.0f %.0f,%.0f %.0f,%.0f %.0f,%.0f",
				quad[0].X, quad[0].Y,
				quad[1].X, quad[1].Y,
				quad[2].X, quad[2].Y,
				quad[3].X, quad[3].Y,
			)
			r.Caveats = append(r.Caveats,
				"perspective decoder suggested: verify suggested_quad corners before use",
				"set auto_quad=true in unpixel_decode to let the decoder detect corners automatically",
			)
		}
	}

	return r, nil
}

// quadTilted reports whether an axis-aligned-detected quad is actually skewed,
// i.e. its top/bottom edges are non-horizontal or its left/right edges are
// non-vertical by more than tiltTolFrac of the quad's larger dimension. A near-
// rectangular quad (upright redaction or clean text) returns false.
func quadTilted(q [4]rectify.Point) bool {
	const tiltTolFrac = 0.06
	// Corner order from DetectQuad: [0]=TL, [1]=TR, [2]=BR, [3]=BL.
	tl, tr, br, bl := q[0], q[1], q[2], q[3]
	minX := min(tl.X, tr.X, br.X, bl.X)
	maxX := max(tl.X, tr.X, br.X, bl.X)
	minY := min(tl.Y, tr.Y, br.Y, bl.Y)
	maxY := max(tl.Y, tr.Y, br.Y, bl.Y)
	w, h := maxX-minX, maxY-minY
	span := max(w, h)
	if span <= 0 {
		return false
	}
	// Deviation of each edge from perfectly axis-aligned.
	dev := max(
		math.Abs(tl.Y-tr.Y), // top edge horizontal?
		math.Abs(bl.Y-br.Y), // bottom edge horizontal?
		math.Abs(tl.X-bl.X), // left edge vertical?
		math.Abs(tr.X-br.X), // right edge vertical?
	)
	return dev/span > tiltTolFrac
}

// heuristic derives the recommended decoder and related fields from analysis
// signals. The logic is intentionally simple and documented inline.
//
// Priority order (earlier cases win):
//  1. High-confidence grid (≥ 0.7) → "mosaic", engine decoder. Block-average
//     mosaics appear mildly blurry to the DCT estimator, so the grid check
//     must come first. Engine (unpixel.Recover with auto-calibration) is the
//     best-config path and outperforms DID on the synthetic fixture panel.
//  2. No grid but blurSigma > 0.5 → "blur", blurred decoder.
//  3. Low-confidence grid → "mosaic", engine decoder (short strings).
//  4. No grid, no blur → "none".
func heuristic(blurSigma float64, hasGrid bool, gridConf, support float64, blockSize int) (redactionType, decoder, charset, rationale string, caveats []string) {
	switch {
	case hasGrid && gridConf >= 0.7:
		rat := fmt.Sprintf(
			"block grid detected (size=%d, confidence=%.2f, support=%.2f); engine decoder recommended (auto-crop, auto-colorspace, auto-calibrate)",
			blockSize, gridConf, support,
		)
		return "mosaic", "engine", unpixel.CharsetAlnum, rat, nil

	case blurSigma > 0.5:
		return "blur", "blurred", unpixel.CharsetAlnum,
			fmt.Sprintf("blur sigma %.1f px detected; use RecoverBlurred", blurSigma),
			nil

	case hasGrid:
		rat := fmt.Sprintf(
			"weak grid signal (size=%d, confidence=%.2f); use engine decoder for short strings",
			blockSize, gridConf,
		)
		return "mosaic", "engine", unpixel.DefaultCharset, rat,
			[]string{"low grid confidence — verify block_size manually"}

	default:
		return "none", "none", "",
			"no regular block grid detected; image may not be mosaic-pixelated",
			[]string{"check whether the image is blurred or uses a non-uniform redaction method"}
	}
}

// ---- unpixel_verify_candidates ----

// verifyInput is the JSON-decoded input for unpixel_verify_candidates.
type verifyInput struct {
	// ImagePath is the filesystem path of the pixelated PNG or JPEG image.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the pixelated PNG or JPEG image"`
	// Candidates is the list of strings to score.
	Candidates []string `json:"candidates" jsonschema:"Candidate strings to verify against the mosaic"`
	// BlockSize overrides auto-detected block size (0 = auto).
	BlockSize int `json:"block_size,omitzero" jsonschema:"Override block size in pixels (0 = auto-detect)"`
	// Charset restricts the rendering alphabet of the faithful scoring model
	// (forwarded as WithCharset). Empty = engine default. Supplying the charset
	// from analyze/propose_hints sharpens the decisive match.
	Charset string `json:"charset,omitzero" jsonschema:"Charset forwarded to the scoring model to restrict the rendering alphabet (empty = default)"`
}

// RankedCandidate is one scored entry in VerifyReport.Ranked.
type RankedCandidate struct {
	// Text is the candidate string.
	Text string `json:"text"`
	// Distance is the whole-image pixel distance in [0,1] (lower is better).
	Distance float64 `json:"distance"`
	// Match reports whether Distance is below [unpixel.VerifyMatchThreshold],
	// indicating a confident physical match.
	Match bool `json:"match"`
}

// VerifyReport is the output of unpixel_verify_candidates.
type VerifyReport struct {
	// Ranked lists all scored candidates in ascending distance order.
	Ranked []RankedCandidate `json:"ranked"`
	// Best is the text of the top-ranked candidate (lowest distance).
	Best string `json:"best"`
	// Margin is the distance gap between the 2nd-best and best candidates
	// (0 when fewer than two candidates were provided).
	Margin float64 `json:"margin"`
	// Pick is the lowest-distance candidate whose Match is true (a confident
	// physical match per [unpixel.VerifyMatchThreshold]). Empty when no
	// candidate meets the threshold.
	Pick string `json:"pick"`
}

// handleVerify is the tool handler for unpixel_verify_candidates.
func handleVerify(ctx context.Context, _ *mcpsdk.CallToolRequest, in verifyInput) (*mcpsdk.CallToolResult, VerifyReport, error) {
	if len(in.Candidates) == 0 {
		return errResult(fmt.Errorf("unpixel_verify_candidates: candidates must not be empty")), VerifyReport{}, nil
	}

	img, err := loadImage(in.ImagePath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_candidates: load image: %w", err)), VerifyReport{}, nil
	}

	report, err := VerifyCandidates(ctx, img, in.Candidates, in.BlockSize, in.Charset)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_verify_candidates: %w", err)), VerifyReport{}, nil
	}
	return toolJSON(report)
}

// VerifyCandidates scores each candidate string against the observed mosaic in
// img using [unpixel.Verify]'s faithful forward model (render→re-pixelate→metric,
// calibrated like Recover) and returns a [VerifyReport] with candidates ranked by
// ascending image distance.
//
// blockSize pins the mosaic block size in pixels (0 = auto-detect). charset
// restricts the rendering alphabet (empty = default). Both are forwarded to
// [unpixel.Verify] as [unpixel.WithBlockSize] and [unpixel.WithCharset] when
// non-zero/non-empty, so the LLM propose→verify flow can pass the block and
// charset discovered by unpixel_analyze / unpixel_propose_hints.
//
// Pick is set to the lowest-distance candidate whose [unpixel.Verdict.Match] is
// true; it is empty when no candidate meets [unpixel.VerifyMatchThreshold].
func VerifyCandidates(ctx context.Context, img image.Image, candidates []string, blockSize int, charset string) (VerifyReport, error) {
	var opts []unpixel.Option
	if blockSize > 0 {
		opts = append(opts, unpixel.WithBlockSize(blockSize))
	}
	if charset != "" {
		opts = append(opts, unpixel.WithCharset(charset))
	}

	verdicts, err := unpixel.Verify(ctx, img, candidates, opts...)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("score candidates: %w", err)
	}

	ranked := make([]RankedCandidate, len(verdicts))
	for i, v := range verdicts {
		ranked[i] = RankedCandidate{Text: v.Text, Distance: v.Distance, Match: v.Match}
	}
	slices.SortFunc(ranked, func(a, b RankedCandidate) int {
		return cmp.Compare(a.Distance, b.Distance)
	})

	report := VerifyReport{Ranked: ranked}
	if len(ranked) > 0 {
		report.Best = ranked[0].Text
	}
	if len(ranked) >= 2 {
		report.Margin = ranked[1].Distance - ranked[0].Distance
	}
	// Pick: lowest-distance candidate with a confident physical match.
	for _, rc := range ranked {
		if rc.Match {
			report.Pick = rc.Text
			break
		}
	}
	return report, nil
}

// ---- helpers ----

// loadImage opens path and decodes the image using the registered decoders.
func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path) // #nosec G304 -- MCP server reads caller-supplied local path by design
	if err != nil {
		return nil, err
	}
	img, _, decErr := image.Decode(f)
	if closeErr := f.Close(); closeErr != nil && decErr == nil {
		return nil, closeErr
	}
	if decErr != nil {
		return nil, decErr
	}
	return img, nil
}

// errResult builds a CallToolResult with IsError=true for the given error
// message. The MCP call itself succeeded — only the tool's logic failed.
func errResult(err error) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: err.Error()},
		},
	}
}

// toolJSON marshals v as a JSON TextContent result and also returns v as the
// typed output so the SDK can populate the MCP output schema.
func toolJSON[T any](v T) (*mcpsdk.CallToolResult, T, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, v, fmt.Errorf("marshal result: %w", err)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(b)},
		},
	}, v, nil
}
