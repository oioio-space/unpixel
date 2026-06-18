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
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/png" // register PNG decoding for RecoverReader/RecoverFile
	"io"
	"os"
	"runtime"
	"time"
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
}

// Config holds all tunable parameters for an Engine. Every field is optional:
// zero values are replaced by the package defaults (see the Default* constants)
// when New is called. Component fields (Renderer, Pixelator, Metric, Strategy)
// are filled by importing the defaults package for its side-effect, or can be
// supplied directly for custom implementations.
type Config struct {
	// Charset is the ordered set of candidate characters tried at each search
	// depth. Defaults to DefaultCharset (a–z plus space).
	Charset string
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
	// ThresholdFor returns the acceptance threshold for a given candidate
	// character. It defaults to a closure that returns SpaceThreshold for ' '
	// and Threshold for all other runes. Override to apply per-class thresholds
	// without modifying search logic.
	ThresholdFor func(rune) float64

	// Style controls the font size, weight, and padding used when rendering
	// candidates. Zero fields use the design defaults (32 pt, 8 px padding).
	Style Style

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

	// BeamWidth is the maximum number of candidates retained per depth level
	// when using BeamStrategy. Values <= 0 are replaced by DefaultBeamWidth.
	// Has no effect when Strategy is GuidedStrategy.
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
	// Guess is the candidate string evaluated at this node.
	Guess string
	// Score is the marginal region diff score for the most recently added
	// character (lower is better; compared against the threshold).
	Score float64
	// TotalScore is the whole-image diff score accumulated so far (used for
	// display and ranking, not for pruning).
	TotalScore float64
	// TooBig is true when the rendered candidate is wider than the redacted
	// image; such candidates are pruned regardless of score.
	TooBig bool
	// GuessImage is a cropped, pixelated rendering of Guess, populated only
	// when the caller opts in via the Strategy implementation.
	GuessImage *image.RGBA
}

// Result is the final output produced by Engine.Run for one surviving grid offset.
// One Result is sent per offset on the results channel returned by Run.
type Result struct {
	// BestGuess is the candidate string with the lowest overall score found
	// during the search rooted at this offset.
	BestGuess string
	// BestScore is the total image-distance score of BestGuess (lower is better).
	BestScore float64
	// Candidates holds every string that passed the threshold gate during the
	// search, in discovery order.
	Candidates []Eval
	// TopN holds the best candidates sorted ascending by score (lowest score
	// first), with ties broken by Guess string for determinism. Its length is
	// at most Config.TopN. TopN[0] is the same candidate as BestGuess when the
	// search produces any result. TopN is nil when no candidates were found.
	TopN []Eval
	// Confidence is 1 − TopN[0].Score, giving a value in [0, 1] where 1
	// represents a pixel-perfect match. It is 0 when TopN is empty.
	Confidence float64
	// Ambiguity is TopN[1].Score − TopN[0].Score: the score gap between the
	// best and second-best candidates. A larger gap means the best guess is
	// more clearly distinguished from alternatives. It is 0 when len(TopN) < 2.
	Ambiguity float64
	// Offset is the grid origin that was searched to produce this result.
	Offset Offset
	// Err is non-nil if the search for this offset aborted due to an error.
	Err error
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
	// Kind identifies which event this Progress value represents.
	Kind EventKind

	// BestGuess is the candidate string with the lowest score found so far
	// across all offsets.
	BestGuess string
	// BestScore is the total image-distance score of BestGuess (lower is better).
	BestScore float64

	// Guess is the candidate string that triggered this specific event.
	Guess string
	// Score is the image-distance score of Guess.
	Score float64

	// Depth is the current search depth, equal to the character length of Guess.
	Depth int
	// Offset is the grid origin currently being searched.
	Offset Offset
	// Evaluated is the cumulative number of candidates evaluated since Run started.
	Evaluated int
	// OffsetsDone is the number of grid offsets fully searched so far.
	OffsetsDone int
	// OffsetsTotal is the total number of surviving grid offsets to be searched.
	OffsetsTotal int

	// PreviewImage is an optional deep copy of the pixelated guess image.
	// It is non-nil only when the Strategy implementation populates it.
	PreviewImage *image.RGBA

	// Elapsed is the wall-clock duration since Engine.Run was called.
	Elapsed time.Duration
	// Done is true only on the EventDone event.
	Done bool
	// Err carries any terminal error that caused the search to abort.
	Err error
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
	rgba := toRGBA(redacted)
	// Auto-contrast: the renderer draws dark text on a light background, so a
	// dark-background image (e.g. a dark-mode screenshot) is inverted to match.
	// Light-background images are left untouched, so the faithful path is byte
	// identical to before.
	if darkBackground(rgba) {
		rgba = invertColors(rgba)
	}
	// Auto-detect the block size from the image when the caller left it unset,
	// before applyDefaults falls back to DefaultBlockSize.
	if cfg.BlockSize <= 0 {
		cfg.BlockSize = InferBlockSize(rgba)
	}
	cfg = applyDefaults(cfg)
	return &Engine{redacted: rgba, cfg: cfg}, nil
}

// InferDarkBackground reports whether img looks like dark text/content on a dark
// background (e.g. a dark-mode screenshot), judged from its border pixels. New
// uses it to decide whether to invert the image so it matches the dark-on-light
// rendering pipeline.
func InferDarkBackground(img image.Image) bool {
	return darkBackground(toRGBA(img))
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
	rgba := toRGBA(img)
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return 0
	}

	g := 0
	prev := -1
	for x := 1; x < w; x++ {
		if columnDiffers(rgba, b, x, h) {
			if prev >= 0 {
				g = gcd(g, x-prev)
			}
			prev = x
		}
	}
	prev = -1
	for y := 1; y < h; y++ {
		if rowDiffers(rgba, b, y, w) {
			if prev >= 0 {
				g = gcd(g, y-prev)
			}
			prev = y
		}
	}

	// A gap GCD of 1 means no regular grid (treat as undetectable).
	if g < 2 {
		return 0
	}
	return g
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

// ErrNilImage is returned by New when a nil image is passed.
var ErrNilImage = errors.New("redacted image must not be nil")

// DefaultComponents is a hook that wires the standard Renderer, Pixelator,
// Metric, and Strategy into cfg for any fields that are still nil. It is
// populated by importing the defaults package for its side-effect, which avoids
// an import cycle between the root package and its internal implementations.
// Applications that supply all component fields explicitly need not set this.
var DefaultComponents func(cfg *Config) error

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

// toRGBA converts any image.Image to *image.RGBA.
func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}
	return dst
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

// WithTopN sets Config.TopN (ranked candidates retained per offset).
func WithTopN(n int) Option { return func(c *Config) { c.TopN = n } }

// WithWorkers sets Config.Workers (max grid offsets searched concurrently;
// non-positive means runtime.GOMAXPROCS).
func WithWorkers(n int) Option { return func(c *Config) { c.Workers = n } }

// WithStrategy sets Config.Strategy (the search algorithm).
func WithStrategy(s Strategy) Option { return func(c *Config) { c.Strategy = s } }

// WithMetric sets Config.Metric (the image-distance metric).
func WithMetric(m Metric) Option { return func(c *Config) { c.Metric = m } }

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

// componentsMissing reports whether any pluggable component is still nil.
func componentsMissing(cfg Config) bool {
	return cfg.Renderer == nil || cfg.Pixelator == nil || cfg.Metric == nil || cfg.Strategy == nil
}
