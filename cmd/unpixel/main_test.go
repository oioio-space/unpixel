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
	base := flagParams{format: "text", strategy: "guided", metric: "pixelmatch"}
	if err := validateParams(base); err != nil {
		t.Errorf("valid params rejected: %v", err)
	}
	cases := map[string]func(*flagParams){
		"bad format":   func(p *flagParams) { p.format = "xml" },
		"bad strategy": func(p *flagParams) { p.strategy = "astar" },
		"bad metric":   func(p *flagParams) { p.metric = "psnr" },
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
		if err := printJSON(r, 5, 10*time.Millisecond); err != nil {
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
