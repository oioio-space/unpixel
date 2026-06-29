package unpixel

import (
	"context"
	"image"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// Verdict is the result of verifying one candidate string against a pixelated
// image using the engine's faithful forward model.
type Verdict struct {
	// Text is the candidate string that was scored.
	Text string
	// Distance is the faithful whole-image distance in [0,1] (≈0 means exact match).
	Distance float64
	// Match reports whether Distance is below VerifyMatchThreshold.
	Match bool
}

const (
	// VerifyMatchThreshold is the absolute distance below which a candidate is
	// considered a confident physical match. Calibrated so that exact recoveries
	// (distance ≈ 0) match and clearly-wrong candidates do not.
	VerifyMatchThreshold = 0.10

	// maxVerifyCandidates is the maximum number of candidates Verify will score.
	// Candidates beyond this cap are silently ignored.
	maxVerifyCandidates = 256
)

// Verify scores each candidate against img using the engine's faithful forward
// model (same render→operator→metric pipeline as Recover), evaluated at the
// candidate's best grid offset. opts mirror Recover's options
// (WithCharset/WithBlockSize/WithStyle/WithAuto…); auto-fingerprinting is
// applied by default when no Pixelator is set. Candidates beyond
// maxVerifyCandidates are ignored. Returns one Verdict per accepted candidate,
// in input order.
//
// Verify returns ErrNilImage for a nil image and ErrNoComponents when a
// required component is missing and no defaults are wired.
//
// Verify uses the mosaic forward model; it does NOT perform the blur sigma-sweep
// that RecoverBlurred does. For Gaussian-blurred redactions, pass an explicit
// blur operator via WithPixelator (e.g. defaults.GaussianBlur(sigma)) to get a
// single-sigma faithful score — otherwise distances may be high for every
// candidate and nothing will Match.
func Verify(ctx context.Context, img image.Image, candidates []string, opts ...Option) ([]Verdict, error) {
	if img == nil {
		return nil, ErrNilImage
	}
	if DefaultVerifyCore == nil {
		return nil, ErrNoComponents
	}

	// Build config from caller options, defaulting to the auto path.
	var cfg Config
	for _, opt := range opts {
		opt(&cfg)
	}

	// Apply auto flags when the caller has not pinned a Pixelator and has not
	// explicitly set a block size. This mirrors the "auto path" Recover uses when
	// called with no options.
	if cfg.Pixelator == nil && cfg.BlockSize <= 0 {
		cfg.autoCrop = true
		cfg.autoColorspace = true
		cfg.autoBlur = true
		cfg.autoCalibrate = true
	}

	// --- Replicate New's preparation prologue (additive; Recover is untouched) ---

	rgba := imutil.ToRGBA(img)

	// Auto-contrast: invert dark-background images to match the dark-on-light
	// rendering pipeline.
	if darkBackground(rgba) {
		rgba = invertColors(rgba)
	}

	// Deskew: detect and correct grid rotation.
	var grid BlockGrid
	rgba, _, grid = detectAndDeskew(rgba)

	// Auto-crop: locate the mosaic band and crop to it. Only fires when the
	// DefaultLocateMosaicBand hook is wired and the band is smaller than the full
	// image — byte-identical behaviour when the hook is absent or the crop is a no-op.
	if cfg.autoCrop && DefaultLocateMosaicBand != nil {
		if band, ok := DefaultLocateMosaicBand(rgba); ok {
			b := rgba.Bounds()
			if band.Dx() < b.Dx() || band.Dy() < b.Dy() {
				ox, oy := band.Min.X-b.Min.X, band.Min.Y-b.Min.Y
				rgba = imutil.Crop(rgba, ox, oy, band.Dx(), band.Dy())
				grid.Size = 0
			}
		}
	}

	// Block-size inference: prefer the grid already computed by detectAndDeskew;
	// fall back to InferBlockSize; applyDefaults provides DefaultBlockSize as the
	// final backstop.
	if cfg.BlockSize <= 0 {
		if grid.Size >= 2 {
			cfg.BlockSize = grid.Size
		} else if s := InferBlockSize(rgba); s >= 2 {
			cfg.BlockSize = s
		}
	}
	cfg = applyDefaults(cfg)

	// Auto-fingerprint: detect the forward operator (colorspace / blur family)
	// and install the matching pixelator when confident.
	applyAutoFingerprint(&cfg, rgba)

	// Auto-calibrate: seed LetterSpacing from the inferred x-stretch.
	if cfg.autoCalibrate && cfg.Style.LetterSpacing == 0 && cfg.BlockSize >= 2 && cfg.Style.FontSize > 0 {
		if refW := int(cfg.Style.FontSize * 0.6); refW > 0 {
			if stretch, ok := InferXStretch(rgba, cfg.BlockSize, refW); ok && (stretch < 0.98 || stretch > 1.02) {
				cfg.Style.LetterSpacing = (stretch - 1) * float64(refW)
			}
		}
	}

	// Wire components (Renderer, Pixelator, Metric) via DefaultComponents when
	// any required field is still nil.
	if cfg.Renderer == nil || cfg.Pixelator == nil || cfg.Metric == nil {
		if DefaultComponents != nil {
			if err := DefaultComponents(&cfg); err != nil {
				return nil, err
			}
		}
	}

	// Cap candidates and delegate the scoring work to the hook (which lives in
	// the defaults package and can import internal/search without a cycle).
	capped := candidates[:min(len(candidates), maxVerifyCandidates)]
	return DefaultVerifyCore(ctx, rgba, cfg, capped)
}
