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
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults" // named: strategy/metric constructors; init() still wires defaults
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
	strategy       string
	metric         string
	format         string
	fontPaths      []string
	fontBoldPath   string
	fontDir        string
	maxLength      int
	blockSize      int
	threshold      float64
	spaceThreshold float64
	fontSize       float64
	letterSpacing  float64
	topN           int
	workers        int
	beamWidth      int
	timeout        time.Duration
	quiet          bool
}

// buildConfig maps flagParams to an unpixel.Config. Scalar zero values are left
// as zero so unpixel.New's applyDefaults (and BlockSize inference) fill them in;
// the strategy and metric are selected from their validated flag names.
// buildConfig assumes p.strategy and p.metric were already validated by run.
func buildConfig(p flagParams) unpixel.Config {
	cfg := unpixel.Config{
		Charset:        p.charset,
		MaxLength:      p.maxLength,
		BlockSize:      p.blockSize,
		Threshold:      p.threshold,
		SpaceThreshold: p.spaceThreshold,
		TopN:           p.topN,
		Workers:        p.workers,
		BeamWidth:      p.beamWidth,
		Style: unpixel.Style{
			// FontSize 0 lets New apply the 32 pt default; a non-zero flag wins.
			FontSize:      p.fontSize,
			LetterSpacing: p.letterSpacing,
		},
	}
	if p.strategy == "beam" {
		cfg.Strategy = defaults.BeamStrategy(p.beamWidth)
	} else {
		cfg.Strategy = defaults.GuidedStrategy()
	}
	if p.metric == "ssim" {
		cfg.Metric = defaults.SSIMMetric(0)
	} else {
		cfg.Metric = defaults.PixelmatchMetric()
	}
	return cfg
}

// charsetForPreset maps a --charset-preset name to a charset constant.
func charsetForPreset(name string) (string, error) {
	switch name {
	case "lower":
		return unpixel.CharsetLower, nil
	case "alnum":
		return unpixel.CharsetAlnum, nil
	case "ascii", "code":
		return unpixel.CharsetASCII, nil
	default:
		return "", fmt.Errorf("--charset-preset must be %q, %q or %q/%q, got %q",
			"lower", "alnum", "ascii", "code", name)
	}
}

// validateParams rejects unknown enum-style flag values before any work begins,
// returning an error naming the offending flag and the accepted values.
func validateParams(p flagParams) error {
	if p.format != "text" && p.format != "json" {
		return fmt.Errorf("--format must be %q or %q, got %q", "text", "json", p.format)
	}
	if p.strategy != "guided" && p.strategy != "beam" {
		return fmt.Errorf("--strategy must be %q or %q, got %q", "guided", "beam", p.strategy)
	}
	if p.metric != "pixelmatch" && p.metric != "ssim" {
		return fmt.Errorf("--metric must be %q or %q, got %q", "pixelmatch", "ssim", p.metric)
	}
	return nil
}

// resultJSON is the stable JSON schema emitted by --format json.
// Field names use snake_case for CLI convention and are stable across versions.
type resultJSON struct {
	BestGuess  string         `json:"best_guess"`
	Font       string         `json:"font,omitempty"`
	Top        []topEntry     `json:"top"`
	Fonts      []fontRankJSON `json:"fonts,omitempty"`
	Offset     offsetJSON     `json:"offset"`
	BestScore  float64        `json:"best_score"`
	TotalScore float64        `json:"total_score"`
	Confidence float64        `json:"confidence"`
	Ambiguity  float64        `json:"ambiguity"`
	Evaluated  int            `json:"evaluated"`
	ElapsedMS  int64          `json:"elapsed_ms"`
}

// fontRankJSON is one font's result in a multi-font sweep, ranked by total_score.
type fontRankJSON struct {
	Font       string  `json:"font"`
	BestGuess  string  `json:"best_guess"`
	TotalScore float64 `json:"total_score"`
	BestScore  float64 `json:"best_score"`
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
	font       string // font file used for this recovery ("" = embedded default)
	top        []unpixel.Eval
	offset     unpixel.Offset
	bestScore  float64
	bestTotal  float64
	confidence float64
	ambiguity  float64
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
			best.bestTotal = r.BestTotal
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

// loadRenderer builds a renderer from the font file at regularPath, optionally
// using boldPath for bold text (empty reuses the regular font). It returns an
// actionable error if a file cannot be read or parsed as a font.
func loadRenderer(regularPath, boldPath string) (unpixel.Renderer, error) {
	regular, err := os.ReadFile(regularPath) // #nosec G304 -- user-provided CLI font path
	if err != nil {
		return nil, fmt.Errorf("read font %q: %w", regularPath, err)
	}
	var bold []byte
	if boldPath != "" {
		if bold, err = os.ReadFile(boldPath); err != nil { // #nosec G304 -- user-provided CLI font path
			return nil, fmt.Errorf("read bold font %q: %w", boldPath, err)
		}
	}
	r, err := defaults.RendererFromFonts(regular, bold)
	if err != nil {
		return nil, fmt.Errorf("load font %q: %w", regularPath, err)
	}
	return r, nil
}

// collectFonts gathers the candidate font files from the repeated --font flags
// and the --font-dir directory (TTF/OTF entries), de-duplicated in order. An
// empty result means "use the embedded default font".
func collectFonts(p flagParams) ([]string, error) {
	fonts := slices.Clone(p.fontPaths)
	if p.fontDir != "" {
		entries, err := os.ReadDir(p.fontDir)
		if err != nil {
			return nil, fmt.Errorf("read font dir %q: %w", p.fontDir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".ttf", ".otf":
				fonts = append(fonts, filepath.Join(p.fontDir, e.Name()))
			}
		}
	}
	seen := make(map[string]bool, len(fonts))
	deduped := fonts[:0]
	for _, f := range fonts {
		if !seen[f] {
			seen[f] = true
			deduped = append(deduped, f)
		}
	}
	return deduped, nil
}

// runSweep recovers img once per candidate font and reports the font that
// reconstructs the redaction best (lowest BestTotal — the whole-image score is
// comparable across fonts, unlike the marginal BestScore which ties at ~0). The
// winning guess goes to stdout; a ranked summary goes to stderr (text mode) or a
// "fonts" array (JSON mode).
func runSweep(ctx context.Context, img image.Image, base unpixel.Config, fonts []string, p flagParams) error {
	verbose := !p.quiet && p.format != "json"
	if verbose {
		fmt.Fprintf(os.Stderr, "Sweeping %d fonts…\n", len(fonts))
	}

	var results []recoveryResult
	var totalElapsed time.Duration
	sumEval := 0
	for _, f := range fonts {
		if ctx.Err() != nil {
			break
		}
		renderer, err := loadRenderer(f, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "unpixel: skipping %q: %v\n", f, err)
			continue
		}
		cfg := base
		cfg.Renderer = renderer
		res, elapsed, evaluated, err := runRecovery(ctx, img, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unpixel: font %q: %v\n", f, err)
			continue
		}
		res.font = f
		totalElapsed += elapsed
		results = append(results, res)
		sumEval += evaluated
		if verbose {
			fmt.Fprintf(os.Stderr, "  %-32s → %-20q total=%.4f (%s)\n",
				filepath.Base(f), res.bestGuess, res.bestTotal, elapsed.Round(time.Millisecond))
		}
	}
	if len(results) == 0 {
		return fmt.Errorf("no font produced a result")
	}

	// Lowest whole-image distance wins; ties break on the marginal score.
	slices.SortStableFunc(results, func(a, b recoveryResult) int {
		if c := cmp.Compare(a.bestTotal, b.bestTotal); c != 0 {
			return c
		}
		return cmp.Compare(a.bestScore, b.bestScore)
	})
	winner := results[0]

	if p.format == "json" {
		ranked := make([]fontRankJSON, len(results))
		for i, r := range results {
			ranked[i] = fontRankJSON{Font: r.font, BestGuess: r.bestGuess, TotalScore: r.bestTotal, BestScore: r.bestScore}
		}
		return printJSON(winner, ranked, sumEval, totalElapsed)
	}

	if !p.quiet {
		fmt.Fprintln(os.Stderr, "\nFont ranking (best fit first):")
		for i, r := range results {
			fmt.Fprintf(os.Stderr, "  %d. %-32s %-20q total=%.4f\n",
				i+1, filepath.Base(r.font), r.bestGuess, r.bestTotal)
		}
		fmt.Fprintf(os.Stderr, "\nBest font: %s\n", filepath.Base(winner.font))
	}
	fmt.Println(winner.bestGuess)
	return nil
}

// warnIfNoMosaic writes a one-line warning to w when the block size is being
// auto-detected (blockSize <= 0) but no mosaic pixelation grid can be inferred
// from img. A grid that reduces to nothing is a strong sign the image is not
// block-pixelated at all — recovery then silently returns no result, which is
// confusing without this hint. It reports whether a warning was emitted.
//
// A forced --block-size (blockSize > 0) suppresses the check: the user is
// asserting the grid explicitly, so UnPixel takes them at their word.
func warnIfNoMosaic(w io.Writer, img image.Image, blockSize int, source string) bool {
	if blockSize > 0 || unpixel.InferBlockSize(img) != 0 {
		return false
	}
	_, _ = fmt.Fprintf(w,
		"unpixel: warning: no mosaic pixelation grid detected in %s — the image may not "+
			"be block-pixelated, so recovery is unlikely to succeed. UnPixel only reverses "+
			"mosaic (block-average) redaction; if you know the block size, pass --block-size.\n",
		source)
	return true
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
			&cli.StringFlag{
				Name:  "charset-preset",
				Usage: `named charset when --charset is unset: "lower", "alnum", or "ascii"/"code"`,
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
				Usage:   "pixelation block side length in pixels (0 = auto-detect from the image)",
				Value:   0,
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
				Name:  "strategy",
				Usage: `search strategy: "guided" (full DFS) or "beam" (bounded, faster)`,
				Value: "guided",
			},
			&cli.IntFlag{
				Name:  "beam-width",
				Usage: "candidates kept per depth level when --strategy beam (0 = default)",
				Value: 0,
			},
			&cli.StringFlag{
				Name:  "metric",
				Usage: `image-distance metric: "pixelmatch" (faithful) or "ssim" (structural)`,
				Value: "pixelmatch",
			},
			&cli.StringSliceFlag{
				Name:  "font",
				Usage: "TTF/OTF font to render candidates with; repeat to sweep several and keep the best fit (default: embedded Liberation Sans ≈ Arial)",
			},
			&cli.StringFlag{
				Name:  "font-dir",
				Usage: "directory of TTF/OTF fonts to sweep (each tried; best fit by whole-image score wins)",
			},
			&cli.StringFlag{
				Name:  "font-bold",
				Usage: "path to a bold TTF/OTF font, single-font mode only (default: reuse --font)",
			},
			&cli.FloatFlag{
				Name:  "font-size",
				Usage: "font size in points to match the redaction (0 = default 32)",
				Value: 0,
			},
			&cli.FloatFlag{
				Name:  "letter-spacing",
				Usage: "extra pixels after each glyph, like CSS letter-spacing (may be negative)",
				Value: 0,
			},
			&cli.IntFlag{
				Name:  "workers",
				Usage: "max grid offsets searched concurrently (0 = number of CPUs)",
				Value: 0,
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

	charset := cmd.String("charset")
	// An explicit --charset always wins; otherwise a preset name expands to a charset.
	if !cmd.IsSet("charset") && cmd.IsSet("charset-preset") {
		cs, err := charsetForPreset(cmd.String("charset-preset"))
		if err != nil {
			return err
		}
		charset = cs
	}

	p := flagParams{
		charset:        charset,
		maxLength:      cmd.Int("max-length"),
		blockSize:      cmd.Int("block-size"),
		threshold:      cmd.Float("threshold"),
		spaceThreshold: cmd.Float("space-threshold"),
		topN:           cmd.Int("top"),
		workers:        cmd.Int("workers"),
		strategy:       cmd.String("strategy"),
		beamWidth:      cmd.Int("beam-width"),
		metric:         cmd.String("metric"),
		format:         cmd.String("format"),
		fontPaths:      cmd.StringSlice("font"),
		fontBoldPath:   cmd.String("font-bold"),
		fontDir:        cmd.String("font-dir"),
		fontSize:       cmd.Float("font-size"),
		letterSpacing:  cmd.Float("letter-spacing"),
		quiet:          cmd.Bool("quiet"),
		timeout:        cmd.Duration("timeout"),
	}

	if err := validateParams(p); err != nil {
		return err
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

	source := imgPath
	if source == "-" {
		source = "the input image"
	}
	warnIfNoMosaic(os.Stderr, img, p.blockSize, source)

	cfg := buildConfig(p)

	fonts, ferr := collectFonts(p)
	if ferr != nil {
		return ferr
	}
	// More than one font: the redaction's real typeface is rarely known, so try
	// each and keep the one that reconstructs the image best (lowest BestTotal).
	if len(fonts) > 1 {
		return runSweep(ctx, img, cfg, fonts, p)
	}
	// A single --font overrides the embedded renderer so candidates are
	// rasterised in the redaction's actual typeface (e.g. user-supplied Consolas).
	font := ""
	if len(fonts) == 1 {
		font = fonts[0]
		renderer, lerr := loadRenderer(font, p.fontBoldPath)
		if lerr != nil {
			return lerr
		}
		cfg.Renderer = renderer
	}

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
				fmt.Fprintf(
					os.Stderr, "\r\033[K[%s] best: %-20s score: %.4f  evaluated: %d  offsets: %d/%d",
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
			best.bestTotal = r.BestTotal
			best.offset = r.Offset
			best.confidence = r.Confidence
			best.ambiguity = r.Ambiguity
			best.top = r.TopN
		}
	}
	best.font = font

	// Wait for the progress goroutine to finish and clear the line.
	<-progressDone
	elapsed := time.Since(start)

	switch p.format {
	case "json":
		return printJSON(best, nil, evaluated, elapsed)
	default:
		printText(best, p.quiet || !isTTY(os.Stderr))
	}
	return nil
}

// printJSON writes the recovery result as a JSON object to stdout. fonts is the
// ranked per-font summary of a sweep, or nil for a single-font/default run.
func printJSON(r recoveryResult, fonts []fontRankJSON, evaluated int, elapsed time.Duration) error {
	top := make([]topEntry, len(r.top))
	for i, e := range r.top {
		top[i] = topEntry{Guess: e.Guess, Score: e.Score}
	}
	out := resultJSON{
		BestGuess:  r.bestGuess,
		Font:       r.font,
		BestScore:  r.bestScore,
		TotalScore: r.bestTotal,
		Offset:     offsetJSON{X: r.offset.X, Y: r.offset.Y},
		Confidence: r.confidence,
		Ambiguity:  r.ambiguity,
		Top:        top,
		Fonts:      fonts,
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
