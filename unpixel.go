// Package unpixel reconstructs text hidden behind pixelation.
// It is a faithful Go port of Bishop Fox's unredacter tool.
package unpixel

import (
	"context"
	"errors"
	"image"
	"time"
)

// Renderer renders candidate text to an RGBA image, placing a blue sentinel
// block immediately to the right of the text. It returns the image and the
// x-coordinate of that sentinel (the text's right edge in pixels).
type Renderer interface {
	Render(text string, style Style) (img *image.RGBA, sentinelX int, err error)
}

// Pixelator replaces every blockSize×blockSize region of an image with the
// mean RGBA of that region. The origin parameters align the grid.
type Pixelator interface {
	Pixelate(img *image.RGBA, originX, originY int) *image.RGBA
}

// Metric measures the visual distance between two same-sized images.
// It returns a value in [0, 1] where 0 means identical.
type Metric interface {
	Compare(a, b *image.RGBA) float64
}

// Strategy runs the full search and emits results on the returned channels.
// Implementations may use different algorithms (DFS, beam search, etc.).
type Strategy interface {
	Search(ctx context.Context, redacted *image.RGBA, cfg Config, out chan<- Progress, results chan<- Result)
}

// Style describes the text rendering style.
type Style struct {
	// FontSize is the font size in points (default 32).
	FontSize float64
	// Bold selects the bold font variant.
	Bold bool
	// PaddingTop is the top padding in pixels (default 8).
	PaddingTop int
	// PaddingLeft is the left padding in pixels (default 8).
	PaddingLeft int
}

// Config holds engine configuration. Zero values use the defaults documented
// in the constants below.
type Config struct {
	// Charset is the set of candidate characters. Defaults to DefaultCharset.
	Charset string
	// MaxLength is the maximum guess length. Defaults to DefaultMaxLength.
	MaxLength int
	// BlockSize is the pixelation block size in pixels. Defaults to DefaultBlockSize.
	BlockSize int
	// Threshold is the score below which a candidate is kept. Defaults to DefaultThreshold.
	Threshold float64
	// SpaceThreshold is the score threshold for the space character. Defaults to DefaultSpaceThreshold.
	SpaceThreshold float64
	// ThresholdFor returns the score threshold for a given candidate character.
	// It defaults to a closure that returns SpaceThreshold for ' ' and Threshold
	// for all other characters. Override to set per-class thresholds without
	// editing search logic.
	ThresholdFor func(rune) float64

	// Style overrides the rendering style. Zero values use the design defaults.
	Style Style

	// Renderer overrides the default renderer (XImage).
	Renderer Renderer
	// Pixelator overrides the default pixelator (BlockAverage).
	Pixelator Pixelator
	// Metric overrides the default metric (Pixelmatch).
	Metric Metric
	// Strategy overrides the default search strategy (GuidedDFS).
	Strategy Strategy
}

// Default configuration constants matching the original unredacter.
const (
	DefaultCharset        = "abcdefghijklmnopqrstuvwxyz "
	DefaultMaxLength      = 20
	DefaultBlockSize      = 8
	DefaultThreshold      = 0.25
	DefaultSpaceThreshold = 0.5
)

// Offset represents one candidate grid origin for the pixelation block.
type Offset struct {
	X, Y  int
	Score float64
}

// Eval holds the result of evaluating a single candidate string.
type Eval struct {
	// Guess is the candidate string.
	Guess string
	// Score is the marginal region diff score (lower is better).
	Score float64
	// TotalScore is the whole-image diff score (for display).
	TotalScore float64
	// TooBig indicates the candidate render is wider than the redacted image.
	TooBig bool
	// GuessImage is the cropped pixelated guess (nil unless requested).
	GuessImage *image.RGBA
}

// Result is the final output of Engine.Run for one offset path.
type Result struct {
	// BestGuess is the candidate string with the lowest score found.
	BestGuess string
	// BestScore is the score of BestGuess.
	BestScore float64
	// Candidates holds all strings that passed the threshold gate.
	Candidates []Eval
	// Offset is the grid origin used for this result.
	Offset Offset
	// Err is non-nil if the search aborted due to an error.
	Err error
}

// EventKind identifies the type of a Progress event.
type EventKind int

const (
	// EventCandidate is emitted for every candidate evaluated.
	// This is a high-frequency, drop-on-full event.
	EventCandidate EventKind = iota
	// EventOffsetProbed is emitted after an offset discovery probe.
	// This is a high-frequency, drop-on-full event.
	EventOffsetProbed
	// EventNewBest is emitted whenever a new overall best guess is found.
	// This event is always delivered (blocking with context).
	EventNewBest
	// EventDone is the final event; always delivered exactly once.
	EventDone
)

// Progress carries a snapshot of search state streamed on the progress channel.
type Progress struct {
	Kind EventKind

	// BestGuess and BestScore track the overall best found so far.
	BestGuess string
	BestScore float64

	// Guess and Score describe the candidate that triggered this event.
	Guess string
	Score float64

	// Depth is the current search depth (length of Guess).
	Depth int
	// Offset is the grid origin being searched.
	Offset Offset
	// Evaluated is the total number of candidates evaluated so far.
	Evaluated int
	// OffsetsDone is how many offsets have been fully searched.
	OffsetsDone int
	// OffsetsTotal is the total number of surviving offsets to search.
	OffsetsTotal int

	// PreviewImage is an optional deep copy of the guess image (opt-in).
	PreviewImage *image.RGBA

	// Elapsed is the time since Engine.Run was called.
	Elapsed time.Duration
	// Done is true only on the EventDone event.
	Done bool
	// Err carries any terminal error.
	Err error
}

// OnProgress is an adapter that calls fn for every Progress event.
// It drains the channel returned by Engine.Run in a blocking manner.
func OnProgress(ch <-chan Progress, fn func(Progress)) {
	for p := range ch {
		fn(p)
	}
}

// Engine orchestrates the full unredact pipeline.
type Engine struct {
	redacted *image.RGBA
	cfg      Config
}

// New creates an Engine for the given redacted image. Config defaults are
// applied for any zero/nil fields. redacted must not be nil.
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

// DefaultComponents is a hook that wires default Renderer, Pixelator, Metric,
// and Strategy into cfg when those fields are nil. It is set by the unpixeldefaults
// package (imported by cmd/unpixel and end-to-end tests) to avoid an import
// cycle between the root package and its internal implementations.
var DefaultComponents func(cfg *Config) error

// Run starts the search and returns a progress channel and a results channel.
// The progress channel is closed after EventDone is delivered. The results
// channel receives one Result per surviving offset and is closed when done.
// Cancelling ctx causes both channels to be closed promptly.
//
// If cfg.Strategy (or other components) are nil and DefaultComponents is set,
// they are wired automatically. Otherwise Run panics with a descriptive message.
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
