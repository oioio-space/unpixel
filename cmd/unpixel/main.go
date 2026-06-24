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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"  // register GIF decoding for animated/static .gif inputs
	_ "image/jpeg" // register JPEG decoding for .jpg/.jpeg inputs (common for real captures)
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	_ "golang.org/x/image/bmp"  // register BMP decoding
	_ "golang.org/x/image/tiff" // register TIFF decoding
	_ "golang.org/x/image/webp" // register WebP decoding
	"golang.org/x/term"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/blind"            // blind recovery API (P6.6)
	"github.com/oioio-space/unpixel/defaults"         // named: strategy/metric constructors; init() still wires defaults
	"github.com/oioio-space/unpixel/fonts"            // bundled redistributable fonts for the zero-config sweep
	"github.com/oioio-space/unpixel/internal/deblur"  // input normalisation for real blurred captures
	"github.com/oioio-space/unpixel/internal/lang"    // dictionary prior (P3.2)
	"github.com/oioio-space/unpixel/internal/secrets" // structured-secret prior (P3.7)
	"github.com/oioio-space/unpixel/internal/varfont" // variable-font renderer + axis fitter (B1)
	"github.com/oioio-space/unpixel/mosaictext"       // analytic HMM decoder (mono-hmm)
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
	charset             string
	strategy            string
	metric              string
	format              string
	fontPaths           []string
	fontBoldPath        string
	fontDir             string
	redaction           string
	maxLength           int
	blockSize           int
	threshold           float64
	spaceThreshold      float64
	fontSize            float64
	letterSpacing       float64
	blurSigma           float64
	minConfidence       float64
	deblur              int
	topN                int
	charsetTopK         int
	workers             int
	beamWidth           int
	timeout             time.Duration
	quiet               bool
	blurExact           bool
	gamma               string // "auto" | "linear" | "srgb"
	language            bool
	secrets             bool
	escalate            bool
	charsetExplicit     bool
	blind               bool
	lang                string
	denoise             int
	decoder             string // "default", "mono-hmm", or "did"
	normalize           bool
	normalizeBg         string // "divide", "subtract", "none"
	normalizeBin        bool
	deblock             int
	remosaic            bool   // --remosaic: Hill et al. PETS-2016 §4 blur+remosaic path
	remosaicGrid        int    // --remosaic-grid N: pin block grid (0 = auto)
	remosaicLinear      bool   // --remosaic-linear: linear-light block average (GEGL/GIMP)
	letterSpacingSearch bool   // --letter-spacing-search: sweep DefaultLetterSpacings
	thmmLang            string // --thmm-lang: language-structured corpus for trained-hmm (B4.1)
	thmmJPEG            int    // --thmm-jpeg: JPEG quality for emission augmentation (B4.2); 0 = off
	varfontText         string // --varfont-text: known cleartext for varfont calibration mode
	varfontAxes         string // --varfont-axes: comma-separated axis specs e.g. "wght:200:900:500"
	varfontLinear       bool   // --varfont-linear: linear-light pixelation for varfont decoder
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
	switch p.decoder {
	case "", "default", "mono-hmm", "ref-match", "window-hmm", "trained-hmm", "varfont", "did": // "" is equivalent to "default"
	default:
		return fmt.Errorf("--decoder must be %q, %q, %q, %q, %q, %q, or %q, got %q",
			"default", "mono-hmm", "ref-match", "window-hmm", "trained-hmm", "varfont", "did", p.decoder)
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
	bestGuess      string
	font           string // font file used for this recovery ("" = embedded default)
	top            []unpixel.Eval
	offset         unpixel.Offset
	bestScore      float64
	bestTotal      float64
	confidence     float64
	ambiguity      float64
	belowThreshold bool // true when no candidate passed the acceptance gate
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
			best.belowThreshold = r.BelowThreshold
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
		bestGuess:      r.BestGuess,
		font:           font,
		top:            r.TopN,
		offset:         r.Offset,
		bestScore:      r.BestScore,
		bestTotal:      r.BestTotal,
		confidence:     r.Confidence,
		ambiguity:      r.Ambiguity,
		belowThreshold: r.BelowThreshold,
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
		// Prefer mosaic when either the exact GCD detector or the robust
		// autocorrelation detector finds a periodic block grid. The robust
		// detector fires on noisy/JPEG-compressed images where GCD fails.
		// Blur wins only when there is real soft-edge signal (σ ≥ 2) AND
		// neither detector sees a block structure.
		if unpixel.InferBlockSize(img) >= 2 {
			return 0 // exact GCD grid detected → mosaic mode
		}
		if _, support := unpixel.InferBlockSizeRobust(img); support >= unpixel.RobustSupportThreshold {
			return 0 // robust autocorrelation grid detected → mosaic mode
		}
		if s := unpixel.InferBlurSigma(img); s >= 2 {
			return s // no grid and clearly blurred → blur mode
		}
		return 0
	}
}

// blurNeedsSearch reports whether the blur path should use RecoverBlurred
// (zero-config σ-search) rather than a fixed-sigma Recover. This is true when
// --redaction blur is forced but no explicit --blur-sigma was given, or when
// auto-mode has decided blur is needed but the sigma is still unresolved. In
// both cases the caller has not pinned σ, so RecoverBlurred's coarse-to-fine
// sweep is the right tool.
func blurNeedsSearch(p flagParams) bool {
	return p.blurSigma <= 0 && (p.redaction == "blur" || p.redaction == "auto")
}

// runBlurRecover runs RecoverBlurred (zero-config σ-search) on img and prints
// the result. It is called when --redaction blur (or auto) is active and no
// explicit --blur-sigma was given. WithConfig(cfg) forwards all caller options
// (charset, style, strategy, language prior, …); RecoverBlurred overrides
// BlockSize and Pixelator internally, so those fields are irrelevant here.
// When --lang is set, buildConfig has already wired cfg.LanguageModel, so the
// language prior flows through automatically.
func runBlurRecover(ctx context.Context, img image.Image, p flagParams, cfg unpixel.Config, start time.Time) error {
	if !p.quiet {
		fmt.Fprintln(os.Stderr, "Redaction: blur (σ auto-search via RecoverBlurred)")
	}

	blurOpts := []unpixel.Option{unpixel.WithConfig(cfg)}
	if p.normalize {
		bg, err := parseNormalizeBg(p.normalizeBg)
		if err != nil {
			return err
		}
		blurOpts = append(blurOpts, unpixel.WithNormalize(func(o *deblur.Options) {
			o.Bg = bg
			o.Binarize = p.normalizeBin
			o.Deblock = p.deblock
		}))
		if !p.quiet {
			fmt.Fprintf(os.Stderr, "Normalising input (bg=%s deblock=%d binarize=%v)\n",
				p.normalizeBg, p.deblock, p.normalizeBin)
		}
	}
	if p.remosaic {
		blurOpts = append(blurOpts, unpixel.WithRemosaicGrid(p.remosaicGrid))
		if p.remosaicLinear {
			blurOpts = append(blurOpts, unpixel.WithRemosaicLinear())
		}
		if !p.quiet {
			grid := "auto"
			if p.remosaicGrid > 0 {
				grid = fmt.Sprintf("%d", p.remosaicGrid)
			}
			light := "sRGB"
			if p.remosaicLinear {
				light = "linear"
			}
			fmt.Fprintf(os.Stderr, "Remosaic: enabled (grid=%s, %s)\n", grid, light)
		}
	}

	res, err := unpixel.RecoverBlurred(ctx, img, blurOpts...)
	if err != nil {
		return fmt.Errorf("RecoverBlurred: %w", err)
	}

	if !p.quiet {
		fmt.Fprintf(os.Stderr, "Blur σ chosen: %.2f  best: %q  total: %.4f  normalized: %v\n",
			res.BlurSigma, res.BestGuess, res.BestTotal, res.Normalized)
	}

	best := resultToRecovery(res, "")
	elapsed := time.Since(start)
	if err := reportConfidence(fidelityOf(best), p); err != nil {
		return err
	}
	switch p.format {
	case "json":
		return printJSON(best, nil, 0, elapsed)
	default:
		printText(best, p.quiet || !isTTY(os.Stderr))
	}
	return nil
}

// parseNormalizeBg converts the --normalize-bg flag string to a deblur.BgModel.
func parseNormalizeBg(s string) (deblur.BgModel, error) {
	switch s {
	case "", "divide":
		return deblur.BgDivide, nil
	case "subtract":
		return deblur.BgSubtract, nil
	case "none":
		return deblur.BgNone, nil
	default:
		return deblur.BgDivide, fmt.Errorf("--normalize-bg: unknown model %q (valid: divide, subtract, none)", s)
	}
}

// parseGammaMode converts the --gamma flag string to a blind.GammaMode.
// An empty string defaults to GammaAuto (the blind-mode default).
// Returns an error for unrecognised values.
func parseGammaMode(s string) (blind.GammaMode, error) {
	switch s {
	case "", "auto":
		return blind.GammaAuto, nil
	case "linear":
		return blind.GammaLinear, nil
	case "srgb":
		return blind.GammaSRGB, nil
	default:
		return blind.GammaAuto, fmt.Errorf(`--gamma must be "auto", "linear", or "srgb", got %q`, s)
	}
}

// runBlind runs the blind-recovery pipeline (P6.6) when --blind is set.
// It reuses --block-size, --font-size, and --gamma from the classic path and
// prints the recovered text to stdout.
func runBlind(ctx context.Context, imgPath string, p flagParams) error {
	l, ok := lang.ParseLanguage(p.lang)
	if !ok {
		return fmt.Errorf("--lang: unknown language %q (supported: en, fr)", p.lang)
	}

	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	gammaMode, err := parseGammaMode(p.gamma)
	if err != nil {
		return err
	}

	opts := []blind.Option{
		blind.WithLanguage(l),
		blind.WithGamma(gammaMode),
	}
	if p.blockSize > 0 {
		opts = append(opts, blind.WithBlock(p.blockSize))
	}
	if p.fontSize > 0 {
		opts = append(opts, blind.WithFontSize(p.fontSize))
	}
	// --denoise: -1 (default) means auto; pass WithDenoise only when the user
	// explicitly set the flag (0 = off, N > 0 = force radius N).
	if p.denoise >= 0 {
		opts = append(opts, blind.WithDenoise(p.denoise))
	}
	if p.letterSpacingSearch {
		opts = append(opts, blind.WithLetterSpacingSearch(blind.DefaultLetterSpacings...))
	}

	if !p.quiet {
		fmt.Fprintf(os.Stderr, "Blind recovery (lang=%s)…\n", l)
	}

	result, err := blind.Recover(ctx, img, opts...)
	if err != nil {
		return fmt.Errorf("blind recovery: %w", err)
	}

	if !p.quiet {
		fmt.Fprintf(os.Stderr, "Font: %s  block: %d  dist: %.4f  denoise: %d  gamma: %s  letter-spacing: %.2f\n",
			result.Font, result.Block, result.Dist, result.Denoise, result.Gamma, result.LetterSpacing)
	}
	fmt.Println(result.Text)
	return nil
}

// runHMM runs the analytic HMM decoder (mosaictext.DecodeHMM) when --decoder
// mono-hmm is set. It reuses --lang, --font-size, --gamma (linear), --charset,
// --quiet, --format, and --block-size from the standard flags. The result is
// printed in the same text/JSON format as the guided path so tooling is consistent.
func runHMM(ctx context.Context, imgPath string, p flagParams) error {
	l, ok := lang.ParseLanguage(p.lang)
	if !ok {
		return fmt.Errorf("--lang: unknown language %q (supported: en, fr)", p.lang)
	}

	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	charset := p.charset
	if charset == "" {
		charset = mosaictext.DefaultHMMCharset
	}

	opts := []mosaictext.HMMOption{
		mosaictext.WithLanguage(l),
		mosaictext.WithCharset(charset),
	}
	if p.fontSize > 0 {
		opts = append(opts, mosaictext.WithFontSize(p.fontSize))
	}
	// Block size is inferred by DecodeHMM from the image; when the user pins
	// it via --block-size we have no direct HMMOption — InferBlockGrid already
	// reads from the image, so we just let it run. A future WithBlockSize
	// option can override if needed.

	// Font selection: when --font names a file that exists on disk, read its bytes
	// and pass WithFontFile so DecodeHMM uses that exact typeface (skipping the
	// bundled-mono sweep). When the path does not exist on disk, treat it as a
	// bundled font name and fall back to WithFont. --font-bold only applies
	// alongside WithFontFile; a read error on an existing bold file is returned.
	fontSource := "bundled mono sweep"
	if len(p.fontPaths) > 0 && p.fontPaths[0] != "" {
		fontPath := p.fontPaths[0]
		data, readErr := os.ReadFile(fontPath) // #nosec G304 -- user-supplied path
		switch {
		case readErr == nil:
			opts = append(opts, mosaictext.WithFontFile(data))
			fontSource = "file:" + filepath.Base(fontPath)
			if p.fontBoldPath != "" {
				boldData, boldErr := os.ReadFile(p.fontBoldPath) // #nosec G304 -- user-supplied path
				if boldErr != nil {
					return fmt.Errorf("--font-bold: %w", boldErr)
				}
				opts = append(opts, mosaictext.WithFontFileBold(boldData))
			}
		case os.IsNotExist(readErr):
			// Path does not exist on disk — interpret as a bundled font name.
			opts = append(opts, mosaictext.WithFont(fontPath))
			fontSource = "bundled:" + fontPath
		default:
			return fmt.Errorf("--font: %w", readErr)
		}
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Decoder: mono-hmm (lang=%s charset=%d chars font=%s)\n",
			l, len([]rune(charset)), fontSource)
	}

	res, err := mosaictext.DecodeHMM(ctx, img, opts...)
	if err != nil {
		return fmt.Errorf("DecodeHMM: %w", err)
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Font: %s  linear: %v  block: %d  N: %d  dist: %.2f\n",
			res.Font, res.Linear, res.BlockSize, res.CharCount, res.Distance)
	}

	switch p.format {
	case "json":
		out := struct {
			BestGuess  string  `json:"best_guess"`
			Font       string  `json:"font,omitempty"`
			Linear     bool    `json:"linear"`
			BlockSize  int     `json:"block_size"`
			CharCount  int     `json:"char_count"`
			GridPhaseX int     `json:"grid_phase_x"`
			Distance   float64 `json:"distance"`
		}{
			BestGuess:  res.Text,
			Font:       res.Font,
			Linear:     res.Linear,
			BlockSize:  res.BlockSize,
			CharCount:  res.CharCount,
			GridPhaseX: res.GridPhaseX,
			Distance:   res.Distance,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(out); encErr != nil {
			return fmt.Errorf("encode json: %w", encErr)
		}
	default:
		fmt.Println(res.Text)
	}
	return nil
}

// runDID runs the Document Image Decoding trellis decoder (mosaictext.DecodeDID)
// when --decoder did is set. It reuses --lang, --font-size, --gamma (linear),
// --charset, --quiet, --format, and --block-size from the standard flags. The
// result is printed in the same text/JSON format as the other decoders so
// tooling is consistent.
func runDID(ctx context.Context, imgPath string, p flagParams) error {
	l, ok := lang.ParseLanguage(p.lang)
	if !ok {
		return fmt.Errorf("--lang: unknown language %q (supported: en, fr)", p.lang)
	}

	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	charset := p.charset
	if charset == "" {
		charset = mosaictext.DefaultDIDCharset
	}

	opts := []mosaictext.DIDOption{
		mosaictext.WithDIDLanguage(l),
		mosaictext.WithDIDCharset(charset),
	}
	if p.fontSize > 0 {
		opts = append(opts, mosaictext.WithDIDFontSize(p.fontSize))
	}
	if p.blockSize > 0 {
		opts = append(opts, mosaictext.WithDIDBlockSize(p.blockSize))
	}

	// Font selection: when --font names a file that exists on disk, read its
	// bytes and pass WithDIDFontFile. When the path does not exist on disk,
	// treat it as a bundled font name and use WithDIDFont.
	fontSource := "bundled sweep"
	if len(p.fontPaths) > 0 && p.fontPaths[0] != "" {
		fontPath := p.fontPaths[0]
		data, readErr := os.ReadFile(fontPath) // #nosec G304 -- user-supplied path
		switch {
		case readErr == nil:
			opts = append(opts, mosaictext.WithDIDFontFile(data))
			fontSource = "file:" + filepath.Base(fontPath)
			if p.fontBoldPath != "" {
				boldData, boldErr := os.ReadFile(p.fontBoldPath) // #nosec G304 -- user-supplied path
				if boldErr != nil {
					return fmt.Errorf("--font-bold: %w", boldErr)
				}
				opts = append(opts, mosaictext.WithDIDFontFileBold(boldData))
			}
		case os.IsNotExist(readErr):
			opts = append(opts, mosaictext.WithDIDFont(fontPath))
			fontSource = "bundled:" + fontPath
		default:
			return fmt.Errorf("--font: %w", readErr)
		}
	}

	// Linear-light mode: --gamma maps to WithDIDLinear.
	// "linear" → linear-light only (1); "srgb" → sRGB only (0); "auto"/""→ sweep (-1).
	switch p.gamma {
	case "linear":
		opts = append(opts, mosaictext.WithDIDLinear(1))
	case "srgb":
		opts = append(opts, mosaictext.WithDIDLinear(0))
	default:
		opts = append(opts, mosaictext.WithDIDLinear(-1))
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Decoder: did (lang=%s charset=%d chars font=%s)\n",
			l, len([]rune(charset)), fontSource)
	}

	res, err := mosaictext.DecodeDID(ctx, img, opts...)
	if err != nil {
		return fmt.Errorf("DecodeDID: %w", err)
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Font: %s  linear: %v  block: %d  phaseX: %d  evals: %d  dist: %.4f\n",
			res.Font, res.Linear, res.BlockSize, res.GridPhaseX, res.EmissionEvals, res.Distance)
	}

	switch p.format {
	case "json":
		out := struct {
			BestGuess     string  `json:"best_guess"`
			Font          string  `json:"font,omitempty"`
			Linear        bool    `json:"linear"`
			BlockSize     int     `json:"block_size"`
			GridPhaseX    int     `json:"grid_phase_x"`
			EmissionEvals int     `json:"emission_evals"`
			Distance      float64 `json:"distance"`
		}{
			BestGuess:     res.Text,
			Font:          res.Font,
			Linear:        res.Linear,
			BlockSize:     res.BlockSize,
			GridPhaseX:    res.GridPhaseX,
			EmissionEvals: res.EmissionEvals,
			Distance:      res.Distance,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(out); encErr != nil {
			return fmt.Errorf("encode json: %w", encErr)
		}
	default:
		fmt.Println(res.Text)
	}
	return nil
}

// runRefMatch runs the reference-matching decoder (mosaictext.DecodeReference)
// when --decoder ref-match is set. It reuses --charset, --font, --font-bold,
// --quiet, and --format from the standard flags. Block size is auto-inferred
// by DecodeReference from the image.
func runRefMatch(ctx context.Context, imgPath string, p flagParams) error {
	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	charset := p.charset
	if charset == "" {
		charset = mosaictext.DefaultRefCharset
	}

	opts := []mosaictext.RefOption{
		mosaictext.WithRefCharset(charset),
	}

	// Font selection: path on disk → WithRefFontFile; bundled name → WithRefFont.
	fontSource := "bundled sweep"
	if len(p.fontPaths) > 0 && p.fontPaths[0] != "" {
		fontPath := p.fontPaths[0]
		data, readErr := os.ReadFile(fontPath) // #nosec G304 -- user-supplied path
		switch {
		case readErr == nil:
			opts = append(opts, mosaictext.WithRefFontFile(data))
			fontSource = "file:" + filepath.Base(fontPath)
			if p.fontBoldPath != "" {
				boldData, boldErr := os.ReadFile(p.fontBoldPath) // #nosec G304 -- user-supplied path
				if boldErr != nil {
					return fmt.Errorf("--font-bold: %w", boldErr)
				}
				opts = append(opts, mosaictext.WithRefFontFileBold(boldData))
			}
		case os.IsNotExist(readErr):
			opts = append(opts, mosaictext.WithRefFont(fontPath))
			fontSource = "bundled:" + fontPath
		default:
			return fmt.Errorf("--font: %w", readErr)
		}
	}

	// Language model: --lang enables the LM beam over per-cell geometric
	// distances. Without --lang the greedy path is used unchanged —
	// additive and opt-in; byte-identical to versions without this option.
	lmNote := ""
	if p.lang != "" {
		l, ok := lang.ParseLanguage(p.lang)
		if !ok {
			return fmt.Errorf("--lang: unknown language %q (supported: en, fr)", p.lang)
		}
		opts = append(opts, mosaictext.WithRefLanguage(l))
		lmNote = " lm=" + p.lang
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Decoder: ref-match (charset=%d chars font=%s%s)\n",
			len([]rune(charset)), fontSource, lmNote)
	}

	res, err := mosaictext.DecodeReference(ctx, img, opts...)
	if err != nil {
		return fmt.Errorf("DecodeReference: %w", err)
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Font: %s  linear: %v  block: %d  N: %d  dist: %.4f\n",
			res.Font, res.Linear, res.BlockSize, res.CharCount, res.Distance)
	}

	switch p.format {
	case "json":
		out := struct {
			BestGuess  string  `json:"best_guess"`
			Font       string  `json:"font,omitempty"`
			Linear     bool    `json:"linear"`
			BlockSize  int     `json:"block_size"`
			CharCount  int     `json:"char_count"`
			GridPhaseX int     `json:"grid_phase_x"`
			Distance   float64 `json:"distance"`
		}{
			BestGuess:  res.Text,
			Font:       res.Font,
			Linear:     res.Linear,
			BlockSize:  res.BlockSize,
			CharCount:  res.CharCount,
			GridPhaseX: res.GridPhaseX,
			Distance:   res.Distance,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(out); encErr != nil {
			return fmt.Errorf("encode json: %w", encErr)
		}
	default:
		fmt.Println(res.Text)
	}
	return nil
}

// runWindowHMM runs the grid-window beam-search decoder
// (mosaictext.DecodeWindowHMM) when --decoder window-hmm is set. It is the
// proportional-font-capable decoder: at each character position it renders the
// full candidate string, extracts a per-character window of block columns, and
// scores by MSE. It reuses --charset, --font/--font-bold (user font → that
// font only, else the bundled sweep), and --format.
func runWindowHMM(ctx context.Context, imgPath string, p flagParams) error {
	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	charset := p.charset
	if charset == "" {
		charset = mosaictext.DefaultWHMMCharset
	}

	opts := []mosaictext.WHMMOption{
		mosaictext.WithWHMMCharset(charset),
	}

	// Font selection: path on disk → WithWHMMFontFile; bundled name → WithWHMMFont.
	fontSource := "bundled sweep"
	if len(p.fontPaths) > 0 && p.fontPaths[0] != "" {
		fontPath := p.fontPaths[0]
		data, readErr := os.ReadFile(fontPath) // #nosec G304 -- user-supplied path
		switch {
		case readErr == nil:
			opts = append(opts, mosaictext.WithWHMMFontFile(data))
			fontSource = "file:" + filepath.Base(fontPath)
			if p.fontBoldPath != "" {
				boldData, boldErr := os.ReadFile(p.fontBoldPath) // #nosec G304 -- user-supplied path
				if boldErr != nil {
					return fmt.Errorf("--font-bold: %w", boldErr)
				}
				opts = append(opts, mosaictext.WithWHMMFontFileBold(boldData))
			}
		case os.IsNotExist(readErr):
			opts = append(opts, mosaictext.WithWHMMFont(fontPath))
			fontSource = "bundled:" + fontPath
		default:
			return fmt.Errorf("--font: %w", readErr)
		}
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Decoder: window-hmm beam (charset=%d chars font=%s)\n",
			len([]rune(charset)), fontSource)
	}

	res, err := mosaictext.DecodeWindowHMM(ctx, img, opts...)
	if err != nil {
		return fmt.Errorf("DecodeWindowHMM: %w", err)
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Font: %s  linear: %v  block: %d  N: %d  dist: %.4f\n",
			res.Font, res.Linear, res.BlockSize, res.CharCount, res.Distance)
	}

	switch p.format {
	case "json":
		out := struct {
			BestGuess string  `json:"best_guess"`
			Font      string  `json:"font,omitempty"`
			Linear    bool    `json:"linear,omitzero"`
			BlockSize int     `json:"block_size,omitzero"`
			CharCount int     `json:"char_count,omitzero"`
			Distance  float64 `json:"distance,omitzero"`
		}{
			BestGuess: res.Text,
			Font:      res.Font,
			Linear:    res.Linear,
			BlockSize: res.BlockSize,
			CharCount: res.CharCount,
			Distance:  res.Distance,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(out); encErr != nil {
			return fmt.Errorf("encode json: %w", encErr)
		}
	default:
		fmt.Println(res.Text)
	}
	return nil
}

// runTrainedHMM runs the blind column-anchored trained-HMM decoder
// (mosaictext.DecodeTrainedHMM) when --decoder trained-hmm is set.
// It implements Hill-2016 §2.2–2.3: trains on a random corpus at the
// discovered grid, clusters window vectors with KMeans, then runs a single
// Viterbi pass over the target's block-column observations without knowing
// character boundaries.
//
// Reuses --charset, --font/--font-bold (user font → that font only, else
// bundled sweep), --gamma (linear), and --format.
func runTrainedHMM(ctx context.Context, imgPath string, p flagParams) error {
	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	charset := p.charset
	if charset == "" {
		charset = mosaictext.DefaultTHMMCharset
	}

	linear := -1 // auto/sweep
	if p.gamma == "linear" {
		linear = 1
	}

	opts := []mosaictext.THMMOption{
		mosaictext.WithTHMMCharset(charset),
		mosaictext.WithTHMMLinear(linear),
	}

	// Font selection: path on disk → WithTHMMFontFile; bundled name → WithTHMMFont.
	fontSource := "bundled sweep"
	if len(p.fontPaths) > 0 && p.fontPaths[0] != "" {
		fontPath := p.fontPaths[0]
		data, readErr := os.ReadFile(fontPath) // #nosec G304 -- user-supplied path
		switch {
		case readErr == nil:
			opts = append(opts, mosaictext.WithTHMMFontFile(data))
			fontSource = "file:" + filepath.Base(fontPath)
			if p.fontBoldPath != "" {
				boldData, boldErr := os.ReadFile(p.fontBoldPath) // #nosec G304 -- user-supplied path
				if boldErr != nil {
					return fmt.Errorf("--font-bold: %w", boldErr)
				}
				opts = append(opts, mosaictext.WithTHMMFontFileBold(boldData))
			}
		case os.IsNotExist(readErr):
			opts = append(opts, mosaictext.WithTHMMFont(fontPath))
			fontSource = "bundled:" + fontPath
		default:
			return fmt.Errorf("--font: %w", readErr)
		}
	}

	// B4.1: language-structured corpus via --thmm-lang.
	// Does NOT fall back to --lang (that flag belongs to the blind decoder and
	// defaults to "en", so inheriting it here would silently enable the language
	// sampler for every trained-hmm invocation, changing the default path).
	if p.thmmLang != "" {
		l, ok := lang.ParseLanguage(p.thmmLang)
		if !ok {
			return fmt.Errorf("--thmm-lang: unknown language %q (supported: en, fr)", p.thmmLang)
		}
		opts = append(opts, mosaictext.WithTHMMLanguage(l))
	}

	// B4.2: JPEG-augmented emission training via --thmm-jpeg.
	if p.thmmJPEG > 0 {
		opts = append(opts, mosaictext.WithTHMMJPEG(p.thmmJPEG))
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Decoder: trained-hmm (charset=%d chars font=%s lang=%s jpeg=%d)\n",
			len([]rune(charset)), fontSource, p.thmmLang, p.thmmJPEG)
	}

	res, err := mosaictext.DecodeTrainedHMM(ctx, img, opts...)
	if err != nil {
		return fmt.Errorf("DecodeTrainedHMM: %w", err)
	}

	if !p.quiet && p.format != "json" {
		fmt.Fprintf(os.Stderr, "Font: %s  linear: %v  block: %d  N: %d  dist: %.4f\n",
			res.Font, res.Linear, res.BlockSize, res.CharCount, res.Distance)
	}

	switch p.format {
	case "json":
		out := struct {
			BestGuess  string  `json:"best_guess"`
			Font       string  `json:"font,omitempty"`
			Linear     bool    `json:"linear,omitzero"`
			BlockSize  int     `json:"block_size,omitzero"`
			CharCount  int     `json:"char_count,omitzero"`
			GridPhaseX int     `json:"grid_phase_x,omitzero"`
			Distance   float64 `json:"distance,omitzero"`
		}{
			BestGuess:  res.Text,
			Font:       res.Font,
			Linear:     res.Linear,
			BlockSize:  res.BlockSize,
			CharCount:  res.CharCount,
			GridPhaseX: res.GridPhaseX,
			Distance:   res.Distance,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(out); encErr != nil {
			return fmt.Errorf("encode json: %w", encErr)
		}
	default:
		fmt.Println(res.Text)
	}
	return nil
}

// parseVarFontAxes parses a comma-separated list of axis specs of the form
// "tag:min:max:start" (e.g. "wght:200:900:500") into a []varfont.AxisSpec.
// Returns an error for malformed entries.
func parseVarFontAxes(s string) ([]varfont.AxisSpec, error) {
	if s == "" {
		return nil, fmt.Errorf("--varfont-axes: at least one axis required (e.g. \"wght:200:900:500\")")
	}
	var specs []varfont.AxisSpec
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.SplitN(part, ":", 4)
		if len(fields) != 4 {
			return nil, fmt.Errorf("--varfont-axes: %q must be tag:min:max:start", part)
		}
		tag := fields[0]
		labels := [3]string{"min", "max", "start"}
		vals := [3]float32{}
		for i, label := range labels {
			v, err := strconv.ParseFloat(fields[1+i], 32)
			if err != nil {
				return nil, fmt.Errorf("--varfont-axes: %q %s: %w", part, label, err)
			}
			vals[i] = float32(v)
		}
		specs = append(specs, varfont.AxisSpec{
			Tag:   tag,
			Min:   vals[0],
			Max:   vals[1],
			Start: vals[2],
		})
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("--varfont-axes: no valid axis specs found in %q", s)
	}
	return specs, nil
}

// runVarFont runs the variable-font decoder (mosaictext.DecodeVarFont) when
// --decoder varfont is set. It fits the font's design axes to the redaction
// using the render→pixelate→metric pipeline and reports the fitted axes.
//
// Two modes:
//   - Calibration (--varfont-text): fits axes to the known text, returning
//     the best-fit axis values. Use this when you know a fragment of the
//     redacted text (Bishop Fox method).
//   - Blind (no --varfont-text): joint text+axis search over --charset
//     candidates (up to MaxBlindCandidates single characters). Only tractable
//     for very short strings; prefer calibration mode for real use.
//
// Reuses --font/--font-bold (user font → that font only, else bundled Nunito
// default), --block-size, --charset, --font-size, --gamma (linear), and
// --format.
func runVarFont(ctx context.Context, imgPath string, p flagParams) error {
	img, err := loadImage(imgPath)
	if err != nil {
		return err
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	// Parse the required axis specs.
	axes, err := parseVarFontAxes(p.varfontAxes)
	if err != nil {
		return err
	}

	// Resolve the variable font: user-supplied file → parse it; else use the
	// bundled Nunito default (DecodeVarFont handles the nil case).
	var font *varfont.Font
	fontSource := "bundled Nunito variable font"
	if len(p.fontPaths) > 0 && p.fontPaths[0] != "" {
		fontPath := p.fontPaths[0]
		data, readErr := os.ReadFile(fontPath) // #nosec G304 -- user-supplied path
		if readErr != nil {
			return fmt.Errorf("--font: %w", readErr)
		}
		font, err = varfont.ParseFont(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("--font: parse variable font %q: %w", fontPath, err)
		}
		fontSource = "file:" + filepath.Base(fontPath)
	}

	linear := p.varfontLinear || p.gamma == "linear"

	style := varfont.DefaultStyle()
	if p.fontSize > 0 {
		style.FontSize = p.fontSize
	}
	if p.letterSpacing != 0 {
		style.LetterSpacing = p.letterSpacing
	}

	opts := []mosaictext.VarFontOption{
		mosaictext.WithVarFontStyle(style),
		mosaictext.WithVarFontLinear(linear),
		mosaictext.WithVarFontAxes(axes),
	}
	if font != nil {
		opts = append(opts, mosaictext.WithVarFont(font))
	}
	if p.blockSize > 0 {
		opts = append(opts, mosaictext.WithVarFontBlockSize(p.blockSize))
	}
	if p.varfontText != "" {
		opts = append(opts, mosaictext.WithVarFontText(p.varfontText))
	}
	if p.charset != "" && p.varfontText == "" {
		// charset only used in blind mode
		opts = append(opts, mosaictext.WithVarFontCharset(p.charset))
	}

	mode := "blind"
	if p.varfontText != "" {
		mode = "calibration (text=" + p.varfontText + ")"
	}
	if !p.quiet && p.format != "json" {
		axisDesc := make([]string, len(axes))
		for i, a := range axes {
			axisDesc[i] = fmt.Sprintf("%s[%.0f–%.0f]@%.0f", a.Tag, a.Min, a.Max, a.Start)
		}
		fmt.Fprintf(os.Stderr, "Decoder: varfont (%s font=%s axes=%s linear=%v)\n",
			mode, fontSource, strings.Join(axisDesc, ","), linear)
	}

	res, err := mosaictext.DecodeVarFont(ctx, img, opts...)
	if err != nil {
		return fmt.Errorf("DecodeVarFont: %w", err)
	}

	if !p.quiet && p.format != "json" {
		axisVals := make([]string, len(res.FittedAxes))
		for i, a := range res.FittedAxes {
			axisVals[i] = fmt.Sprintf("%s=%.1f", a.Tag, a.Value)
		}
		fmt.Fprintf(os.Stderr, "Fitted: %s  dist=%.4f  evals=%d  linear=%v  block=%d\n",
			strings.Join(axisVals, " "), res.Distance, res.Evals, res.Linear, res.BlockSize)
	}

	switch p.format {
	case "json":
		type axisJSON struct {
			Tag   string  `json:"tag"`
			Value float64 `json:"value"`
		}
		axesJSON := make([]axisJSON, len(res.FittedAxes))
		for i, a := range res.FittedAxes {
			axesJSON[i] = axisJSON{Tag: a.Tag, Value: float64(a.Value)}
		}
		out := struct {
			BestGuess  string     `json:"best_guess"`
			FittedAxes []axisJSON `json:"fitted_axes"`
			Distance   float64    `json:"distance,omitzero"`
			Evals      int        `json:"evals,omitzero"`
			Linear     bool       `json:"linear,omitzero"`
			BlockSize  int        `json:"block_size,omitzero"`
		}{
			BestGuess:  res.Text,
			FittedAxes: axesJSON,
			Distance:   res.Distance,
			Evals:      res.Evals,
			Linear:     res.Linear,
			BlockSize:  res.BlockSize,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(out); encErr != nil {
			return fmt.Errorf("encode json: %w", encErr)
		}
	default:
		fmt.Println(res.Text)
	}
	return nil
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
			&cli.StringFlag{
				Name:  "decoder",
				Usage: `decoder backend: "default" (guided DFS / beam), "mono-hmm" (analytic HMM beam for monospace), "ref-match" (pixel-exact reference matching), "window-hmm" (grid-window beam; proportional fonts), "trained-hmm" (blind column-anchored trained HMM; Hill-2016 §2.2–2.3), or "varfont" (variable-font axis fitter; requires --varfont-axes)`,
				Value: "default",
			},
			&cli.BoolFlag{
				Name:  "blind",
				Usage: "blind recovery mode: no charset needed — recovers text via dictionary beam search (P6.6)",
			},
			&cli.StringFlag{
				Name:  "lang",
				Usage: `language model for --blind: "en" (English, default) or "fr" (French)`,
				Value: "en",
			},
			&cli.IntFlag{
				Name:  "denoise",
				Value: -1,
				Usage: "blind mode: median denoise radius — -1=auto-detect (default), 0=off, N=force ((2N+1)×(2N+1) kernel; 1=3×3, 2=5×5)",
			},
			&cli.BoolFlag{
				Name:  "letter-spacing-search",
				Usage: "blind mode: sweep letter-spacing values " + fmt.Sprintf("%v", blind.DefaultLetterSpacings) + " px and keep the result with the lowest image distance; records the winner in the diagnostic line. Default off (spacing 0 only).",
			},
			&cli.BoolFlag{
				Name:  "blur-exact",
				Usage: "use the exact Gaussian for blur even at large sigma (default: fast box approximation when sigma is large)",
			},
			&cli.StringFlag{
				Name:  "gamma",
				Usage: `colour space for block averaging: "auto" (try both, pick lower distance), "linear" (GIMP/GEGL Pixelize, CSS), or "srgb" (original unredacter / Jimp). Default: "auto" for --blind, "srgb" for the classic mosaic path.`,
				Value: "",
			},
			&cli.IntFlag{
				Name:  "deblur",
				Usage: "exploratory: pre-sharpen the input with Richardson-Lucy deconvolution for N iterations (uses --blur-sigma or auto; 0 = off)",
				Value: 0,
			},
			&cli.BoolFlag{
				Name:  "normalize",
				Usage: "apply input normalisation before blur recovery (recommended for real captures with textured/vignette backgrounds, dark themes, or JPEG blocking)",
			},
			&cli.StringFlag{
				Name:  "normalize-bg",
				Usage: `background-removal model for --normalize: "divide" (multiplicative vignette, default), "subtract" (additive offset), or "none" (skip)`,
				Value: "divide",
			},
			&cli.BoolFlag{
				Name:  "normalize-binarize",
				Usage: "binarise the normalised image (threshold at mean luminance); useful for very noisy captures",
			},
			&cli.IntFlag{
				Name:  "deblock",
				Usage: "median-filter radius applied during --normalize to suppress JPEG blocking (0 = off, -1 = auto, positive = forced radius)",
				Value: -1,
			},
			&cli.BoolFlag{
				Name: "remosaic",
				Usage: "enable Hill–Zhou–Saul–Shacham PETS-2016 §4 remosaic error-correction for blur recovery: " +
					"forward operator becomes render→GaussianBlur(σ)→BlockAverage(b); " +
					"target is pre-mosaiced by BlockAverage(b) once, collapsing σ-mismatch and JPEG noise. " +
					"Only affects --redaction blur / RecoverBlurred. " +
					"Combine with --remosaic-grid to pin the block size (default: auto, b=max(2,round(σ))).",
			},
			&cli.IntFlag{
				Name:  "remosaic-grid",
				Usage: "block grid size b for --remosaic (0 = auto: b=max(2,round(σ))). Implies --remosaic.",
				Value: 0,
			},
			&cli.BoolFlag{
				Name:  "remosaic-linear",
				Usage: "use linear-light block averaging for --remosaic (GEGL/GIMP-redacted targets). Implies --remosaic.",
			},
			&cli.StringFlag{
				Name:  "thmm-lang",
				Usage: `trained-hmm (B4.1): draw training strings from the word list for "en" or "fr" instead of uniform-random chars, baking real letter n-grams into the HMM transition matrix. Falls back to --lang when unset. Default off (uniform-random, output identical to previous versions).`,
				Value: "",
			},
			&cli.IntFlag{
				Name:  "thmm-jpeg",
				Usage: "trained-hmm (B4.2): JPEG-roundtrip each rendered training image at this quality [1–100] before pixelation, so the KMeans emission clusters absorb JPEG artefacts. Use when the target was captured as a JPEG. 0 = off (default, output identical to previous versions).",
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
			&cli.StringFlag{
				Name:  "varfont-text",
				Usage: "varfont decoder: known cleartext in the redaction for calibration mode (Bishop Fox method); omit for blind single-character axis search",
				Value: "",
			},
			&cli.StringFlag{
				Name:  "varfont-axes",
				Usage: `varfont decoder: comma-separated axis specs "tag:min:max:start" (e.g. "wght:200:900:500"); required for --decoder varfont`,
				Value: "",
			},
			&cli.BoolFlag{
				Name:  "varfont-linear",
				Usage: "varfont decoder: use linear-light block averaging (GEGL/GIMP Pixelize); overridden by --gamma=linear",
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
		charset:             charset,
		maxLength:           cmd.Int("max-length"),
		blockSize:           cmd.Int("block-size"),
		threshold:           cmd.Float("threshold"),
		spaceThreshold:      cmd.Float("space-threshold"),
		topN:                cmd.Int("top"),
		workers:             cmd.Int("workers"),
		strategy:            cmd.String("strategy"),
		beamWidth:           cmd.Int("beam-width"),
		metric:              cmd.String("metric"),
		format:              cmd.String("format"),
		fontPaths:           cmd.StringSlice("font"),
		fontBoldPath:        cmd.String("font-bold"),
		fontDir:             cmd.String("font-dir"),
		redaction:           cmd.String("redaction"),
		fontSize:            cmd.Float("font-size"),
		letterSpacing:       cmd.Float("letter-spacing"),
		blurSigma:           cmd.Float("blur-sigma"),
		blurExact:           cmd.Bool("blur-exact"),
		gamma:               cmd.String("gamma"),
		deblur:              cmd.Int("deblur"),
		language:            cmd.Bool("language"),
		secrets:             cmd.Bool("secrets"),
		minConfidence:       cmd.Float("min-confidence"),
		escalate:            cmd.Bool("escalate"),
		charsetTopK:         cmd.Int("charset-topk"),
		charsetExplicit:     cmd.IsSet("charset") || cmd.IsSet("charset-preset"),
		quiet:               cmd.Bool("quiet"),
		timeout:             cmd.Duration("timeout"),
		blind:               cmd.Bool("blind"),
		lang:                cmd.String("lang"),
		denoise:             cmd.Int("denoise"),
		decoder:             cmd.String("decoder"),
		normalize:           cmd.Bool("normalize"),
		normalizeBg:         cmd.String("normalize-bg"),
		normalizeBin:        cmd.Bool("normalize-binarize"),
		deblock:             cmd.Int("deblock"),
		remosaic:            cmd.Bool("remosaic") || cmd.IsSet("remosaic-grid") || cmd.Bool("remosaic-linear"),
		remosaicGrid:        cmd.Int("remosaic-grid"),
		remosaicLinear:      cmd.Bool("remosaic-linear"),
		letterSpacingSearch: cmd.Bool("letter-spacing-search"),
		thmmLang:            cmd.String("thmm-lang"),
		thmmJPEG:            cmd.Int("thmm-jpeg"),
		varfontText:         cmd.String("varfont-text"),
		varfontAxes:         cmd.String("varfont-axes"),
		varfontLinear:       cmd.Bool("varfont-linear"),
	}

	if err := validateParams(p); err != nil {
		return err
	}

	if p.blind {
		return runBlind(ctx, imgPath, p)
	}
	switch p.decoder {
	case "mono-hmm":
		return runHMM(ctx, imgPath, p)
	case "ref-match":
		return runRefMatch(ctx, imgPath, p)
	case "window-hmm":
		return runWindowHMM(ctx, imgPath, p)
	case "varfont":
		return runVarFont(ctx, imgPath, p)
	case "trained-hmm":
		return runTrainedHMM(ctx, imgPath, p)
	case "did":
		return runDID(ctx, imgPath, p)
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

	start := time.Now()

	cfg := buildConfig(p)

	// Zero-config σ-search: --redaction blur (or auto-detected blur) with no
	// explicit --blur-sigma and no user-specified font. RecoverBlurred runs its
	// own coarse-to-fine σ sweep (parallel coarse, sequential fine) so the
	// caller need not know σ. WithConfig(cfg) carries charset, style, strategy,
	// and any language prior set via --language/--lang.
	if blurNeedsSearch(p) && len(p.fontPaths) == 0 && p.fontDir == "" {
		// Calibrate font size from image content height when the user did not pin one.
		if p.fontSize == 0 {
			if est := unpixel.InferFontSize(img); est >= 8 {
				cfg.Style.FontSize = est
				if !p.quiet {
					fmt.Fprintf(os.Stderr, "Calibrated font size ≈ %.0f pt\n", est)
				}
			}
		}
		return runBlurRecover(ctx, img, p, cfg, start)
	}

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
		if p.gamma == "linear" {
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
				fmt.Fprintln(os.Stderr, "--gamma=linear ignored: no mosaic block grid detected (pass --block-size)")
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

	start = time.Now()
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
			best.belowThreshold = r.BelowThreshold
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
// The best guess goes to stdout; the Top-N table and any caveat go to stderr
// (unless quiet). When r.belowThreshold is true, a low-confidence warning is
// printed before the Top-N table so the user knows no candidate passed the
// acceptance gate and the guess is a best-effort approximation only.
func printText(r recoveryResult, quiet bool) {
	if r.belowThreshold && !quiet {
		fmt.Fprintf(os.Stderr, "best-effort guess: %q  total: %.4f  (below threshold — low confidence)\n",
			r.bestGuess, r.bestTotal)
	}
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
