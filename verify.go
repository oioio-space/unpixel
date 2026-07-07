package unpixel

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"os"

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

// verifyCropMargin is the white border (right, bottom) added around a WithCrop
// band before search. It gives the verifier's alignment sweep room to slide the
// rendered candidate over the band (verifyCore aligns over a position range of
// ~64 px) and to absorb the block-multiple padding the pixelator adds, so a tight
// band does not clip a candidate that renders slightly wider than the observed ink.
var verifyCropMargin = image.Pt(128, 48)

// cropToBand crops rgba to band (clamped to its bounds) and pads the result with a
// white [verifyCropMargin] border. It returns rgba unchanged when band does not
// intersect the image.
func cropToBand(rgba *image.RGBA, band image.Rectangle) *image.RGBA {
	band = band.Intersect(rgba.Bounds())
	if band.Empty() {
		return rgba
	}
	sub := imutil.Crop(rgba, band.Min.X-rgba.Bounds().Min.X, band.Min.Y-rgba.Bounds().Min.Y, band.Dx(), band.Dy())
	return imutil.PadWhite(sub, sub.Bounds().Dx()+verifyCropMargin.X, sub.Bounds().Dy()+verifyCropMargin.Y)
}

// prepareVerify runs the shared preparation prologue for the Verify family:
// it resolves opts into a Config (enabling the auto path when neither a
// Pixelator nor a block size is set), converts img to RGBA, auto-contrast /
// deskews / crops it, infers the block size and forward operator, auto-calibrates
// letter spacing, and wires the default components. It returns the prepped image
// and resolved Config, or the error from DefaultComponents. It does not score
// anything — Verify and VerifyImage supply their own scoring step.
func prepareVerify(img image.Image, opts []Option) (*image.RGBA, Config, error) {
	var cfg Config
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.Pixelator == nil && cfg.BlockSize <= 0 {
		cfg.autoCrop = true
		cfg.autoColorspace = true
		cfg.autoBlur = true
		cfg.autoCalibrate = true
	}

	rgba := imutil.ToRGBA(img)
	if !cfg.crop.Empty() {
		rgba = cropToBand(rgba, cfg.crop)
	}
	if darkBackground(rgba) {
		rgba = invertColors(rgba)
	}

	var grid BlockGrid
	rgba, _, grid = detectAndDeskew(rgba)

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

	if cfg.BlockSize <= 0 {
		if grid.Size >= 2 {
			cfg.BlockSize = grid.Size
		} else if s := InferBlockSize(rgba); s >= 2 {
			cfg.BlockSize = s
		}
	}
	cfg = applyDefaults(cfg)

	applyAutoFingerprint(&cfg, rgba)

	if cfg.autoCalibrate && cfg.Style.LetterSpacing == 0 && cfg.BlockSize >= 2 && cfg.Style.FontSize > 0 {
		if refW := int(cfg.Style.FontSize * 0.6); refW > 0 {
			if stretch, ok := InferXStretch(rgba, cfg.BlockSize, refW); ok && (stretch < 0.98 || stretch > 1.02) {
				cfg.Style.LetterSpacing = (stretch - 1) * float64(refW)
			}
		}
	}

	if cfg.Renderer == nil || cfg.Pixelator == nil || cfg.Metric == nil {
		if DefaultComponents != nil {
			if err := DefaultComponents(&cfg); err != nil {
				return nil, cfg, err
			}
		}
	}

	return rgba, cfg, nil
}

// ImageVerdict is the result of physically verifying a restored image against a
// redaction by re-applying the forward operator and comparing.
type ImageVerdict struct {
	// Distance is the whole-image distance in [0,1] between the redaction and the
	// re-pixelated restored image at its best grid phase (lower = more consistent).
	Distance float64
	// Match reports whether Distance is below VerifyMatchThreshold.
	Match bool
}

// VerifyImage physically verifies a restored (clean) image against a redaction:
// it re-applies the engine's forward operator to restored (re-pixelate at the
// mosaic block, or blur when a blur Pixelator is set via WithPixelator) and
// compares the result to redacted with the faithful metric, at the best grid
// phase. It is the image-input analogue of [Verify]: VerifyImage(redacted,
// render(text)) is the physical core of Verify(redacted, []string{text}).
//
// Use it as an anti-hallucination gate for an external restorer (e.g. a diffusion
// sidecar): a faithful restoration re-pixelates back to the observed redaction
// (low Distance, Match=true); a hallucination does not — except where the mosaic
// is genuinely ambiguous (many restorations map to the same mosaic), which no
// physical check can disambiguate.
//
// VerifyImage returns ErrNilImage when either image is nil and ErrNoComponents
// when the defaults package is not imported. opts mirror Verify's
// (WithBlockSize/WithCharset/WithPixelator/WithAuto…); for blurred redactions
// pass an explicit blur operator via WithPixelator, like Verify.
func VerifyImage(ctx context.Context, redacted, restored image.Image, opts ...Option) (ImageVerdict, error) {
	if redacted == nil || restored == nil {
		return ImageVerdict{}, ErrNilImage
	}
	if DefaultVerifyImageCore == nil {
		return ImageVerdict{}, ErrNoComponents
	}
	rgba, cfg, err := prepareVerify(redacted, opts)
	if err != nil {
		return ImageVerdict{}, err
	}
	return DefaultVerifyImageCore(ctx, rgba, imutil.ToRGBA(restored), cfg)
}

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
	rgba, cfg, err := prepareVerify(img, opts)
	if err != nil {
		return nil, err
	}
	capped := candidates[:min(len(candidates), maxVerifyCandidates)]
	return DefaultVerifyCore(ctx, rgba, cfg, capped)
}

// VerifyReader decodes an image from r (PNG is registered; import the format's
// image/<fmt> package for others) and calls Verify. It is the io.Reader counterpart
// of Verify, for callers holding a stream rather than a decoded image.Image.
func VerifyReader(ctx context.Context, r io.Reader, candidates []string, opts ...Option) ([]Verdict, error) {
	img, err := decodeImage(r)
	if err != nil {
		return nil, err
	}
	return Verify(ctx, img, candidates, opts...)
}

// VerifyBytes decodes an image from in-memory data and calls Verify — the []byte
// counterpart of Verify (HTTP bodies, embedded assets).
func VerifyBytes(ctx context.Context, data []byte, candidates []string, opts ...Option) ([]Verdict, error) {
	return VerifyReader(ctx, bytes.NewReader(data), candidates, opts...)
}

// VerifyFile opens the image at path and calls VerifyReader. Use "-"-to-stdin
// handling at the call site if needed; VerifyFile always opens a file.
func VerifyFile(ctx context.Context, path string, candidates []string, opts ...Option) ([]Verdict, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-provided image path is the operation's purpose
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	defer func() { _ = f.Close() }()
	return VerifyReader(ctx, f, candidates, opts...)
}
