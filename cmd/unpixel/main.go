// Command unpixel reconstructs text hidden behind pixelation.
//
// UnPixel attempts to recover the original text from a pixelated (mosaic-blurred)
// image by rendering candidate strings, re-pixelating them with the same block
// grid, and measuring the pixel-level distance. It is a Go port of Bishop Fox's
// unredacter tool.
//
// Usage:
//
//	unpixel [flags] <redacted-image.png>
//	unpixel [flags] -          # read PNG from stdin
//
// Examples:
//
//	unpixel redacted.png
//	unpixel --format json --top 10 redacted.png
//	unpixel --charset abcdefghijklmnopqrstuvwxyz --threshold 0.2 redacted.png
//	cat redacted.png | unpixel -
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults"
)

// version and commit are injected by goreleaser via -ldflags -X.
var (
	version = "dev"
	commit  = "none" //nolint:unused // injected by goreleaser
)

func main() {
	app := buildApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "unpixel: %v\n", err)
		os.Exit(1)
	}
}

// flagParams holds the parsed flag values before they are mapped to
// unpixel.Config. Keeping them in a plain struct lets later batches extend
// buildConfig without touching the flag wiring.
type flagParams struct {
	charset        string
	maxLength      int
	blockSize      int
	threshold      float64
	spaceThreshold float64
	topN           int
	format         string
	quiet          bool
	timeout        time.Duration
}

// buildConfig maps flagParams to an unpixel.Config. Zero values are intentionally
// left as zero so that unpixel.New's applyDefaults fills in package defaults.
func buildConfig(p flagParams) unpixel.Config {
	return unpixel.Config{
		Charset:        p.charset,
		MaxLength:      p.maxLength,
		BlockSize:      p.blockSize,
		Threshold:      p.threshold,
		SpaceThreshold: p.spaceThreshold,
		TopN:           p.topN,
	}
}

// resultJSON is the stable JSON schema emitted by --format json.
// Field names use snake_case for CLI convention and are stable across versions.
type resultJSON struct {
	BestGuess  string     `json:"best_guess"`
	BestScore  float64    `json:"best_score"`
	Offset     offsetJSON `json:"offset"`
	Confidence float64    `json:"confidence"`
	Ambiguity  float64    `json:"ambiguity"`
	Top        []topEntry `json:"top"`
	Evaluated  int        `json:"evaluated"`
	ElapsedMS  int64      `json:"elapsed_ms"`
}

// offsetJSON is the JSON representation of a grid offset.
type offsetJSON struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// topEntry is one element in the Top-N ranked candidate list.
type topEntry struct {
	Guess string  `json:"guess"`
	Score float64 `json:"score"`
}

// recoveryResult collects the aggregated output of runRecovery.
type recoveryResult struct {
	bestGuess  string
	bestScore  float64
	offset     unpixel.Offset
	confidence float64
	ambiguity  float64
	top        []unpixel.Eval
}

// runRecovery runs the unpixel engine on img with cfg and drains all channels.
// It returns the best result across all offsets, the total wall time, the total
// candidates evaluated, and any terminal error.
func runRecovery(ctx context.Context, img image.Image, cfg unpixel.Config) (recoveryResult, time.Duration, int, error) {
	eng, err := unpixel.New(img, cfg)
	if err != nil {
		return recoveryResult{}, 0, 0, fmt.Errorf("create engine: %w", err)
	}

	start := time.Now()
	progCh, resCh := eng.Run(ctx)

	var evaluated int
	// Drain progress to count evaluations; drop-on-full semantics mean this
	// loop must not block the search goroutine.
	go func() {
		for p := range progCh {
			if p.Kind == unpixel.EventCandidate {
				evaluated = p.Evaluated
			}
		}
	}()

	var best recoveryResult
	best.bestScore = 1.0
	for r := range resCh {
		if r.Err != nil {
			continue
		}
		if r.BestScore < best.bestScore || best.bestGuess == "" {
			best.bestGuess = r.BestGuess
			best.bestScore = r.BestScore
			best.offset = r.Offset
			best.confidence = r.Confidence
			best.ambiguity = r.Ambiguity
			best.top = r.TopN
		}
	}

	return best, time.Since(start), evaluated, nil
}

// loadImage reads a PNG from path. If path is "-" it reads from stdin.
func loadImage(path string) (img image.Image, err error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		// path is the user-supplied CLI argument naming the image to recover —
		// opening exactly that file is the command's purpose, not a traversal risk.
		f, openErr := os.Open(path) // #nosec G304 -- user-provided CLI file argument
		if openErr != nil {
			return nil, fmt.Errorf("open image: %w", openErr)
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && err == nil {
				err = fmt.Errorf("close image: %w", cerr)
			}
		}()
		r = f
	}
	img, _, err = image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return img, nil
}

// isTTY reports whether f is connected to a terminal.
func isTTY(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// buildApp constructs the urfave/cli application.
func buildApp() *cli.Command {
	return &cli.Command{
		Name:    "unpixel",
		Version: version,
		Usage:   "recover text hidden behind pixelation",
		UsageText: `unpixel [flags] <redacted-image.png>
   unpixel [flags] -`,
		Description: `UnPixel reconstructs text that has been obscured by mosaic (block-average)
pixelation. It works by rendering each candidate string, re-applying the same
pixelation at each candidate grid origin, and measuring the pixel-level distance
against the redacted region.

The algorithm is a Go port of Bishop Fox's unredacter (GPL-3.0):
https://github.com/bishopfox/unredacter

Pass the path to a PNG file, or - to read from stdin.

Examples:
  unpixel redacted.png
  unpixel --format json --top 10 redacted.png
  unpixel --charset abcdefghijklmnopqrstuvwxyz --threshold 0.2 redacted.png
  cat redacted.png | unpixel -`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "charset",
				Usage: "ordered set of candidate characters (default: a-z plus space)",
				Value: unpixel.DefaultCharset,
			},
			&cli.IntFlag{
				Name:    "max-length",
				Aliases: []string{"m"},
				Usage:   "maximum candidate string length before backtracking",
				Value:   unpixel.DefaultMaxLength,
			},
			&cli.IntFlag{
				Name:    "block-size",
				Aliases: []string{"b"},
				Usage:   "pixelation block side length in pixels (must match the redaction)",
				Value:   unpixel.DefaultBlockSize,
			},
			&cli.FloatFlag{
				Name:  "threshold",
				Usage: "per-block score gate for non-space characters (lower = stricter)",
				Value: unpixel.DefaultThreshold,
			},
			&cli.FloatFlag{
				Name:  "space-threshold",
				Usage: "per-block score gate for the space character",
				Value: unpixel.DefaultSpaceThreshold,
			},
			&cli.IntFlag{
				Name:    "top",
				Aliases: []string{"n"},
				Usage:   "maximum number of ranked top candidates to retain per offset",
				Value:   unpixel.DefaultTopN,
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   `output format: "text" or "json"`,
				Value:   "text",
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "suppress the live progress display on stderr",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Usage: "maximum time to spend on recovery (0 = no limit)",
				Value: 0,
			},
		},
		Action: run,
	}
}

// run is the primary action called when the user invokes unpixel.
func run(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return fmt.Errorf("exactly one argument required: path to redacted PNG (or - for stdin)")
	}
	imgPath := cmd.Args().First()

	p := flagParams{
		charset:        cmd.String("charset"),
		maxLength:      cmd.Int("max-length"),
		blockSize:      cmd.Int("block-size"),
		threshold:      cmd.Float("threshold"),
		spaceThreshold: cmd.Float("space-threshold"),
		topN:           cmd.Int("top"),
		format:         cmd.String("format"),
		quiet:          cmd.Bool("quiet"),
		timeout:        cmd.Duration("timeout"),
	}

	if p.format != "text" && p.format != "json" {
		return fmt.Errorf("--format must be %q or %q, got %q", "text", "json", p.format)
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	cfg := buildConfig(p)

	showProgress := !p.quiet && isTTY(os.Stderr)

	eng, err := unpixel.New(img, cfg)
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	start := time.Now()
	progCh, resCh := eng.Run(ctx)

	var evaluated int
	progressDone := make(chan struct{})

	go func() {
		defer close(progressDone)
		for ev := range progCh {
			if ev.Kind == unpixel.EventCandidate {
				evaluated = ev.Evaluated
			}
			if showProgress {
				fmt.Fprintf(os.Stderr, "\r\033[K[%s] best: %-20s score: %.4f  evaluated: %d  offsets: %d/%d",
					ev.Elapsed.Round(time.Millisecond),
					ev.BestGuess,
					ev.BestScore,
					ev.Evaluated,
					ev.OffsetsDone,
					ev.OffsetsTotal,
				)
			}
		}
		if showProgress {
			// Clear the progress line before final output.
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
	}()

	var best recoveryResult
	best.bestScore = 1.0
	for r := range resCh {
		if r.Err != nil {
			continue
		}
		if r.BestScore < best.bestScore || best.bestGuess == "" {
			best.bestGuess = r.BestGuess
			best.bestScore = r.BestScore
			best.offset = r.Offset
			best.confidence = r.Confidence
			best.ambiguity = r.Ambiguity
			best.top = r.TopN
		}
	}

	// Wait for the progress goroutine to finish and clear the line.
	<-progressDone
	elapsed := time.Since(start)

	switch p.format {
	case "json":
		return printJSON(best, evaluated, elapsed)
	default:
		printText(best, p.quiet || !isTTY(os.Stderr))
	}
	return nil
}

// printJSON writes the recovery result as a JSON object to stdout.
func printJSON(r recoveryResult, evaluated int, elapsed time.Duration) error {
	top := make([]topEntry, len(r.top))
	for i, e := range r.top {
		top[i] = topEntry{Guess: e.Guess, Score: e.Score}
	}
	out := resultJSON{
		BestGuess:  r.bestGuess,
		BestScore:  r.bestScore,
		Offset:     offsetJSON{X: r.offset.X, Y: r.offset.Y},
		Confidence: r.confidence,
		Ambiguity:  r.ambiguity,
		Top:        top,
		Evaluated:  evaluated,
		ElapsedMS:  elapsed.Milliseconds(),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

// printText writes the recovery result as human-readable text.
// The best guess goes to stdout; the Top-N table goes to stderr (unless quiet).
func printText(r recoveryResult, quiet bool) {
	fmt.Println(r.bestGuess)
	if !quiet && len(r.top) > 0 {
		fmt.Fprintln(os.Stderr, "\nTop candidates:")
		fmt.Fprintf(os.Stderr, "  %-3s  %-24s  %8s  %10s  %10s\n",
			"#", "guess", "score", "confidence", "ambiguity")
		for i, e := range r.top {
			c, a := 0.0, 0.0
			if i == 0 {
				c, a = r.confidence, r.ambiguity
			}
			fmt.Fprintf(os.Stderr, "  %-3d  %-24s  %8.4f  %10.4f  %10.4f\n",
				i+1, e.Guess, e.Score, c, a)
		}
	}
}
