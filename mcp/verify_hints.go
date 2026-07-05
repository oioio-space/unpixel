package mcpserver

// verify_hints.go — VerifyWithHints, the calibration-aware core of
// unpixel_verify_candidates. It closes the LLM propose→physics-verify loop on
// real, non-pipeline redactions: whole-string verification only discriminates the
// truth on a real image when the candidate is rendered in the right font at the
// right geometry and colourspace, and compared against a tight crop of the
// redaction band (measured — see docs/VERIFY-SPIKE.md). Every hint here is one an
// LLM client already discovers via the other tools: crop and block from
// unpixel_analyze, font from unpixel_rank_fonts, font size / x-scale from
// unpixel_calibrate, and the linear-light colourspace from the analyze fingerprint.

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"slices"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/lang"
	"github.com/oioio-space/unpixel/rerank"
)

// verifyCropMargin is the white border (right, bottom) added around a crop before
// verification. It gives the aligner room to slide the rendered candidate over the
// redaction (verifyCore's alignment sweep spans up to alignPosRange≈64 px) and to
// absorb the block-multiple padding the pixelator adds, so a tight analyze band
// does not clip a candidate that renders slightly wider than the observed ink.
var verifyCropMargin = image.Pt(128, 48)

// VerifyHints carries the physical-calibration hints that make whole-string
// verification work on a real redaction. The zero value reproduces the legacy
// [VerifyCandidates] behaviour (auto everything, default font). Fields:
//
//   - BlockSize, Charset, RerankWeight — as in [VerifyCandidates].
//   - Font — a bundled font name (e.g. "Noto Sans Mono", from unpixel_rank_fonts).
//   - FontData — raw TTF/OTF bytes; takes precedence over Font.
//   - FontSize, XScale, LetterSpacing — render style (unpixel_calibrate / geometry).
//   - LinearLight — use GEGL/GIMP linear-light block averaging (needs BlockSize > 0).
//   - Crop — when non-empty, the redaction band to crop before verifying
//     (from unpixel_analyze); a white margin is added so alignment has room.
type VerifyHints struct {
	BlockSize     int
	Charset       string
	RerankWeight  float64
	Font          string
	FontData      []byte
	FontSize      float64
	XScale        float64
	LetterSpacing float64
	LinearLight   bool
	Crop          image.Rectangle
}

// VerifyWithHints scores candidates against img using [unpixel.Verify]'s faithful
// forward model, calibrated by h, and returns a [VerifyReport] ranked by ascending
// image distance. It is the calibration-aware core of unpixel_verify_candidates;
// [VerifyCandidates] is the thin block/charset/rerank-only wrapper over it.
//
// Pick is always the lowest-distance candidate whose [unpixel.Verdict.Match] is
// true — a physical decision independent of any language re-ranking.
func VerifyWithHints(ctx context.Context, img image.Image, candidates []string, h VerifyHints) (VerifyReport, error) {
	target, err := cropForVerify(img, h.Crop)
	if err != nil {
		return VerifyReport{}, err
	}

	opts, err := hintOptions(h)
	if err != nil {
		return VerifyReport{}, err
	}

	verdicts, err := unpixel.Verify(ctx, target, candidates, opts...)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("score candidates: %w", err)
	}

	ranked := make([]RankedCandidate, len(verdicts))
	if h.RerankWeight > 0 {
		// Fused order: blend physical distance with the English language prior.
		rr, rerr := rerank.Default().Rerank(ctx, target, verdicts, lang.PriorFor(lang.English), h.RerankWeight)
		if rerr != nil {
			return VerifyReport{}, fmt.Errorf("rerank candidates: %w", rerr)
		}
		for i, r := range rr {
			ranked[i] = RankedCandidate{Text: r.Text, Distance: r.Distance, Match: r.Distance < unpixel.VerifyMatchThreshold}
		}
	} else {
		// Physical order.
		for i, v := range verdicts {
			ranked[i] = RankedCandidate{Text: v.Text, Distance: v.Distance, Match: v.Match}
		}
		slices.SortFunc(ranked, func(a, b RankedCandidate) int {
			return cmp.Compare(a.Distance, b.Distance)
		})
	}

	report := VerifyReport{Ranked: ranked}
	if len(ranked) > 0 {
		report.Best = ranked[0].Text
	}
	if len(ranked) >= 2 {
		report.Margin = ranked[1].Distance - ranked[0].Distance
	}
	report.Pick = lowestDistanceMatch(verdicts)
	return report, nil
}

// cropForVerify returns img (converted to RGBA) unchanged when crop is empty, or a
// white canvas holding the cropped band at its top-left with a [verifyCropMargin]
// border on the right and bottom. The margin gives the verifier's alignment sweep
// room to slide and to absorb pixelator padding.
func cropForVerify(img image.Image, crop image.Rectangle) (image.Image, error) {
	rgba := imutil.ToRGBA(img)
	if crop.Empty() {
		return rgba, nil
	}
	crop = crop.Intersect(rgba.Bounds())
	if crop.Empty() {
		return nil, fmt.Errorf("crop region does not intersect the image bounds")
	}
	sub := imutil.Crop(rgba, crop.Min.X-rgba.Bounds().Min.X, crop.Min.Y-rgba.Bounds().Min.Y, crop.Dx(), crop.Dy())
	return imutil.PadWhite(sub, sub.Bounds().Dx()+verifyCropMargin.X, sub.Bounds().Dy()+verifyCropMargin.Y), nil
}

// cropRect converts a [x0, y0, x1, y1] corner slice into an image.Rectangle. This
// matches the [x0, y0, x1, y1] convention of the redaction_bbox that unpixel_analyze
// reports, so a client can pass that bbox straight through as the crop. An empty/nil
// slice yields the zero rectangle (no crop); any other length, or an empty rectangle
// (x1 ≤ x0 or y1 ≤ y0), is an error.
func cropRect(v []int) (image.Rectangle, error) {
	if len(v) == 0 {
		return image.Rectangle{}, nil
	}
	if len(v) != 4 {
		return image.Rectangle{}, fmt.Errorf("crop must be [x0, y0, x1, y1] (4 ints), got %d", len(v))
	}
	r := image.Rect(v[0], v[1], v[2], v[3])
	if r.Empty() {
		return image.Rectangle{}, fmt.Errorf("crop [x0, y0, x1, y1] must have x1 > x0 and y1 > y0, got %v", v)
	}
	return r, nil
}

// hintOptions translates h's calibration fields into unpixel.Option values.
func hintOptions(h VerifyHints) ([]unpixel.Option, error) {
	var opts []unpixel.Option

	var fontData []byte
	switch {
	case len(h.FontData) > 0:
		fontData = h.FontData // explicit bytes win over a bundled name
	case h.Font != "":
		d, ok := bundledFontData(h.Font)
		if !ok {
			return nil, fmt.Errorf("unknown bundled font %q; see the unpixel://fonts resource for names", h.Font)
		}
		fontData = d
	}
	if fontData != nil {
		r, err := defaults.RendererFromFonts(fontData, nil)
		if err != nil {
			return nil, fmt.Errorf("build renderer from font: %w", err)
		}
		opts = append(opts, unpixel.WithRenderer(r))
	}

	if h.BlockSize > 0 {
		opts = append(opts, unpixel.WithBlockSize(h.BlockSize))
	}
	if h.Charset != "" {
		opts = append(opts, unpixel.WithCharset(h.Charset))
	}
	if h.FontSize > 0 || h.XScale != 0 || h.LetterSpacing != 0 {
		opts = append(opts, unpixel.WithStyle(unpixel.Style{
			FontSize:      h.FontSize,
			XScale:        h.XScale,
			LetterSpacing: h.LetterSpacing,
		}))
	}
	if h.LinearLight && h.BlockSize > 0 {
		opts = append(opts, unpixel.WithPixelator(defaults.LinearBlockAverage(h.BlockSize)))
	}
	return opts, nil
}
