// Package unpixel reconstructs text hidden behind pixelation.
//
// UnPixel attempts to recover the original text from a pixelated (mosaic-blurred)
// image region. The algorithm works by rendering each candidate string into an
// off-screen image, re-applying the same block-average pixelation with the same
// grid origin, and measuring the pixel-level distance against the redacted region.
// A guided depth-first search explores the candidate space character by character,
// pruning branches whose cumulative score exceeds the configured threshold.
//
// # Usage
//
// Obtain or synthesise an image.Image containing the pixelated region, construct
// an Engine with New, and call Run. Import the defaults package for its side-effect
// to wire the standard renderer, pixelator, metric, and search strategy; without
// it the components must be supplied explicitly in Config.
//
//	import _ "github.com/oioio-space/unpixel/defaults" // wire default components
//
//	eng, err := unpixel.New(img, unpixel.Config{})
//	if err != nil { ... }
//	progCh, resCh := eng.Run(ctx)
//	unpixel.OnProgress(progCh, func(p unpixel.Progress) {
//	    fmt.Println(p.BestGuess)
//	})
//	for r := range resCh {
//	    fmt.Println(r.BestGuess, r.BestScore)
//	}
//
// UnPixel is a faithful Go port of Bishop Fox's unredacter (GPL-3.0).
package unpixel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/png" // register PNG decoding for RecoverReader/RecoverFile
	"io"
	"math"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/oioio-space/unpixel/internal/deblur"
	"github.com/oioio-space/unpixel/internal/imutil"
)

// Renderer renders candidate text to an RGBA image, placing a blue sentinel
// block immediately to the right of the text. It returns the image and the
// x-coordinate of that sentinel, which marks the text's right edge in pixels.
type Renderer interface {
	Render(text string, style Style) (img *image.RGBA, sentinelX int, err error)
}

// Pixelator replaces every blockSize×blockSize region of an image with the
// mean RGBA colour of that region. The originX and originY parameters align
// the grid so that it matches the original pixelation applied to the source.
type Pixelator interface {
	Pixelate(img *image.RGBA, originX, originY int) *image.RGBA
}

// Metric measures the visual distance between two same-sized RGBA images.
// Compare returns a value in [0, 1] where 0 means pixel-perfect identical.
type Metric interface {
	Compare(a, b *image.RGBA) float64
}

// Strategy runs the full character-by-character search over the candidate
// space and streams progress and results on the provided channels.
// Implementations may use different algorithms (guided DFS, beam search, etc.).
// Search must close neither channel; Engine.Run owns their lifecycle.
type Strategy interface {
	Search(ctx context.Context, redacted *image.RGBA, cfg Config, out chan<- Progress, results chan<- Result)
}

// Style describes the text rendering parameters that must match the
// typography of the original, pre-pixelated screenshot.
type Style struct {
	// FontSize is the font size in points. Defaults to 32.
	FontSize float64
	// Bold selects the bold font variant when true.
	Bold bool
	// PaddingTop is the top padding applied before the text, in pixels. Defaults to 8.
	PaddingTop int
	// PaddingLeft is the left padding applied before the text, in pixels. Defaults to 8.
	PaddingLeft int
	// LetterSpacing is extra horizontal space, in pixels, inserted after each
	// glyph — the equivalent of the CSS letter-spacing property. It may be
	// negative to tighten text (e.g. -0.2 to match a Consolas redaction). The
	// default 0 leaves glyph advances untouched and preserves kerning; a
	// non-zero value renders glyph-by-glyph without kerning.
	LetterSpacing float64
}

// Config holds all tunable parameters for an Engine. Every field is optional:
// zero values are replaced by the package defaults (see the Default* constants)
// when New is called. Component fields (Renderer, Pixelator, Metric, Strategy)
// are filled by importing the defaults package for its side-effect, or can be
// supplied directly for custom implementations.
type Config struct {
	// Renderer is the component that rasterises candidate strings to RGBA.
	// Defaults to the XImage renderer (wired by the defaults package).
	Renderer Renderer
	// Pixelator is the component that applies block-average pixelation.
	// Defaults to BlockAverage (wired by the defaults package).
	Pixelator Pixelator
	// Metric is the component that measures pixel-level image distance.
	// Defaults to Pixelmatch (wired by the defaults package).
	Metric Metric
	// Strategy is the component that drives the search algorithm.
	// Defaults to GuidedDFS (wired by the defaults package).
	Strategy Strategy

	// LanguageModel optionally scores a candidate string's linguistic
	// plausibility (higher = more plausible). When set, the final ranking breaks
	// ties between candidates of equal image distance toward plausible text — the
	// image cannot separate visually near-identical candidates (especially behind
	// heavy blur), but a language prior can. nil disables it (the default).
	LanguageModel func(string) float64

	// ThresholdFor returns the acceptance threshold for a given candidate
	// character. It defaults to a closure that returns SpaceThreshold for ' '
	// and Threshold for all other runes. Override to apply per-class thresholds
	// without modifying search logic.
	ThresholdFor func(rune) float64

	// Charset is the ordered set of candidate characters tried at each search
	// depth. Defaults to DefaultCharset (a–z plus space).
	Charset string

	// CharsetTopK, when > 0 and LanguageModel is set, prunes the per-position
	// charset to the K most-likely next characters (by the language prior) before
	// evaluating any — turning a wide charset (e.g. full ASCII) into K renders per
	// position. It trades a little recall (the true char must be in the top K) for
	// speed; 0 (the default) evaluates the whole charset and never loses recall.
	CharsetTopK int

	// Style controls the font size, weight, and padding used when rendering
	// candidates. Zero fields use the design defaults (32 pt, 8 px padding).
	Style Style

	// MaxLength is the maximum number of characters the search will attempt
	// before backtracking. Defaults to DefaultMaxLength.
	MaxLength int
	// BlockSize is the side length in pixels of each pixelation block. It must
	// match the block size used to produce the redacted image. When left
	// non-positive, New auto-detects it from the image via InferBlockSize,
	// falling back to DefaultBlockSize if the grid cannot be determined.
	BlockSize int
	// Threshold is the per-block score below which a non-space candidate is
	// accepted and the search descends deeper. Lower values are stricter.
	// Defaults to DefaultThreshold.
	Threshold float64
	// SpaceThreshold is the acceptance threshold applied specifically to the
	// space character, which is harder to distinguish visually. Defaults to
	// DefaultSpaceThreshold.
	SpaceThreshold float64
	// TopN is the maximum number of ranked candidates retained per grid offset
	// in Result.TopN. Candidates are sorted ascending by score (lowest first);
	// ties are broken by Guess string so results are deterministic. Zero or
	// negative values are replaced by DefaultTopN in applyDefaults.
	TopN int

	// BeamWidth is the maximum number of candidates retained per depth level
	// when using BeamStrategy. Values <= 0 are replaced by DefaultBeamWidth.
	// Only BeamStrategy reads this field; other strategies ignore it.
	BeamWidth int
	// CacheSize is the maximum number of stageImage results held by
	// CachingScorer. Zero disables prefix-render memoization; positive values
	// enable an LRU cache of that capacity shared across all Eval calls for a
	// single Search invocation. Defaults to DefaultCacheSize.
	CacheSize int
	// Workers is the maximum number of grid offsets probed and searched
	// concurrently. Values <= 0 default to runtime.GOMAXPROCS. Set to 1 to force
	// fully sequential execution. Results are merged deterministically regardless
	// of Workers, so this affects throughput only, never the output.
	Workers int

	// normalize, when non-nil, enables input normalisation before blur recovery.
	// Set via WithNormalize; never set directly (unexported so external struct
	// literals cannot accidentally include it, keeping the zero Config clean).
	normalize *deblur.Options

	// remosaic, when true, enables the Hill–Zhou–Saul–Shacham PETS-2016 §4
	// remosaic-as-error-correction path in RecoverBlurred. Set via WithRemosaic
	// or WithRemosaicGrid; never set directly.
	remosaic bool
	// remosaicGrid is the block size b for the remosaic operator. 0 means "auto:
	// choose b = max(2, round(σ))". Set via WithRemosaicGrid.
	remosaicGrid int
	// remosaicLinear selects linear-light block averaging in the remosaic operator.
	// Mirrors the gamma/linear flag used by the plain mosaic path.
	remosaicLinear bool

	// l0deblur, when non-nil, enables L0-regularised text deblurring as a
	// preprocessing step before the σ-search. Set via WithL0Deblur; never set
	// directly (unexported to keep the zero Config clean).
	l0deblur *deblur.L0Options
}

// Candidate-alphabet presets for Config.Charset (or WithCharset). A wider
// charset recovers more text but enlarges the search space, so prefer the
// narrowest one that fits the target.
const (
	// CharsetLower is lowercase ASCII letters plus space (the faithful default).
	CharsetLower = DefaultCharset
	// CharsetAlnum adds uppercase letters and digits to CharsetLower.
	CharsetAlnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	// CharsetASCII is every printable ASCII character (0x20–0x7E). Use it for
	// source code and other symbol-heavy text.
	CharsetASCII = " !\"#$%&'()*+,-./0123456789:;<=>?@" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"
)

// Default configuration values, matching the original unredacter reference implementation.
const (
	// DefaultCharset is the candidate alphabet: lowercase ASCII letters and space.
	DefaultCharset = "abcdefghijklmnopqrstuvwxyz "
	// DefaultMaxLength is the maximum candidate string length the search explores.
	DefaultMaxLength = 20
	// DefaultBlockSize is the pixelation block side length in pixels.
	DefaultBlockSize = 8
	// DefaultThreshold is the per-block score gate for non-space characters;
	// candidates scoring above this value are pruned.
	DefaultThreshold = 0.25
	// DefaultSpaceThreshold is the per-block score gate for the space character,
	// which is visually ambiguous and requires a more permissive threshold.
	DefaultSpaceThreshold = 0.5
	// DefaultTopN is the default maximum number of ranked candidates retained
	// per grid offset and exposed on Result.TopN.
	DefaultTopN = 5
	// DefaultBeamWidth is the number of candidates retained per depth level by
	// BeamStrategy. Larger values improve recall at the cost of more evaluations.
	DefaultBeamWidth = 16
	// DefaultCacheSize is the maximum number of stageImage results held by
	// CachingScorer. Zero disables caching.
	DefaultCacheSize = 4096
)

// Offset represents one candidate grid origin for the pixelation block alignment.
// The search probes multiple (X, Y) origins and ranks them by Score before
// committing to the full character-level search.
type Offset struct {
	// X is the horizontal grid origin in pixels.
	X int
	// Y is the vertical grid origin in pixels.
	Y int
	// Score is the image-distance score for this origin (lower is better).
	Score float64
}

// Eval holds the result of evaluating a single candidate string at one search node.
type Eval struct {
	// GuessImage is a cropped, pixelated rendering of Guess, populated only
	// when the caller opts in via the Strategy implementation.
	GuessImage *image.RGBA
	// Guess is the candidate string evaluated at this node.
	Guess string
	// Score is the marginal region diff score for the most recently added
	// character (lower is better; compared against the threshold).
	Score float64
	// TooBig is true when the rendered candidate is wider than the redacted
	// image; such candidates are pruned regardless of score.
	TooBig bool
}

// Result is the final output produced by Engine.Run for one surviving grid offset.
// One Result is sent per offset on the results channel returned by Run.
type Result struct {
	// Err is non-nil if the search for this offset aborted due to an error.
	Err error
	// BestGuess is the candidate string with the lowest overall score found
	// during the search rooted at this offset.
	BestGuess string
	// Candidates holds every string that passed the threshold gate during the
	// search, in discovery order.
	Candidates []Eval
	// TopN holds the best candidates sorted ascending by score (lowest score
	// first), with ties broken by Guess string for determinism. Its length is
	// at most Config.TopN. TopN[0] is the same candidate as BestGuess when the
	// search produces any result. TopN is nil when no candidates were found.
	TopN []Eval
	// Offset is the grid origin that was searched to produce this result.
	Offset Offset
	// BestScore is the marginal-region diff score of BestGuess (lower is better).
	BestScore float64
	// BestTotal is the whole-image distance of BestGuess against the redaction
	// (0 = pixel-perfect, 1 = worst). Unlike BestScore — which is a per-character
	// marginal score that a correct prefix or a coincidental match can drive to
	// ~0 — BestTotal measures how well the full guess explains the entire
	// redaction, so it is comparable across separate runs. That makes it the
	// right signal for choosing between candidate fonts or styles: the run with
	// the lowest BestTotal recovered the redaction most faithfully.
	BestTotal float64
	// Confidence is 1 − TopN[0].Score, giving a value in [0, 1] where 1
	// represents a pixel-perfect match. It is 0 when TopN is empty.
	Confidence float64
	// Ambiguity is TopN[1].Score − TopN[0].Score: the score gap between the
	// best and second-best candidates. A larger gap means the best guess is
	// more clearly distinguished from alternatives. It is 0 when len(TopN) < 2.
	Ambiguity float64
	// BlurSigma is the Gaussian-blur standard deviation (in pixels) used to
	// produce this result. It is set by RecoverBlurred and zero for all other
	// recovery paths (block-mosaic, manual Pixelator, etc.).
	BlurSigma float64
	// BelowThreshold is true when BestGuess was promoted from the best-seen
	// sub-threshold candidate because no candidate actually passed the
	// acceptance gate. When false (the normal case), BestGuess/TopN/Confidence
	// are computed entirely from candidates that passed the gate and are
	// byte-identical to previous behaviour.
	//
	// Per-offset edge case: when the global winner is above-threshold
	// (BelowThreshold=false), a non-winning offset's per-offset TopN may still
	// contain a sub-threshold best-seen candidate. Callers iterating all
	// per-offset results should not treat every per-offset TopN entry as
	// having passed the acceptance gate; check the per-offset BelowThreshold
	// field individually.
	BelowThreshold bool
	// Normalized is true when RecoverBlurred applied input normalisation
	// (via WithNormalize) before the σ-search. It is always false for other
	// recovery paths (Recover, mosaic, etc.) and when WithNormalize was not
	// passed. Use it to confirm the normalisation step was active.
	Normalized bool
	// L0Deblurred is true when RecoverBlurred applied L0-regularised text
	// deblurring (via WithL0Deblur) as a preprocessing step before the σ-search.
	// Always false for the mosaic path and when WithL0Deblur was not passed.
	L0Deblurred bool
}

// String returns a one-line human-readable summary of the result: the best
// guess with its score and confidence, or an error / "no result" note.
func (r Result) String() string {
	switch {
	case r.Err != nil:
		return "unpixel: error: " + r.Err.Error()
	case r.BestGuess == "":
		return "unpixel: no candidate recovered"
	default:
		return fmt.Sprintf("%q (score %.4f, confidence %.2f)", r.BestGuess, r.BestScore, r.Confidence)
	}
}

// Fidelity reports how well BestGuess reproduces the whole redaction, in [0, 1]
// (1 = pixel-perfect), as 1 − BestTotal. It is the honest confidence signal:
// unlike Confidence (derived from the marginal score, which a prefix or fluke
// drives to ~1), Fidelity reflects the whole-image match, so a low value means
// "this recovery is probably wrong / unrecoverable". It is 0 for an empty guess.
func (r Result) Fidelity() float64 {
	if r.BestGuess == "" {
		return 0
	}
	return max(0, min(1, 1-r.BestTotal))
}

// String returns the candidate and its marginal score, e.g. `"hello" (0.0123)`.
func (e Eval) String() string {
	return fmt.Sprintf("%q (%.4f)", e.Guess, e.Score)
}

// EventKind identifies the type of a Progress event delivered on the channel
// returned by Engine.Run.
type EventKind int

const (
	// EventCandidate is emitted for every candidate string evaluated during
	// the search. It is high-frequency; the channel is non-blocking and events
	// are dropped when the buffer is full.
	EventCandidate EventKind = iota
	// EventOffsetProbed is emitted after each grid-origin probe during the
	// offset-discovery phase. It is high-frequency with the same drop semantics
	// as EventCandidate.
	EventOffsetProbed
	// EventNewBest is emitted whenever the search finds a new overall best
	// guess. Delivery is guaranteed: the sender blocks (with context) until the
	// consumer reads the event.
	EventNewBest
	// EventDone is the terminal event, delivered exactly once after all offsets
	// have been searched or the context is cancelled. Delivery is guaranteed.
	EventDone
)

// Progress carries a snapshot of search state, streamed on the channel returned
// by Engine.Run. Consumers should switch on Kind to decide which fields are
// meaningful for a given event.
type Progress struct {
	// Err carries any terminal error that caused the search to abort.
	Err error

	// PreviewImage is an optional deep copy of the pixelated guess image.
	// It is non-nil only when the Strategy implementation populates it.
	PreviewImage *image.RGBA

	// BestGuess is the candidate string with the lowest score found so far
	// across all offsets.
	BestGuess string

	// Guess is the candidate string that triggered this specific event.
	Guess string
	// Offset is the grid origin currently being searched.
	Offset Offset
	// Kind identifies which event this Progress value represents.
	Kind EventKind

	// BestScore is the total image-distance score of BestGuess (lower is better).
	BestScore float64

	// Score is the image-distance score of Guess.
	Score float64

	// Depth is the current search depth, equal to the character length of Guess.
	Depth int
	// Evaluated is the cumulative number of candidates evaluated since Run started.
	Evaluated int
	// OffsetsDone is the number of grid offsets fully searched so far.
	OffsetsDone int
	// OffsetsTotal is the total number of surviving grid offsets to be searched.
	OffsetsTotal int

	// Elapsed is the wall-clock duration since Engine.Run was called.
	Elapsed time.Duration
	// Done is true only on the EventDone event.
	Done bool
}

// OnProgress drains the progress channel returned by Engine.Run, calling fn
// for every event in order. It blocks until the channel is closed (i.e. until
// EventDone has been delivered and consumed). It is a convenience wrapper
// around a range loop and does not start a goroutine.
func OnProgress(ch <-chan Progress, fn func(Progress)) {
	for p := range ch {
		fn(p)
	}
}

// Engine orchestrates the full unpixel pipeline: grid-origin discovery,
// candidate rendering, re-pixelation, distance measurement, and guided search.
// Create one with New and call Run to start the search.
type Engine struct {
	redacted *image.RGBA
	cfg      Config
	skewInfo SkewInfo
}

// Config returns the engine's resolved configuration, with scalar defaults
// applied and BlockSize auto-inferred. Component fields (Renderer, Pixelator,
// Metric, Strategy) are non-nil only after Run has wired them. It is intended
// for introspection — for example, reading the inferred BlockSize.
func (e *Engine) Config() Config { return e.cfg }

// New creates an Engine for the given pixelated image. Scalar zero values in
// cfg are replaced by package defaults; nil component fields are left for Run
// to fill via DefaultComponents (or must be set explicitly). New returns
// ErrNilImage if redacted is nil.
func New(redacted image.Image, cfg Config) (*Engine, error) {
	if redacted == nil {
		return nil, ErrNilImage
	}
	rgba := imutil.ToRGBA(redacted)
	// Auto-contrast: the renderer draws dark text on a light background, so a
	// dark-background image (e.g. a dark-mode screenshot) is inverted to match.
	// Light-background images are left untouched, so the faithful path is byte
	// identical to before.
	if darkBackground(rgba) {
		rgba = invertColors(rgba)
	}
	// Skew detection and deskew rectification: if the mosaic grid appears rotated
	// (low InferBlockGrid confidence), search for the correcting angle and apply
	// it only when the confidence gain clearly exceeds deskewMinGain and the
	// post-rotation confidence exceeds deskewMinConfidence. Axis-aligned images
	// are left byte-identical — the gate inside detectAndDeskew returns early
	// when the baseline confidence is already high.
	rgba, skewInfo, grid := detectAndDeskew(rgba)
	// Auto-detect the block size from the image when the caller left it unset,
	// reusing the grid detectAndDeskew already computed (Size matches
	// InferBlockSize); applyDefaults falls back to DefaultBlockSize when the grid
	// is undetectable (Size < 2).
	if cfg.BlockSize <= 0 && grid.Size >= 2 {
		cfg.BlockSize = grid.Size
	}
	cfg = applyDefaults(cfg)
	return &Engine{redacted: rgba, cfg: cfg, skewInfo: skewInfo}, nil
}

// InferDarkBackground reports whether img looks like dark text/content on a dark
// background (e.g. a dark-mode screenshot), judged from its border pixels. New
// uses it to decide whether to invert the image so it matches the dark-on-light
// rendering pipeline.
func InferDarkBackground(img image.Image) bool {
	return darkBackground(imutil.ToRGBA(img))
}

// darkBackground samples the image border (where the background usually shows)
// and returns true when its mean luminance is clearly dark.
func darkBackground(img *image.RGBA) bool {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return false
	}
	var sum, n float64
	add := func(x, y int) {
		c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
		sum += 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
		n++
	}
	for x := range w {
		add(x, 0)
		add(x, h-1)
	}
	for y := range h {
		add(0, y)
		add(w-1, y)
	}
	return sum/n < 96 // conservative: only clearly-dark borders trigger inversion
}

// invertColors returns a fresh image with every RGB channel inverted (alpha
// kept), turning light-on-dark into dark-on-light.
func invertColors(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			out.SetRGBA(x, y, color.RGBA{R: 255 - c.R, G: 255 - c.G, B: 255 - c.B, A: c.A})
		}
	}
	return out
}

// InferBlockSize estimates the pixelation block size, in pixels, of a mosaic-
// redacted image by detecting the spacing of its block grid. It scans for the
// columns and rows where the colour changes — the boundaries between constant-
// colour blocks — and returns the greatest common divisor of the gaps between
// them, which equals the block side length even when some adjacent blocks happen
// to share a colour (their gap is then a multiple of the block size).
//
// It returns 0 when the grid cannot be determined — a uniform image, an image
// smaller than 2×2, or a non-pixelated image whose gaps reduce to 1. New uses it
// to fill a non-positive Config.BlockSize, falling back to DefaultBlockSize when
// inference returns 0.
func InferBlockSize(img image.Image) int {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return 0
	}

	// The block size is the GCD of the gaps between colour-change boundaries; the
	// shared helpers in grid.go compute it the same way InferBlockGrid does.
	g := gcdOfGaps(
		detectBoundaryPositions(rgba, b, w, h, true),
		detectBoundaryPositions(rgba, b, w, h, false),
	)
	// A gap GCD of 1 means no regular grid (treat as undetectable).
	if g < 2 {
		return 0
	}
	return g
}

// InferBlockSizeRobust detects the mosaic block size using autocorrelation of
// the boundary signal, tolerating noisy or anti-aliased block edges caused by
// resampling, JPEG compression, or screenshot scaling.
//
// It builds a binary boundary signal (1 at each column/row where the colour
// changes appreciably, 0 elsewhere), computes its autocorrelation, and finds
// the dominant period in the range [minRobustPeriod, maxRobustPeriod]. The
// support score in [0, 1] reflects how consistently boundaries align with that
// period: 1.0 means every boundary falls exactly on a grid line; values above
// RobustSupportThreshold indicate a detectable periodic structure.
//
// When the exact-GCD result from InferBlockSize is available and its implied
// autocorrelation support exceeds the autocorrelation peak, the exact result
// wins (preserving byte-identical behaviour for clean mosaics). When the image
// is too small, uniform, or has no detectable period, it returns (0, 0).
func InferBlockSizeRobust(img image.Image) (blockSize int, support float64) {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return 0, 0
	}

	// Exact-GCD path uses the pixel-precise boundary detector (any difference).
	exactColPos := detectBoundaryPositions(rgba, b, w, h, true)
	exactRowPos := detectBoundaryPositions(rgba, b, w, h, false)

	// Robust autocorrelation path uses a threshold-filtered detector that
	// ignores gradients and JPEG noise, keeping only true block-edge transitions.
	robustColPos := robustBoundaryPositions(rgba, b, w, h, true)
	robustRowPos := robustBoundaryPositions(rgba, b, w, h, false)

	// Try the exact-GCD path first; it wins when clean boundaries are present.
	exact := gcdOfGaps(exactColPos, exactRowPos)
	exactSupport := 0.0
	if exact >= 2 {
		exactSupport = autocorrSupport(exactColPos, w, exact) +
			autocorrSupport(exactRowPos, h, exact)
		n := 2
		if len(exactColPos) == 0 {
			n--
		}
		if len(exactRowPos) == 0 {
			n--
		}
		if n > 0 {
			exactSupport /= float64(n)
		}
	}

	// Autocorrelation path: find the dominant period of the robust boundary signal.
	acPeriod, acSupport := dominantPeriod(robustColPos, w, robustRowPos, h)

	// Choose whichever gives higher support; exact-GCD wins ties.
	switch {
	case exact >= 2 && exactSupport >= acSupport:
		return exact, exactSupport
	case acPeriod >= 2 && acSupport > 0:
		return acPeriod, acSupport
	case exact >= 2:
		return exact, exactSupport
	default:
		return 0, 0
	}
}

// RobustSupportThreshold is the minimum autocorrelation support score (in
// [0, 1]) that InferBlockSizeRobust considers a detectable periodic block
// structure. A value of 0.5 means at least half of the boundary signal must
// align with the dominant period; below this the detector reports no grid.
const RobustSupportThreshold = 0.5

// minRobustPeriod is the smallest block size the robust detector will consider.
// Blocks smaller than 4 px are sub-pixel after moderate anti-aliasing.
const minRobustPeriod = 4

// maxRobustPeriodFrac is the upper bound on detectable period as a fraction of
// the image dimension. Blocks covering more than half the image cannot produce
// enough repetitions to establish a period reliably.
const maxRobustPeriodFrac = 0.5

// robustBoundaryThreshold is the minimum mean per-channel luminance delta
// (averaged across all rows/columns of the strip pair) required for a
// column/row transition to count as a real block boundary in the robust
// detector. After one round of box-blur (simulating resampling), a real mosaic
// edge of ~30 units spreads to ~10 units at the boundary column and ~5 at its
// neighbours; JPEG noise adds ±4 units raising the floor to ~3. A threshold of
// 5 sits between the noise floor (~3) and the blurred-edge peak (~7–10),
// correctly separating block boundaries from both random noise and smooth
// gradients (which produce uniform low deltas, not localised peaks).
const robustBoundaryThreshold = 5

// robustBoundaryPositions returns the positions where the mean absolute
// per-channel colour difference between adjacent column (columns=true) or row
// (columns=false) strips exceeds robustBoundaryThreshold. Unlike
// detectBoundaryPositions (which fires on any 1-pixel difference), this
// averages the difference across all rows/columns of the strip pair so that
// sharp mosaic edges (uniform colour change) score high while smooth gradients
// and JPEG noise score low.
func robustBoundaryPositions(img *image.RGBA, b image.Rectangle, w, h int, columns bool) []int {
	var positions []int
	if columns {
		for x := 1; x < w; x++ {
			if columnMeanDelta(img, b, x, h) >= robustBoundaryThreshold {
				positions = append(positions, x)
			}
		}
	} else {
		for y := 1; y < h; y++ {
			if rowMeanDelta(img, b, y, w) >= robustBoundaryThreshold {
				positions = append(positions, y)
			}
		}
	}
	return positions
}

// columnMeanDelta returns the mean absolute per-channel difference between
// column x and column x-1, averaged over all rows.
func columnMeanDelta(img *image.RGBA, b image.Rectangle, x, h int) float64 {
	sum := 0.0
	for y := range h {
		ca := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
		cb := img.RGBAAt(b.Min.X+x-1, b.Min.Y+y)
		dr := absDiff(ca.R, cb.R)
		dg := absDiff(ca.G, cb.G)
		db := absDiff(ca.B, cb.B)
		sum += float64(dr+dg+db) / 3
	}
	if h == 0 {
		return 0
	}
	return sum / float64(h)
}

// rowMeanDelta returns the mean absolute per-channel difference between row y
// and row y-1, averaged over all columns.
func rowMeanDelta(img *image.RGBA, b image.Rectangle, y, w int) float64 {
	sum := 0.0
	for x := range w {
		ca := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
		cb := img.RGBAAt(b.Min.X+x, b.Min.Y+y-1)
		dr := absDiff(ca.R, cb.R)
		dg := absDiff(ca.G, cb.G)
		db := absDiff(ca.B, cb.B)
		sum += float64(dr+dg+db) / 3
	}
	if w == 0 {
		return 0
	}
	return sum / float64(w)
}

// absDiff returns |a - b| for uint8 values without wrapping.
func absDiff(a, b uint8) uint8 {
	if a >= b {
		return a - b
	}
	return b - a
}

// autocorrSupport returns the normalised autocorrelation value at the given
// period for a boundary signal defined by positions in [0, dim).
//
// Normalisation: R[period] / (density * (dim-period)), where density is the
// fraction of positions per unit length (R[0]/dim). This gives 1.0 when every
// boundary position has a matching one exactly period later, regardless of how
// many boundaries there are or how long the signal is.
func autocorrSupport(positions []int, dim, period int) float64 {
	if len(positions) == 0 || period <= 0 || period >= dim {
		return 0
	}
	sig := make([]float64, dim)
	for _, p := range positions {
		if p >= 0 && p < dim {
			sig[p] = 1
		}
	}
	r0 := dotSelf(sig)
	if r0 == 0 {
		return 0
	}
	rp := dotLag(sig, period)
	// Expected value of R[period] under uniform random placement:
	//   E[R[period]] = (r0/dim)*(dim-period)
	// A perfect periodic signal achieves rp = r0 (every boundary has a partner),
	// so we normalise by r0 to get 1.0 for a perfect grid.
	// Adjust for edge-of-signal shortfall: positions near the end have no partner.
	norm := r0 * float64(dim-period) / float64(dim)
	if norm == 0 {
		return 0
	}
	return rp / norm
}

// dominantPeriod finds the lag in [minRobustPeriod, maxRobustPeriodFrac*dim]
// with the highest average normalised autocorrelation across the robust column
// and row boundary signals. Returns (0, 0) when no signal exists.
func dominantPeriod(colPos []int, w int, rowPos []int, h int) (period int, support float64) {
	if len(colPos) == 0 && len(rowPos) == 0 {
		return 0, 0
	}

	colSig := boundarySignal(colPos, w)
	rowSig := boundarySignal(rowPos, h)
	colR0 := dotSelf(colSig)
	rowR0 := dotSelf(rowSig)

	maxLag := int(float64(max(w, h)) * maxRobustPeriodFrac)
	bestPeriod, bestSupp := 0, 0.0

	// If maxLag < minRobustPeriod the image is too small to contain repeated
	// periods; the loop body never executes and (0, 0) is returned intentionally.
	for lag := minRobustPeriod; lag <= maxLag; lag++ {
		supp := 0.0
		n := 0
		if colR0 > 0 && lag < w {
			norm := colR0 * float64(w-lag) / float64(w)
			if norm > 0 {
				supp += dotLag(colSig, lag) / norm
				n++
			}
		}
		if rowR0 > 0 && lag < h {
			norm := rowR0 * float64(h-lag) / float64(h)
			if norm > 0 {
				supp += dotLag(rowSig, lag) / norm
				n++
			}
		}
		if n == 0 {
			continue
		}
		supp /= float64(n)
		if supp > bestSupp {
			bestSupp = supp
			bestPeriod = lag
		}
	}
	return bestPeriod, bestSupp
}

// boundarySignal returns a float64 slice of length dim with 1.0 at each
// boundary position and 0 elsewhere.
func boundarySignal(positions []int, dim int) []float64 {
	sig := make([]float64, dim)
	for _, p := range positions {
		if p >= 0 && p < dim {
			sig[p] = 1
		}
	}
	return sig
}

// dotSelf returns the dot product of sig with itself (sum of squares).
func dotSelf(sig []float64) float64 {
	s := 0.0
	for _, v := range sig {
		s += v * v
	}
	return s
}

// dotLag returns the unnormalised autocorrelation of sig at the given lag:
// Σ sig[i]*sig[i+lag] for i in [0, len(sig)-lag).
func dotLag(sig []float64, lag int) float64 {
	s := 0.0
	n := len(sig) - lag
	for i := range n {
		s += sig[i] * sig[i+lag]
	}
	return s
}

// columnDiffers reports whether column x differs from column x-1 at any row.
func columnDiffers(img *image.RGBA, b image.Rectangle, x, h int) bool {
	for y := range h {
		if img.RGBAAt(b.Min.X+x, b.Min.Y+y) != img.RGBAAt(b.Min.X+x-1, b.Min.Y+y) {
			return true
		}
	}
	return false
}

// rowDiffers reports whether row y differs from row y-1 at any column.
func rowDiffers(img *image.RGBA, b image.Rectangle, y, w int) bool {
	for x := range w {
		if img.RGBAAt(b.Min.X+x, b.Min.Y+y) != img.RGBAAt(b.Min.X+x, b.Min.Y+y-1) {
			return true
		}
	}
	return false
}

// gcd returns the greatest common divisor of a and b (gcd(0, k) == k).
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// InferBlurSigma estimates the Gaussian-blur standard deviation (in pixels) of a
// blurred image using an edge-spread formula with a density-adaptive gradient
// percentile. The approach is purely spatial — no FFT — and is calibrated for
// both sparse step-edge inputs (large images, small σ) and dense text images
// (small images, large σ) (Polyblur / Chen & Ma gradient-ratio insight, Hill
// 2016 redaction paper).
//
// # Method
//
// For a Gaussian-blurred step edge, the peak gradient is contrast/(σ·√(2π)).
// Inverting: σ = contrast/(gPeak·√(2π)). The challenge is robustly estimating
// gPeak from the gradient distribution:
//
//   - A fixed high percentile (e.g. 99th) over-selects for dense-edge images
//     (text) but under-selects for sparse-edge images (single step in a large
//     field), landing on the gradient shoulder rather than the peak.
//   - A fixed very-high percentile (e.g. 99.9th) works for sparse edges but
//     over-selects for dense-edge text images, picking noise outliers.
//
// The solution: measure the edge density (fraction of gradient pairs above 5%
// of contrast), and adaptively choose the percentile as:
//
//	pct = clamp(1 − edgeFrac × 0.05, 0.95, 0.999)
//
// This keeps the selected gradient within the true-peak region of the edge
// distribution regardless of whether edges cover 1% (large step) or 20%
// (dense text) of the image area. The constant 0.05 is calibrated to within
// ±35% accuracy for σ ∈ [1,8] on both step-edge and rendered-text inputs.
//
// It returns 0 when the image is too small or essentially flat (contrast < 8).
// A returned value near 0 means the image is sharp (probably not blurred); a
// larger value is the estimated blur radius. The estimate is a starting point
// for the σ-sweep in RecoverBlurred — accuracy within ±35% is sufficient.
func InferBlurSigma(img image.Image) float64 {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 3 || h < 3 {
		return 0
	}

	lum := func(x, y int) float64 {
		c := rgba.RGBAAt(b.Min.X+x, b.Min.Y+y)
		return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
	}

	// Collect 4-connected absolute gradient magnitudes; track contrast and the
	// fraction of pairs above 5% of contrast (edge density).
	var minL, maxL float64 = 255, 0
	for y := range h {
		for x := range w {
			l := lum(x, y)
			minL = min(minL, l)
			maxL = max(maxL, l)
		}
	}
	contrast := maxL - minL
	if contrast < 8 {
		return 0 // essentially flat — nothing to estimate
	}
	threshold := contrast * 0.05 // 5% of contrast distinguishes edge from background

	grads := make([]float64, 0, w*h*2)
	aboveThresh := 0
	for y := range h {
		for x := range w {
			l := lum(x, y)
			if x > 0 {
				d := math.Abs(l - lum(x-1, y))
				grads = append(grads, d)
				if d > threshold {
					aboveThresh++
				}
			}
			if y > 0 {
				d := math.Abs(l - lum(x, y-1))
				grads = append(grads, d)
				if d > threshold {
					aboveThresh++
				}
			}
		}
	}
	if len(grads) == 0 {
		return 0
	}

	// Density-adaptive percentile: sample closer to the true peak gradient for
	// sparse edges (large pct) and accept the natural sampling for dense edges
	// (smaller pct). k=0.05 is calibrated to within ±35% accuracy for σ ∈ [1,8]
	// on both step-edge and rendered-text inputs.
	edgeFrac := float64(aboveThresh) / float64(len(grads))
	pct := 1.0 - edgeFrac*0.05
	pct = max(0.95, min(0.999, pct))

	slices.Sort(grads)
	idx := int(float64(len(grads)) * pct)
	if idx >= len(grads) {
		idx = len(grads) - 1
	}
	gPeak := grads[idx]
	if gPeak <= 0 {
		return 0
	}

	// Edge-spread inversion: σ = contrast / (gPeak · √(2π)).
	return contrast / (gPeak * math.Sqrt(2*math.Pi))
}

// InferFontSize estimates the point size of the text in img from its content
// height, for zero-config calibration: rendered text spans about 0.92×fontSize
// vertically (ascenders to descenders), so fontSize ≈ contentHeight / 0.92.
// "Content" is any row that differs from the image's background colour.
//
// It returns 0 when no content stands out. The estimate is a ballpark — a short
// word without ascenders/descenders, or blur spreading the glyphs, skews it — so
// it is best used to seed the search (optionally with a small size sweep) rather
// than as an exact value. Measure it on the redacted region (see LocateRedaction)
// or on a screenshot's sharp reference text, whichever is cleaner.
func InferFontSize(img image.Image) float64 {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return 0
	}
	bg := rgba.RGBAAt(b.Min.X, b.Min.Y) // top-left corner ≈ background
	isContent := func(x, y int) bool {
		c := rgba.RGBAAt(b.Min.X+x, b.Min.Y+y)
		d := abs(int(c.R)-int(bg.R)) + abs(int(c.G)-int(bg.G)) + abs(int(c.B)-int(bg.B))
		return d > 48
	}
	top, bot := -1, -1
	for y := range h {
		for x := range w {
			if isContent(x, y) {
				if top < 0 {
					top = y
				}
				bot = y
				break
			}
		}
	}
	if top < 0 {
		return 0
	}
	const ascDescRatio = 0.92
	return float64(bot-top+1) / ascDescRatio
}

// impulseNoiseThreshold is the minimum BT.601 luminance deviation from the
// neighbourhood median that must hold for a pixel to be a candidate impulse.
// The value 80 (on a 0–255 scale, i.e. ~31 %) was chosen to:
//   - comfortably exceed normal block-edge gradients in 8-pixel mosaic images
//     (which are ≤ 60 on the typical redaction palette), so structured edges
//     are never counted as impulses; and
//   - catch genuine salt-and-pepper spikes (absolute black/white against a
//     mid-grey background), which have deviation ≥ 100.
//
// Tune by comparing InferImpulseNoise on clean mosaic fixtures (must stay
// below autoDenoiseThreshold) and on salt-and-pepper test images (must be
// clearly above it).
const impulseNoiseThreshold = 80

// lum601 is the BT.601 luma of an 8-bit RGB pixel, in [0, 255]. Small enough to
// inline, so callers in pixel loops pay no call overhead.
func lum601(r, g, b uint8) int {
	return (299*int(r) + 587*int(g) + 114*int(b)) / 1000
}

// InferImpulseNoise estimates the fraction [0, 1] of impulse-corrupted pixels
// in img. It is a cheap sampled estimate (O(samples), ≤ 50 000 pixels checked)
// used by blind.Recover to decide whether to apply a median pre-filter
// automatically before block-size detection and decoding.
//
// Method (RVIN / local-extremum test from the SAPN literature): for each
// sampled interior pixel p, collect the BT.601 luminance of its 8 neighbours.
// Pixel p is counted as an impulse if both of the following hold:
//
//  1. |lum(p) – median8| > impulseNoiseThreshold (large deviation from context).
//  2. lum(p) is the strict minimum OR strict maximum among the 9 values in the
//     3×3 window (isolated spike, not part of a ramp).
//
// Condition 2 rejects smooth gradients and structured block edges — an edge
// pixel sits between two blocks whose colours straddle its own, so it is never
// the strict extremum of its 3×3 neighbourhood.
//
// Returns impulse_count / samples_checked. An empty or sub-3×3 image returns 0.
func InferImpulseNoise(img image.Image) float64 {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 3 || h < 3 {
		return 0
	}

	// Work directly on the pixel buffer to avoid closure heap-escapes and
	// interface dispatch through RGBAAt. Each pixel is 4 bytes (R,G,B,A) at
	// offset y*stride + x*4 from the start of the slice.
	pix := rgba.Pix
	stride := rgba.Stride
	// base is the byte offset of the top-left corner (b.Min) in pix.
	base := rgba.PixOffset(b.Min.X, b.Min.Y)

	// Stride: sample at most ~50 000 interior pixels. Interior pixels skip the
	// 1-pixel border so every sampled pixel has a full 8-neighbour window.
	iw, ih := w-2, h-2 // interior dimensions
	totalInterior := iw * ih
	const maxSamples = 50_000
	step := 1
	if totalInterior > maxSamples {
		step = int(math.Sqrt(float64(totalInterior)/maxSamples)) + 1
	}

	var impulses, sampled int
	for iy := 1; iy < h-1; iy += step {
		for ix := 1; ix < w-1; ix += step {
			off := base + iy*stride + ix*4
			lp := lum601(pix[off], pix[off+1], pix[off+2])

			// Gather 8-neighbour luminances into a fixed array (stack, no alloc).
			var ns [8]int
			ni := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					noff := base + (iy+dy)*stride + (ix+dx)*4
					ns[ni] = lum601(pix[noff], pix[noff+1], pix[noff+2])
					ni++
				}
			}

			// Insertion-sort ns to find the upper-median (position 4 of 8).
			for j := 1; j < 8; j++ {
				v := ns[j]
				k := j - 1
				for k >= 0 && ns[k] > v {
					ns[k+1] = ns[k]
					k--
				}
				ns[k+1] = v
			}
			m := ns[4]

			sampled++
			if abs(lp-m) <= impulseNoiseThreshold {
				continue
			}
			// Check strict min/max in the 3×3 window (9 values including p).
			isMin, isMax := true, true
			for _, n := range ns {
				if n < lp {
					isMax = false
				}
				if n > lp {
					isMin = false
				}
			}
			if isMin || isMax {
				impulses++
			}
		}
	}
	if sampled == 0 {
		return 0
	}
	return float64(impulses) / float64(sampled)
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// LocateRedaction returns the bounding box of a blurred region inside img — for
// zero-config recovery of a redaction embedded in a larger screenshot. A blurred
// edge has a bounded peak gradient (≈ contrast/(σ·√(2π)), far below sharp text),
// so a row/column is "blurred content" when it carries contrast yet its peak
// luminance gradient stays low. The result is the tight box of the largest such
// band; ok is false when no blurred region stands out (e.g. an all-sharp image).
//
// It is the missing piece for screenshots: estimating sigma or recovering on the
// whole image is skewed by sharp surrounding text (on the Bishop Fox challenge,
// whole-image σ≈0.6 vs ≈5.6 on the blurred line), so crop to this box first.
func LocateRedaction(img image.Image) (image.Rectangle, bool) {
	rgba := imutil.ToRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 4 || h < 4 {
		return image.Rectangle{}, false
	}
	lum := func(x, y int) int {
		c := rgba.RGBAAt(b.Min.X+x, b.Min.Y+y)
		return (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
	}

	const (
		contrastMin = 24 // a row/column carries content when its range exceeds this
		blurMaxPeak = 90 // a blurred edge's peak gradient stays below this (sharp ≫)
	)

	// Classify each row as blurred-content: has contrast but a low peak gradient.
	blurred := make([]bool, h)
	for y := range h {
		lo, hi, peak, prev := 255, 0, 0, lum(0, y)
		for x := range w {
			v := lum(x, y)
			lo, hi = min(lo, v), max(hi, v)
			if d := v - prev; d > peak || -d > peak {
				peak = max(d, -d)
			}
			prev = v
		}
		blurred[y] = (hi-lo) >= contrastMin && peak < blurMaxPeak
	}

	// Largest contiguous run of blurred rows → vertical band.
	y0, y1 := longestRun(blurred)
	if y1 <= y0 {
		return image.Rectangle{}, false
	}

	// Horizontal extent: columns within the band that carry contrast.
	x0, x1 := w, 0
	for x := range w {
		lo, hi := 255, 0
		for y := y0; y < y1; y++ {
			v := lum(x, y)
			lo, hi = min(lo, v), max(hi, v)
		}
		if hi-lo >= contrastMin {
			x0, x1 = min(x0, x), max(x1, x+1)
		}
	}
	if x1 <= x0 {
		x0, x1 = 0, w
	}
	return image.Rect(b.Min.X+x0, b.Min.Y+y0, b.Min.X+x1, b.Min.Y+y1), true
}

// longestRun returns [start,end) of the longest run of true values in flags.
func longestRun(flags []bool) (start, end int) {
	bestLen, curStart := 0, 0
	for i := 0; i <= len(flags); i++ {
		if i < len(flags) && flags[i] {
			continue
		}
		if i-curStart > bestLen {
			bestLen, start, end = i-curStart, curStart, i
		}
		curStart = i + 1
	}
	return start, end
}

// ErrNilImage is returned by New when a nil image is passed.
var ErrNilImage = errors.New("redacted image must not be nil")

// DefaultComponents is a hook that wires the standard Renderer, Pixelator,
// Metric, and Strategy into cfg for any fields that are still nil. It is
// populated by importing the defaults package for its side-effect, which avoids
// an import cycle between the root package and its internal implementations.
// Applications that supply all component fields explicitly need not set this.
var DefaultComponents func(cfg *Config) error

// DefaultBlurStrategy is a hook that returns the default Strategy used by
// RecoverBlurred when the caller has not supplied one via WithStrategy. It is
// populated by importing the defaults package for its side-effect.
//
// The default is BeamStrategy: beam search bounds the per-level branching
// factor to DefaultBeamWidth candidates, giving O(length × width) evaluations
// regardless of charset size — critical for longer words (≥5 chars) behind
// blur, where per-character image signal is too weak for guided DFS's unbounded
// expansion to finish in reasonable time. Callers that need full recall
// (at higher cost) can override with WithStrategy(defaults.GuidedStrategy()).
var DefaultBlurStrategy func() Strategy

// Run starts the search and returns a progress channel and a results channel.
// The progress channel carries Progress events and is closed after EventDone is
// delivered. The results channel receives one Result per surviving grid offset
// and is closed once all offsets have been searched. Cancelling ctx causes both
// channels to be closed promptly after the in-flight search step completes.
//
// If any component field in cfg is nil, Run invokes DefaultComponents to fill
// it before starting. If DefaultComponents is also nil, Run panics with a
// descriptive message directing the caller to import the defaults package.
func (e *Engine) Run(ctx context.Context) (<-chan Progress, <-chan Result) {
	if e.cfg.Renderer == nil || e.cfg.Pixelator == nil || e.cfg.Metric == nil || e.cfg.Strategy == nil {
		if DefaultComponents != nil {
			// DefaultComponents (defaults.Wire) fills only the nil fields, so a
			// partially-configured Config keeps the components it already has.
			if err := DefaultComponents(&e.cfg); err != nil {
				progCh := make(chan Progress, 1)
				resultCh := make(chan Result, 1)
				progCh <- Progress{Kind: EventDone, Done: true, Err: err}
				close(progCh)
				close(resultCh)
				return progCh, resultCh
			}
		} else {
			panic("unpixel: Engine.Run called with a nil component and no DefaultComponents wired; " +
				"import github.com/oioio-space/unpixel/defaults or set all component fields explicitly")
		}
	}

	progCh := make(chan Progress, 64)
	resultCh := make(chan Result, 8)

	go func() {
		defer close(progCh)
		defer close(resultCh)
		e.cfg.Strategy.Search(ctx, e.redacted, e.cfg, progCh, resultCh)
	}()

	return progCh, resultCh
}

// applyDefaults fills in zero values in cfg with package defaults.
func applyDefaults(cfg Config) Config {
	if cfg.Charset == "" {
		cfg.Charset = DefaultCharset
	}
	if cfg.MaxLength == 0 {
		cfg.MaxLength = DefaultMaxLength
	}
	if cfg.BlockSize == 0 {
		cfg.BlockSize = DefaultBlockSize
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.SpaceThreshold == 0 {
		cfg.SpaceThreshold = DefaultSpaceThreshold
	}
	if cfg.TopN <= 0 {
		cfg.TopN = DefaultTopN
	}
	if cfg.ThresholdFor == nil {
		// Capture the resolved thresholds so the closure is independent of
		// further mutations to cfg.
		threshold := cfg.Threshold
		spaceThreshold := cfg.SpaceThreshold
		cfg.ThresholdFor = func(ch rune) float64 {
			if ch == ' ' {
				return spaceThreshold
			}
			return threshold
		}
	}
	if cfg.Style.FontSize == 0 {
		cfg.Style.FontSize = 32
	}
	if cfg.Style.PaddingTop == 0 {
		cfg.Style.PaddingTop = 8
	}
	if cfg.Style.PaddingLeft == 0 {
		cfg.Style.PaddingLeft = 8
	}
	if cfg.BeamWidth <= 0 {
		cfg.BeamWidth = DefaultBeamWidth
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
	}
	if cfg.CacheSize == 0 {
		cfg.CacheSize = DefaultCacheSize
	}
	return cfg
}

// Option configures a Config for the high-level Recover helpers. Options are
// applied in order, so a later option overrides an earlier one; WithConfig
// (which replaces the whole Config) is therefore best passed first.
type Option func(*Config)

// WithConfig uses cfg as the starting point before any other options are
// applied. Pass it first to layer further options on top of a full Config.
func WithConfig(cfg Config) Option { return func(c *Config) { *c = cfg } }

// WithCharset sets Config.Charset (the ordered candidate alphabet).
func WithCharset(charset string) Option { return func(c *Config) { c.Charset = charset } }

// WithMaxLength sets Config.MaxLength (the maximum candidate length).
func WithMaxLength(n int) Option { return func(c *Config) { c.MaxLength = n } }

// WithBlockSize sets Config.BlockSize. A non-positive value lets New auto-detect
// the block size from the image (see InferBlockSize).
func WithBlockSize(n int) Option { return func(c *Config) { c.BlockSize = n } }

// WithThreshold sets Config.Threshold (the per-block acceptance gate).
func WithThreshold(t float64) Option { return func(c *Config) { c.Threshold = t } }

// WithStyle sets Config.Style (font size, weight, and padding). It must match
// the style of the redacted text for recovery to succeed.
func WithStyle(s Style) Option { return func(c *Config) { c.Style = s } }

// WithTopN sets Config.TopN (ranked candidates retained per offset).
func WithTopN(n int) Option { return func(c *Config) { c.TopN = n } }

// WithWorkers sets Config.Workers (max grid offsets searched concurrently;
// non-positive means runtime.GOMAXPROCS).
func WithWorkers(n int) Option { return func(c *Config) { c.Workers = n } }

// WithStrategy sets Config.Strategy (the search algorithm).
func WithStrategy(s Strategy) Option { return func(c *Config) { c.Strategy = s } }

// WithRenderer sets Config.Renderer (the text rasteriser). Use it with a
// custom font, e.g. defaults.RendererFromFonts, to match the typeface of the
// redacted image.
func WithRenderer(r Renderer) Option { return func(c *Config) { c.Renderer = r } }

// WithMetric sets Config.Metric (the image-distance metric).
func WithMetric(m Metric) Option { return func(c *Config) { c.Metric = m } }

// WithPixelator sets Config.Pixelator (the redaction operator the search
// reproduces). Use it with defaults.GaussianBlur(sigma) and WithBlockSize(1) to
// recover blurred text instead of mosaic pixelation.
func WithPixelator(p Pixelator) Option { return func(c *Config) { c.Pixelator = p } }

// WithLanguageModel sets Config.LanguageModel, a plausibility scorer used to
// break ties between candidates of equal image distance toward plausible text.
func WithLanguageModel(score func(string) float64) Option {
	return func(c *Config) { c.LanguageModel = score }
}

// WithCharsetTopK sets Config.CharsetTopK: with a LanguageModel set, evaluate
// only the k most-likely next characters per position (k<=0 disables pruning).
func WithCharsetTopK(k int) Option { return func(c *Config) { c.CharsetTopK = k } }

// WithBeamWidth sets Config.BeamWidth (candidates retained per depth level by
// BeamStrategy). Values ≤ 0 defer to DefaultBeamWidth. Only BeamStrategy reads
// this field; other strategies ignore it.
func WithBeamWidth(width int) Option { return func(c *Config) { c.BeamWidth = width } }

// WithNormalize enables input normalisation before blur recovery. It is opt-in
// and only affects RecoverBlurred; Recover and the mosaic path are unchanged.
//
// When called with no arguments, sensible defaults are used:
//   - BgDivide: divide out a multiplicative illumination estimate (vignette).
//   - InvertAuto: invert when the image is predominantly dark (dark-theme support).
//   - Stretch: false — enable a 1st/99th-percentile contrast stretch with
//     o.Stretch = true.
//   - Deblock: 0 (off) — set to -1 (auto) or a positive radius to median-filter
//     JPEG blocking.
//   - Binarize: false.
//
// Pass [deblur.Options] values to customise individual steps, e.g.:
//
//	unpixel.WithNormalize(func(o *deblur.Options) { o.Deblock = 2 })
//
// Result.Normalized is set to true when normalisation was applied.
func WithNormalize(fns ...func(*deblur.Options)) Option {
	return func(c *Config) {
		opts := deblur.DefaultOptions()
		for _, fn := range fns {
			fn(&opts)
		}
		c.normalize = &opts
	}
}

// WithRemosaic enables the Hill–Zhou–Saul–Shacham PETS-2016 §4 remosaic path
// inside [RecoverBlurred]. When active the forward operator becomes:
//
//	render(candidate) → GaussianBlur(σ) → BlockAverage(b)
//
// and the target image is pre-mosaiced by BlockAverage(b) once before the
// search. The mosaic stage collapses small pixel-level differences caused by
// σ-mismatch or JPEG compression noise, making the comparison more robust than
// plain GaussianBlur alone.
//
// The grid size b is chosen automatically as max(2, round(σ)); use
// [WithRemosaicGrid] to override it. The linear-light block average is used when
// [WithRemosaicLinear] is passed instead of WithRemosaic; by default sRGB means
// are used (matching the plain mosaic path).
//
// WithRemosaic has no effect on [Recover] or the mosaic path.
func WithRemosaic() Option {
	return func(c *Config) { c.remosaic = true }
}

// WithRemosaicGrid enables the remosaic path (like [WithRemosaic]) and
// additionally pins the block grid size to b pixels. b ≤ 0 selects auto
// (b = max(2, round(σ)), same as WithRemosaic).
func WithRemosaicGrid(b int) Option {
	return func(c *Config) {
		c.remosaic = true
		c.remosaicGrid = b
	}
}

// WithRemosaicLinear enables the remosaic path and uses linear-light block
// averaging instead of the default sRGB means. Use it when the target was
// redacted by a GEGL/GIMP-based tool that computes its block average in linear
// light.
func WithRemosaicLinear() Option {
	return func(c *Config) {
		c.remosaic = true
		c.remosaicLinear = true
	}
}

// WithL0Deblur enables L0-regularised text deblurring (Pan et al., CVPR 2014)
// as an opt-in preprocessing step before blur recovery. It is only effective
// in [RecoverBlurred]; Recover and the mosaic path are unchanged.
//
// When called with no arguments the defaults from the paper are used:
//   - Lambda: 2×10⁻³ (gradient L0 sparsity weight)
//   - Mu: 5×10⁻⁴    (two-tone intensity prior weight)
//   - Iterations: 20  (outer HQS alternating-minimisation steps)
//
// Pass functional options to override individual parameters, e.g.:
//
//	unpixel.WithL0Deblur(
//	    func(o *deblur.L0Options) { o.Iterations = 30 },
//	)
//
// The sigma used for the PSF is the σ estimated by [InferBlurSigma] at the
// time RecoverBlurred is called (before the σ-search), so it does not need to
// be set by the caller. Result.L0Deblurred is set to true when active.
func WithL0Deblur(fns ...func(*deblur.L0Options)) Option {
	return func(c *Config) {
		opts := deblur.L0Options{
			Lambda:     2e-3,
			Mu:         5e-4,
			Iterations: 20,
		}
		for _, fn := range fns {
			fn(&opts)
		}
		c.l0deblur = &opts
	}
}

// WithPriors composes one or more plausibility priors into Config.LanguageModel.
// Each prior is a function from candidate string to a non-negative log-space
// score (higher = more plausible). Nil priors are silently skipped.
//
// The resulting LanguageModel is the sum of all provided priors AND any
// LanguageModel already set on the Config, so WithPriors composes correctly
// regardless of option order:
//
//	unpixel.Recover(ctx, img,
//	    unpixel.WithLanguageModel(myBigram),   // set first
//	    unpixel.WithPriors(secrets.Prior),     // adds on top
//	)
//
// The combined prior is used in two places:
//   - RankFinal breaks ties between candidates whose whole-image scores are
//     within lmTieBand (0.01) of each other, preferring higher-prior strings.
//   - WithCharsetTopK prunes the per-position charset to the K most-likely
//     next characters before evaluating any candidates, trading a little recall
//     for speed on wide charsets.
func WithPriors(priors ...func(string) float64) Option {
	return func(c *Config) {
		// Collect non-nil priors.
		active := make([]func(string) float64, 0, len(priors)+1)
		for _, p := range priors {
			if p != nil {
				active = append(active, p)
			}
		}
		if len(active) == 0 {
			return
		}
		// Incorporate any prior already on the Config.
		if c.LanguageModel != nil {
			active = append(active, c.LanguageModel)
		}
		// Single prior — avoid the allocation of a closure that just calls one func.
		if len(active) == 1 {
			c.LanguageModel = active[0]
			return
		}
		// Capture by value so the closure is independent of further mutations.
		captured := active
		c.LanguageModel = func(s string) float64 {
			var sum float64
			for _, p := range captured {
				sum += p(s)
			}
			return sum
		}
	}
}

// ErrNoComponents is returned by the Recover helpers when the Config leaves a
// component (Renderer, Pixelator, Metric, or Strategy) nil and the defaults
// package has not been imported to wire them.
var ErrNoComponents = errors.New("unpixel: missing component and no DefaultComponents wired; " +
	"import github.com/oioio-space/unpixel/defaults or set the component via options")

// Recover is the one-call entry point: it runs the full search on the redacted
// image and returns the single best Result across all grid offsets. It builds a
// Config from opts, auto-detects what it can (e.g. the block size), wires the
// default components (when the defaults package is imported), runs the search,
// and drains the channels for the caller.
//
// The common usage is a side-effect import of the defaults package:
//
//	import _ "github.com/oioio-space/unpixel/defaults"
//	res, err := unpixel.Recover(ctx, img)
//	fmt.Println(res.BestGuess)
//
// Recover returns ErrNilImage for a nil image and ErrNoComponents when a
// component is missing and no defaults are wired. A search that finds nothing
// returns the zero Result and a nil error.
func Recover(ctx context.Context, redacted image.Image, opts ...Option) (Result, error) {
	if redacted == nil {
		return Result{}, ErrNilImage
	}
	var cfg Config
	for _, opt := range opts {
		opt(&cfg)
	}
	if componentsMissing(cfg) && DefaultComponents == nil {
		return Result{}, ErrNoComponents
	}

	eng, err := New(redacted, cfg)
	if err != nil {
		return Result{}, err
	}

	progCh, resultCh := eng.Run(ctx)
	// Drain progress concurrently: high-priority events (EventNewBest/EventDone)
	// are blocking sends, so the search would stall if nothing reads them.
	progDone := make(chan struct{})
	go func() {
		defer close(progDone)
		for range progCh { //nolint:revive // intentional drain
		}
	}()

	// Every offset's Result carries the same global BestScore, so rank offsets by
	// their own top candidate to return the one that actually holds the winner.
	var (
		best     Result
		bestRank = 2.0 // worse than any score in [0, 1]
		found    bool
	)
	for r := range resultCh {
		if r.Err != nil {
			err = r.Err
			continue
		}
		rank := 1.0
		if len(r.TopN) > 0 {
			rank = r.TopN[0].Score
		}
		if !found || rank < bestRank {
			best = r
			bestRank = rank
			found = true
		}
	}
	<-progDone

	if !found {
		return Result{}, err
	}
	return best, nil
}

// RecoverReader decodes an image (PNG is registered; register other formats by
// importing their image/<fmt> package) from r and calls Recover.
func RecoverReader(ctx context.Context, r io.Reader, opts ...Option) (Result, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return Result{}, fmt.Errorf("decode image: %w", err)
	}
	return Recover(ctx, img, opts...)
}

// RecoverFile opens the image at path and calls RecoverReader. Pass "-" handling
// at the call site if stdin support is needed; RecoverFile always opens a file.
func RecoverFile(ctx context.Context, path string, opts ...Option) (Result, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-provided image path is the operation's purpose
	if err != nil {
		return Result{}, fmt.Errorf("open image: %w", err)
	}
	defer func() { _ = f.Close() }()
	return RecoverReader(ctx, f, opts...)
}

// RecoverBlurredReader decodes an image (PNG is registered; register other
// formats by importing their image/<fmt> package) from r and calls
// RecoverBlurred.
func RecoverBlurredReader(ctx context.Context, r io.Reader, opts ...Option) (Result, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return Result{}, fmt.Errorf("decode image: %w", err)
	}
	return RecoverBlurred(ctx, img, opts...)
}

// RecoverBlurredFile opens the image at path and calls RecoverBlurredReader.
// Pass "-" handling at the call site if stdin support is needed;
// RecoverBlurredFile always opens a file.
func RecoverBlurredFile(ctx context.Context, path string, opts ...Option) (Result, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-provided image path is the operation's purpose
	if err != nil {
		return Result{}, fmt.Errorf("open image: %w", err)
	}
	defer func() { _ = f.Close() }()
	return RecoverBlurredReader(ctx, f, opts...)
}

// FontResult pairs one candidate renderer with the Result it produced, as
// returned by RecoverMultiFont.
type FontResult struct {
	// Result is the recovery produced with this renderer.
	Result Result
	// Index is the position of the renderer in the slice passed to
	// RecoverMultiFont, identifying which font produced this Result.
	Index int
}

// RecoverMultiFont runs Recover once per renderer and returns the results ranked
// best-fit first, so the caller can recover a redaction without knowing its exact
// typeface — supply several candidate fonts and let the tool pick.
//
// Ranking is by Result.BestTotal, the whole-image distance of the best guess:
// unlike BestScore (a per-character marginal score that ties at ~0 across fonts),
// BestTotal is comparable between separate runs, so the lowest-BestTotal font is
// the one that reconstructs the redaction most faithfully. ret[0] is the best
// fit. A renderer with an empty recovery is ranked last; a renderer whose run
// errors is omitted. RecoverMultiFont returns an error only when redacted is nil,
// renderers is empty, or every renderer failed.
//
// The runs execute in parallel within a core budget — the resolved
// Config.Workers (set via WithWorkers or WithConfig), or runtime.GOMAXPROCS when
// unset: min(len(renderers), budget) run concurrently, each search using an
// equal share of workers, so the two layers never oversubscribe the CPU. The
// per-run renderer and worker count are set internally, overriding any
// WithRenderer/WithWorkers already in opts.
//
// Build the renderers with [github.com/oioio-space/unpixel/defaults.RendererFromFonts]:
//
//	var rs []unpixel.Renderer
//	for _, data := range fontData {
//	    r, err := defaults.RendererFromFonts(data, nil)
//	    if err != nil { return err }
//	    rs = append(rs, r)
//	}
//	ranked, err := unpixel.RecoverMultiFont(ctx, img, rs, unpixel.WithBlockSize(5))
//	best := ranked[0]
func RecoverMultiFont(ctx context.Context, redacted image.Image, renderers []Renderer, opts ...Option) ([]FontResult, error) {
	if redacted == nil {
		return nil, ErrNilImage
	}
	if len(renderers) == 0 {
		return nil, errors.New("unpixel: no renderers provided")
	}

	// Split the core budget between fonts in parallel and each font's offset
	// fan-out so the two layers don't oversubscribe. The budget is the resolved
	// Config.Workers (WithWorkers/WithConfig), defaulting to runtime.GOMAXPROCS.
	budget := 0
	{
		var probe Config
		for _, opt := range opts {
			opt(&probe)
		}
		budget = probe.Workers
	}
	if budget <= 0 {
		budget = runtime.GOMAXPROCS(0)
	}
	concurrency := max(1, min(len(renderers), budget))
	workersPerFont := max(1, budget/concurrency)

	results := make([]FontResult, len(renderers))
	errs := make([]error, len(renderers))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, r := range renderers {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				errs[i] = err
				return
			}
			runOpts := append(slices.Clone(opts), WithRenderer(r), WithWorkers(workersPerFont))
			res, err := Recover(ctx, redacted, runOpts...)
			results[i] = FontResult{Result: res, Index: i}
			errs[i] = err
		})
	}
	wg.Wait()

	ranked := make([]FontResult, 0, len(renderers))
	var firstErr error
	for i := range results {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		ranked = append(ranked, results[i])
	}
	if len(ranked) == 0 {
		return nil, firstErr
	}

	// effectiveTotal treats an empty recovery as the worst distance so a font
	// that found nothing (e.g. cancelled) never out-ranks a real match.
	effectiveTotal := func(fr FontResult) float64 {
		if fr.Result.BestGuess == "" {
			return 1
		}
		return fr.Result.BestTotal
	}
	slices.SortStableFunc(ranked, func(a, b FontResult) int {
		if c := cmp.Compare(effectiveTotal(a), effectiveTotal(b)); c != 0 {
			return c
		}
		return cmp.Compare(a.Result.BestScore, b.Result.BestScore)
	})
	return ranked, nil
}

// componentsMissing reports whether any pluggable component is still nil.
func componentsMissing(cfg Config) bool {
	return cfg.Renderer == nil || cfg.Pixelator == nil || cfg.Metric == nil || cfg.Strategy == nil
}
