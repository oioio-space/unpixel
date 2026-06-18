package unpixel_test

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"slices"
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
