package mosaictext

// windowhmm_test.go — unit tests for the sliding-window beam-search decoder.

import (
	"errors"
	"image"
	"testing"

	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/fonts"
)

// --- Parallel sweep byte-identity tests ---

// TestDecodeWindowHMM_ParallelByteIdentical verifies that the parallel font
// sweep (default workers) produces byte-identical decoded text to the serial
// path (WithWHMMMaxWorkers(1)) for two distinct inputs: a monospace digit
// string and a proportional-font word string.
//
// This is the primary correctness gate for the parallelisation: the winner
// selection must visit results in original (fe,lin,fs) ordinal order so that
// tie-breaking on distance is deterministic regardless of goroutine scheduling.
func TestDecodeWindowHMM_ParallelByteIdentical(t *testing.T) {
	t.Parallel()

	monoData := findFont(t, "Liberation Mono")
	sansData := findFont(t, "Liberation Sans")

	monoR, err := defaults.RendererFromFonts(monoData, nil)
	if err != nil {
		t.Fatalf("build mono renderer: %v", err)
	}
	sansR, err := defaults.RendererFromFonts(sansData, nil)
	if err != nil {
		t.Fatalf("build sans renderer: %v", err)
	}

	cases := []struct {
		name      string
		text      string
		fs        float64
		block     int
		linear    bool
		charset   string
		fontOpt   WHMMOption
		windowOpt WHMMOption // nil if not needed
	}{
		{
			name:    "mono-digits",
			text:    "3141592653",
			fs:      32.0,
			block:   4,
			linear:  false,
			charset: "0123456789 ",
			fontOpt: WithWHMMFont("Liberation Mono"),
		},
		{
			name:      "sans-proportional",
			text:      "hello world",
			fs:        18.0,
			block:     4,
			linear:    false,
			charset:   "abcdefghijklmnopqrstuvwxyz ",
			fontOpt:   WithWHMMFont("Liberation Sans"),
			windowOpt: WithWHMMWindow(3),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			renderer := monoR
			if tc.name == "sans-proportional" {
				renderer = sansR
			}

			mosaicImg := renderMosaic(t, renderer, tc.text, tc.fs, tc.block, tc.linear)
			path := saveTestPNG(t, mosaicImg)
			img := loadTestPNG(t, path)

			sharedOpts := []WHMMOption{
				tc.fontOpt,
				WithWHMMCharset(tc.charset),
				WithWHMMLinear(0),
				WithWHMMBeamWidth(50),
			}
			if tc.windowOpt != nil {
				sharedOpts = append(sharedOpts, tc.windowOpt)
			}

			// Serial: 1 worker gives the deterministic ordinal-order baseline.
			serialRes, serErr := DecodeWindowHMM(t.Context(), img,
				append(sharedOpts, WithWHMMMaxWorkers(1))...,
			)
			if serErr != nil {
				t.Fatalf("serial decode: %v", serErr)
			}

			// Parallel: default worker count (min(NumCPU, whmmWorkerCap)).
			parallelRes, parErr := DecodeWindowHMM(t.Context(), img, sharedOpts...)
			if parErr != nil {
				t.Fatalf("parallel decode: %v", parErr)
			}

			t.Logf("%s: serial=%q parallel=%q dist=%.4f",
				tc.name, serialRes.Text, parallelRes.Text, parallelRes.Distance)

			if parallelRes.Text != serialRes.Text {
				t.Errorf("byte-identity failure: serial=%q parallel=%q",
					serialRes.Text, parallelRes.Text)
			}
		})
	}
}

// TestWithWHMMMaxWorkersOption verifies that WithWHMMMaxWorkers stores the
// value and that n≤0 is a no-op (existing value unchanged).
func TestWithWHMMMaxWorkersOption(t *testing.T) {
	t.Parallel()

	cfg := defaultWHMMConfig()
	WithWHMMMaxWorkers(4)(&cfg)
	if cfg.maxWorkers != 4 {
		t.Errorf("maxWorkers after WithWHMMMaxWorkers(4): got %d, want 4", cfg.maxWorkers)
	}

	// n≤0 must be a no-op.
	WithWHMMMaxWorkers(0)(&cfg)
	if cfg.maxWorkers != 4 {
		t.Errorf("maxWorkers after WithWHMMMaxWorkers(0): got %d, want 4 (no-op)", cfg.maxWorkers)
	}
	WithWHMMMaxWorkers(-1)(&cfg)
	if cfg.maxWorkers != 4 {
		t.Errorf("maxWorkers after WithWHMMMaxWorkers(-1): got %d, want 4 (no-op)", cfg.maxWorkers)
	}
}

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

	_, decErr := DecodeWindowHMM(
		t.Context(), img,
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

	_, decErr := DecodeWindowHMM(
		t.Context(), img,
		WithWHMMFontFile([]byte("not a font")),
	)
	if decErr == nil {
		t.Error("bad font bytes: expected error, got nil")
	}
}
