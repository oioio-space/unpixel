package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults"
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
