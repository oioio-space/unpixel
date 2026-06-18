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
	"image"
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
	// match the block size used to produce the redacted image. Defaults to
	// DefaultBlockSize.
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
}

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

// New creates an Engine for the given pixelated image. Scalar zero values in
// cfg are replaced by package defaults; nil component fields are left for Run
// to fill via DefaultComponents (or must be set explicitly). New returns
// ErrNilImage if redacted is nil.
func New(redacted image.Image, cfg Config) (*Engine, error) {
	if redacted == nil {
		return nil, ErrNilImage
	}
	cfg = applyDefaults(cfg)
	rgba := toRGBA(redacted)
	return &Engine{redacted: rgba, cfg: cfg}, nil
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
	if e.cfg.Strategy == nil {
		if DefaultComponents != nil {
			if err := DefaultComponents(&e.cfg); err != nil {
				progCh := make(chan Progress, 1)
				resultCh := make(chan Result, 1)
				progCh <- Progress{Kind: EventDone, Done: true, Err: err}
				close(progCh)
				close(resultCh)
				return progCh, resultCh
			}
		} else {
			panic("unpixel: Engine.Run called with nil Strategy and no DefaultComponents wired; " +
				"import github.com/oioio-space/unpixel/defaults or set cfg.Strategy explicitly")
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
