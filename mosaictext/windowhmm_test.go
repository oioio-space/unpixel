package mosaictext

// windowhmm_test.go — unit tests for the sliding-window beam-search decoder.

import (
	"errors"
	"image"
	"testing"

	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
)

// --- WHMMOption wiring tests ---

// TestWHMMOptionDefaults verifies that defaultWHMMConfig produces the expected
// zero-value fields for options that are set by caller opts.
func TestWHMMOptionDefaults(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	if cfg.charset != DefaultWHMMCharset {
		t.Errorf("default charset: got %q, want %q", cfg.charset, DefaultWHMMCharset)
	}
	if cfg.linear != -1 {
		t.Errorf("default linear: got %d, want -1 (auto)", cfg.linear)
	}
	if cfg.fontName != "" {
		t.Errorf("default fontName: got %q, want empty", cfg.fontName)
	}
}

// TestWithWHMMCharsetIgnoresEmpty verifies that passing an empty string to
// WithWHMMCharset leaves the charset unchanged (the option is a no-op).
func TestWithWHMMCharsetIgnoresEmpty(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	WithWHMMCharset("")(&cfg)
	if cfg.charset != DefaultWHMMCharset {
		t.Errorf("charset after empty set: got %q, want %q", cfg.charset, DefaultWHMMCharset)
	}
}

// TestWithWHMMFontSetsName verifies WithWHMMFont sets cfg.fontName.
func TestWithWHMMFontSetsName(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	WithWHMMFont("Liberation Mono")(&cfg)
	if cfg.fontName != "Liberation Mono" {
		t.Errorf("fontName: got %q, want %q", cfg.fontName, "Liberation Mono")
	}
}

// TestWithWHMMFontFileIgnoresEmpty verifies that WithWHMMFontFile with a nil
// slice is a no-op (fontData stays nil).
func TestWithWHMMFontFileIgnoresEmpty(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	WithWHMMFontFile(nil)(&cfg)
	if cfg.fontData != nil {
		t.Errorf("fontData: expected nil after WithWHMMFontFile(nil), got non-nil")
	}
}

// TestWithWHMMFontFileSetsData verifies that WithWHMMFontFile stores non-nil
// bytes in cfg.fontData.
func TestWithWHMMFontFileSetsData(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	stub := []byte{0x00, 0x01}
	WithWHMMFontFile(stub)(&cfg)
	if len(cfg.fontData) != len(stub) {
		t.Errorf("fontData len: got %d, want %d", len(cfg.fontData), len(stub))
	}
}

// TestWithWHMMFontFileBoldIgnoresEmpty verifies that WithWHMMFontFileBold with
// nil bytes is a no-op (fontBold stays nil).
func TestWithWHMMFontFileBoldIgnoresEmpty(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	WithWHMMFontFileBold(nil)(&cfg)
	if cfg.fontBold != nil {
		t.Errorf("fontBold: expected nil after WithWHMMFontFileBold(nil), got non-nil")
	}
}

// TestWithWHMMFontFileBoldSetsData verifies WithWHMMFontFileBold stores bytes.
func TestWithWHMMFontFileBoldSetsData(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	stub := []byte{0xFF}
	WithWHMMFontFileBold(stub)(&cfg)
	if len(cfg.fontBold) != 1 {
		t.Errorf("fontBold len: got %d, want 1", len(cfg.fontBold))
	}
}

// TestWithWHMMLinear verifies all three linear-mode values are stored.
func TestWithWHMMLinear(t *testing.T) {
	t.Parallel()
	for _, mode := range []int{-1, 0, 1} {
		cfg := defaultWHMMConfig()
		WithWHMMLinear(mode)(&cfg)
		if cfg.linear != mode {
			t.Errorf("linear mode %d: got %d", mode, cfg.linear)
		}
	}
}

// TestWithWHMMWindowIgnoresNonPositive verifies WithWHMMWindow(0) is a no-op.
func TestWithWHMMWindowIgnoresNonPositive(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	cfg.windowW = 3
	WithWHMMWindow(0)(&cfg)
	if cfg.windowW != 3 {
		t.Errorf("windowW after WithWHMMWindow(0): got %d, want 3 (no-op)", cfg.windowW)
	}
}

// TestWithWHMMBeamWidthIgnoresNonPositive verifies WithWHMMBeamWidth(0) is a no-op.
func TestWithWHMMBeamWidthIgnoresNonPositive(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	cfg.beamWidth = 500
	WithWHMMBeamWidth(0)(&cfg)
	if cfg.beamWidth != 500 {
		t.Errorf("beamWidth after WithWHMMBeamWidth(0): got %d, want 500 (no-op)", cfg.beamWidth)
	}
}

// TestWithWHMMSeedStoresSeed verifies WithWHMMSeed stores the given seed.
func TestWithWHMMSeedStoresSeed(t *testing.T) {
	t.Parallel()
	cfg := defaultWHMMConfig()
	WithWHMMSeed(42)(&cfg)
	if cfg.seed != 42 {
		t.Errorf("seed: got %d, want 42", cfg.seed)
	}
}

// --- DecodeWindowHMM error-path tests ---

// TestDecodeWindowHMM_NonMosaicReturnsErrNoMosaic verifies that a plain white
// image (no block grid) causes DecodeWindowHMM to return ErrNoMosaic.
func TestDecodeWindowHMM_NonMosaicReturnsErrNoMosaic(t *testing.T) {
	t.Parallel()
	white := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	_, err := DecodeWindowHMM(t.Context(), white)
	if !errors.Is(err, ErrNoMosaic) {
		t.Errorf("white image: got %v, want ErrNoMosaic", err)
	}
}

// TestDecodeWindowHMM_UnknownFontNameReturnsErrNoContent verifies that
// WithWHMMFont with a name that matches no bundled font returns ErrNoContent.
// It uses a real rendered mosaic so the block-grid check passes and the
// decoder reaches the font-name lookup.
func TestDecodeWindowHMM_UnknownFontNameReturnsErrNoContent(t *testing.T) {
	t.Parallel()
	all := fonts.All()
	var monoFont fonts.Font
	for _, f := range all {
		if f.Name == "Liberation Mono" {
			monoFont = f
			break
		}
	}
	if monoFont.Name == "" {
		t.Skip("Liberation Mono not found in bundled fonts")
	}
	r, err := defaults.RendererFromFonts(monoFont.Data, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}
	// Render a real mosaic so InferBlockGrid detects a grid.
	img := renderMosaic(t, r, "123", 32.0, 4, false)

	_, decErr := DecodeWindowHMM(t.Context(), img,
		WithWHMMFont("NoSuchFontXYZ"),
	)
	if !errors.Is(decErr, ErrNoContent) {
		t.Errorf("unknown font name: got %v, want ErrNoContent", decErr)
	}
}

// TestDecodeWindowHMM_BadFontFileReturnsError verifies that WithWHMMFontFile
// with invalid bytes returns a non-nil error (not a panic).
// It uses a real rendered mosaic so the block-grid check passes and the
// decoder reaches the font-file parse step.
func TestDecodeWindowHMM_BadFontFileReturnsError(t *testing.T) {
	t.Parallel()
	all := fonts.All()
	var monoFont fonts.Font
	for _, f := range all {
		if f.Name == "Liberation Mono" {
			monoFont = f
			break
		}
	}
	if monoFont.Name == "" {
		t.Skip("Liberation Mono not found in bundled fonts")
	}
	r, err := defaults.RendererFromFonts(monoFont.Data, nil)
	if err != nil {
		t.Fatalf("build renderer: %v", err)
	}
	img := renderMosaic(t, r, "123", 32.0, 4, false)

	_, decErr := DecodeWindowHMM(t.Context(), img,
		WithWHMMFontFile([]byte("not a font")),
	)
	if decErr == nil {
		t.Error("bad font bytes: expected error, got nil")
	}
}
