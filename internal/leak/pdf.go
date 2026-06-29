package leak

import (
	"bytes"
	"sort"
	"strings"

	pdflib "rsc.io/pdf"
)

// maxPDFPages caps how many pages pdfText scans.
const maxPDFPages = 50

// minRectArea is the minimum area (in points²) for a rectangle to be
// treated as a plausible redaction box. At 72 dpi, a typical 14 pt font
// takes ~20 pt of height, so a 2×text-height strip across a page
// (≈ 400 × 40 = 16 000 pt²) is a conservative minimum.
const minRectArea = 400.0

// pdfText recovers text that lies beneath filled rectangles in a PDF.
//
// It opens the PDF from data, iterates pages (up to maxPDFPages), and for
// each page collects any rectangle whose area exceeds minRectArea. Text
// glyphs whose baseline (X, Y) falls inside such a rectangle are gathered,
// sorted left-to-right, and joined into a string. If any non-empty leaked
// text is found, pdfText returns a Result with Confidence 0.9.
//
// rsc.io/pdf can panic on malformed input; a deferred recover converts any
// panic to an abstain.
func pdfText(data []byte) (res Result, found bool) {
	defer func() {
		if r := recover(); r != nil {
			res, found = Result{}, false
		}
	}()

	r, err := pdflib.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{}, false
	}

	var leaked strings.Builder
	nPages := min(r.NumPage(), maxPDFPages)
	for i := range nPages {
		content := r.Page(i + 1).Content()
		pageText := textUnderRects(content)
		leaked.WriteString(pageText)
	}

	text := leaked.String()
	if text == "" {
		return Result{}, false
	}
	return Result{
		Source:     SourcePDFText,
		Text:       text,
		Confidence: 0.9,
	}, true
}

// textUnderRects returns all text from content whose baseline falls inside
// any rectangle large enough to be a redaction box.
func textUnderRects(content pdflib.Content) string {
	// Filter to plausible redaction boxes.
	var boxes []pdflib.Rect
	for _, rect := range content.Rect {
		w := rect.Max.X - rect.Min.X
		h := rect.Max.Y - rect.Min.Y
		if w > 0 && h > 0 && w*h >= minRectArea {
			boxes = append(boxes, rect)
		}
	}
	if len(boxes) == 0 {
		return ""
	}

	// Collect glyphs inside any box.
	var inside []pdflib.Text
	for _, t := range content.Text {
		if insideAny(t, boxes) {
			inside = append(inside, t)
		}
	}
	if len(inside) == 0 {
		return ""
	}

	// Sort left-to-right (TextHorizontal implements sort.Interface).
	sort.Sort(pdflib.TextHorizontal(inside))

	var sb strings.Builder
	for _, t := range inside {
		sb.WriteString(t.S)
	}
	return sb.String()
}

// insideAny reports whether text glyph t's baseline falls within any of boxes.
func insideAny(t pdflib.Text, boxes []pdflib.Rect) bool {
	for _, b := range boxes {
		if t.X >= b.Min.X && t.X <= b.Max.X && t.Y >= b.Min.Y && t.Y <= b.Max.Y {
			return true
		}
	}
	return false
}
