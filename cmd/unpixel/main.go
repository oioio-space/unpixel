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
	"image/draw"
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
	"github.com/oioio-space/unpixel/defaults"         // named: strategy/metric constructors; init() still wires defaults
	"github.com/oioio-space/unpixel/fonts"            // bundled redistributable fonts for the zero-config sweep
	"github.com/oioio-space/unpixel/internal/lang"    // dictionary prior (P3.2)
	"github.com/oioio-space/unpixel/internal/secrets" // structured-secret prior (P3.7)
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
	charset         string
	strategy        string
	metric          string
	format          string
	fontPaths       []string
	fontBoldPath    string
	fontDir         string
	redaction       string
	maxLength       int
	blockSize       int
	threshold       float64
	spaceThreshold  float64
	fontSize        float64
	letterSpacing   float64
	blurSigma       float64
	minConfidence   float64
	deblur          int
	topN            int
	charsetTopK     int
	workers         int
	beamWidth       int
	timeout         time.Duration
	quiet           bool
	blurExact       bool
	gamma           bool
	language        bool
	secrets         bool
	escalate        bool
	charsetExplicit bool
}

// fastBlurMinSigma is the sigma at/above which blur mode uses the O(1) box
// approximation (FastBlur) by default: it is ~3× cheaper and, at this radius and
// up, preserves the candidate ranking (validated by the blur matrix). Below it
// the exact GaussianBlur is already cheap, so it stays exact. --blur-exact forces
// the exact operator at any sigma.
const fastBlurMinSigma = 6.0

// blurOperator returns the redaction operator for blur mode: the exact Gaussian
// when forced or at small sigma, else the fast box approximation.
func blurOperator(sigma float64, exact bool) unpixel.Pixelator {
	if exact || sigma < fastBlurMinSigma {
		return defaults.GaussianBlur(sigma)
	}
	return defaults.FastBlur(sigma)
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
		CharsetTopK:    p.charsetTopK,
		Workers:        p.workers,
		BeamWidth:      p.beamWidth,
		Style: unpixel.Style{
			// FontSize 0 lets New apply the 32 pt default; a non-zero flag wins.
			FontSize:      p.fontSize,
			LetterSpacing: p.letterSpacing,
		},
	}
	switch p.strategy {
	case "beam":
		cfg.Strategy = defaults.BeamStrategy(p.beamWidth)
	case "mono":
		cfg.Strategy = defaults.MonospaceStrategy()
	default:
		cfg.Strategy = defaults.GuidedStrategy()
	}
	if p.metric == "ssim" {
		cfg.Metric = defaults.SSIMMetric(0)
	} else {
		cfg.Metric = defaults.PixelmatchMetric()
	}
	// Compose priors: --language and --secrets both contribute when set.
	// WithPriors sums them and merges with any prior already on cfg.LanguageModel.
	var priors []func(string) float64
	if p.language {
		// --language wires both the character-bigram model (local letter
		// transitions) and the dictionary prior (whole-word validity). The two
		// priors are complementary: bigram handles spelling plausibility within
		// tokens; dictionary rewards candidates composed of real words. Both are
		// summed by WithPriors, so enabling one flag gives the full language signal.
		priors = append(priors, defaults.LanguageModel(), lang.DictionaryPrior())
	}
	if p.secrets {
		priors = append(priors, secrets.Prior)
	}
	if len(priors) > 0 {
		unpixel.WithPriors(priors...)(&cfg)
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
	if p.strategy != "guided" && p.strategy != "beam" && p.strategy != "mono" {
		return fmt.Errorf("--strategy must be %q, %q or %q, got %q", "guided", "beam", "mono", p.strategy)
	}
	if p.metric != "pixelmatch" && p.metric != "ssim" {
		return fmt.Errorf("--metric must be %q or %q, got %q", "pixelmatch", "ssim", p.metric)
	}
	switch p.redaction {
	case "auto", "mosaic", "blur":
	default:
		return fmt.Errorf("--redaction must be %q, %q or %q, got %q", "auto", "mosaic", "blur", p.redaction)
	}
	return nil
}

// resultJSON is the stable JSON schema emitted by --format json.
// Field names use snake_case for CLI convention and are stable across versions.
type resultJSON struct {
	BestGuess   string         `json:"best_guess"`
	Font        string         `json:"font,omitempty"`
	Top         []topEntry     `json:"top"`
	Fonts       []fontRankJSON `json:"fonts,omitempty"`
	Offset      offsetJSON     `json:"offset"`
	BestScore   float64        `json:"best_score"`
	TotalScore  float64        `json:"total_score"`
	Fidelity    float64        `json:"fidelity"`
	Trustworthy bool           `json:"trustworthy"`
	Confidence  float64        `json:"confidence"`
	Ambiguity   float64        `json:"ambiguity"`
	Evaluated   int            `json:"evaluated"`
	ElapsedMS   int64          `json:"elapsed_ms"`
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

// candidateFont is one renderer entering a sweep, with labels for display
// (short) and the JSON "font" field (path or bundled name).
type candidateFont struct {
	r        unpixel.Renderer
	display  string
	jsonName string
}

// pathCandidates builds a candidate per font file, skipping (with a note) any
// that fail to parse. boldPath, when set, applies to every font.
func pathCandidates(paths []string, boldPath string) []candidateFont {
	cands := make([]candidateFont, 0, len(paths))
	for _, p := range paths {
		r, err := loadRenderer(p, boldPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unpixel: skipping %q: %v\n", p, err)
			continue
		}
		cands = append(cands, candidateFont{r: r, display: filepath.Base(p), jsonName: p})
	}
	return cands
}

// bundleCandidates builds a candidate per embedded redistributable font.
func bundleCandidates() ([]candidateFont, error) {
	all := fonts.All()
	cands := make([]candidateFont, len(all))
	for i, f := range all {
		r, err := defaults.RendererFromFonts(f.Data, nil)
		if err != nil {
			return nil, fmt.Errorf("built-in font %q: %w", f.Name, err)
		}
		cands[i] = candidateFont{r: r, display: f.Name, jsonName: f.Name}
	}
	return cands, nil
}

// sweepOutcome is the result of one font sweep: the winning recovery plus the
// per-font ranking (and the candidates that produced it) for reporting.
type sweepOutcome struct {
	winner  recoveryResult
	ranked  []unpixel.FontResult
	cands   []candidateFont
	elapsed time.Duration
}

// sweepRecover recovers img once per candidate font (in parallel, via the library
// helper) and returns the best-fit winner and the ranking — without printing, so
// it can be re-run across charset tiers (escalation) before any output.
func sweepRecover(ctx context.Context, img image.Image, base unpixel.Config, cands []candidateFont, p flagParams) (sweepOutcome, error) {
	if len(cands) == 0 {
		return sweepOutcome{}, fmt.Errorf("no usable font to sweep")
	}
	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Sweeping %d fonts…\n", len(cands))
	}
	renderers := make([]unpixel.Renderer, len(cands))
	for i, c := range cands {
		renderers[i] = c.r
	}
	start := time.Now()
	ranked, err := unpixel.RecoverMultiFont(ctx, img, renderers, unpixel.WithConfig(base))
	if err != nil {
		return sweepOutcome{}, err
	}
	winner := resultToRecovery(ranked[0].Result, cands[ranked[0].Index].jsonName)
	return sweepOutcome{winner: winner, ranked: ranked, cands: cands, elapsed: time.Since(start)}, nil
}

// printSweep emits a sweep outcome: the confidence verdict, then the winning
// guess to stdout and the per-font ranking to stderr (text) or a "fonts" array
// (JSON). It applies the --min-confidence honest-abort gate.
func printSweep(o sweepOutcome, p flagParams) error {
	if err := reportConfidence(fidelityOf(o.winner), p); err != nil {
		return err
	}
	if p.format == "json" {
		rankedJSON := make([]fontRankJSON, len(o.ranked))
		for i, fr := range o.ranked {
			rankedJSON[i] = fontRankJSON{
				Font:       o.cands[fr.Index].jsonName,
				BestGuess:  fr.Result.BestGuess,
				TotalScore: fr.Result.BestTotal,
				BestScore:  fr.Result.BestScore,
			}
		}
		return printJSON(o.winner, rankedJSON, 0, o.elapsed)
	}
	if !p.quiet {
		fmt.Fprintln(os.Stderr, "\nFont ranking (best fit first):")
		for i, fr := range o.ranked {
			fmt.Fprintf(os.Stderr, "  %d. %-32s %-20q total=%.4f\n",
				i+1, o.cands[fr.Index].display, fr.Result.BestGuess, fr.Result.BestTotal)
		}
		fmt.Fprintf(os.Stderr, "\nBest font: %s\n", o.cands[o.ranked[0].Index].display)
	}
	fmt.Println(o.winner.bestGuess)
	return nil
}

// runSweep is sweepRecover followed by printSweep (the non-escalating path).
func runSweep(ctx context.Context, img image.Image, base unpixel.Config, cands []candidateFont, p flagParams) error {
	o, err := sweepRecover(ctx, img, base, cands, p)
	if err != nil {
		return err
	}
	return printSweep(o, p)
}

// runEscalation widens the charset (lowercase → alphanumeric → ASCII) until a
// recovery clears the confidence bar, then prints the best tier (P3.6). The first
// tier sweeps the whole font bundle; once the best-fit font is known it is locked
// for the wider, costlier tiers, so escalation only pays for charset width — and
// only when the narrower charset wasn't confident (a lowercase secret stops at
// tier 1).
func runEscalation(ctx context.Context, img image.Image, base unpixel.Config, cands []candidateFont, p flagParams) error {
	tiers := []string{unpixel.CharsetLower, unpixel.CharsetAlnum, unpixel.CharsetASCII}
	var best sweepOutcome
	haveBest := false
	active := cands
	for i, cs := range tiers {
		if ctx.Err() != nil {
			break
		}
		cfg := base
		cfg.Charset = cs
		if !p.quiet && p.format != "json" {
			fmt.Fprintf(os.Stderr, "Charset tier %d/%d (%d chars)…\n", i+1, len(tiers), len([]rune(cs)))
		}
		o, err := sweepRecover(ctx, img, cfg, active, p)
		if err != nil {
			return err
		}
		if !haveBest || fidelityOf(o.winner) > fidelityOf(best.winner) {
			best, haveBest = o, true
		}
		if fidelityOf(o.winner) >= trustBar {
			break // confident enough — don't widen further
		}
		// Lock to the best-fit font so the wider tiers don't re-sweep the bundle.
		active = []candidateFont{o.cands[o.ranked[0].Index]}
	}
	if !haveBest {
		return fmt.Errorf("no usable font to sweep")
	}
	return printSweep(best, p)
}

// trustBar is the whole-image confidence below which a recovery is reported as
// untrustworthy ("uncertain — possibly unrecoverable"). It is the default verdict
// threshold; --min-confidence gates the output itself.
const trustBar = 0.5

// fidelityOf is the CLI mirror of unpixel.Result.Fidelity for a recoveryResult:
// 1 − bestTotal (0 for an empty guess), the honest whole-image confidence.
func fidelityOf(r recoveryResult) float64 {
	if r.bestGuess == "" {
		return 0
	}
	return max(0, min(1, 1-r.bestTotal))
}

// reportConfidence prints a confidence verdict (unless quiet/JSON) and returns an
// error when the recovery is below --min-confidence — an honest abort instead of
// emitting a likely-wrong guess. fidelity is the whole-image confidence in [0,1].
func reportConfidence(fidelity float64, p flagParams) error {
	if !p.quiet && p.format != "json" {
		label := "high"
		switch {
		case fidelity < trustBar:
			label = "low — possibly unrecoverable"
		case fidelity < 0.8:
			label = "medium"
		}
		fmt.Fprintf(os.Stderr, "Confidence: %.2f (%s)\n", fidelity, label)
	}
	if p.minConfidence > 0 && fidelity < p.minConfidence {
		return fmt.Errorf("recovery confidence %.2f is below --min-confidence %.2f; not reporting a likely-wrong guess",
			fidelity, p.minConfidence)
	}
	return nil
}

// resultToRecovery adapts a library unpixel.Result (plus its font name) to the
// CLI's recoveryResult for the shared text/JSON printers.
func resultToRecovery(r unpixel.Result, font string) recoveryResult {
	return recoveryResult{
		bestGuess:  r.BestGuess,
		font:       font,
		top:        r.TopN,
		offset:     r.Offset,
		bestScore:  r.BestScore,
		bestTotal:  r.BestTotal,
		confidence: r.Confidence,
		ambiguity:  r.Ambiguity,
	}
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

// smaller reports whether r covers less than the full bounds b (so cropping is
// worthwhile).
func smaller(r, b image.Rectangle) bool {
	return r.Dx() < b.Dx() || r.Dy() < b.Dy()
}

// cropToRegion returns a fresh origin-(0,0) RGBA copy of img restricted to r, so
// downstream stages (which require equal, zero-origin bounds) work correctly.
func cropToRegion(img image.Image, r image.Rectangle) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	return dst
}

// resolveBlur decides whether to recover blurred (rather than mosaic) text and
// at what sigma, returning 0 for mosaic mode. --redaction blur forces blur
// (sigma from --blur-sigma, else estimated); --redaction mosaic forces mosaic;
// auto picks blur only when there is no mosaic grid and the image looks blurred.
//
// Caveat: auto estimates sigma over the whole image, so a screenshot whose sharp
// reference text dominates can under-estimate; crop to the redacted region, or
// pass --redaction blur (optionally with --blur-sigma).
func resolveBlur(img image.Image, p flagParams) float64 {
	atLeastOne := func(s float64) float64 {
		if s < 1 {
			return 1
		}
		return s
	}
	switch p.redaction {
	case "mosaic":
		return 0
	case "blur":
		if p.blurSigma > 0 {
			return p.blurSigma
		}
		return atLeastOne(unpixel.InferBlurSigma(img))
	default: // auto
		if p.blurSigma > 0 {
			return p.blurSigma
		}
		if unpixel.InferBlockSize(img) >= 2 {
			return 0 // a mosaic grid is present → mosaic mode
		}
		if s := unpixel.InferBlurSigma(img); s >= 2 {
			return s // no grid and clearly blurred → blur mode
		}
		return 0
	}
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

With no --font, UnPixel sweeps a set of built-in redistributable fonts and keeps
the best fit, so you needn't know the redaction's typeface. Pass --font (one or
more) or --font-dir to sweep your own fonts instead, or a single --font to skip
the sweep.

Examples:
  unpixel redacted.png                       # zero-config: sweep the built-in fonts
  unpixel --font Consolas.ttf -b 5 redacted.png
  unpixel --format json --top 10 redacted.png
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
				Usage: `search strategy: "guided" (full DFS), "beam" (bounded), or "mono" (monospace fast-path)`,
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
				Usage: "TTF/OTF font to render candidates with; repeat to sweep several and keep the best fit (default: sweep the built-in redistributable fonts)",
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
			&cli.StringFlag{
				Name:  "redaction",
				Usage: `redaction type: "auto", "mosaic", or "blur"`,
				Value: "auto",
			},
			&cli.FloatFlag{
				Name:  "blur-sigma",
				Usage: "Gaussian blur radius (sigma, px) for --redaction blur; 0 = auto-estimate",
				Value: 0,
			},
			&cli.BoolFlag{
				Name:  "blur-exact",
				Usage: "use the exact Gaussian for blur even at large sigma (default: fast box approximation when sigma is large)",
			},
			&cli.BoolFlag{
				Name:  "gamma",
				Usage: "average mosaic blocks in linear light (matches GIMP/GEGL Pixelize, CSS, most editors) instead of sRGB; use for redactions made by those tools",
			},
			&cli.IntFlag{
				Name:  "deblur",
				Usage: "exploratory: pre-sharpen the input with Richardson-Lucy deconvolution for N iterations (uses --blur-sigma or auto; 0 = off)",
				Value: 0,
			},
			&cli.BoolFlag{
				Name:  "language",
				Usage: "break ties between equally-matching candidates toward plausible text (char-bigram prior)",
			},
			&cli.BoolFlag{
				Name:  "secrets",
				Usage: "prefer structured secrets (UUIDs, hex tokens, Luhn card numbers, common passwords) when breaking ties",
			},
			&cli.FloatFlag{
				Name:  "min-confidence",
				Usage: "refuse a recovery below this whole-image confidence in [0,1] (0 = always report)",
				Value: 0,
			},
			&cli.BoolFlag{
				Name:  "escalate",
				Usage: "when no charset is given, widen it (lower → alnum → ascii) until a confident result",
				Value: true,
			},
			&cli.IntFlag{
				Name:  "charset-topk",
				Usage: "with --language, evaluate only the k most-likely next chars per position (0 = all; trades recall for speed on wide charsets)",
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
		charset:         charset,
		maxLength:       cmd.Int("max-length"),
		blockSize:       cmd.Int("block-size"),
		threshold:       cmd.Float("threshold"),
		spaceThreshold:  cmd.Float("space-threshold"),
		topN:            cmd.Int("top"),
		workers:         cmd.Int("workers"),
		strategy:        cmd.String("strategy"),
		beamWidth:       cmd.Int("beam-width"),
		metric:          cmd.String("metric"),
		format:          cmd.String("format"),
		fontPaths:       cmd.StringSlice("font"),
		fontBoldPath:    cmd.String("font-bold"),
		fontDir:         cmd.String("font-dir"),
		redaction:       cmd.String("redaction"),
		fontSize:        cmd.Float("font-size"),
		letterSpacing:   cmd.Float("letter-spacing"),
		blurSigma:       cmd.Float("blur-sigma"),
		blurExact:       cmd.Bool("blur-exact"),
		gamma:           cmd.Bool("gamma"),
		deblur:          cmd.Int("deblur"),
		language:        cmd.Bool("language"),
		secrets:         cmd.Bool("secrets"),
		minConfidence:   cmd.Float("min-confidence"),
		escalate:        cmd.Bool("escalate"),
		charsetTopK:     cmd.Int("charset-topk"),
		charsetExplicit: cmd.IsSet("charset") || cmd.IsSet("charset-preset"),
		quiet:           cmd.Bool("quiet"),
		timeout:         cmd.Duration("timeout"),
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

	// Zero-config screenshots: when looking for a blur and there is no mosaic
	// grid, crop to the blurred region so sigma estimation and recovery aren't
	// skewed by surrounding sharp text (the whole-image vs region sigma gap).
	if p.redaction != "mosaic" && p.blurSigma == 0 && unpixel.InferBlockSize(img) == 0 {
		if region, ok := unpixel.LocateRedaction(img); ok && smaller(region, img.Bounds()) {
			img = cropToRegion(img, region)
			if !p.quiet {
				fmt.Fprintf(os.Stderr, "Located blurred region: %dx%d\n", region.Dx(), region.Dy())
			}
		}
	}

	// Exploratory: pre-sharpen a blurred input with Richardson-Lucy deconvolution
	// before the search. Off by default; uses the pinned sigma or an estimate.
	if p.deblur > 0 {
		sigma := p.blurSigma
		if sigma <= 0 {
			sigma = unpixel.InferBlurSigma(img)
		}
		if sigma > 0 {
			img = defaults.Deblur(img, sigma, p.deblur)
			if !p.quiet {
				fmt.Fprintf(os.Stderr, "Deblurred input (Richardson-Lucy, sigma=%.1f, %d iterations)\n", sigma, p.deblur)
			}
		}
	}

	cfg := buildConfig(p)
	// Resolve the redaction operator (mosaic vs Gaussian blur). In blur mode the
	// search reproduces a blur instead of mosaic; BlockSize=1 disables the grid.
	if blurSigma := resolveBlur(img, p); blurSigma > 0 {
		cfg.Pixelator = blurOperator(blurSigma, p.blurExact)
		cfg.BlockSize = 1
		// Calibrate the font size from the region's content height when the user
		// did not pin one (blur has no other size cue). A ballpark that seeds the
		// search; an exact --font-size still wins.
		if p.fontSize == 0 {
			if est := unpixel.InferFontSize(img); est >= 8 {
				cfg.Style.FontSize = est
				if !p.quiet {
					fmt.Fprintf(os.Stderr, "Calibrated font size ≈ %.0f pt\n", est)
				}
			}
		}
		if !p.quiet {
			mode := "Gaussian"
			if !p.blurExact && blurSigma >= fastBlurMinSigma {
				mode = "fast (box-approx) Gaussian"
			}
			fmt.Fprintf(os.Stderr, "Redaction: %s blur (σ≈%.1f)\n", mode, blurSigma)
		}
	} else {
		warnIfNoMosaic(os.Stderr, img, p.blockSize, source)
		if p.gamma {
			// Linear-light mosaic (GIMP/GEGL Pixelize, CSS, most editors). Resolve
			// the block size now so the pixelator's grid matches the engine's.
			block := p.blockSize
			if block <= 0 {
				block = unpixel.InferBlockSize(img)
			}
			if block >= 2 {
				cfg.Pixelator = defaults.LinearBlockAverage(block)
				cfg.BlockSize = block
				if !p.quiet {
					fmt.Fprintf(os.Stderr, "Mosaic: linear-light block average (block=%d)\n", block)
				}
			} else if !p.quiet {
				fmt.Fprintln(os.Stderr, "--gamma ignored: no mosaic block grid detected (pass --block-size)")
			}
		}
	}

	fontPaths, ferr := collectFonts(p)
	if ferr != nil {
		return ferr
	}
	switch {
	case len(fontPaths) == 0:
		// Zero-config: no --font given → sweep the bundled redistributable fonts
		// and keep the best fit, so the user needn't know the redaction's typeface.
		cands, berr := bundleCandidates()
		if berr != nil {
			return berr
		}
		// No charset given → widen it until a confident result (P3.6).
		if p.escalate && !p.charsetExplicit {
			return runEscalation(ctx, img, cfg, cands, p)
		}
		return runSweep(ctx, img, cfg, cands, p)
	case len(fontPaths) > 1:
		// Several fonts given: sweep them and keep the best fit.
		return runSweep(ctx, img, cfg, pathCandidates(fontPaths, ""), p)
	}
	// Exactly one --font: render in that typeface (no sweep; keeps live progress).
	font := fontPaths[0]
	renderer, lerr := loadRenderer(font, p.fontBoldPath)
	if lerr != nil {
		return lerr
	}
	cfg.Renderer = renderer

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

	if err := reportConfidence(fidelityOf(best), p); err != nil {
		return err
	}
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
	fid := fidelityOf(r)
	out := resultJSON{
		BestGuess:   r.bestGuess,
		Font:        r.font,
		BestScore:   r.bestScore,
		TotalScore:  r.bestTotal,
		Fidelity:    fid,
		Trustworthy: fid >= trustBar,
		Offset:      offsetJSON{X: r.offset.X, Y: r.offset.Y},
		Confidence:  r.confidence,
		Ambiguity:   r.ambiguity,
		Top:         top,
		Fonts:       fonts,
		Evaluated:   evaluated,
		ElapsedMS:   elapsed.Milliseconds(),
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
