package blind_test

import (
	"image"
	"testing"

	xdraw "golang.org/x/image/draw"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/blind"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
)

// syntheticBandSRGB renders phrase and pixelates with sRGB BlockAverage(testBlock).
// Used to build a fixture where GammaAuto should pick "srgb" (lower dist).
func syntheticBandSRGB(t *testing.T, phrase string, offsetX int) image.Image {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	img, sx, err := r.Render(phrase, unpixel.Style{FontSize: testFontSize})
	if err != nil {
		t.Fatalf("render %q: %v", phrase, err)
	}
	ink := inkBounds(img, sx)
	inkImg := image.NewRGBA(image.Rect(0, 0, ink.Dx(), ink.Dy()))
	xdraw.Draw(inkImg, inkImg.Bounds(), img, ink.Min, xdraw.Src)
	pix := pixelate.NewBlockAverage(testBlock)
	return pix.Pixelate(inkImg, offsetX, 0)
}

// TestGammaMode_WithLinearCompat verifies that WithLinear(true) and
// WithGamma(GammaLinear) produce identical Gamma/Text results, and that
// WithLinear(false) and WithGamma(GammaSRGB) also agree.
func TestGammaMode_WithLinearCompat(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}
	t.Parallel()

	img := syntheticBand(t, "the cat", 0)
	ctx := t.Context()
	baseOpts := []blind.Option{
		blind.WithLanguage(lang.English),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithFonts("sans"),
	}

	gotLinearTrue, err := blind.Recover(ctx, img, append(baseOpts, blind.WithLinear(true))...)
	if err != nil {
		t.Fatalf("WithLinear(true): %v", err)
	}
	gotGammaLinear, err := blind.Recover(ctx, img, append(baseOpts, blind.WithGamma(blind.GammaLinear))...)
	if err != nil {
		t.Fatalf("WithGamma(GammaLinear): %v", err)
	}

	if got, want := gotGammaLinear.Text, gotLinearTrue.Text; got != want {
		t.Errorf("Text: WithGamma(GammaLinear)=%q, WithLinear(true)=%q", got, want)
	}
	if got, want := gotGammaLinear.Gamma, gotLinearTrue.Gamma; got != want {
		t.Errorf("Gamma: WithGamma(GammaLinear)=%q, WithLinear(true)=%q", got, want)
	}
	if got, want := gotGammaLinear.Gamma, "linear"; got != want {
		t.Errorf("GammaLinear.Gamma: got %q, want %q", got, want)
	}

	gotLinearFalse, err := blind.Recover(ctx, img, append(baseOpts, blind.WithLinear(false))...)
	if err != nil {
		t.Fatalf("WithLinear(false): %v", err)
	}
	gotGammaSRGB, err := blind.Recover(ctx, img, append(baseOpts, blind.WithGamma(blind.GammaSRGB))...)
	if err != nil {
		t.Fatalf("WithGamma(GammaSRGB): %v", err)
	}

	if got, want := gotGammaSRGB.Text, gotLinearFalse.Text; got != want {
		t.Errorf("Text: WithGamma(GammaSRGB)=%q, WithLinear(false)=%q", got, want)
	}
	if got, want := gotGammaSRGB.Gamma, gotLinearFalse.Gamma; got != want {
		t.Errorf("Gamma: WithGamma(GammaSRGB)=%q, WithLinear(false)=%q", got, want)
	}
	if got, want := gotGammaSRGB.Gamma, "srgb"; got != want {
		t.Errorf("GammaSRGB.Gamma: got %q, want %q", got, want)
	}
}

// TestGammaAuto_PicksLinear verifies that when the source image is pixelated in
// linear space, GammaAuto selects "linear" (lower dist than sRGB).
func TestGammaAuto_PicksLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}
	t.Parallel()

	// syntheticBand uses LinearBlockAverage — ground truth is linear.
	img := syntheticBand(t, "the cat", 0)
	ctx := t.Context()

	result, err := blind.Recover(
		ctx, img,
		blind.WithLanguage(lang.English),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithFonts("sans"),
		// Default is GammaAuto; no WithGamma call.
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	t.Logf("GammaAuto on linear fixture: gamma=%q dist=%.6f", result.Gamma, result.Dist)
	if got, want := result.Gamma, "linear"; got != want {
		t.Errorf("Gamma: got %q, want %q", got, want)
	}
}

// TestGammaAuto_PicksSRGB verifies that when the source image is pixelated in
// sRGB space, GammaAuto selects "srgb" (lower dist than linear).
func TestGammaAuto_PicksSRGB(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}
	t.Parallel()

	// syntheticBandSRGB uses BlockAverage — ground truth is sRGB.
	img := syntheticBandSRGB(t, "the cat", 0)
	ctx := t.Context()

	result, err := blind.Recover(
		ctx, img,
		blind.WithLanguage(lang.English),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithFonts("sans"),
		// Default is GammaAuto; no WithGamma call.
	)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	t.Logf("GammaAuto on sRGB fixture: gamma=%q dist=%.6f", result.Gamma, result.Dist)
	if got, want := result.Gamma, "srgb"; got != want {
		t.Errorf("Gamma: got %q, want %q", got, want)
	}
}

// TestGammaDefault_IsAuto verifies that not calling WithGamma behaves identically
// to WithGamma(GammaAuto) — the default is auto mode.
func TestGammaDefault_IsAuto(t *testing.T) {
	if testing.Short() {
		t.Skip("full blind decode; skipping in -short mode")
	}
	t.Parallel()

	img := syntheticBand(t, "ok", 0)
	ctx := t.Context()
	baseOpts := []blind.Option{
		blind.WithLanguage(lang.English),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithFonts("sans"),
	}

	gotDefault, err := blind.Recover(ctx, img, baseOpts...)
	if err != nil {
		t.Fatalf("default Recover: %v", err)
	}
	gotExplicitAuto, err := blind.Recover(ctx, img, append(baseOpts, blind.WithGamma(blind.GammaAuto))...)
	if err != nil {
		t.Fatalf("WithGamma(GammaAuto): %v", err)
	}

	if got, want := gotDefault.Gamma, gotExplicitAuto.Gamma; got != want {
		t.Errorf("Gamma: default=%q, WithGamma(GammaAuto)=%q", got, want)
	}
	if got, want := gotDefault.Text, gotExplicitAuto.Text; got != want {
		t.Errorf("Text: default=%q, WithGamma(GammaAuto)=%q", got, want)
	}
}

// BenchmarkRecover_AutoGamma measures the cost of GammaAuto (two decoder runs)
// compared to a forced single-gamma run. Auto roughly doubles the decode work —
// this is expected and acceptable as a one-time calibration step, not a per-
// candidate hot loop.
func BenchmarkRecover_AutoGamma(b *testing.B) {
	img := syntheticBandB(b, "ok", 0)
	ctx := b.Context()
	baseOpts := []blind.Option{
		blind.WithLanguage(lang.English),
		blind.WithBlock(testBlock),
		blind.WithFontSize(testFontSize),
		blind.WithFonts("sans"),
	}

	b.Run("auto", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var err error
			sink, err = blind.Recover(ctx, img, append(baseOpts, blind.WithGamma(blind.GammaAuto))...)
			if err != nil {
				b.Fatalf("Recover: %v", err)
			}
		}
	})

	b.Run("linear", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var err error
			sink, err = blind.Recover(ctx, img, append(baseOpts, blind.WithGamma(blind.GammaLinear))...)
			if err != nil {
				b.Fatalf("Recover: %v", err)
			}
		}
	})

	b.Run("srgb", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var err error
			sink, err = blind.Recover(ctx, img, append(baseOpts, blind.WithGamma(blind.GammaSRGB))...)
			if err != nil {
				b.Fatalf("Recover: %v", err)
			}
		}
	})
}
