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
	"runtime"
	"sync"

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
	workers   int // candidate-evaluation concurrency; ≤0 → GOMAXPROCS
	autoQuad  bool
	detectTol int // background-difference threshold for DetectQuad
}

func defaultPerspectiveConfig() perspectiveConfig {
	return perspectiveConfig{
		charset:   "abcdefghijklmnopqrstuvwxyz ",
		fontSize:  32,
		blockSize: 8,
		beamWidth: 36,
		maxLen:    12,
		workers:   runtime.GOMAXPROCS(0),
		detectTol: 40,
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

// WithPerspectiveAutoQuad enables automatic detection of the redaction quad via
// rectify.DetectQuad when WithPerspectiveQuad is not supplied: the four extreme
// points of the region that differs from the background by more than tol (sum of
// abs R/G/B diffs) are taken as the corners. Works when the redaction is one
// convex region on a roughly-uniform background; pass tol ≤ 0 for the default
// (40). An explicit WithPerspectiveQuad always takes precedence.
func WithPerspectiveAutoQuad(tol int) PerspectiveOption {
	return func(c *perspectiveConfig) {
		c.autoQuad = true
		if tol > 0 {
			c.detectTol = tol
		}
	}
}

// WithPerspectiveWorkers sets how many goroutines evaluate candidates (render →
// re-pixelate → score) concurrently per beam level. Defaults to GOMAXPROCS.
// Values ≤ 0 are ignored. The decoded result is independent of this setting.
func WithPerspectiveWorkers(n int) PerspectiveOption {
	return func(c *perspectiveConfig) {
		if n > 0 {
			c.workers = n
		}
	}
}

// WithPerspectiveLinear is retained for API compatibility. It has no effect in
// the pure forward-model beam search path and is accepted silently.
func WithPerspectiveLinear(on bool) PerspectiveOption {
	return func(*perspectiveConfig) {}
}

// parallelEval runs fn(0..n-1) across up to `workers` goroutines (clamped to
// [1,n]), split into contiguous chunks. fn(i) must only write index i of any
// shared slice, so results are race-free and independent of scheduling — the
// decode is byte-identical to a serial run. Cancelled ctx skips remaining work.
// No goroutine outlives the call (goleak-safe: every worker exits its chunk).
func parallelEval(ctx context.Context, n, workers int, fn func(i int)) {
	if n <= 0 {
		return
	}
	workers = min(max(workers, 1), n)
	if workers == 1 {
		for i := range n {
			if ctx.Err() != nil {
				return
			}
			fn(i)
		}
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for w := range workers {
		lo := w * chunk
		if lo >= n {
			break
		}
		hi := min(lo+chunk, n)
		wg.Go(func() {
			for i := lo; i < hi; i++ {
				if ctx.Err() != nil {
					return
				}
				fn(i)
			}
		})
	}
	wg.Wait()
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

// renderCandidate renders the prefix text through the reused redactor and
// returns the re-pixelated image. The shared redactor parses the font once
// (rather than per candidate) and is safe for concurrent use.
func renderCandidate(text string, spec fixture.Spec, rd *fixture.Redactor) (*image.RGBA, error) {
	spec.Text = text
	return rd.Redact(spec)
}

// beamHyp is one live hypothesis in the perspective beam search.
type beamHyp struct {
	prefix string
	dist   float64
	// img is the rendered+pixelated candidate, retained only within a beam level
	// so the survivor full-Distance pass can reuse it without re-rendering; it is
	// cleared before the hypothesis carries to the next level.
	img *image.RGBA
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
	if !cfg.quadSet && !cfg.autoQuad {
		return PerspectiveResult{}, errors.New("mosaictext: DecodePerspective requires WithPerspectiveQuad (or WithPerspectiveAutoQuad)")
	}

	rgba := imutil.ToRGBA(photo)
	quad := cfg.quad
	if !cfg.quadSet {
		detected, err := rectify.DetectQuad(rgba, cfg.detectTol)
		if err != nil {
			return PerspectiveResult{}, fmt.Errorf("mosaictext: DecodePerspective: auto-detect quad: %w", err)
		}
		quad = detected
	}

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

	// One reused renderer for the whole search: the font is parsed once instead
	// of per candidate (the previous fixture.Redact path re-parsed it every call,
	// ~21% of allocations). Safe to share across the parallel workers.
	var redactor *fixture.Redactor
	if ttf != nil {
		redactor, err = fixture.NewRedactorFont(ttf, nil)
	} else {
		redactor, err = fixture.NewRedactor()
	}
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
		// Enumerate this level's unique candidate strings (serial, deterministic),
		// then evaluate them — render → re-pixelate → PartialDistance — in
		// parallel. Each evaluation is independent and read-only on the projector,
		// and renderCandidate builds its own renderer, so there is no shared state;
		// results are written by index, keeping the grouping below independent of
		// scheduling (byte-identical decode).
		seen := make(map[string]bool)
		var candidates []string
		for _, h := range beam {
			for _, ch := range charRunes {
				if h.prefix == "" && ch == ' ' {
					continue
				}
				cand := h.prefix + string(ch)
				if !seen[cand] {
					seen[cand] = true
					candidates = append(candidates, cand)
				}
			}
		}

		evals := make([]beamHyp, len(candidates))
		widths := make([]int, len(candidates))
		parallelEval(ctx, len(candidates), cfg.workers, func(i int) {
			cand, err := renderCandidate(candidates[i], spec, redactor)
			if err != nil {
				return // evals[i] stays zero-valued (nil img) and is skipped below
			}
			candW := cand.Bounds().Dx()
			// Beam score: PartialDistance over complete block columns only, so a
			// narrow trailing glyph doesn't inflate the intra-width score.
			coveredPx := float64(candW / cfg.blockSize * cfg.blockSize)
			xFrac := coveredPx / rW
			evals[i] = beamHyp{prefix: candidates[i], dist: proj.PartialDistance(cand, xFrac), img: cand}
			widths[i] = candW
		})

		widthGroups := make(map[int][]beamHyp)
		for i, h := range evals {
			if h.img == nil {
				continue // render failed for this candidate
			}
			widthGroups[widths[i]] = append(widthGroups[widths[i]], h)
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

		// Global best uses the full-quad Distance, computed ONLY for the pruned
		// survivors rather than every extension (the dominant cost — see the
		// BenchmarkDecodePerspective profile, where Distance is ~66%). The true
		// string at the right length fills rectW exactly (dist ≈ 0) and always
		// survives its width class — its left blocks match, so its PartialDistance
		// is low — so restricting the full scan to survivors preserves the result.
		for i := range next {
			if fullDist := proj.Distance(next[i].img); fullDist < bestDist {
				bestDist = fullDist
				bestText = next[i].prefix
			}
			next[i].img = nil // release the rendered image; not needed past this level
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
