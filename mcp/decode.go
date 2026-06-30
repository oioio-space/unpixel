package mcpserver

// decode.go — unpixel_decode tool: a single MCP tool that dispatches to every
// UnPixel decoder behind a "method" enum. All decode result types are normalised
// into a single DecodeResult struct so callers always receive the same schema.
//
// Supported methods:
//
//	auto        — choose the best decoder (engine for axis-aligned mosaics).
//	engine      — unpixel.Recover with full auto-calibration (best-config path).
//	mosaic      — mosaictext.Decode (zero-config monospace).
//	blurred     — unpixel.RecoverBlurred (Gaussian-blur path).
//	mono-hmm    — mosaictext.DecodeHMM (LM-guided beam, monospace).
//	window-hmm  — mosaictext.DecodeWindowHMM (proportional beam).
//	trained-hmm — mosaictext.DecodeTrainedHMM (Hill-2016 column HMM).
//	did         — mosaictext.DecodeDID (Kopec-Chou trellis, proportional).
//	varfont     — mosaictext.DecodeVarFont (variable-font axis fitting).
//	perspective — mosaictext.DecodePerspective (tilted photo beam).
//	reference   — mosaictext.DecodeReference (Depix-style per-phase match).
//	blind       — blind.Recover (zero-config blind recovery).
//	ensemble    — mosaictext.DecodeEnsemble (best of mosaic+hmm+did).
//	multi-frame — mosaictext.DecodeMultiFrame (IBP frame fusion + mosaic).

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/blind"
	"github.com/oioio-space/unpixel/defaults"
	_ "github.com/oioio-space/unpixel/defaults" // wire standard components
	"github.com/oioio-space/unpixel/fontprior"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/secrets"
	"github.com/oioio-space/unpixel/mosaictext"
)

// jsonMarshal marshals v and wraps any error with context. It is a thin
// convenience wrapper used by async tool paths that need to marshal
// non-DecodeResult values.
func jsonMarshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return b, nil
}

// toolDecode is the tool descriptor for unpixel_decode.
var toolDecode = &mcpsdk.Tool{
	Name: "unpixel_decode",
	Description: "Recovers the hidden text from a redacted (pixelated or blurred) image. " +
		"Use this as the primary decode tool whenever you have a pixelated or blurred redaction and want to recover its text. " +
		"It dispatches to the best available decoder based on the 'method' field (default: 'auto'). " +
		"Long decodes (engine, did, ensemble) may take 10–120 s on a full redaction — set timeout_seconds accordingly. " +
		"NOT suitable for non-redacted images; run unpixel_analyze first when in doubt. " +
		"Methods: auto (recommended, routes to engine for axis-aligned mosaics), " +
		"engine (best-config path: unpixel.Recover with explicit charset/block/font/max-length — use this for hard fixtures; charset_preset is the key parameter), " +
		"mosaic (zero-config monospace), blurred (Gaussian blur), " +
		"mono-hmm (LM beam, monospace), window-hmm (proportional beam), trained-hmm (Hill-2016 column HMM), " +
		"did (Kopec-Chou trellis, proportional text), varfont (variable-font axis fit), " +
		"perspective (tilted/photographed mosaic — supply quad corners OR set auto_quad=true), " +
		"reference (Depix-style, calibrates from known_visible_text when supplied), " +
		"blind (fully zero-config), ensemble (best of mosaic+hmm+did), multi-frame (IBP fusion). " +
		"Language (en|fr) is forwarded to mono-hmm, trained-hmm, did, and reference decoders; ignored by others. " +
		"The prefix field is accepted but not forwarded to any decoder (no-op). " +
		"Engine/blurred options: charset_preset (lower|alnum|ascii|digits), block_size, font_size, max_length, denoise, font_path/font_base64. " +
		"Perspective options: quad, auto_quad, auto_quad_tol, font_size, block_size, beam_width, rect_size_w, rect_size_h, workers. " +
		"Custom font (all font-consuming methods): supply font_path (local TTF/OTF) or font_base64 (base64-encoded TTF/OTF) — " +
		"at most one; custom font takes precedence over bundled defaults. " +
		"Font precedence: custom font data > bundled font name > decoder default.",
}

// FrameInput is one additional frame for the multi-frame decoder. Each frame
// is a same-content mosaic captured at a different grid phase. OffsetX and
// OffsetY are the pixel-level grid phase shifts relative to the primary image;
// omitting them (or supplying 0) means the phase is unknown and the decoder
// will attempt to determine it automatically.
type FrameInput struct {
	// Path is the filesystem path to the frame PNG or JPEG image.
	Path string `json:"path" jsonschema:"Filesystem path to the additional frame PNG/JPEG"`
	// OffsetX is the horizontal grid phase offset in pixels (0 = unknown/auto).
	OffsetX int `json:"offset_x,omitzero" jsonschema:"Horizontal sub-block grid phase in pixels (0 = auto)"`
	// OffsetY is the vertical grid phase offset in pixels (0 = unknown/auto).
	OffsetY int `json:"offset_y,omitzero" jsonschema:"Vertical sub-block grid phase in pixels (0 = auto)"`
}

// decodeInput is the JSON input schema for unpixel_decode.
type decodeInput struct {
	// ImagePath is the filesystem path to the pixelated or blurred image.
	ImagePath string `json:"image_path" jsonschema:"Filesystem path to the pixelated or blurred PNG/JPEG image"`
	// Method selects the decode algorithm.
	Method string `json:"method,omitzero" jsonschema:"Decoder: auto|mosaic|blurred|mono-hmm|window-hmm|trained-hmm|did|varfont|perspective|reference|blind|ensemble|multi-frame (default: auto)"`
	// CharsetPreset restricts the search alphabet.
	CharsetPreset string `json:"charset_preset,omitzero" jsonschema:"Alphabet: lower|alnum|ascii (default: alnum)"`
	// Language sets the language model used for disambiguation.
	Language string `json:"language,omitzero" jsonschema:"Language model: en|fr (default: en)"`
	// MaxLength caps the maximum recovered string length (0 = decoder default).
	MaxLength int `json:"max_length,omitzero" jsonschema:"Maximum string length to search (0 = decoder default)"`
	// Prefix is accepted for forward-compatibility but is not forwarded to any
	// decoder in this MCP layer. No mosaictext decoder currently exposes a
	// prefix-constraint option; the root unpixel.Engine does (via unpixel.WithPrefix)
	// but that engine is not reached from this tool. Setting this field has no
	// effect — omit it.
	Prefix string `json:"prefix,omitzero" jsonschema:"Reserved: not currently forwarded to any decoder; has no effect."`
	// ExpectedFormat constrains the search to a structured-secret format
	// (digits|credit_card|iban|date|phone_fr|phone_us|phone_e164). Applied on
	// the engine path (including the engine sub-call inside ensemble) via
	// unpixel.WithExpectedFormat; ignored by all other decoders. Declaring the
	// wrong format will reject the true answer. Omit for free text.
	ExpectedFormat string `json:"expected_format,omitzero" jsonschema:"Structured-secret format applied on engine path (incl. ensemble engine sub-call): digits|credit_card|iban|date|phone_fr|phone_us|phone_e164 (omit for free text)"`
	// FontPriorTopK, when > 0, runs a blind font-prior-ordered multi-font sweep
	// pruned to the K best-ranked bundled fonts (engine path). 0 = disabled
	// (single default font). See fontprior.RecoverWithPrior.
	FontPriorTopK int `json:"font_prior_top_k,omitzero" jsonschema:"Engine-only: run a blind font-prior sweep pruned to the top-K bundled fonts (0 = off)"`
	// KnownVisibleText is cleartext known to appear in (or adjacent to) the redaction.
	KnownVisibleText string `json:"known_visible_text,omitzero" jsonschema:"Cleartext known to appear in or adjacent to the redaction (used by reference and varfont decoders)"`
	// Frames lists additional mosaic frames for multi-frame IBP fusion. Each
	// entry carries the path and optional per-frame grid-phase offsets.
	// Previous callers that passed []string are no longer supported — supply
	// FrameInput objects instead.
	Frames []FrameInput `json:"frames,omitzero" jsonschema:"Additional frames for multi-frame method; each entry has path, offset_x, offset_y"`

	// FontSize overrides the rendering font size in points (engine, perspective;
	// default 32). Values ≤ 0 are ignored.
	FontSize float64 `json:"font_size,omitzero" jsonschema:"Font size in points for candidate rendering (engine and perspective; default 32; 0 = auto)"`
	// BlockSize pins the mosaic block size (engine, perspective; default auto-detect).
	// Values ≤ 0 let the engine auto-detect from the image.
	BlockSize int `json:"block_size,omitzero" jsonschema:"Mosaic block size in pixels (engine and perspective; 0 = auto-detect)"`
	// Denoise, when true, applies input normalisation (vignette removal,
	// dark-theme inversion) before the search. Supported by engine and blurred.
	// Use it on noisy real-world captures (e.g. hello-world-noisy.png).
	Denoise bool `json:"denoise,omitzero" jsonschema:"Apply input normalisation (vignette/dark-theme) before search — engine and blurred only; helps noisy real captures"`

	// ---- perspective-specific options ----

	// Quad is four corner coordinates for perspective decoding.
	Quad string `json:"quad,omitzero" jsonschema:"Quad corners for perspective: 'x0,y0 x1,y1 x2,y2 x3,y3' (top-left, top-right, bottom-right, bottom-left). Takes precedence over auto_quad."`
	// AutoQuad, when true, lets the perspective decoder detect the redaction
	// quad automatically via background-contrast analysis. Ignored when quad
	// is also supplied (explicit quad wins). Supported only by method=perspective.
	AutoQuad bool `json:"auto_quad,omitzero" jsonschema:"perspective only: auto-detect quad corners from background contrast (ignored when quad is also set)"`
	// AutoQuadTol is the background-difference threshold for auto_quad (0 = default 40).
	AutoQuadTol int `json:"auto_quad_tol,omitzero" jsonschema:"perspective only: background-difference tolerance for auto_quad (0 = default 40)"`
	// BeamWidth sets the number of surviving prefixes per beam level for the
	// perspective decoder (default 36).
	BeamWidth int `json:"beam_width,omitzero" jsonschema:"perspective only: beam width — number of prefixes kept per level (default 36)"`
	// RectSizeW pins the axis-aligned redaction rectangle width in pixels
	// (perspective decoder only; 0 = derive from quad edge lengths).
	RectSizeW int `json:"rect_size_w,omitzero" jsonschema:"perspective only: axis-aligned rectangle width in pixels (0 = estimate from quad)"`
	// RectSizeH pins the axis-aligned redaction rectangle height in pixels
	// (perspective decoder only; 0 = derive from quad edge lengths).
	RectSizeH int `json:"rect_size_h,omitzero" jsonschema:"perspective only: axis-aligned rectangle height in pixels (0 = estimate from quad)"`
	// Workers sets the number of goroutines for candidate evaluation in the
	// perspective decoder (0 = GOMAXPROCS).
	Workers int `json:"workers,omitzero" jsonschema:"perspective only: goroutine concurrency for candidate evaluation (0 = GOMAXPROCS)"`

	// ---- custom font upload (all font-consuming decoders) ----

	// FontPath is a local filesystem path to a TTF/OTF font file. At most one
	// of font_path and font_base64 may be set. Supported by: mosaic (mono-hmm,
	// window-hmm, trained-hmm), did, reference, perspective. Takes precedence
	// over the font name field. varfont requires a variable font; for other
	// methods any TTF/OTF is accepted.
	FontPath string `json:"font_path,omitzero" jsonschema:"Local TTF/OTF path for a custom font (mutually exclusive with font_base64). Supported by mosaic/hmm/did/reference/perspective."`
	// FontBase64 carries raw TTF/OTF bytes encoded as standard base64. At most
	// one of font_path and font_base64 may be set.
	FontBase64 string `json:"font_base64,omitzero" jsonschema:"Raw TTF/OTF bytes as base64 (mutually exclusive with font_path). Supported by mosaic/hmm/did/reference/perspective."`

	// fontData is the resolved font bytes. Populated by handleDecode (from
	// FontPath/FontBase64) or by Decode (from DecodeOptions.FontData) before
	// dispatchDecode is called. Not settable via JSON.
	fontData []byte

	// TimeoutSeconds bounds total decode time (0 = 120 s default).
	TimeoutSeconds int `json:"timeout_seconds,omitzero" jsonschema:"Maximum seconds before cancellation (0 = 120 s)"`
	// Async, when true, starts the decode in a background goroutine and
	// immediately returns a job_id. Poll with unpixel_job_result; cancel with
	// unpixel_job_cancel. Recommended for long methods (did, ensemble, blind)
	// to avoid MCP transport timeouts.
	Async bool `json:"async,omitzero" jsonschema:"When true, returns immediately with a job_id; poll via unpixel_job_result"`
}

// DecodeResult is the unified output of unpixel_decode, independent of which
// decoder was used.
//
// Distance semantics vary by decoder: mosaic/HMM decoders return raw block-value
// MSE (unbounded, lower is better); blurred/blind decoders return a normalised
// value in [0,1]. Fidelity is only meaningful when Distance is in [0,1] — it is
// set to 0 when Distance exceeds 1.
type DecodeResult struct {
	// Text is the recovered string.
	Text string `json:"text"`
	// Distance is the image distance of the winning reconstruction (lower is better).
	// Mosaic/HMM decoders return raw block-value MSE (unbounded); blurred/blind
	// decoders return a value in [0, 1].
	Distance float64 `json:"distance"`
	// Fidelity is 1-Distance clamped to [0,1] (higher is better).
	// Only meaningful when Distance is in [0,1] — see Distance notes above.
	Fidelity float64 `json:"fidelity,omitzero"`
	// Font is the bundled font name that produced the best fit (if known).
	Font string `json:"font,omitzero"`
	// BlockSize is the mosaic block size resolved during decoding (0 if not applicable).
	BlockSize int `json:"block_size,omitzero"`
	// MethodUsed records which concrete decoder was selected (useful when method=auto).
	MethodUsed string `json:"method_used"`
	// Notes carries supplementary information (warnings, tips, caveat strings).
	Notes []string `json:"notes,omitzero"`
}

// DecodeOptions carries the optional parameters for [Decode]. Zero value uses
// all defaults.
type DecodeOptions struct {
	// CharsetPreset selects the search alphabet: "lower", "alnum", "ascii", or
	// "digits" (0–9 only). Empty string defaults to "alnum".
	CharsetPreset string
	// Language selects the language model: "en" or "fr". Empty defaults to "en".
	Language string
	// MaxLength caps the maximum decoded string length (0 = decoder default).
	MaxLength int
	// Prefix is accepted for forward-compatibility but is not forwarded to any
	// decoder. Setting it has no effect.
	Prefix string
	// ExpectedFormat constrains the engine search to a structured-secret format.
	// Forwarded only to the engine method; ignored by other decoders.
	ExpectedFormat string
	// FontPriorTopK, when > 0, runs a blind font-prior-ordered multi-font sweep
	// pruned to the K best-ranked bundled fonts (engine path). 0 = disabled.
	FontPriorTopK int
	// KnownVisibleText is cleartext known to appear in or adjacent to the redaction.
	KnownVisibleText string
	// Frames lists additional mosaic frames for the multi-frame method.
	// Each [FrameInput] carries a path and optional per-frame grid-phase offsets.
	Frames []FrameInput
	// Quad is four corner coordinates for perspective decoding ("x0,y0 x1,y1 x2,y2 x3,y3").
	Quad string
	// AutoQuad enables automatic quad detection for method=perspective.
	// Ignored when Quad is also set.
	AutoQuad bool
	// AutoQuadTol is the background-difference tolerance for AutoQuad (0 = default 40).
	AutoQuadTol int
	// FontSize overrides the rendering font size in points (engine and perspective
	// decoders). Values ≤ 0 are ignored (engine auto-detects; perspective defaults to 32).
	FontSize float64
	// BlockSize pins the mosaic block size (engine and perspective decoders).
	// Values ≤ 0 let the engine auto-detect from the image.
	BlockSize int
	// BeamWidth sets the beam width (perspective decoder).
	BeamWidth int
	// RectSizeW pins the axis-aligned rectangle width (perspective decoder; 0 = auto).
	RectSizeW int
	// RectSizeH pins the axis-aligned rectangle height (perspective decoder; 0 = auto).
	RectSizeH int
	// Workers sets goroutine concurrency (perspective decoder; 0 = GOMAXPROCS).
	Workers int
	// FontData carries raw TTF/OTF bytes for a custom font. When non-nil it
	// takes precedence over any named font. Supported by engine/mosaic/hmm/did/reference/perspective.
	FontData []byte
	// Denoise, when true, applies input normalisation before the search (engine
	// and blurred methods only). Useful for noisy real-world captures.
	Denoise bool
	// TimeoutSeconds bounds total decode time (0 = 120 s).
	TimeoutSeconds int
}

// Decode recovers hidden text from img using the named method. method must be
// one of the values accepted by the unpixel_decode tool (e.g. "auto",
// "mosaic", "did"). opts supplies optional parameters; the zero value uses
// all defaults. Decode is the testable core of the unpixel_decode MCP tool.
func Decode(ctx context.Context, img image.Image, method string, opts DecodeOptions) (DecodeResult, error) {
	in := decodeInput{
		Method:           method,
		CharsetPreset:    opts.CharsetPreset,
		Language:         opts.Language,
		MaxLength:        opts.MaxLength,
		Prefix:           opts.Prefix,
		ExpectedFormat:   opts.ExpectedFormat,
		FontPriorTopK:    opts.FontPriorTopK,
		KnownVisibleText: opts.KnownVisibleText,
		Frames:           opts.Frames,
		Quad:             opts.Quad,
		AutoQuad:         opts.AutoQuad,
		AutoQuadTol:      opts.AutoQuadTol,
		FontSize:         opts.FontSize,
		BlockSize:        opts.BlockSize,
		BeamWidth:        opts.BeamWidth,
		RectSizeW:        opts.RectSizeW,
		RectSizeH:        opts.RectSizeH,
		Workers:          opts.Workers,
		Denoise:          opts.Denoise,
		TimeoutSeconds:   opts.TimeoutSeconds,
	}
	in.fontData = opts.FontData
	if in.Method == "" {
		in.Method = "auto"
	}
	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if in.Method == "auto" {
		report, aErr := Analyze(img)
		if aErr != nil {
			return DecodeResult{}, fmt.Errorf("auto-analyze: %w", aErr)
		}
		method = report.RecommendedDecoder
		if method == "" || method == "none" {
			method = "engine"
		}
		switch method {
		case "blurred":
			// keep
		case "perspective":
			// keep
		default:
			// engine is the best-config path for all axis-aligned mosaics,
			// including when analyze recommends "did" or "default".
			method = "engine"
		}
	}
	return dispatchDecode(ctx, img, method, in)
}

// asyncDecodeResult is returned by unpixel_decode when async=true. The client
// should poll with unpixel_job_result{job_id} and cancel with
// unpixel_job_cancel{job_id}.
type asyncDecodeResult struct {
	// JobID is the opaque identifier for the background job.
	JobID string `json:"job_id"`
	// Status is always "pending" immediately after submission.
	Status string `json:"status"`
	// Message provides human-readable context.
	Message string `json:"message"`
}

// handleDecode is the tool handler for unpixel_decode.
func handleDecode(ctx context.Context, req *mcpsdk.CallToolRequest, in decodeInput) (*mcpsdk.CallToolResult, DecodeResult, error) {
	img, err := loadImage(in.ImagePath)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_decode: load image: %w", err)), DecodeResult{}, nil
	}

	fontData, fErr := LoadFontData(in.FontPath, in.FontBase64)
	if fErr != nil {
		return errResult(fmt.Errorf("unpixel_decode: %w", fErr)), DecodeResult{}, nil
	}
	in.fontData = fontData

	opts := DecodeOptions{
		CharsetPreset:    in.CharsetPreset,
		Language:         in.Language,
		MaxLength:        in.MaxLength,
		Prefix:           in.Prefix,
		ExpectedFormat:   in.ExpectedFormat,
		FontPriorTopK:    in.FontPriorTopK,
		KnownVisibleText: in.KnownVisibleText,
		Frames:           in.Frames,
		Quad:             in.Quad,
		AutoQuad:         in.AutoQuad,
		AutoQuadTol:      in.AutoQuadTol,
		FontSize:         in.FontSize,
		BlockSize:        in.BlockSize,
		BeamWidth:        in.BeamWidth,
		RectSizeW:        in.RectSizeW,
		RectSizeH:        in.RectSizeH,
		Workers:          in.Workers,
		FontData:         fontData,
		Denoise:          in.Denoise,
		TimeoutSeconds:   in.TimeoutSeconds,
	}
	method := cmp.Or(in.Method, "auto")

	if in.Async {
		// Detach from the MCP request context so the job outlives the HTTP
		// response. Use the server process's background context; the registry
		// cancel func is the only lifecycle control.
		jobID, jErr := jobRegistry.submit(context.Background(), func(jCtx context.Context) (DecodeResult, error) {
			return Decode(jCtx, img, method, opts)
		})
		if jErr != nil {
			return errResult(fmt.Errorf("unpixel_decode: start async job: %w", jErr)), DecodeResult{}, nil
		}
		ar := asyncDecodeResult{
			JobID:   jobID,
			Status:  "pending",
			Message: fmt.Sprintf("decode started async (method=%s); poll with unpixel_job_result", method),
		}
		b, mErr := jsonMarshal(ar)
		if mErr != nil {
			return errResult(mErr), DecodeResult{}, nil
		}
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		}, DecodeResult{}, nil
	}

	// Synchronous path: optionally report progress if the client supplied a token.
	progressToken := req.Params.GetProgressToken()
	if progressToken != nil && req.Session != nil {
		_ = req.Session.NotifyProgress(ctx, &mcpsdk.ProgressNotificationParams{
			ProgressToken: progressToken,
			Progress:      0,
			Total:         1,
			Message:       fmt.Sprintf("unpixel_decode: starting %s", method),
		})
	}

	res, dErr := Decode(ctx, img, method, opts)
	if dErr != nil {
		return errResult(fmt.Errorf("unpixel_decode: %w", dErr)), DecodeResult{}, nil
	}

	if progressToken != nil && req.Session != nil {
		_ = req.Session.NotifyProgress(ctx, &mcpsdk.ProgressNotificationParams{
			ProgressToken: progressToken,
			Progress:      1,
			Total:         1,
			Message:       fmt.Sprintf("unpixel_decode: done (text=%q)", res.Text),
		})
	}

	return toolJSON(res)
}

// parseLang converts the language field ("en", "fr", …) into a lang.Language.
// Unrecognised values fall back to lang.English (same as decoder defaults).
func parseLang(s string) lang.Language {
	l, _ := lang.ParseLanguage(s)
	return l
}

// charsetFor converts the charset_preset field into an alphabet string.
func charsetFor(preset string) string {
	switch strings.ToLower(preset) {
	case "lower":
		return unpixel.DefaultCharset
	case "ascii":
		return unpixel.CharsetASCII
	case "digits":
		return "0123456789"
	default: // "alnum" and anything else
		return unpixel.CharsetAlnum
	}
}

// dispatchDecode calls the right mosaictext/unpixel function and normalises the result.
func dispatchDecode(ctx context.Context, img image.Image, method string, in decodeInput) (DecodeResult, error) {
	switch method {
	case "engine", "default":
		// "default" is a legacy alias for "engine"; both reach the best-config path.
		return decodeEngine(ctx, img, in)
	case "mosaic":
		return decodeMosaic(ctx, img, in)
	case "blurred":
		return decodeBlurred(ctx, img, in)
	case "mono-hmm":
		return decodeMonoHMM(ctx, img, in)
	case "window-hmm":
		return decodeWindowHMM(ctx, img, in)
	case "trained-hmm":
		return decodeTrainedHMM(ctx, img, in)
	case "did":
		return decodeDID(ctx, img, in)
	case "varfont":
		return decodeVarFont(ctx, img, in)
	case "perspective":
		return decodePerspective(ctx, img, in)
	case "reference":
		return decodeReference(ctx, img, in)
	case "blind":
		return decodeBlind(ctx, img, in)
	case "ensemble":
		return decodeEnsemble(ctx, img, in)
	case "multi-frame":
		return decodeMultiFrame(ctx, img, in)
	default:
		return DecodeResult{}, fmt.Errorf("unknown method %q; valid values: auto, engine, mosaic, blurred, mono-hmm, window-hmm, trained-hmm, did, varfont, perspective, reference, blind, ensemble, multi-frame", method)
	}
}

// decodeEngine calls unpixel.Recover with the caller-supplied configuration:
// charset, block size, font size, max length, and optional denoise. This is the
// path that achieves 17/17 on the synthetic fixture panel and is the recommended
// decoder for axis-aligned mosaics — it differs from the zero-config "mosaic"
// method by accepting an explicit charset preset (incl. digits), block size, and
// font size, which unlocks the hard fixtures (alnum, symbols, secrets).
//
// Auto-colorspace and auto-calibrate are intentionally NOT applied here: on
// clean tight-crop fixture images they break the search by misdetecting the
// colorspace (selecting linear-light averaging for images produced with sRGB).
// They are useful only for real-world screenshots where the image provenance is
// unknown; callers who need them can reach unpixel.Recover directly with
// WithAutoColorspace()/WithAutoCalibrate()/WithAutoCrop().
func decodeEngine(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	opts := []unpixel.Option{
		unpixel.WithCharset(charsetFor(in.CharsetPreset)),
	}
	if in.BlockSize > 0 {
		opts = append(opts, unpixel.WithBlockSize(in.BlockSize))
	}
	if in.FontSize > 0 {
		// WithStyle replaces the whole Style; construct with FontSize only so
		// applyDefaults fills PaddingTop/PaddingLeft with the package defaults (8 px).
		opts = append(opts, unpixel.WithStyle(unpixel.Style{FontSize: in.FontSize}))
	}
	if in.MaxLength > 0 {
		opts = append(opts, unpixel.WithMaxLength(in.MaxLength))
	}
	if in.Denoise {
		opts = append(opts, unpixel.WithNormalize())
	}
	expFmt, expFmtOK := secrets.ParseFormat(in.ExpectedFormat)
	if expFmtOK && expFmt != secrets.FormatNone {
		opts = append(opts, unpixel.WithExpectedFormat(expFmt))
	}
	if len(in.fontData) > 0 {
		r, err := defaults.RendererFromFonts(in.fontData, nil)
		if err != nil {
			return DecodeResult{}, fmt.Errorf("engine: build renderer: %w", err)
		}
		opts = append(opts, unpixel.WithRenderer(r))
	}

	var notes []string
	if in.Denoise {
		notes = append(notes, "denoise applied")
	}
	if expFmtOK && expFmt != secrets.FormatNone {
		notes = append(notes, "expected_format="+in.ExpectedFormat+" applied")
	}

	// Font-prior sweep: when FontPriorTopK > 0, rank bundled fonts blind and
	// decode with the top-K best-ranked fonts, returning the winner.
	if in.FontPriorTopK > 0 {
		fopts := append([]unpixel.Option{}, opts...)
		fopts = append(fopts, unpixel.WithFontPriorTopK(in.FontPriorTopK))
		ranked, ferr := fontprior.RecoverWithPrior(ctx, img, fopts...)
		if ferr != nil {
			return DecodeResult{}, ferr
		}
		// RecoverWithPrior errors when all fonts fail, so ranked is non-empty.
		best := ranked[0]
		notes = append(notes, fmt.Sprintf("font prior top-%d; chose %s", in.FontPriorTopK, best.Font))
		return DecodeResult{
			Text:       best.Result.BestGuess,
			Distance:   best.Result.BestTotal,
			Fidelity:   best.Result.Fidelity(),
			Font:       best.Font,
			BlockSize:  in.BlockSize, // 0 when auto-detected; mirrors the single-font path
			MethodUsed: "engine",
			Notes:      notes,
		}, nil
	}

	res, err := unpixel.Recover(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{
		Text:       res.BestGuess,
		Distance:   res.BestTotal,
		Fidelity:   res.Fidelity(),
		BlockSize:  in.BlockSize, // 0 when auto-detected (caller did not pin it)
		MethodUsed: "engine",
		Notes:      notes,
	}, nil
}

func decodeMosaic(ctx context.Context, img image.Image, _ decodeInput) (DecodeResult, error) {
	res, err := mosaictext.Decode(ctx, img)
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		Font:       res.Font,
		BlockSize:  res.BlockSize,
		MethodUsed: "mosaic",
	}, nil
}

func decodeBlurred(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	var opts []unpixel.Option
	if in.Denoise {
		opts = append(opts, unpixel.WithNormalize())
	}
	res, err := unpixel.RecoverBlurred(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	notes := make([]string, 0, 2)
	if res.Normalized {
		notes = append(notes, "input normalization applied")
	}
	if res.L0Deblurred {
		notes = append(notes, "L0 deblur applied")
	}
	return DecodeResult{
		Text:       res.BestGuess,
		Distance:   1 - res.Fidelity(),
		Fidelity:   res.Fidelity(),
		MethodUsed: "blurred",
		Notes:      notes,
	}, nil
}

func decodeMonoHMM(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	opts := []mosaictext.HMMOption{
		mosaictext.WithLanguage(parseLang(in.Language)),
	}
	if len(in.fontData) > 0 {
		opts = append(opts, mosaictext.WithFontFile(in.fontData))
	}
	res, err := mosaictext.DecodeHMM(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		Font:       res.Font,
		BlockSize:  res.BlockSize,
		MethodUsed: "mono-hmm",
	}, nil
}

func decodeWindowHMM(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	var opts []mosaictext.WHMMOption
	if len(in.fontData) > 0 {
		opts = append(opts, mosaictext.WithWHMMFontFile(in.fontData))
	}
	res, err := mosaictext.DecodeWindowHMM(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		Font:       res.Font,
		BlockSize:  res.BlockSize,
		MethodUsed: "window-hmm",
	}, nil
}

func decodeTrainedHMM(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	opts := []mosaictext.THMMOption{
		mosaictext.WithTHMMLanguage(parseLang(in.Language)),
	}
	if len(in.fontData) > 0 {
		opts = append(opts, mosaictext.WithTHMMFontFile(in.fontData))
	}
	res, err := mosaictext.DecodeTrainedHMM(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		Font:       res.Font,
		BlockSize:  res.BlockSize,
		MethodUsed: "trained-hmm",
	}, nil
}

func decodeDID(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	opts := []mosaictext.DIDOption{
		mosaictext.WithDIDCharset(charsetFor(in.CharsetPreset)),
		mosaictext.WithDIDLanguage(parseLang(in.Language)),
	}
	if len(in.fontData) > 0 {
		opts = append(opts, mosaictext.WithDIDFontFile(in.fontData))
	}
	res, err := mosaictext.DecodeDID(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	notes := []string{fmt.Sprintf("emission evals: %d", res.EmissionEvals)}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		Font:       res.Font,
		BlockSize:  res.BlockSize,
		MethodUsed: "did",
		Notes:      notes,
	}, nil
}

func decodeVarFont(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	opts := []mosaictext.VarFontOption{}
	if in.KnownVisibleText != "" {
		opts = append(opts, mosaictext.WithVarFontText(in.KnownVisibleText))
	}
	res, err := mosaictext.DecodeVarFont(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	axes := make([]string, len(res.FittedAxes))
	for i, a := range res.FittedAxes {
		axes[i] = fmt.Sprintf("%s=%.1f", a.Tag, a.Value)
	}
	notes := []string{
		fmt.Sprintf("fitted axes: %s", strings.Join(axes, ", ")),
		fmt.Sprintf("evals: %d", res.Evals),
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		BlockSize:  res.BlockSize,
		MethodUsed: "varfont",
		Notes:      notes,
	}, nil
}

func decodePerspective(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	var opts []mosaictext.PerspectiveOption

	// Quad takes precedence over auto_quad. If neither is provided the decoder
	// will return an error (it requires one or the other).
	switch {
	case in.Quad != "":
		corners, pErr := parseQuad(in.Quad)
		if pErr != nil {
			return DecodeResult{}, fmt.Errorf("parse quad: %w", pErr)
		}
		opts = append(opts, mosaictext.WithPerspectiveQuad(corners))
		if in.AutoQuad {
			// Explicit quad wins; note the override for the caller.
			in.AutoQuad = false // suppress WithPerspectiveAutoQuad below
		}
	case in.AutoQuad:
		opts = append(opts, mosaictext.WithPerspectiveAutoQuad(in.AutoQuadTol))
	}

	if cs := charsetFor(in.CharsetPreset); cs != "" {
		opts = append(opts, mosaictext.WithPerspectiveCharset(cs))
	}
	if in.MaxLength > 0 {
		opts = append(opts, mosaictext.WithPerspectiveMaxLen(in.MaxLength))
	}
	if in.FontSize > 0 {
		opts = append(opts, mosaictext.WithPerspectiveFontSize(in.FontSize))
	}
	if in.BlockSize > 0 {
		opts = append(opts, mosaictext.WithPerspectiveBlockSize(in.BlockSize))
	}
	if in.BeamWidth > 0 {
		opts = append(opts, mosaictext.WithPerspectiveBeamWidth(in.BeamWidth))
	}
	if in.RectSizeW > 0 || in.RectSizeH > 0 {
		opts = append(opts, mosaictext.WithPerspectiveRectSize(in.RectSizeW, in.RectSizeH))
	}
	if in.Workers > 0 {
		opts = append(opts, mosaictext.WithPerspectiveWorkers(in.Workers))
	}
	if len(in.fontData) > 0 {
		opts = append(opts, mosaictext.WithPerspectiveFontFile(in.fontData))
	}

	var notes []string
	if in.Quad != "" && in.AutoQuad {
		notes = append(notes, "note: both quad and auto_quad were set; explicit quad was used")
	}

	res, err := mosaictext.DecodePerspective(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	notes = append(notes, fmt.Sprintf("inferred rect %dx%d px", res.RectW, res.RectH))
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		MethodUsed: "perspective",
		Notes:      notes,
	}, nil
}

func decodeReference(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	opts := []mosaictext.RefOption{
		mosaictext.WithRefLanguage(parseLang(in.Language)),
	}
	if cs := charsetFor(in.CharsetPreset); cs != "" {
		opts = append(opts, mosaictext.WithRefCharset(cs))
	}
	if len(in.fontData) > 0 {
		opts = append(opts, mosaictext.WithRefFontFile(in.fontData))
	}
	if in.KnownVisibleText != "" {
		opts = append(opts, mosaictext.WithRefVisibleText(in.KnownVisibleText))
	}
	res, err := mosaictext.DecodeReference(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	var notes []string
	if in.KnownVisibleText != "" {
		notes = append(notes, fmt.Sprintf("known_visible_text=%q recorded for font calibration", in.KnownVisibleText))
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		Font:       res.Font,
		BlockSize:  res.BlockSize,
		MethodUsed: "reference",
		Notes:      notes,
	}, nil
}

func decodeBlind(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	opts := []blind.Option{}
	switch strings.ToLower(in.Language) {
	case "fr":
		opts = append(opts, blind.WithLanguage(blind.French))
	default:
		opts = append(opts, blind.WithLanguage(blind.English))
	}
	res, err := blind.Recover(ctx, img, opts...)
	if err != nil {
		return DecodeResult{}, err
	}
	notes := []string{
		fmt.Sprintf("gamma: %s", res.Gamma),
		fmt.Sprintf("lines: %d", len(res.Lines)),
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Dist,
		Fidelity:   clampFidelity(res.Dist),
		Font:       res.Font,
		BlockSize:  res.Block,
		MethodUsed: "blind",
		Notes:      notes,
	}, nil
}

// ensembleEngineConfidence is the engine fidelity (1−BestTotal) above which the
// charset-aware engine result is trusted outright. The engine's fidelity is a
// reliable self-confidence signal — empirically ~1.0 on an exact recovery and
// ~0.0 when it collapses to a single garbage char — so this threshold cleanly
// separates "engine nailed it" from "engine abstained".
const ensembleEngineConfidence = 0.85

// decodeEnsemble combines the two complementary mosaic decoders — the charset-
// aware engine ([unpixel.Recover]) and the zero-config mosaic ([mosaictext.Decode]).
// Each recovers fixtures the other misses (engine needs an explicit charset for
// e.g. "Go2"/"x=1"; mosaic auto-calibrates better on plain words like "go"), and
// their distances live on incomparable scales (engine BestTotal in [0,1] vs
// block-MSE), so a lowest-distance merge is unsound and re-scoring via a single
// metric is unreliable (its calibration is looser than the decoders' own).
//
// Instead it uses the engine's trustworthy [0,1] fidelity as the arbiter: take
// the engine result when it is confident, otherwise fall back to the zero-config
// mosaic. When the engine abstains AND mosaic yields nothing, the low-confidence
// engine text (if any) is returned as a last resort.
func decodeEnsemble(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	eng, engErr := decodeEngine(ctx, img, in)
	if engErr == nil && eng.Text != "" && eng.Fidelity >= ensembleEngineConfidence {
		eng.MethodUsed = "ensemble(engine)"
		return eng, nil
	}

	mos, mosErr := decodeMosaic(ctx, img, in)
	if mosErr == nil && mos.Text != "" {
		mos.MethodUsed = "ensemble(mosaic)"
		mos.Notes = append(mos.Notes, fmt.Sprintf("engine abstained (fidelity %.2f); used zero-config mosaic", eng.Fidelity))
		return mos, nil
	}

	// Neither was confident: return the engine's best-effort text if it produced
	// one, else surface the most informative error.
	if engErr == nil && eng.Text != "" {
		eng.MethodUsed = "ensemble(engine,low-confidence)"
		return eng, nil
	}
	if engErr != nil {
		return DecodeResult{}, engErr
	}
	if mosErr != nil {
		return DecodeResult{}, mosErr
	}
	return DecodeResult{}, mosaictext.ErrNoContent
}

func decodeMultiFrame(ctx context.Context, img image.Image, in decodeInput) (DecodeResult, error) {
	frames := []mosaictext.Frame{{Img: img}}
	for _, fi := range in.Frames {
		frameImg, err := loadImage(fi.Path)
		if err != nil {
			return DecodeResult{}, fmt.Errorf("load frame %q: %w", fi.Path, err)
		}
		frames = append(frames, mosaictext.Frame{
			Img:     frameImg,
			OffsetX: fi.OffsetX,
			OffsetY: fi.OffsetY,
		})
	}
	res, err := mosaictext.DecodeMultiFrame(ctx, frames)
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{
		Text:       res.Text,
		Distance:   res.Distance,
		Fidelity:   clampFidelity(res.Distance),
		Font:       res.Font,
		BlockSize:  res.BlockSize,
		MethodUsed: "multi-frame",
		Notes:      []string{fmt.Sprintf("fused %d frames", len(frames))},
	}, nil
}

// ---- unpixel_job_result ----

// toolJobResult is the tool descriptor for unpixel_job_result.
var toolJobResult = &mcpsdk.Tool{
	Name: "unpixel_job_result",
	Description: "Polls the result of an async unpixel_decode job started with async=true. " +
		"Returns the DecodeResult once the job is done, or status='pending' while still running. " +
		"Call repeatedly (e.g. every 5 s) until status='done'. " +
		"On success the job is automatically removed from the registry (one-time retrieval). " +
		"NOT needed for synchronous decodes (async=false or omitted).",
}

// jobResultInput is the JSON input schema for unpixel_job_result.
type jobResultInput struct {
	// JobID is the job identifier returned by unpixel_decode async=true.
	JobID string `json:"job_id" jsonschema:"Job identifier returned by unpixel_decode with async=true"`
}

// jobResultOutput is the response from unpixel_job_result.
type jobResultOutput struct {
	// Status is "pending" when still running, "done" when complete.
	Status string `json:"status"`
	// Result is the DecodeResult; only populated when status="done".
	Result *DecodeResult `json:"result,omitzero"`
}

// handleJobResult is the tool handler for unpixel_job_result.
func handleJobResult(_ context.Context, _ *mcpsdk.CallToolRequest, in jobResultInput) (*mcpsdk.CallToolResult, jobResultOutput, error) {
	if in.JobID == "" {
		return errResult(fmt.Errorf("unpixel_job_result: job_id must not be empty")), jobResultOutput{}, nil
	}
	res, done, err := jobRegistry.retrieve(in.JobID, false)
	if err != nil {
		return errResult(fmt.Errorf("unpixel_job_result: %w", err)), jobResultOutput{}, nil
	}
	if !done {
		out := jobResultOutput{Status: "pending"}
		return toolJSON(out)
	}
	out := jobResultOutput{Status: "done", Result: &res}
	return toolJSON(out)
}

// ---- unpixel_job_cancel ----

// toolJobCancel is the tool descriptor for unpixel_job_cancel.
var toolJobCancel = &mcpsdk.Tool{
	Name: "unpixel_job_cancel",
	Description: "Cancels a running async unpixel_decode job. " +
		"After cancellation the job context is terminated and any in-progress decode is aborted. " +
		"Use when the result is no longer needed to free server resources. " +
		"NOT needed for synchronous decodes.",
}

// jobCancelInput is the JSON input schema for unpixel_job_cancel.
type jobCancelInput struct {
	// JobID is the job identifier returned by unpixel_decode async=true.
	JobID string `json:"job_id" jsonschema:"Job identifier returned by unpixel_decode with async=true"`
}

// jobCancelOutput is the response from unpixel_job_cancel.
type jobCancelOutput struct {
	// Cancelled is true when the job was successfully cancelled.
	Cancelled bool `json:"cancelled"`
	// Message provides human-readable confirmation.
	Message string `json:"message"`
}

// handleJobCancel is the tool handler for unpixel_job_cancel.
func handleJobCancel(_ context.Context, _ *mcpsdk.CallToolRequest, in jobCancelInput) (*mcpsdk.CallToolResult, jobCancelOutput, error) {
	if in.JobID == "" {
		return errResult(fmt.Errorf("unpixel_job_cancel: job_id must not be empty")), jobCancelOutput{}, nil
	}
	if err := jobRegistry.cancel(in.JobID); err != nil {
		return errResult(fmt.Errorf("unpixel_job_cancel: %w", err)), jobCancelOutput{}, nil
	}
	out := jobCancelOutput{
		Cancelled: true,
		Message:   fmt.Sprintf("job %s cancelled", in.JobID),
	}
	return toolJSON(out)
}

// clampFidelity converts a distance (MSE, lower=better) to fidelity (higher=better),
// clamped to [0, 1].
func clampFidelity(dist float64) float64 {
	f := 1 - dist
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}
