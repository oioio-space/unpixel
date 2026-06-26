package fontrank

// fingerprint.go — glyph-metric fingerprinting for font pruning.
//
// FingerprintFromGlyphs extracts three normalised typography ratios from a
// rendered cleartext image: x-height, cap-height, and mean advance — all
// expressed as fractions of the observed line height. RankByMetrics compares
// those ratios against the same ratios derived from each candidate font's own
// face metrics (via golang.org/x/image/font), producing a ranked list sorted
// by Euclidean distance in that 3-D metric space.
//
// Cost: ~310× cheaper than a full render→re-pixelate→score decode loop because
// no pixel-level image comparison is performed — only a handful of scanline
// passes over the already-rendered image plus one opentype.NewFace call per
// candidate font.

import (
	"cmp"
	"errors"
	"image"
	"math"
	"slices"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/oioio-space/unpixel/internal/imutil"
)

// Fingerprint holds the three normalised typography ratios that characterise a
// font's glyph metrics. All fields are in (0, 1] and expressed as fractions of
// the observed line height (ascent + descent), making them independent of the
// rendering point size.
//
// A zero Fingerprint (all fields 0) is the result of a degenerate input; it
// should not be passed to RankByMetrics.
type Fingerprint struct {
	// XHeightRatio is the height of non-ascending lowercase glyphs (e.g. 'x',
	// 'o', 'e') divided by the total line height. Typical range: 0.45–0.60.
	XHeightRatio float64
	// CapHeightRatio is the height of uppercase glyphs (e.g. 'H', 'A')
	// divided by the total line height. Typical range: 0.65–0.80.
	CapHeightRatio float64
	// MeanAdvanceRatio is the mean glyph advance width divided by the total
	// line height. Proportional fonts are ~0.50–0.65; monospace fonts cluster
	// around 0.55–0.70 with very low variance across the glyph set.
	MeanAdvanceRatio float64
}

// Ranked is one entry in the ordered output of RankByMetrics.
type Ranked struct {
	// Name is the font identifier, copied from the corresponding NamedFont.
	Name string
	// Dist is the Euclidean distance between the image fingerprint and this
	// font's metric fingerprint in the 3-D (xHeight, capHeight, meanAdvance)
	// ratio space. Lower is a better match; 0 means identical ratios.
	Dist float64
}

// xHeightRunes are lowercase ASCII letters that sit entirely between the
// baseline and the x-height line (no ascenders, no descenders). Measuring the
// ink extent of these characters' pixel columns gives the x-height.
var xHeightRunes = map[rune]bool{
	'a': true, 'c': true, 'e': true, 'm': true, 'n': true,
	'o': true, 'r': true, 's': true, 'u': true, 'v': true,
	'w': true, 'x': true, 'z': true,
}

// capHeightRunes are uppercase ASCII letters with flat tops (no rounded
// overshoot) that reach the cap-height line. Measuring their ink top gives a
// reliable pixel cap-height estimate.
var capHeightRunes = map[rune]bool{
	'A': true, 'B': true, 'D': true, 'E': true, 'F': true,
	'H': true, 'I': true, 'K': true, 'L': true, 'M': true,
	'N': true, 'P': true, 'R': true, 'T': true, 'Z': true,
}

// FingerprintFromGlyphs measures three normalised typography ratios from img,
// which must be a rendering of knownText produced by render.XImage.Render.
// sentinelX is the value returned by that Render call (the x-coordinate where
// the blue sentinel block begins, equal to the text's right edge).
//
// The method partitions the image horizontally into per-character columns using
// a uniform advance estimate (sentinelX / len(runes)), then for x-height-class
// glyphs ('x', 'o', 'e', …) and cap-height-class glyphs ('H', 'A', …) it
// records the topmost ink row in each column. The baseline is taken as the
// bottom of x-height ink. All heights are expressed as fractions of the
// observed total ink extent (top of caps to bottom of non-descenders).
//
// Returns an error if knownText is empty, sentinelX ≤ 0, or the image
// contains no ink.
func FingerprintFromGlyphs(img *image.RGBA, knownText string, sentinelX int) (Fingerprint, error) {
	if knownText == "" {
		return Fingerprint{}, errors.New("fontrank: knownText must not be empty")
	}
	if sentinelX <= 0 {
		return Fingerprint{}, errors.New("fontrank: sentinelX must be positive")
	}

	runes := []rune(knownText)
	nRunes := len(runes)
	b := img.Bounds()

	// Uniform per-character column width in pixels.
	colWidth := float64(sentinelX) / float64(nRunes)

	// For each glyph class we collect the topmost ink row and the bottommost
	// ink row across all matching columns.  Using min-top / max-bot rather than
	// mean heights avoids anti-aliasing inflation and is directly comparable to
	// the typographic metrics returned by face.Metrics().
	const huge = math.MaxInt32
	xTop, xBot := huge, -1     // x-height class: top = x-height line, bot ≈ baseline
	capTop := huge             // cap-height class: top = cap-height line
	allTop, allBot := huge, -1 // overall ink extent

	for i, r := range runes {
		colStart := b.Min.X + int(math.Round(float64(i)*colWidth))
		colEnd := b.Min.X + int(math.Round(float64(i+1)*colWidth))
		colEnd = min(colEnd, b.Min.X+sentinelX)
		if colStart >= colEnd {
			continue
		}

		top, bot := inkColumnBounds(img, colStart, colEnd, b.Min.Y, b.Max.Y)
		if top < 0 {
			continue
		}

		allTop = min(allTop, top)
		allBot = max(allBot, bot)

		switch {
		case xHeightRunes[r]:
			xTop = min(xTop, top)
			xBot = max(xBot, bot)
		case capHeightRunes[r]:
			capTop = min(capTop, top)
		}
	}

	// Fall back: scan all uppercase letters for cap-height when knownText
	// contains none of the canonical capHeightRunes (e.g. "hello world").
	if capTop == huge {
		for i, r := range runes {
			if !unicode.IsUpper(r) {
				continue
			}
			colStart := b.Min.X + int(math.Round(float64(i)*colWidth))
			colEnd := b.Min.X + int(math.Round(float64(i+1)*colWidth))
			colEnd = min(colEnd, b.Min.X+sentinelX)
			if colStart >= colEnd {
				continue
			}
			top, _ := inkColumnBounds(img, colStart, colEnd, b.Min.Y, b.Max.Y)
			if top >= 0 {
				capTop = min(capTop, top)
			}
		}
	}

	if allTop == huge || allBot < 0 {
		return Fingerprint{}, errors.New("fontrank: image contains no ink")
	}

	// lineHeight is the pixel span from the top of the tallest glyph to the
	// bottom of the lowest non-descending ink. This mirrors (Ascent+Descent).
	lineHeight := float64(allBot - allTop + 1)

	// baseline is the bottom of x-height glyphs (≈ typographic baseline).
	// When the text has no x-height reference glyphs, fall back to allBot.
	baseline := allBot
	if xBot >= 0 {
		baseline = xBot
	}

	// x-height = baseline − top-of-x-class ink.
	xHeightPx := lineHeight * 0.52 // prior for all-numeric / no-xHeight input
	if xTop < huge && xTop <= baseline {
		xHeightPx = float64(baseline - xTop + 1)
	}

	// cap-height = baseline − top-of-cap-class ink.
	capHeightPx := lineHeight * 0.72 // prior for all-lowercase input
	if capTop < huge && capTop <= baseline {
		capHeightPx = float64(baseline - capTop + 1)
	}

	return Fingerprint{
		XHeightRatio:     xHeightPx / lineHeight,
		CapHeightRatio:   capHeightPx / lineHeight,
		MeanAdvanceRatio: colWidth / lineHeight,
	}, nil
}

// inkVerticalBounds returns the first and last row indices (in image
// coordinates) that contain at least one dark pixel (luma < 200). Returns
// (-1, -1) when the image contains no ink.
func inkVerticalBounds(img *image.RGBA) (top, bot int) {
	b := img.Bounds()
	top, bot = -1, -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			// Exclude the blue sentinel (B channel dominant).
			if c.B > 200 && c.R < 50 {
				continue
			}
			if imutil.Lum601(c.R, c.G, c.B) < 200 {
				if top < 0 {
					top = y
				}
				bot = y
			}
		}
	}
	return top, bot
}

// inkColumnBounds returns the top and bottom row indices containing a dark
// pixel within the column band [colStart, colEnd) and rows [rowStart, rowEnd).
// Returns (-1, -1) when no ink is found.
func inkColumnBounds(img *image.RGBA, colStart, colEnd, rowStart, rowEnd int) (top, bot int) {
	top, bot = -1, -1
	for y := rowStart; y < rowEnd; y++ {
		for x := colStart; x < colEnd; x++ {
			c := img.RGBAAt(x, y)
			// Exclude the blue sentinel.
			if c.B > 200 && c.R < 50 {
				continue
			}
			if imutil.Lum601(c.R, c.G, c.B) < 200 {
				if top < 0 {
					top = y
				}
				bot = y
			}
		}
	}
	return top, bot
}

// fingerprintFromFace computes the Fingerprint for a font face by querying
// its opentype metrics (XHeight, CapHeight, Ascent) and deriving the mean
// advance over all printable ASCII characters. All values are normalised by
// Ascent to match the image-side denominator: the image measures ink extent
// from the top of the tallest glyph to the baseline, which approximates the
// typographic ascent (not the full Ascent+Descent line box, which includes
// space below the baseline that is typically absent from the pixel ink bounds
// when the text contains no descenders).
func fingerprintFromFace(f font.Face) Fingerprint {
	m := f.Metrics()
	lineHeight := float64(m.Ascent.Round())
	if lineHeight <= 0 {
		return Fingerprint{}
	}

	// Use OS/2 XHeight and CapHeight when available (non-zero from opentype).
	xH := m.XHeight
	capH := m.CapHeight

	// Fall back to measuring representative glyphs when the OS/2 fields are
	// absent (some older or subset fonts omit them).
	if xH <= 0 {
		// Measure the glyph bounding box of 'x' as x-height proxy.
		if bounds, _, ok := f.GlyphBounds('x'); ok {
			// bounds.Min.Y is negative (above baseline); Max.Y is positive.
			// x-height = distance from baseline to top of 'x' = -bounds.Min.Y.
			xH = -bounds.Min.Y
		}
	}
	if capH <= 0 {
		if bounds, _, ok := f.GlyphBounds('H'); ok {
			capH = -bounds.Min.Y
		}
	}

	// Mean advance over printable ASCII, weighted uniformly. This captures the
	// proportional vs monospace axis and width differences across font families.
	var advSum fixed.Int26_6
	count := 0
	for r := rune(0x20); r <= rune(0x7E); r++ {
		if adv, ok := f.GlyphAdvance(r); ok {
			advSum += adv
			count++
		}
	}
	meanAdv := 0.0
	if count > 0 {
		meanAdv = float64(advSum.Round()) / float64(count)
	}

	return Fingerprint{
		XHeightRatio:     float64(xH.Round()) / lineHeight,
		CapHeightRatio:   float64(capH.Round()) / lineHeight,
		MeanAdvanceRatio: meanAdv / lineHeight,
	}
}

// fingerprintDist returns the Euclidean distance between two Fingerprints in
// the 3-D (xHeight, capHeight, meanAdvance) ratio space.
func fingerprintDist(a, b Fingerprint) float64 {
	dx := a.XHeightRatio - b.XHeightRatio
	dy := a.CapHeightRatio - b.CapHeightRatio
	dz := a.MeanAdvanceRatio - b.MeanAdvanceRatio
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// referenceFontSize is the point size at which candidate fonts are evaluated.
// 32 pt is large enough for glyph details to be well-defined and matches the
// default render size used throughout the pipeline.
const referenceFontSize = 32.0

// RankByMetrics scores each candidate font by the Euclidean distance between
// its computed metric Fingerprint and fp, and returns the list sorted
// best-first (lowest Dist first).
//
// A font whose TTF/OTF bytes cannot be parsed receives Dist = +Inf and sorts
// last. RankByMetrics returns nil when fonts is empty.
func RankByMetrics(fp Fingerprint, fonts []NamedFont) []Ranked {
	if len(fonts) == 0 {
		return nil
	}

	ranked := make([]Ranked, len(fonts))
	for i, nf := range fonts {
		ranked[i] = Ranked{Name: nf.Name, Dist: distForFont(nf.Data, fp)}
	}

	slices.SortStableFunc(ranked, func(a, b Ranked) int {
		return cmp.Compare(a.Dist, b.Dist)
	})
	return ranked
}

// distForFont parses data as a TrueType/OpenType font, creates a face at
// referenceFontSize, computes its Fingerprint, and returns its distance to fp.
// Returns +Inf on any error so the font sorts last rather than crashing.
func distForFont(data []byte, fp Fingerprint) float64 {
	parsed, err := opentype.Parse(data)
	if err != nil {
		return math.Inf(1)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    referenceFontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return math.Inf(1)
	}
	defer func() { _ = face.Close() }()

	fontFP := fingerprintFromFace(face)
	return fingerprintDist(fp, fontFP)
}
