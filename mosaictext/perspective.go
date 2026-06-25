package mosaictext

// perspective.go — DecodePerspective: rectify-to-propose + forward-model-distance.
//
// DecodePerspective recovers text from a mosaic-pixelated redaction that was
// photographed at an angle. The algorithm has two stages:
//
//  1. Rectify-to-propose: the user supplies the four corners of the redaction
//     quadrilateral in the photo's pixel coordinates (top-left, top-right,
//     bottom-right, bottom-left). DecodePerspective computes the homography that
//     maps those corners to an axis-aligned rectangle, warps the photo to produce
//     a flat crop, and passes the crop through DecodeReference to recover the text.
//
//  2. Forward-model distance: a [rectify.Projector] scores the proposed content
//     back in the photo's native pixel space — it projects each axis-aligned
//     candidate through H and compares against the photo's pixels. This avoids
//     the interpolation loss of rectify-then-decode and gives a perspective-correct
//     quality signal. Result.Distance reports this score (lower is better; near
//     zero means the geometry and proposed text are self-consistent).
//
// The quad corners must be supplied by the caller (manual annotation for now).

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/rectify"
)

// PerspectiveResult holds the output of a DecodePerspective call.
type PerspectiveResult struct {
	// Text is the most plausible decoded string recovered from the rectified crop.
	Text string
	// RectW is the inferred width of the axis-aligned redaction rectangle in pixels
	// (average of the top and bottom edge lengths, clamped to ≥1).
	RectW int
	// RectH is the inferred height of the axis-aligned redaction rectangle in pixels
	// (average of the left and right edge lengths, clamped to ≥1).
	RectH int
	// Distance is the forward-model mean per-channel RGB difference, normalised to
	// [0,1], between the photo and the proposed text projected back through H.
	// Lower is better; near zero means the geometry and text are self-consistent.
	Distance float64
}

// PerspectiveOption configures DecodePerspective.
type PerspectiveOption func(*perspectiveConfig)

type perspectiveConfig struct {
	quad      [4]rectify.Point
	quadSet   bool
	charset   string
	font      string
	fontTTF   []byte
	linear    int // -1 = auto/sweep, 0 = sRGB only, 1 = linear only
	blockSize int // 0 → auto-detect; >1 → pin the block size for DecodeReference
}

func defaultPerspectiveConfig() perspectiveConfig {
	return perspectiveConfig{linear: -1}
}

// WithPerspectiveQuad sets the four corners of the redaction quadrilateral in
// photo pixel coordinates: top-left, top-right, bottom-right, bottom-left. This
// option is required; DecodePerspective returns an error when it is not set.
func WithPerspectiveQuad(corners [4]rectify.Point) PerspectiveOption {
	return func(c *perspectiveConfig) {
		c.quad = corners
		c.quadSet = true
	}
}

// WithPerspectiveCharset sets the candidate alphabet for the underlying
// DecodeReference call. Defaults to DefaultRefCharset when empty.
func WithPerspectiveCharset(cs string) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if cs != "" {
			c.charset = cs
		}
	}
}

// WithPerspectiveFont pins the decoder to a specific bundled font by name (e.g.
// "Liberation Sans"). When set, only that font is tried, skipping the full
// bundled sweep. Ignored when WithPerspectiveFontFile is also set.
func WithPerspectiveFont(name string) PerspectiveOption {
	return func(c *perspectiveConfig) { c.font = name }
}

// WithPerspectiveFontFile supplies raw TrueType/OpenType bytes for the font
// face. When set, DecodeReference renders all candidates with this font
// exclusively — the bundled sweep is skipped entirely.
func WithPerspectiveFontFile(regularTTF []byte) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if len(regularTTF) > 0 {
			c.fontTTF = regularTTF
		}
	}
}

// WithPerspectiveBlockSize pins the mosaic block size rather than relying on
// auto-detection from the rectified crop. This is useful when the block size is
// known from metadata, the manifest, or the original capture tool's settings.
// Values ≤ 1 are ignored.
func WithPerspectiveBlockSize(size int) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if size > 1 {
			c.blockSize = size
		}
	}
}

// WithPerspectiveLinear controls whether linear-light (GIMP/GEGL) or sRGB
// block averaging is used. Tri-state: -1 = auto/sweep both (default), 0 = sRGB
// only, 1 = linear only.
func WithPerspectiveLinear(on bool) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if on {
			c.linear = 1
		} else {
			c.linear = 0
		}
	}
}

// edgeLen returns the Euclidean distance between two points.
func edgeLen(a, b rectify.Point) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	return math.Sqrt(dx*dx + dy*dy)
}

// DecodePerspective recovers text from a mosaic-pixelated redaction that was
// photographed at an angle. The caller must supply the four corners of the
// redaction quadrilateral in photo pixel coordinates via WithPerspectiveQuad
// (top-left, top-right, bottom-right, bottom-left).
//
// It returns an error when:
//   - WithPerspectiveQuad is not set,
//   - the quad is geometrically degenerate (three collinear corners),
//   - or the underlying DecodeReference fails (e.g. no mosaic grid detected).
func DecodePerspective(ctx context.Context, photo image.Image, opts ...PerspectiveOption) (PerspectiveResult, error) {
	cfg := defaultPerspectiveConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if !cfg.quadSet {
		return PerspectiveResult{}, errors.New("mosaictext: DecodePerspective requires WithPerspectiveQuad")
	}

	rgba := imutil.ToRGBA(photo)
	quad := cfg.quad

	// Derive the axis-aligned rectangle dimensions from the quad's edge lengths.
	// rectW = average of top and bottom edges; rectH = average of left and right.
	topLen := edgeLen(quad[0], quad[1])
	botLen := edgeLen(quad[3], quad[2])
	leftLen := edgeLen(quad[0], quad[3])
	rightLen := edgeLen(quad[1], quad[2])

	rectW := max(1, int(math.Round((topLen+botLen)/2)))
	rectH := max(1, int(math.Round((leftLen+rightLen)/2)))

	// Build the homography that maps the axis-aligned rect to the photo quad,
	// then warp the photo to produce the flat rectified crop.
	rectToPhoto, err := rectify.RectToQuad(float64(rectW), float64(rectH), quad)
	if err != nil {
		return PerspectiveResult{}, fmt.Errorf("mosaictext: DecodePerspective: compute homography: %w", err)
	}
	rectCrop := rectify.Warp(rgba, rectToPhoto, rectW, rectH)

	// Decode the rectified crop via reference matching.
	refOpts := []RefOption{}
	if cfg.charset != "" {
		refOpts = append(refOpts, WithRefCharset(cfg.charset))
	}
	switch {
	case len(cfg.fontTTF) > 0:
		refOpts = append(refOpts, WithRefFontFile(cfg.fontTTF))
	case cfg.font != "":
		refOpts = append(refOpts, WithRefFont(cfg.font))
	}
	refOpts = append(refOpts, WithRefLinear(cfg.linear))
	if cfg.blockSize > 1 {
		refOpts = append(refOpts, WithRefBlockSize(cfg.blockSize))
	}

	ref, err := DecodeReference(ctx, rectCrop, refOpts...)
	if err != nil {
		// Rectifying resamples away the crisp block edges, so auto grid detection
		// often fails on the warped crop; the caller usually knows the block size.
		if errors.Is(err, ErrNoMosaic) && cfg.blockSize <= 1 {
			return PerspectiveResult{}, fmt.Errorf("mosaictext: DecodePerspective: %w (pin it with WithPerspectiveBlockSize / --block-size — grid detection is unreliable on a rectified crop)", err)
		}
		return PerspectiveResult{}, fmt.Errorf("mosaictext: DecodePerspective: decode: %w", err)
	}

	// Forward-model verification: build a projector and score the rectCrop
	// against the native photo pixels to measure geometric self-consistency.
	proj, err := rectify.NewProjector(rgba, quad, rectW, rectH)
	if err != nil {
		return PerspectiveResult{}, fmt.Errorf("mosaictext: DecodePerspective: build projector: %w", err)
	}
	dist := proj.Distance(rectCrop)

	return PerspectiveResult{
		Text:     ref.Text,
		RectW:    rectW,
		RectH:    rectH,
		Distance: dist,
	}, nil
}
