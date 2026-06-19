package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oioio-space/unpixel"
)

// TestBuildConfig verifies that buildConfig maps flag values to unpixel.Config
// correctly, including the TopN and Threshold fields.
func TestBuildConfig(t *testing.T) {
	p := flagParams{
		charset:        "abc",
		maxLength:      10,
		blockSize:      16,
		threshold:      0.3,
		spaceThreshold: 0.6,
		topN:           3,
	}
	cfg := buildConfig(p)
	if cfg.Charset != "abc" {
		t.Errorf("Charset: got %q, want %q", cfg.Charset, "abc")
	}
	if cfg.MaxLength != 10 {
		t.Errorf("MaxLength: got %d, want 10", cfg.MaxLength)
	}
	if cfg.BlockSize != 16 {
		t.Errorf("BlockSize: got %d, want 16", cfg.BlockSize)
	}
	if cfg.Threshold != 0.3 {
		t.Errorf("Threshold: got %v, want 0.3", cfg.Threshold)
	}
	if cfg.SpaceThreshold != 0.6 {
		t.Errorf("SpaceThreshold: got %v, want 0.6", cfg.SpaceThreshold)
	}
	if cfg.TopN != 3 {
		t.Errorf("TopN: got %d, want 3", cfg.TopN)
	}
}

// TestBuildConfig_Defaults verifies zero/unset fields map to package defaults.
func TestBuildConfig_Defaults(t *testing.T) {
	cfg := buildConfig(flagParams{})
	// Zero values are left as zero — applyDefaults inside New fills them in.
	// buildConfig is a thin mapping layer; it does not call applyDefaults.
	if cfg.Charset != "" {
		t.Errorf("Charset: expected empty (let New apply default), got %q", cfg.Charset)
	}
}

// TestJSONOutput verifies the resultJSON struct round-trips through encoding/json
// with stable field names and that all required keys are present.
func TestJSONOutput(t *testing.T) {
	r := resultJSON{
		BestGuess:  "hello",
		BestScore:  0.12,
		Offset:     offsetJSON{X: 1, Y: 2},
		Confidence: 0.88,
		Ambiguity:  0.15,
		Top:        []topEntry{{Guess: "hello", Score: 0.12}, {Guess: "helo", Score: 0.27}},
		Evaluated:  42,
		ElapsedMS:  123,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(data)
	for _, key := range []string{
		`"best_guess"`, `"best_score"`, `"offset"`, `"confidence"`,
		`"ambiguity"`, `"top"`, `"evaluated"`, `"elapsed_ms"`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("JSON output missing key %s; got: %s", key, s)
		}
	}
	// Round-trip.
	var back resultJSON
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if back.BestGuess != "hello" {
		t.Errorf("round-trip BestGuess: got %q, want %q", back.BestGuess, "hello")
	}
	if back.Evaluated != 42 {
		t.Errorf("round-trip Evaluated: got %d, want 42", back.Evaluated)
	}
}

// TestRunSyntheticRoundTrip performs a fast end-to-end recovery on a tiny
// synthetic redaction (1-char charset "a", small white image).
// It exercises runRecovery integration without a slow real-world recovery.
func TestRunSyntheticRoundTrip(t *testing.T) {
	ctx := t.Context()

	// Build a tiny redacted image: a white 16×16 image (no text),
	// which the engine handles gracefully — it may return an empty best guess.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}

	cfg := unpixel.Config{
		Charset:   "a",
		MaxLength: 1,
		BlockSize: 8,
	}
	res, elapsed, eval, err := runRecovery(ctx, img, cfg)
	// A white image finds no candidates; runRecovery must not error in this case.
	if err != nil {
		t.Fatalf("runRecovery: unexpected error: %v", err)
	}
	_ = res
	if elapsed < 0 {
		t.Errorf("elapsed negative: %v", elapsed)
	}
	if eval < 0 {
		t.Errorf("evaluated negative: %d", eval)
	}
}

// TestLoadImage verifies loadImage reads a valid PNG file and rejects missing files.
func TestLoadImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if encErr := png.Encode(f, src); encErr != nil {
		_ = f.Close()
		t.Fatalf("encode: %v", encErr)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	got, err := loadImage(path)
	if err != nil {
		t.Fatalf("loadImage(%q): %v", path, err)
	}
	if got.Bounds() != src.Bounds() {
		t.Errorf("bounds: got %v, want %v", got.Bounds(), src.Bounds())
	}

	_, err = loadImage(filepath.Join(dir, "nonexistent.png"))
	if err == nil {
		t.Error("loadImage(missing): expected error, got nil")
	}
}

// captureStdout redirects os.Stdout while fn runs and returns everything written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	return <-done
}

// whitePNG writes a white w×h PNG to a temp file and returns its path.
func whitePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "redacted.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

// TestWarnIfNoMosaic verifies the guard fires on non-pixelated input, stays
// silent on a genuine mosaic grid, and is suppressed by a forced --block-size.
func TestWarnIfNoMosaic(t *testing.T) {
	// Uniform white image: no grid → InferBlockSize returns 0 → warn.
	white := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range white.Pix {
		white.Pix[i] = 0xFF
	}
	var b strings.Builder
	if !warnIfNoMosaic(&b, white, 0, "test.png") {
		t.Error("expected a warning for a non-pixelated image")
	}
	if !strings.Contains(b.String(), "no mosaic") {
		t.Errorf("warning text missing the key phrase: %q", b.String())
	}

	// A forced --block-size asserts the grid explicitly → no check, no output.
	b.Reset()
	if warnIfNoMosaic(&b, white, 8, "test.png") {
		t.Error("forced --block-size should suppress the warning")
	}
	if b.Len() != 0 {
		t.Errorf("suppressed path wrote output: %q", b.String())
	}

	// Genuine mosaic: vertical 8 px blocks of alternating grey → grid inferred.
	mosaic := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := range 16 {
		for x := range 32 {
			v := uint8(200)
			if (x/8)%2 == 1 {
				v = 50
			}
			mosaic.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	b.Reset()
	if warnIfNoMosaic(&b, mosaic, 0, "m.png") {
		t.Errorf("a real mosaic image should not warn; got %q", b.String())
	}
}

// embeddedFontData reads the repo's bundled font so tests can exercise the
// font-loading paths without depending on any system font.
func embeddedFontData(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../internal/render/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Fatalf("read embedded font: %v", err)
	}
	return b
}

// TestRunSweep drives the parallel multi-font sweep over a synthetic image with
// two valid fonts and one unparseable one (exercising the skip branch), in both
// text and JSON output modes.
func TestRunSweep(t *testing.T) {
	data := embeddedFontData(t)
	dir := t.TempDir()
	var fonts []string
	for _, n := range []string{"f1.ttf", "f2.ttf"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
		fonts = append(fonts, p)
	}
	bogus := filepath.Join(dir, "bad.ttf") // unparseable → skipped with a note
	if err := os.WriteFile(bogus, []byte("not a font"), 0o600); err != nil {
		t.Fatalf("write bogus: %v", err)
	}
	fonts = append(fonts, bogus)

	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	cfg := buildConfig(flagParams{
		charset: "a", maxLength: 1, blockSize: 8, threshold: 0.25,
		spaceThreshold: 0.5, topN: 5, strategy: "guided", metric: "pixelmatch",
	})

	// pathCandidates drops the unparseable bogus font (with a note), leaving 2.
	cands := pathCandidates(fonts, "")
	if len(cands) != 2 {
		t.Fatalf("pathCandidates kept %d, want 2 (bogus skipped)", len(cands))
	}

	// Text mode: prints the winning guess (empty for a white image) + newline.
	textOut := captureStdout(t, func() {
		if err := runSweep(t.Context(), img, cfg, cands, flagParams{format: "text", quiet: true, workers: 2}); err != nil {
			t.Errorf("runSweep text: %v", err)
		}
	})
	if !strings.HasSuffix(textOut, "\n") {
		t.Errorf("runSweep text output should end with a newline, got %q", textOut)
	}

	// JSON mode: emits a ranked "fonts" array.
	jsonOut := captureStdout(t, func() {
		if err := runSweep(t.Context(), img, cfg, cands, flagParams{format: "json", quiet: true, workers: 2}); err != nil {
			t.Errorf("runSweep json: %v", err)
		}
	})
	var r resultJSON
	if err := json.Unmarshal([]byte(jsonOut), &r); err != nil {
		t.Fatalf("runSweep json: invalid JSON: %v\n%s", err, jsonOut)
	}
	if len(r.Fonts) != 2 {
		t.Errorf("ranked fonts = %d, want 2", len(r.Fonts))
	}

	// No usable candidate → an error.
	if err := runSweep(t.Context(), img, cfg, pathCandidates([]string{bogus}, ""), flagParams{format: "text", quiet: true}); err == nil {
		t.Error("runSweep with only an invalid font: expected error, got nil")
	}
}

// TestRun_singleFont drives the CLI end to end with one --font (the single-font
// branch + loadRenderer).
func TestRun_singleFont(t *testing.T) {
	dir := t.TempDir()
	fontPath := filepath.Join(dir, "f.ttf")
	if err := os.WriteFile(fontPath, embeddedFontData(t), 0o600); err != nil {
		t.Fatalf("write font: %v", err)
	}
	img := whitePNG(t, 16, 16)
	out := captureStdout(t, func() {
		args := []string{"unpixel", "--quiet", "--font", fontPath, "--font-size", "24", "--letter-spacing", "-0.2", "--block-size", "8", "--max-length", "1", "--charset", "a", img}
		if err := buildApp().Run(t.Context(), args); err != nil {
			t.Errorf("run: %v", err)
		}
	})
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected newline-terminated output, got %q", out)
	}
}

// TestBundleCandidates verifies the zero-config sweep set builds from the
// embedded bundle (every font parses into a renderer with a display name).
func TestBundleCandidates(t *testing.T) {
	cands, err := bundleCandidates()
	if err != nil {
		t.Fatalf("bundleCandidates: %v", err)
	}
	if len(cands) < 5 {
		t.Fatalf("bundleCandidates = %d, want the full bundle", len(cands))
	}
	for i, c := range cands {
		if c.r == nil || c.display == "" || c.jsonName == "" {
			t.Errorf("candidate %d malformed: %+v", i, c)
		}
	}
}

// TestCollectFonts verifies that explicit --font paths and a --font-dir scan
// are merged, filtered to font extensions, and de-duplicated in order.
func TestCollectFonts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.ttf", "b.otf", "notes.txt", "c.TTF"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	explicit := filepath.Join(dir, "a.ttf") // also present in the dir → must dedup
	got, err := collectFonts(flagParams{fontPaths: []string{explicit}, fontDir: dir})
	if err != nil {
		t.Fatalf("collectFonts: %v", err)
	}
	// Expect: explicit a.ttf, then dir's a.ttf (deduped away), b.otf, c.TTF; notes.txt excluded.
	want := []string{
		explicit,
		filepath.Join(dir, "b.otf"),
		filepath.Join(dir, "c.TTF"),
	}
	if len(got) != len(want) {
		t.Fatalf("collectFonts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("collectFonts[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// A missing directory is an error.
	if _, err := collectFonts(flagParams{fontDir: filepath.Join(dir, "nope")}); err == nil {
		t.Error("collectFonts(missing dir): expected error, got nil")
	}

	// No fonts at all → empty (caller uses the embedded default).
	if got, err := collectFonts(flagParams{}); err != nil || len(got) != 0 {
		t.Errorf("collectFonts(empty) = %v, %v; want [], nil", got, err)
	}
}

// TestCharsetForPreset verifies preset name → charset mapping and rejection.
func TestCharsetForPreset(t *testing.T) {
	cases := map[string]string{
		"lower": unpixel.CharsetLower,
		"alnum": unpixel.CharsetAlnum,
		"ascii": unpixel.CharsetASCII,
		"code":  unpixel.CharsetASCII,
	}
	for name, want := range cases {
		got, err := charsetForPreset(name)
		if err != nil {
			t.Errorf("charsetForPreset(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("charsetForPreset(%q) = %q, want %q", name, got, want)
		}
	}
	if _, err := charsetForPreset("klingon"); err == nil {
		t.Error("charsetForPreset(unknown) = nil error, want error")
	}
}

// TestRun_charsetPreset drives the CLI with --charset-preset end to end.
func TestRun_charsetPreset(t *testing.T) {
	path := whitePNG(t, 16, 16)
	out := captureStdout(t, func() {
		args := []string{"unpixel", "--quiet", "--charset-preset", "alnum", "--max-length", "1", "--block-size", "8", path}
		if err := buildApp().Run(t.Context(), args); err != nil {
			t.Errorf("run: %v", err)
		}
	})
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected a newline-terminated result, got %q", out)
	}
}

// TestRun_badCharsetPreset rejects an unknown preset.
func TestRun_badCharsetPreset(t *testing.T) {
	path := whitePNG(t, 8, 8)
	if err := buildApp().Run(t.Context(), []string{"unpixel", "--charset-preset", "nope", path}); err == nil {
		t.Error("expected error for bad --charset-preset")
	}
}

// TestValidateParams checks the enum-style flag validation.
func TestValidateParams(t *testing.T) {
	base := flagParams{format: "text", strategy: "guided", metric: "pixelmatch", redaction: "auto"}
	if err := validateParams(base); err != nil {
		t.Errorf("valid params rejected: %v", err)
	}
	cases := map[string]func(*flagParams){
		"bad format":    func(p *flagParams) { p.format = "xml" },
		"bad strategy":  func(p *flagParams) { p.strategy = "astar" },
		"bad metric":    func(p *flagParams) { p.metric = "psnr" },
		"bad redaction": func(p *flagParams) { p.redaction = "scribble" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			p := base
			mut(&p)
			if err := validateParams(p); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// TestResolveBlur covers the redaction-mode decision.
func TestResolveBlur(t *testing.T) {
	// A blurred step image → auto should pick blur with a positive sigma.
	const w, h = 81, 30
	step := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(0)
			if x >= w/2 {
				v = 255
			}
			step.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	white := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range white.Pix {
		white.Pix[i] = 0xFF
	}

	cases := []struct {
		name    string
		img     image.Image
		p       flagParams
		wantPos bool
	}{
		{"mosaic forced", step, flagParams{redaction: "mosaic"}, false},
		{"blur forced explicit sigma", white, flagParams{redaction: "blur", blurSigma: 6}, true},
		{"blur forced auto sigma", step, flagParams{redaction: "blur"}, true},
		{"auto sharp → mosaic", step, flagParams{redaction: "auto"}, false},
		{"explicit sigma in auto → blur", white, flagParams{redaction: "auto", blurSigma: 4}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveBlur(c.img, c.p) > 0; got != c.wantPos {
				t.Errorf("resolveBlur > 0 = %v, want %v (σ=%.2f)", got, c.wantPos, resolveBlur(c.img, c.p))
			}
		})
	}
}

// TestBuildConfig_strategyAndMetric covers the beam/SSIM selection branches and
// the worker/beam-width passthrough.
func TestBuildConfig_strategyAndMetric(t *testing.T) {
	cfg := buildConfig(flagParams{strategy: "beam", beamWidth: 8, metric: "ssim", workers: 3})
	if cfg.Strategy == nil {
		t.Error("beam: Strategy is nil")
	}
	if cfg.Metric == nil {
		t.Error("ssim: Metric is nil")
	}
	if cfg.BeamWidth != 8 {
		t.Errorf("BeamWidth: got %d, want 8", cfg.BeamWidth)
	}
	if cfg.Workers != 3 {
		t.Errorf("Workers: got %d, want 3", cfg.Workers)
	}

	def := buildConfig(flagParams{strategy: "guided", metric: "pixelmatch"})
	if def.Strategy == nil || def.Metric == nil {
		t.Error("guided/pixelmatch: Strategy or Metric is nil")
	}
}

// TestRun_endToEndJSON drives the full CLI through buildApp on a temp PNG and
// asserts the emitted stdout is valid result JSON.
func TestRun_endToEndJSON(t *testing.T) {
	path := whitePNG(t, 16, 16)
	out := captureStdout(t, func() {
		app := buildApp()
		args := []string{"unpixel", "--quiet", "--format", "json", "--max-length", "1", "--charset", "a", "--block-size", "8", path}
		if err := app.Run(t.Context(), args); err != nil {
			t.Errorf("run: %v", err)
		}
	})
	var r resultJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\ngot: %s", err, out)
	}
}

// TestRun_endToEndText drives the text output path.
func TestRun_endToEndText(t *testing.T) {
	path := whitePNG(t, 16, 16)
	out := captureStdout(t, func() {
		app := buildApp()
		args := []string{"unpixel", "--quiet", "--max-length", "1", "--charset", "a", "--block-size", "8", path}
		if err := app.Run(t.Context(), args); err != nil {
			t.Errorf("run: %v", err)
		}
	})
	// A white image yields an empty best guess; the path must still print a line.
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("text output should end with a newline, got %q", out)
	}
}

// TestRun_validationAndArgErrors covers the early error returns of run.
func TestRun_validationAndArgErrors(t *testing.T) {
	path := whitePNG(t, 8, 8)
	cases := map[string][]string{
		"bad format":    {"unpixel", "--format", "xml", path},
		"bad strategy":  {"unpixel", "--strategy", "astar", path},
		"bad metric":    {"unpixel", "--metric", "psnr", path},
		"missing arg":   {"unpixel"},
		"too many args": {"unpixel", path, path},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if err := buildApp().Run(t.Context(), args); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// TestPrintJSONAndText exercises the formatters directly.
func TestPrintJSONAndText(t *testing.T) {
	r := recoveryResult{
		bestGuess:  "hi",
		bestScore:  0.1,
		offset:     unpixel.Offset{X: 1, Y: 2},
		confidence: 0.9,
		ambiguity:  0.1,
		top:        []unpixel.Eval{{Guess: "hi", Score: 0.1}},
	}
	jsonOut := captureStdout(t, func() {
		if err := printJSON(r, nil, 5, 10*time.Millisecond); err != nil {
			t.Errorf("printJSON: %v", err)
		}
	})
	if !strings.Contains(jsonOut, `"best_guess": "hi"`) {
		t.Errorf("printJSON missing best_guess: %s", jsonOut)
	}
	textOut := captureStdout(t, func() { printText(r, false) })
	if !strings.Contains(textOut, "hi") {
		t.Errorf("printText missing guess: %s", textOut)
	}
}
