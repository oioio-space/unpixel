package mosaictext

// perspective.go — DecodePerspective: pure forward-model beam search.
//
// DecodePerspective recovers text from a mosaic-pixelated redaction that was
// photographed at an angle. The algorithm is a pure forward-model beam search:
// rather than resampling (rectifying) the photo, each candidate string is
// rendered and re-pixelated in axis-aligned space, then scored directly against
// the native photo pixels via a planar homography projection. No interpolation
// loss from rectify-resampling: the homography enters the forward model as
// render → pixelate → project → compare.
//
// For each length 1..maxLen the beam extends every surviving prefix by every
// charset rune, renders and pixelates the candidate via the fixture pipeline,
// and scores it with rectify.Projector.Distance. The beamWidth lowest-distance
// prefixes survive to the next length. The globally lowest-distance string
// across ALL lengths is the result: a shorter or longer candidate leaves white
// space / overflows the quad, producing a higher distance, so the correct
// length wins naturally.
//
// The quad corners must be supplied by the caller (manual annotation for now).

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/rectify"
)

// PerspectiveResult holds the output of a DecodePerspective call.
type PerspectiveResult struct {
	// Text is the most plausible decoded string recovered by the beam search.
	Text string
	// RectW is the inferred width of the axis-aligned redaction rectangle in
	// pixels (average of the top and bottom edge lengths, clamped to ≥1).
	RectW int
	// RectH is the inferred height of the axis-aligned redaction rectangle in
	// pixels (average of the left and right edge lengths, clamped to ≥1).
	RectH int
	// Distance is the forward-model mean per-channel RGB difference, normalised
	// to [0,1], between the photo and the best candidate projected through H.
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
	fontSize  float64
	blockSize int
	beamWidth int
	maxLen    int
	rectW     int // 0 → derive from quad edge lengths
	rectH     int // 0 → derive from quad edge lengths
}

func defaultPerspectiveConfig() perspectiveConfig {
	return perspectiveConfig{
		charset:   "abcdefghijklmnopqrstuvwxyz ",
		fontSize:  32,
		blockSize: 8,
		beamWidth: 36,
		maxLen:    12,
	}
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

// WithPerspectiveCharset sets the candidate alphabet for the beam search.
// Defaults to lowercase ASCII plus space when empty.
func WithPerspectiveCharset(cs string) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if cs != "" {
			c.charset = cs
		}
	}
}

// WithPerspectiveFont pins the decoder to a specific bundled font by name
// (e.g. "Liberation Sans"). Ignored when WithPerspectiveFontFile is also set.
func WithPerspectiveFont(name string) PerspectiveOption {
	return func(c *perspectiveConfig) { c.font = name }
}

// WithPerspectiveFontFile supplies raw TrueType/OpenType bytes for the font
// face. When set, candidates are rendered with this font exclusively.
func WithPerspectiveFontFile(regularTTF []byte) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if len(regularTTF) > 0 {
			c.fontTTF = regularTTF
		}
	}
}

// WithPerspectiveFontSize sets the font size in points used to render
// candidates. Defaults to 32. Values ≤ 0 are ignored.
func WithPerspectiveFontSize(px float64) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if px > 0 {
			c.fontSize = px
		}
	}
}

// WithPerspectiveBlockSize pins the mosaic block size for candidate rendering.
// Defaults to 8. Values ≤ 1 are ignored.
func WithPerspectiveBlockSize(size int) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if size > 1 {
			c.blockSize = size
		}
	}
}

// WithPerspectiveBeamWidth sets the number of lowest-distance prefixes kept at
// each beam level. Defaults to 8. Values ≤ 0 are ignored.
func WithPerspectiveBeamWidth(w int) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if w > 0 {
			c.beamWidth = w
		}
	}
}

// WithPerspectiveMaxLen sets the maximum candidate string length the beam
// searches up to. Defaults to 12. Values ≤ 0 are ignored.
func WithPerspectiveMaxLen(n int) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if n > 0 {
			c.maxLen = n
		}
	}
}

// WithPerspectiveRectSize pins the axis-aligned rectangle dimensions used to
// build the forward-model projector. When set, DecodePerspective uses these
// values instead of estimating them from the quad's edge lengths.
//
// Use this when the true rendered size is known (e.g. from a fixture manifest)
// to avoid the estimation error introduced by perspective foreshortening.
// Values ≤ 0 are ignored.
func WithPerspectiveRectSize(w, h int) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if w > 0 {
			c.rectW = w
		}
		if h > 0 {
			c.rectH = h
		}
	}
}

// WithPerspectiveLinear is retained for API compatibility. It has no effect in
// the pure forward-model beam search path and is accepted silently.
func WithPerspectiveLinear(on bool) PerspectiveOption {
	return func(*perspectiveConfig) {}
}

// edgeLen returns the Euclidean distance between two points.
func edgeLen(a, b rectify.Point) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	return math.Sqrt(dx*dx + dy*dy)
}

// resolveFont returns the TTF bytes to use for candidate rendering. It
// preferentially uses fontTTF when set, then looks up font by name in the
// bundled catalog, then falls back to nil (fixture.Redact uses embedded default).
func resolveFont(cfg *perspectiveConfig) ([]byte, error) {
	if len(cfg.fontTTF) > 0 {
		return cfg.fontTTF, nil
	}
	if cfg.font != "" {
		for _, f := range fonts.All() {
			if f.Name == cfg.font {
				return f.Data, nil
			}
		}
		return nil, fmt.Errorf("mosaictext: bundled font %q not found", cfg.font)
	}
	return nil, nil // use embedded default (Liberation Sans)
}

// renderCandidate renders the prefix text through the fixture pipeline and
// returns the re-pixelated image. When ttf is non-nil it uses RedactFont;
// otherwise it uses Redact (embedded Liberation Sans).
func renderCandidate(text string, spec fixture.Spec, ttf []byte) (*image.RGBA, error) {
	spec.Text = text
	if ttf != nil {
		return fixture.RedactFont(spec, ttf, nil)
	}
	return fixture.Redact(spec)
}

// beamHyp is one live hypothesis in the perspective beam search.
type beamHyp struct {
	prefix string
	dist   float64
}

// DecodePerspective recovers text from a mosaic-pixelated redaction that was
// photographed at an angle. The caller must supply the four corners of the
// redaction quadrilateral in photo pixel coordinates via WithPerspectiveQuad
// (top-left, top-right, bottom-right, bottom-left).
//
// The algorithm is a pure forward-model beam search: for each length 1..maxLen
// it extends every surviving prefix by every charset rune, renders and
// pixelates the full prefix via the fixture pipeline (render → crop → pad →
// pixelate), and scores it with rectify.Projector.Distance. The beamWidth
// lowest-distance prefixes survive to the next level. The string with the
// globally lowest distance across all lengths is returned.
//
// It returns an error when:
//   - WithPerspectiveQuad is not set,
//   - the quad is geometrically degenerate (three collinear corners),
//   - the named font is not in the bundled catalog, or
//   - ctx is cancelled before any level completes.
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

	// Derive the axis-aligned rectangle dimensions. When WithPerspectiveRectSize
	// provides them directly (e.g. from a manifest that records the true rendered
	// size), use those values; otherwise estimate from the quad's edge lengths.
	rectW := cfg.rectW
	rectH := cfg.rectH
	if rectW <= 0 || rectH <= 0 {
		topLen := edgeLen(quad[0], quad[1])
		botLen := edgeLen(quad[3], quad[2])
		leftLen := edgeLen(quad[0], quad[3])
		rightLen := edgeLen(quad[1], quad[2])
		if rectW <= 0 {
			rectW = max(1, int(math.Round((topLen+botLen)/2)))
		}
		if rectH <= 0 {
			rectH = max(1, int(math.Round((leftLen+rightLen)/2)))
		}
	}

	proj, err := rectify.NewProjector(rgba, quad, rectW, rectH)
	if err != nil {
		return PerspectiveResult{}, fmt.Errorf("mosaictext: DecodePerspective: build projector: %w", err)
	}

	ttf, err := resolveFont(&cfg)
	if err != nil {
		return PerspectiveResult{}, err
	}

	spec := fixture.Spec{
		Charset:     cfg.charset,
		FontSize:    cfg.fontSize,
		BlockSize:   cfg.blockSize,
		PaddingTop:  8,
		PaddingLeft: 8,
	}

	charRunes := []rune(cfg.charset)
	beam := []beamHyp{{prefix: "", dist: math.Inf(1)}}
	bestText := ""
	bestDist := math.Inf(1)
	rW := float64(rectW)

	for range cfg.maxLen {
		if ctx.Err() != nil {
			break
		}

		// widthGroups maps rendered-pixel-width → candidates in that width class.
		// Stratifying by rendered width prevents wide wrong candidates (e.g.
		// "hebo" at rectW pixels) from crowding out correct narrow prefixes
		// (e.g. "hell" at narrower width) that extend to the exact-width winner.
		// Each width class is pruned independently to beamWidth, then merged;
		// a seen map prevents re-scoring the same string from different paths.
		widthGroups := make(map[int][]beamHyp)
		seen := make(map[string]bool)

		for _, h := range beam {
			for _, ch := range charRunes {
				if h.prefix == "" && ch == ' ' {
					continue
				}
				if ctx.Err() != nil {
					break
				}

				candidate := h.prefix + string(ch)
				if seen[candidate] {
					continue
				}
				seen[candidate] = true

				cand, err := renderCandidate(candidate, spec, ttf)
				if err != nil {
					continue
				}
				candW := cand.Bounds().Dx()

				// Beam score: PartialDistance over complete block columns only,
				// so a narrow trailing glyph doesn't inflate the intra-width score.
				coveredPx := float64(candW / cfg.blockSize * cfg.blockSize)
				xFrac := coveredPx / rW
				beamDist := proj.PartialDistance(cand, xFrac)
				widthGroups[candW] = append(widthGroups[candW], beamHyp{prefix: candidate, dist: beamDist})

				// Global best uses full-quad Distance: the true string at the right
				// length fills rectW exactly (dist ≈ 0); any other length scores
				// higher due to white padding or overflow.
				if fullDist := proj.Distance(cand); fullDist < bestDist {
					bestDist = fullDist
					bestText = candidate
				}
			}
		}

		if len(widthGroups) == 0 {
			break
		}

		// Keep the top-beamWidth survivors per width class, then merge all
		// classes. Apply a global cap of beamWidth×6 on the merged beam so
		// that many width groups do not cause exponential candidate growth.
		var next []beamHyp
		for _, group := range widthGroups {
			if len(group) > cfg.beamWidth {
				partialSortBeam(group, cfg.beamWidth)
				group = group[:cfg.beamWidth]
			}
			next = append(next, group...)
		}
		globalCap := cfg.beamWidth * 6
		if len(next) > globalCap {
			partialSortBeam(next, globalCap)
			next = next[:globalCap]
		}
		beam = next
	}

	if bestText == "" {
		if ctx.Err() != nil {
			return PerspectiveResult{}, ctx.Err()
		}
		return PerspectiveResult{}, errors.New("mosaictext: DecodePerspective: no candidate produced a result")
	}

	return PerspectiveResult{
		Text:     bestText,
		RectW:    rectW,
		RectH:    rectH,
		Distance: bestDist,
	}, nil
}

// partialSortBeam rearranges hyps so the k smallest (by distance) are in
// hyps[:k] in any order. It uses a simple selection-sort for the small k values
// (beamWidth ≤ 32 in practice) encountered here — this is O(n·k) with a very
// small constant and avoids allocating a heap.
func partialSortBeam(hyps []beamHyp, k int) {
	n := len(hyps)
	if k >= n {
		return
	}
	for i := range k {
		minIdx := i
		for j := i + 1; j < n; j++ {
			if hyps[j].dist < hyps[minIdx].dist {
				minIdx = j
			}
		}
		hyps[i], hyps[minIdx] = hyps[minIdx], hyps[i]
	}
}
