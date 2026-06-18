package unpixel_test

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oioio-space/unpixel"
)

func TestRecover_roundTrip(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)
	redacted := makeSyntheticRedacted(t, c, "hello", style, blockSize)

	res, err := unpixel.Recover(t.Context(), redacted,
		unpixel.WithMaxLength(7),
		unpixel.WithBlockSize(blockSize),
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	guesses := []string{res.BestGuess}
	for _, e := range res.Candidates {
		guesses = append(guesses, e.Guess)
	}
	if !slices.Contains(guesses, "hello") {
		t.Errorf("Recover did not recover %q; best=%q candidates=%d", "hello", res.BestGuess, len(res.Candidates))
	}
}

func TestRecoverReader_roundTrip(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)
	redacted := makeSyntheticRedacted(t, c, "hello", style, blockSize)

	var buf bytes.Buffer
	if err := png.Encode(&buf, redacted); err != nil {
		t.Fatalf("encode: %v", err)
	}
	res, err := unpixel.RecoverReader(t.Context(), &buf,
		unpixel.WithMaxLength(7), unpixel.WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("RecoverReader: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("RecoverReader returned empty best guess")
	}
}

func TestRecoverFile_roundTrip(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)
	redacted := makeSyntheticRedacted(t, c, "hello", style, blockSize)

	path := filepath.Join(t.TempDir(), "redacted.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := png.Encode(f, redacted); err != nil {
		_ = f.Close()
		t.Fatalf("encode: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	res, err := unpixel.RecoverFile(t.Context(), path,
		unpixel.WithMaxLength(7), unpixel.WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("RecoverFile: %v", err)
	}
	if res.BestGuess == "" {
		t.Error("RecoverFile returned empty best guess")
	}
}

func TestRecover_nilImage(t *testing.T) {
	if _, err := unpixel.Recover(t.Context(), nil); !errors.Is(err, unpixel.ErrNilImage) {
		t.Errorf("Recover(nil) error = %v, want ErrNilImage", err)
	}
}

func TestRecoverReader_badImage(t *testing.T) {
	if _, err := unpixel.RecoverReader(t.Context(), bytes.NewReader([]byte("not a png"))); err == nil {
		t.Error("RecoverReader(garbage) = nil error, want decode error")
	}
}

func TestRecoverFile_missing(t *testing.T) {
	if _, err := unpixel.RecoverFile(t.Context(), filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Error("RecoverFile(missing) = nil error, want open error")
	}
}

// TestResultString checks the human-readable Result/Eval summaries.
func TestResultString(t *testing.T) {
	r := unpixel.Result{BestGuess: "hello", BestScore: 0.0123, Confidence: 0.94}
	if got := r.String(); !strings.Contains(got, `"hello"`) || !strings.Contains(got, "confidence") {
		t.Errorf("Result.String() = %q, want guess + confidence", got)
	}
	if got := (unpixel.Result{}).String(); !strings.Contains(got, "no candidate") {
		t.Errorf("empty Result.String() = %q, want 'no candidate'", got)
	}
	if got := (unpixel.Result{Err: errors.New("boom")}).String(); !strings.Contains(got, "boom") {
		t.Errorf("errored Result.String() = %q, want the error", got)
	}
	if got := (unpixel.Eval{Guess: "ab", Score: 0.5}).String(); !strings.Contains(got, `"ab"`) {
		t.Errorf("Eval.String() = %q, want the guess", got)
	}
}

// TestCharsetPresets checks the exported charset presets are well-formed and
// progressively wider.
func TestCharsetPresets(t *testing.T) {
	if unpixel.CharsetLower != unpixel.DefaultCharset {
		t.Errorf("CharsetLower = %q, want DefaultCharset", unpixel.CharsetLower)
	}
	if len(unpixel.CharsetASCII) != 95 { // printable ASCII 0x20..0x7E
		t.Errorf("CharsetASCII length = %d, want 95", len(unpixel.CharsetASCII))
	}
	for _, c := range unpixel.CharsetASCII {
		if c < 0x20 || c > 0x7E {
			t.Errorf("CharsetASCII contains non-printable %q", c)
		}
	}
	if len(unpixel.CharsetLower) >= len(unpixel.CharsetAlnum) || len(unpixel.CharsetAlnum) >= len(unpixel.CharsetASCII) {
		t.Errorf("presets not progressively wider: lower=%d alnum=%d ascii=%d",
			len(unpixel.CharsetLower), len(unpixel.CharsetAlnum), len(unpixel.CharsetASCII))
	}
	// No duplicate characters within a preset (ordered set).
	for name, cs := range map[string]string{"alnum": unpixel.CharsetAlnum, "ascii": unpixel.CharsetASCII} {
		seen := map[rune]bool{}
		for _, c := range cs {
			if seen[c] {
				t.Errorf("%s preset has duplicate %q", name, c)
			}
			seen[c] = true
		}
	}
}

// TestOptions verifies that each Option mutates the intended Config field, and
// that WithConfig provides a base that later options override.
func TestOptions(t *testing.T) {
	var cfg unpixel.Config
	unpixel.WithConfig(unpixel.Config{Charset: "base", MaxLength: 1})(&cfg)
	if cfg.Charset != "base" || cfg.MaxLength != 1 {
		t.Fatalf("WithConfig did not seed the base config: %+v", cfg)
	}
	for _, opt := range []unpixel.Option{
		unpixel.WithCharset("abc"),
		unpixel.WithMaxLength(9),
		unpixel.WithBlockSize(16),
		unpixel.WithThreshold(0.3),
		unpixel.WithTopN(7),
		unpixel.WithWorkers(3),
	} {
		opt(&cfg)
	}
	switch {
	case cfg.Charset != "abc":
		t.Errorf("Charset = %q, want abc", cfg.Charset)
	case cfg.MaxLength != 9:
		t.Errorf("MaxLength = %d, want 9", cfg.MaxLength)
	case cfg.BlockSize != 16:
		t.Errorf("BlockSize = %d, want 16", cfg.BlockSize)
	case cfg.Threshold != 0.3:
		t.Errorf("Threshold = %v, want 0.3", cfg.Threshold)
	case cfg.TopN != 7:
		t.Errorf("TopN = %d, want 7", cfg.TopN)
	case cfg.Workers != 3:
		t.Errorf("Workers = %d, want 3", cfg.Workers)
	}
}
