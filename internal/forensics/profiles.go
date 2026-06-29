package forensics

import (
	"fmt"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// Profile is a named redaction-tool forward-operator configuration. Tool is a
// human-readable label; the remaining fields describe the forward operator that
// the tool applies. For mosaic profiles, Gamma is set and Kernel/Edge are zero.
// For blur profiles, Kernel and Edge are set and Gamma is zero.
type Profile struct {
	// Tool is the human-readable name of the redaction tool, e.g. "GEGL".
	Tool string
	// Kind is the operator family: KindMosaic or KindBlur.
	Kind Kind
	// Gamma is the colorspace used for mosaic averaging (KindMosaic only).
	Gamma Gamma
	// Kernel is the blur kernel family (KindBlur only).
	Kernel Kernel
	// Edge is the border-handling mode used by the blur kernel (KindBlur only).
	Edge pixelate.Edge
}

// Zoo returns the catalogue of known tool profiles, covering GEGL, Photoshop,
// GIMP, CSS, ffmpeg, and OpenCV in their mosaic and/or blur variants.
//
// Profiles are not deduplicated by config here; callers that need deduplication
// should group by [Profile.configKey].
func Zoo() []Profile {
	return []Profile{
		// Mosaic operators
		{Tool: "GEGL", Kind: KindMosaic, Gamma: GammaLinear},
		{Tool: "CSS", Kind: KindMosaic, Gamma: GammaLinear},
		{Tool: "Photoshop", Kind: KindMosaic, Gamma: GammaSRGB},
		{Tool: "GIMP", Kind: KindMosaic, Gamma: GammaSRGB},
		// Blur operators
		{Tool: "GEGL", Kind: KindBlur, Kernel: KernelBox3, Edge: pixelate.EdgeClamp},
		{Tool: "CSS", Kind: KindBlur, Kernel: KernelBox3, Edge: pixelate.EdgeClamp},
		{Tool: "Photoshop", Kind: KindBlur, Kernel: KernelTrueGauss, Edge: pixelate.EdgeClamp},
		{Tool: "GIMP", Kind: KindBlur, Kernel: KernelBox3, Edge: pixelate.EdgeReflect},
		{Tool: "ffmpeg", Kind: KindBlur, Kernel: KernelTrueGauss, Edge: pixelate.EdgeClamp},
		{Tool: "OpenCV", Kind: KindBlur, Kernel: KernelTrueGauss, Edge: pixelate.EdgeReflect},
	}
}

// configKey returns the deduplication key for p at the given block size (mosaic)
// or sigma (blur). Two profiles that produce the same forward operator for the
// same block/sigma will share a key, regardless of Tool. This lets callers
// collapse identical operators without running them twice.
func (p Profile) configKey(block int, sigma float64) string {
	switch p.Kind {
	case KindMosaic:
		return fmt.Sprintf("mosaic:gamma=%d:block=%d", p.Gamma, block)
	case KindBlur:
		// Bucket sigma to two decimal places so tiny float differences do not
		// prevent deduplication of profiles that are effectively identical.
		return fmt.Sprintf("blur:kernel=%d:edge=%d:sigma=%.2f", p.Kernel, p.Edge, sigma)
	default:
		return fmt.Sprintf("unknown:block=%d:sigma=%.2f", block, sigma)
	}
}
