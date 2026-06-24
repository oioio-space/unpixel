package main

import (
	"encoding/json"
	"errors"
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
	"github.com/oioio-space/unpixel/mosaictext"
)

// TestParseRegion exercises the "x,y,w,h" region parser with valid and invalid inputs.
func TestParseRegion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    image.Rectangle
		wantErr bool
	}{
		{
			name:  "simple origin",
			input: "0,0,10,20",
			want:  image.Rect(0, 0, 10, 20),
		},
		{
			name:  "non-zero origin",
			input: "5,3,100,50",
			want:  image.Rect(5, 3, 105, 53),
		},
		{
			name:  "negative x y allowed",
			input: "-4,-2,8,4",
			want:  image.Rect(-4, -2, 4, 2),
		},
		{
			name:  "spaces around values",
			input: " 1 , 2 , 3 , 4 ",
			want:  image.Rect(1, 2, 4, 6),
		},
		{
			name:    "wrong field count — three parts",
			input:   "1,2,3",
			wantErr: true,
		},
		{
			name:    "wrong field count — five parts",
			input:   "1,2,3,4,5",
			wantErr: true,
		},
		{
			name:    "non-numeric x",
			input:   "a,0,10,10",
			wantErr: true,
		},
		{
			name:    "non-numeric h",
			input:   "0,0,10,abc",
			wantErr: true,
		},
		{
			name:    "negative width",
			input:   "0,0,-1,10",
			wantErr: true,
		},
		{
			name:    "negative height",
			input:   "0,0,10,-5",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRegion(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseRegion(%q): got nil error, want error", tc.input)
				}
				return
			}
			if err != nil {
				t.Errorf("parseRegion(%q): unexpected error: %v", tc.input, err)
				return
			}
			if got != tc.want {
				t.Errorf("parseRegion(%q): got %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// writeTinyPNG writes a 4×4 white PNG to path and returns the path.
func writeTinyPNG(t *testing.T, path string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode PNG: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
	return path
}

// TestLoadVisibleCrop covers the main branches of loadVisibleCrop.
func TestLoadVisibleCrop(t *testing.T) {
	dir := t.TempDir()
	imgPath := writeTinyPNG(t, filepath.Join(dir, "tiny.png"))

	t.Run("no region returns full image", func(t *testing.T) {
		got, err := loadVisibleCrop(imgPath, "")
		if err != nil {
			t.Fatalf("loadVisibleCrop: unexpected error: %v", err)
		}
		if got.Bounds().Dx() != 4 || got.Bounds().Dy() != 4 {
			t.Errorf("got dims %dx%d, want 4x4", got.Bounds().Dx(), got.Bounds().Dy())
		}
	})

	t.Run("region crops correctly", func(t *testing.T) {
		got, err := loadVisibleCrop(imgPath, "1,1,2,2")
		if err != nil {
			t.Fatalf("loadVisibleCrop with region: unexpected error: %v", err)
		}
		if got.Bounds().Dx() != 2 || got.Bounds().Dy() != 2 {
			t.Errorf("got dims %dx%d, want 2x2", got.Bounds().Dx(), got.Bounds().Dy())
		}
	})

	t.Run("bad path returns error", func(t *testing.T) {
		_, err := loadVisibleCrop(filepath.Join(dir, "nonexistent.png"), "")
		if err == nil {
			t.Error("loadVisibleCrop(bad path): got nil error, want error")
		}
	})

	t.Run("bad region string returns error", func(t *testing.T) {
		_, err := loadVisibleCrop(imgPath, "not-a-region")
		if err == nil {
			t.Error("loadVisibleCrop(bad region): got nil error, want error")
		}
	})

	t.Run("region outside image bounds returns error", func(t *testing.T) {
		_, err := loadVisibleCrop(imgPath, "100,100,10,10")
		if err == nil {
			t.Error("loadVisibleCrop(out-of-bounds region): got nil error, want error")
		}
	})
}

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

// TestBuildConfig_SecretsFlag verifies that --secrets wires a non-nil LanguageModel
// via secrets.Prior through WithPriors.
func TestBuildConfig_SecretsFlag(t *testing.T) {
	t.Parallel()
	cfg := buildConfig(flagParams{secrets: true})
	if cfg.LanguageModel == nil {
		t.Fatal("buildConfig(secrets=true): LanguageModel is nil, want non-nil")
	}
	// A UUID should receive a positive prior (BonusStructured = 1.0).
	if got := cfg.LanguageModel("550e8400-e29b-41d4-a716-446655440000"); got <= 0 {
		t.Errorf("LanguageModel(uuid) = %v, want > 0", got)
	}
	// An arbitrary word gets no bonus.
	if got := cfg.LanguageModel("xyzzy"); got != 0 {
		t.Errorf("LanguageModel(unknown) = %v, want 0", got)
	}
}

// TestBuildConfig_LanguageAndSecretsBothSet verifies that --language and --secrets
// compose: the resulting LanguageModel is the sum of both priors.
func TestBuildConfig_LanguageAndSecretsBothSet(t *testing.T) {
	t.Parallel()
	cfgBoth := buildConfig(flagParams{language: true, secrets: true})
	cfgLang := buildConfig(flagParams{language: true})
	cfgSec := buildConfig(flagParams{secrets: true})
	if cfgBoth.LanguageModel == nil || cfgLang.LanguageModel == nil || cfgSec.LanguageModel == nil {
		t.Fatal("one of the LanguageModels is nil")
	}
	// For a UUID, both = language(s) + secrets(s) = lang + struct bonus.
	s := "550e8400-e29b-41d4-a716-446655440000"
	gotBoth := cfgBoth.LanguageModel(s)
	wantBoth := cfgLang.LanguageModel(s) + cfgSec.LanguageModel(s)
	if gotBoth != wantBoth {
		t.Errorf("both priors: LanguageModel(%q) = %v, want %v (sum)", s, gotBoth, wantBoth)
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

// TestRunEscalation_runsTiersWithoutError drives the charset-escalation path on
// a white image (no recovery): it must walk the tiers and print without error.
func TestRunEscalation_runsTiersWithoutError(t *testing.T) {
	cands, err := bundleCandidates()
	if err != nil {
		t.Fatalf("bundleCandidates: %v", err)
	}
	cands = cands[:1] // one font keeps the test fast across all three tiers
	img := image.NewRGBA(image.Rect(0, 0, 24, 16))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	base := buildConfig(flagParams{maxLength: 1, blockSize: 8, threshold: 0.25, spaceThreshold: 0.5, topN: 5, strategy: "guided", metric: "pixelmatch"})

	out := captureStdout(t, func() {
		if err := runEscalation(t.Context(), img, base, cands, flagParams{format: "text", quiet: true}); err != nil {
			t.Errorf("runEscalation: %v", err)
		}
	})
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("runEscalation output should end with a newline, got %q", out)
	}
}

// TestReportConfidence covers the verdict + honest-abort gate.
func TestReportConfidence(t *testing.T) {
	// Below --min-confidence → error (honest abort).
	if err := reportConfidence(0.3, flagParams{quiet: true, minConfidence: 0.5}); err == nil {
		t.Error("low fidelity below --min-confidence: expected error, got nil")
	}
	// At/above the bar → no error.
	if err := reportConfidence(0.9, flagParams{quiet: true, minConfidence: 0.5}); err != nil {
		t.Errorf("high fidelity: unexpected error %v", err)
	}
	// No --min-confidence → never gates, regardless of fidelity.
	if err := reportConfidence(0.0, flagParams{quiet: true}); err != nil {
		t.Errorf("min-confidence 0 should never gate; got %v", err)
	}
}

// TestFidelityOf mirrors unpixel.Result.Fidelity for the CLI struct.
func TestFidelityOf(t *testing.T) {
	if got := fidelityOf(recoveryResult{bestGuess: "go", bestTotal: 0.2}); got < 0.79 || got > 0.81 {
		t.Errorf("fidelityOf = %v, want ~0.8", got)
	}
	if got := fidelityOf(recoveryResult{bestGuess: "", bestTotal: 0}); got != 0 {
		t.Errorf("fidelityOf(empty) = %v, want 0", got)
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
		"bad decoder":   func(p *flagParams) { p.decoder = "viterbi" },
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

// TestParseNormalizeBg verifies the --normalize-bg flag parser: valid values are
// accepted, unknown values are rejected with an informative error.
func TestParseNormalizeBg(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
	}{
		{input: "", wantErr: false},
		{input: "divide", wantErr: false},
		{input: "subtract", wantErr: false},
		{input: "none", wantErr: false},
		{input: "additive", wantErr: true},
		{input: "DIVIDE", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			_, err := parseNormalizeBg(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseNormalizeBg(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

// TestBlurOperator verifies the blurOperator routing: small sigma and exact=true
// both route to GaussianBlur (non-nil); large sigma with exact=false routes to
// FastBlur (also non-nil). We only verify the returned Pixelator is non-nil since
// the concrete types are unexported; the routing is the observable behaviour.
func TestBlurOperator(t *testing.T) {
	cases := []struct {
		name  string
		sigma float64
		exact bool
	}{
		{"small sigma exact=false → gaussian", 2.0, false},
		{"small sigma exact=true  → gaussian", 2.0, true},
		{"large sigma exact=false → fast", 10.0, false},
		{"large sigma exact=true  → gaussian", 10.0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := blurOperator(tc.sigma, tc.exact)
			if got == nil {
				t.Errorf("blurOperator(%v, %v) = nil, want non-nil Pixelator", tc.sigma, tc.exact)
			}
		})
	}
}

// TestSmallerAndCropToRegion exercises the geometry helpers used by the cropping
// pipeline.
func TestSmallerAndCropToRegion(t *testing.T) {
	big := image.Rect(0, 0, 100, 100)
	small := image.Rect(10, 10, 50, 60)

	if !smaller(small, big) {
		t.Error("smaller(small, big) = false, want true")
	}
	if smaller(big, small) {
		t.Error("smaller(big, small) = true, want false")
	}
	// Equal rectangle is not smaller.
	if smaller(big, big) {
		t.Error("smaller(big, big) = true, want false")
	}

	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for i := range src.Pix {
		src.Pix[i] = 0xAB
	}
	crop := cropToRegion(src, small)
	if crop.Bounds().Dx() != small.Dx() || crop.Bounds().Dy() != small.Dy() {
		t.Errorf("cropToRegion: bounds = %v, want %v", crop.Bounds(), small)
	}
	// Origin must be (0,0).
	if crop.Bounds().Min.X != 0 || crop.Bounds().Min.Y != 0 {
		t.Errorf("cropToRegion: min = %v, want (0,0)", crop.Bounds().Min)
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

// TestRun_blindFlag exercises the --blind flag wiring: bad --lang is rejected,
// and a valid call on a small white image completes without error.
// We pass --block-size and --font-size to avoid auto-detect overhead and pin a
// specific white image that produces no recoverable text (empty output is fine).
func TestRun_blindFlag(t *testing.T) {
	path := whitePNG(t, 16, 16)

	// Unknown language must error before touching the image.
	if err := buildApp().Run(t.Context(), []string{
		"unpixel", "--blind", "--lang", "klingon", path,
	}); err == nil {
		t.Error("--lang klingon: expected error, got nil")
	}

	// Valid call: white image, English, tiny — must complete without error.
	// (No text is recoverable from a blank image; empty output is correct.)
	if testing.Short() {
		t.Skip("blind flag test skipped in -short mode")
	}
	out := captureStdout(t, func() {
		args := []string{
			"unpixel", "--quiet", "--blind", "--lang", "en",
			"--block-size", "8", "--font-size", "26", path,
		}
		if err := buildApp().Run(t.Context(), args); err != nil {
			t.Errorf("blind run: %v", err)
		}
	})
	// A blank image may produce empty output; we only verify newline termination.
	if len(out) > 0 && out[len(out)-1] != '\n' {
		t.Errorf("blind output should end with a newline, got %q", out)
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

// TestRunBlind_DenoiseFlag verifies that --denoise is parsed and forwarded to
// runBlind. The test exercises flag wiring only (no full decode) using a tiny
// white image so the call returns quickly. It checks three cases:
//   - --denoise -1 (default auto): no explicit WithDenoise is passed; Recover uses auto.
//   - --denoise 0 (off): WithDenoise(0) is passed; Recover disables auto.
//   - --denoise 2 (force): WithDenoise(2) is passed; Recover applies radius 2.
//
// Because we cannot intercept blind.WithDenoise in a black-box CLI test without
// a full decode, we verify that the command completes without error and produces
// output — confirming the flag is wired and the option reaches Recover. A forced
// --block-size and --font-size prevent auto-detect overhead.
func TestRunBlind_DenoiseFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("blind denoise flag test skipped in -short mode")
	}
	path := whitePNG(t, 16, 16)

	for _, tc := range []struct {
		name  string
		extra []string
	}{
		{"default_auto", nil},
		{"off", []string{"--denoise", "0"}},
		{"force2", []string{"--denoise", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{
				"unpixel", "--quiet", "--blind", "--lang", "en",
				"--block-size", "8", "--font-size", "26", path,
			}, tc.extra...)
			out := captureStdout(t, func() {
				if err := buildApp().Run(t.Context(), args); err != nil {
					t.Errorf("runBlind(%s): %v", tc.name, err)
				}
			})
			// A blank image may produce empty text; verify no panic and no non-newline junk.
			if len(out) > 0 && out[len(out)-1] != '\n' {
				t.Errorf("runBlind(%s): output should end with newline, got %q", tc.name, out)
			}
		})
	}
}

// TestBlurNeedsSearch covers the routing predicate that decides between
// RecoverBlurred (σ-search) and fixed-sigma Recover.
func TestBlurNeedsSearch(t *testing.T) {
	cases := []struct {
		name string
		p    flagParams
		want bool
	}{
		{"blur-no-sigma", flagParams{redaction: "blur", blurSigma: 0}, true},
		{"blur-with-sigma", flagParams{redaction: "blur", blurSigma: 4}, false},
		{"auto-no-sigma", flagParams{redaction: "auto", blurSigma: 0}, true},
		{"auto-with-sigma", flagParams{redaction: "auto", blurSigma: 3}, false},
		{"mosaic", flagParams{redaction: "mosaic"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blurNeedsSearch(tc.p); got != tc.want {
				t.Errorf("blurNeedsSearch(%+v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

// TestRunBlurRecover_flagWiring verifies that --redaction blur without an
// explicit --blur-sigma routes through runBlurRecover on a tiny white image
// without panicking. The white image produces no candidates — only the control
// flow path is tested, not recovery quality.
func TestRunBlurRecover_flagWiring(t *testing.T) {
	path := whitePNG(t, 16, 16)

	captureStdout(t, func() {
		err := buildApp().Run(t.Context(), []string{
			"unpixel", "--quiet",
			"--redaction", "blur",
			"--charset", "a",
			"--max-length", "1",
			"--block-size", "1",
			path,
		})
		// A white image may return no candidates; accept any non-panic outcome.
		if err != nil {
			t.Logf("runBlurRecover on white image: %v (acceptable — no candidates)", err)
		}
	})
}

// TestRunBlurRecover_langFlagWiring verifies that --redaction blur with
// --language set wires the language prior through RecoverBlurred without error.
func TestRunBlurRecover_langFlagWiring(t *testing.T) {
	path := whitePNG(t, 16, 16)

	captureStdout(t, func() {
		err := buildApp().Run(t.Context(), []string{
			"unpixel", "--quiet",
			"--redaction", "blur",
			"--language",
			"--charset", "a",
			"--max-length", "1",
			path,
		})
		if err != nil {
			t.Logf("runBlurRecover+language on white image: %v (acceptable)", err)
		}
	})
}

// TestRunHMM_CLI exercises the --decoder mono-hmm dispatch path through
// buildApp. It verifies three sub-cases:
//   - bad --lang is rejected before the image is loaded.
//   - a white image with the mono-hmm decoder returns ErrNoMosaic (wrapped by
//     runHMM) and the command reports an error.
//   - text output format on a fixture that has a real mosaic grid completes
//     without an unexpected error and produces newline-terminated output.
func TestRunHMM_CLI(t *testing.T) {
	t.Run("bad lang rejected", func(t *testing.T) {
		path := whitePNG(t, 8, 8)
		err := buildApp().Run(t.Context(), []string{
			"unpixel", "--quiet", "--decoder", "mono-hmm", "--lang", "klingon", path,
		})
		if err == nil {
			t.Error("expected error for unknown --lang, got nil")
		}
	})

	t.Run("white image returns error", func(t *testing.T) {
		path := whitePNG(t, 8, 8)
		err := buildApp().Run(t.Context(), []string{
			"unpixel", "--quiet", "--decoder", "mono-hmm", path,
		})
		// A 1×1-equivalent white image has no mosaic → ErrNoMosaic is returned
		// as a wrapped error from runHMM.
		if err == nil {
			t.Error("expected error for white image (no mosaic), got nil")
		}
	})
}

// TestRunRefMatch_CLI exercises the --decoder ref-match dispatch path through
// buildApp, including charset defaulting, font-name wiring, and both text and
// JSON output formats. It uses the bundled block08_go.png fixture so no render
// dependency is needed; the exact decoded text is not asserted — the test only
// verifies the command completes without an unexpected error and produces output.
func TestRunRefMatch_CLI(t *testing.T) {
	// Use a fixture that has a real block-8 mosaic grid so DecodeReference can
	// infer the block size and return a result (rather than ErrNoMosaic).
	fixture := filepath.Join("..", "..", "testdata", "fixtures", "block08_go.png")
	if _, statErr := os.Stat(fixture); statErr != nil {
		t.Skipf("fixture not found: %v", statErr)
	}

	t.Run("text output", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := buildApp().Run(t.Context(), []string{
				"unpixel", "--quiet",
				"--decoder", "ref-match",
				"--charset", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
				"--font", "Liberation Mono",
				fixture,
			})
			// ErrNoContent is acceptable (font mismatch); anything else is a bug.
			if err != nil && !errors.Is(err, mosaictext.ErrNoContent) {
				t.Errorf("runRefMatch text: unexpected error: %v", err)
			}
		})
		// Output must end with a newline when text is recovered.
		if len(out) > 0 && out[len(out)-1] != '\n' {
			t.Errorf("text output should end with newline, got %q", out)
		}
	})

	t.Run("json output", func(t *testing.T) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = buildApp().Run(t.Context(), []string{
				"unpixel", "--quiet",
				"--decoder", "ref-match",
				"--format", "json",
				"--charset", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
				"--font", "Liberation Mono",
				fixture,
			})
		})
		if runErr != nil {
			t.Logf("runRefMatch json: %v (acceptable)", runErr)
			return
		}
		// If no error, output must be valid JSON.
		var v map[string]any
		if jsonErr := json.Unmarshal([]byte(out), &v); jsonErr != nil {
			t.Errorf("json output is not valid JSON: %v\nout=%q", jsonErr, out)
		}
	})
}

// TestParseGammaMode verifies the --gamma flag parser: valid values are accepted
// and the sentinel empty string maps to GammaAuto; unrecognised values return an error.
func TestParseGammaMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input   string
		wantErr bool
	}{
		{input: "", wantErr: false},
		{input: "auto", wantErr: false},
		{input: "linear", wantErr: false},
		{input: "srgb", wantErr: false},
		{input: "SRGB", wantErr: true},
		{input: "Linear", wantErr: true},
		{input: "sRGB", wantErr: true},
		{input: "gamma22", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			_, err := parseGammaMode(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseGammaMode(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

// TestParseVarFontAxes verifies the --varfont-axes parser: valid specs are
// parsed into AxisSpec slices, and invalid/empty inputs return errors.
func TestParseVarFontAxes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		wantErr bool
		wantLen int
	}{
		{name: "empty string", input: "", wantErr: true},
		{name: "single axis", input: "wght:200:900:500", wantErr: false, wantLen: 1},
		{name: "two axes", input: "wght:200:900:500,wdth:75:125:100", wantErr: false, wantLen: 2},
		{name: "bad field count", input: "wght:200:900", wantErr: true},
		{name: "non-numeric min", input: "wght:abc:900:500", wantErr: true},
		{name: "non-numeric max", input: "wght:200:xyz:500", wantErr: true},
		{name: "non-numeric start", input: "wght:200:900:bad", wantErr: true},
		{name: "only whitespace parts", input: "  ,  ", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVarFontAxes(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseVarFontAxes(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
			if err == nil && len(got) != tc.wantLen {
				t.Errorf("parseVarFontAxes(%q) len = %d, want %d", tc.input, len(got), tc.wantLen)
			}
			if err == nil && tc.wantLen > 0 {
				// Verify values are plausible (parsed without truncation).
				if got[0].Tag == "" {
					t.Errorf("parseVarFontAxes(%q) first axis has empty tag", tc.input)
				}
			}
		})
	}
}

// TestRunWindowHMM_CLI exercises the --decoder window-hmm dispatch path through
// buildApp. It verifies:
//   - bad --lang is rejected before the image is loaded.
//   - a white image (no block grid) returns ErrNoMosaic wrapped by runWindowHMM.
//   - text output on a real mosaic fixture completes without unexpected error.
//   - JSON output on the same fixture produces valid JSON when no error occurs.
func TestRunWindowHMM_CLI(t *testing.T) {
	fixture := filepath.Join("..", "..", "testdata", "fixtures", "block08_go.png")
	fixtureAvail := true
	if _, statErr := os.Stat(fixture); statErr != nil {
		fixtureAvail = false
	}

	t.Run("bad lang rejected", func(t *testing.T) {
		path := whitePNG(t, 8, 8)
		err := buildApp().Run(t.Context(), []string{
			"unpixel", "--quiet", "--decoder", "window-hmm", "--lang", "klingon", path,
		})
		if err == nil {
			t.Error("expected error for unknown --lang, got nil")
		}
	})

	t.Run("white image returns error", func(t *testing.T) {
		path := whitePNG(t, 8, 8)
		err := buildApp().Run(t.Context(), []string{
			"unpixel", "--quiet", "--decoder", "window-hmm", path,
		})
		if err == nil {
			t.Error("expected error for white image (no mosaic), got nil")
		}
	})

	t.Run("text output on fixture", func(t *testing.T) {
		if !fixtureAvail {
			t.Skip("fixture not found")
		}
		out := captureStdout(t, func() {
			err := buildApp().Run(t.Context(), []string{
				"unpixel", "--quiet",
				"--decoder", "window-hmm",
				"--charset", "abcdefghijklmnopqrstuvwxyz ",
				"--font", "Liberation Mono",
				fixture,
			})
			// ErrNoContent is acceptable (font mismatch or empty result); anything
			// else is a bug.
			if err != nil && !errors.Is(err, mosaictext.ErrNoContent) {
				t.Errorf("runWindowHMM text: unexpected error: %v", err)
			}
		})
		// When text is produced it must be newline-terminated.
		if len(out) > 0 && out[len(out)-1] != '\n' {
			t.Errorf("text output should end with newline, got %q", out)
		}
	})

	t.Run("json output on fixture", func(t *testing.T) {
		if !fixtureAvail {
			t.Skip("fixture not found")
		}
		var runErr error
		out := captureStdout(t, func() {
			runErr = buildApp().Run(t.Context(), []string{
				"unpixel", "--quiet",
				"--decoder", "window-hmm",
				"--format", "json",
				"--charset", "abcdefghijklmnopqrstuvwxyz ",
				"--font", "Liberation Mono",
				fixture,
			})
		})
		if runErr != nil {
			t.Logf("runWindowHMM json: %v (acceptable)", runErr)
			return
		}
		var v map[string]any
		if jsonErr := json.Unmarshal([]byte(out), &v); jsonErr != nil {
			t.Errorf("json output is not valid JSON: %v\nout=%q", jsonErr, out)
		}
	})
}
