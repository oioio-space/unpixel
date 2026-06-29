// Package forensics identifies the forward (redaction) operator applied to a
// redacted image region: mosaic vs. Gaussian blur, sRGB vs. linear-light
// colorspace, blur sigma, and kernel family. It aggregates the detectors in
// [github.com/oioio-space/unpixel/internal/pixelate] and exposes a single
// [Fingerprint] call that returns an [Operator] descriptor.
//
// Usage:
//
//	op := forensics.Fingerprint(img, forensics.Hint{Block: blockSize})
//	if px, ok := op.Build(0.5); ok {
//	    // px satisfies unpixel.Pixelator structurally
//	}
package forensics

import (
	"image"

	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// Kind classifies the redaction's forward operator family.
type Kind uint8

const (
	// KindUnknown is returned when detection is inconclusive.
	KindUnknown Kind = iota
	// KindMosaic indicates a block-average (mosaic/pixelate) redaction.
	KindMosaic
	// KindBlur indicates a Gaussian (or box-approximate) blur redaction.
	KindBlur
)

// String returns a human-readable label for k, suitable for reports and logs.
func (k Kind) String() string {
	switch k {
	case KindMosaic:
		return "mosaic"
	case KindBlur:
		return "blur"
	default:
		return "unknown"
	}
}

// Gamma classifies the colorspace in which the mosaic averaging was performed.
type Gamma uint8

const (
	// GammaUnknown means colorspace detection was not run or was inconclusive.
	GammaUnknown Gamma = iota
	// GammaSRGB indicates averaging in the perceptual (gamma-compressed) domain.
	GammaSRGB
	// GammaLinear indicates averaging in linear-light (physically correct) domain.
	GammaLinear
)

// String returns a human-readable label for g.
func (g Gamma) String() string {
	switch g {
	case GammaSRGB:
		return "sRGB"
	case GammaLinear:
		return "linear"
	default:
		return "unknown"
	}
}

// Kernel distinguishes kernel families for the blur operator.
type Kernel uint8

const (
	// KernelUnknown means the kernel could not be determined.
	KernelUnknown Kernel = iota
	// KernelTrueGauss is an exact separable Gaussian convolution.
	KernelTrueGauss
	// KernelBox3 is a 3-pass box-blur approximation (FastBlur / GIMP).
	KernelBox3
)

// String returns a human-readable label for k.
func (k Kernel) String() string {
	switch k {
	case KernelTrueGauss:
		return "true-gauss"
	case KernelBox3:
		return "box3"
	default:
		return "unknown"
	}
}

// Conf holds per-attribute detection confidence, each in [0, 1].
type Conf struct {
	// Kind is the confidence that the detected operator Kind is correct.
	Kind float64
	// Gamma is the confidence that the detected Gamma is correct (mosaic only).
	Gamma float64
	// Sigma is the confidence in the estimated blur sigma (blur only).
	Sigma float64
}

// Operator describes the detected forward (redaction) operator.
type Operator struct {
	// Kind is the operator family (mosaic or blur).
	Kind Kind
	// Gamma is the colorspace of mosaic averaging (KindMosaic only).
	Gamma Gamma
	// Block is the mosaic block size passed via Hint; 0 if unknown.
	Block int
	// Sigma is the estimated Gaussian sigma (KindBlur only).
	Sigma float64
	// Kernel is the blur kernel family (KindBlur only).
	Kernel Kernel
	// Tool is a best-effort informative label for the likely redaction tool.
	Tool string
	// Conf holds per-attribute detection confidences.
	Conf Conf
}

// Hint carries what the caller already knows, to avoid re-detection.
type Hint struct {
	// Block is the inferred mosaic block size (≥ 2), or 0 if unknown.
	Block int
}

// Pixelator is the subset of unpixel.Pixelator that this package constructs.
// Any value returned by [Operator.Build] satisfies unpixel.Pixelator
// structurally (same method set), so callers can assign it without importing
// the root package — which would create an import cycle.
type Pixelator interface {
	Pixelate(img *image.RGBA, originX, originY int) *image.RGBA
}

// Fingerprint analyses img and returns the best-effort detected operator.
// hint.Block is taken as-is (caller-inferred, e.g. via unpixel.InferBlockSize).
//
// Detection logic:
//   - [pixelate.DetectBlur] classifies mosaic vs. blur, estimates sigma/kernel.
//   - For mosaic with hint.Block ≥ 2, [pixelate.DetectColorspace] distinguishes
//     sRGB from linear-light averaging.
//   - Tool is set heuristically: "GEGL/CSS" for linear+box3, "Photoshop/GIMP"
//     for sRGB mosaic; empty when unrecognised.
func Fingerprint(img image.Image, hint Hint) Operator {
	rgba := imutil.ToRGBA(img)
	bi := pixelate.DetectBlur(rgba, hint.Block)

	op := Operator{
		Block: hint.Block,
		Sigma: bi.Sigma,
		Conf:  Conf{Kind: bi.Conf},
	}

	switch bi.Kind {
	case pixelate.BlurKindMosaic:
		op.Kind = KindMosaic
		op.Kernel = KernelUnknown // kernel not meaningful for mosaic
		if hint.Block >= 2 {
			linear, gconf := pixelate.DetectColorspace(rgba, hint.Block)
			op.Conf.Gamma = gconf
			if linear {
				op.Gamma = GammaLinear
			} else {
				op.Gamma = GammaSRGB
			}
		}
		op.Tool = mosaicTool(op.Gamma)
	case pixelate.BlurKindGaussian:
		op.Kind = KindBlur
		op.Kernel = mapKernel(bi.Kernel)
		op.Tool = blurTool(op.Kernel)
	default:
		op.Kind = KindUnknown
	}

	return op
}

// Build constructs the forward pixelator for o when detection was confident
// enough. It returns (nil, false) when any decisive confidence is below
// threshold so the caller can keep its default — guaranteeing no regression.
//
// For KindMosaic both Conf.Kind and Conf.Gamma must meet threshold (block +
// operator family + colorspace all needed). For KindBlur it is Conf.Kind.
func (o Operator) Build(threshold float64) (Pixelator, bool) {
	switch o.Kind {
	case KindMosaic:
		if o.Block < 2 || o.Conf.Kind < threshold || o.Conf.Gamma < threshold {
			return nil, false
		}
		if o.Gamma == GammaLinear {
			return pixelate.NewLinearBlockAverage(o.Block), true
		}
		return pixelate.NewBlockAverage(o.Block), true
	case KindBlur:
		if o.Conf.Kind < threshold || o.Sigma <= 0 {
			return nil, false
		}
		if o.Kernel == KernelBox3 {
			return pixelate.NewFastBlur(o.Sigma), true
		}
		return pixelate.NewGaussianBlur(o.Sigma), true
	default:
		return nil, false
	}
}

// mapKernel converts a pixelate.BlurKernel to the local Kernel type.
func mapKernel(k pixelate.BlurKernel) Kernel {
	switch k {
	case pixelate.BlurKernelTrueGauss:
		return KernelTrueGauss
	case pixelate.BlurKernelBox3:
		return KernelBox3
	default:
		return KernelUnknown
	}
}

// mosaicTool returns a best-effort tool label for mosaic redactions.
func mosaicTool(g Gamma) string {
	switch g {
	case GammaLinear:
		return "GEGL/CSS"
	case GammaSRGB:
		return "Photoshop/GIMP"
	default:
		return ""
	}
}

// blurTool returns a best-effort tool label for blur redactions.
func blurTool(k Kernel) string {
	switch k {
	case KernelBox3:
		return "GIMP/CSS"
	case KernelTrueGauss:
		return "Photoshop"
	default:
		return ""
	}
}
