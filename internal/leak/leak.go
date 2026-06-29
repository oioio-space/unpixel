// Package leak provides a file-level pre-pass that recovers original content
// from metadata and format leaks — EXIF embedded thumbnails, PDF text under
// rectangles, Office (OOXML) body text, and visible-text-assisted partial
// redactions — before any pixel solving.
//
// Usage:
//
//	res, found, err := leak.Scan(path, leak.Options{VisibleText: hint})
//	if err != nil {
//	    // unreadable path
//	}
//	if found {
//	    // res.Source, res.Text, res.Image, res.Confidence are populated
//	}
package leak

import (
	"fmt"
	"image"
	"os"
)

// Source identifies which leak channel recovered the content.
type Source string

const (
	// SourceEXIFThumbnail means an EXIF IFD1 embedded JPEG thumbnail was found.
	SourceEXIFThumbnail Source = "exif-thumbnail"
	// SourcePDFText means text was found beneath a filled rectangle in a PDF.
	SourcePDFText Source = "pdf-text"
	// SourceOfficeText means body text was extracted from an OOXML document.
	SourceOfficeText Source = "office-text"
	// SourcePartial means the caller-supplied visible text was surfaced alongside
	// a detected solid redaction block.
	SourcePartial Source = "partial-redaction"
)

// Result holds recovered content from a leak channel.
// At most one of Text and Image is non-zero per result.
type Result struct {
	// Source identifies the leak channel that produced this result.
	Source Source
	// Text is the recovered plaintext (PDF, Office, or partial).
	Text string
	// Image is the recovered image (EXIF thumbnail only).
	Image image.Image
	// Confidence is in [0, 1]; 1.0 means the content is byte-identical to the original.
	Confidence float64
	// Notes carries human-readable diagnostic remarks from the detector.
	Notes []string
}

// Options configures Scan behaviour.
type Options struct {
	// VisibleText is text the caller has already read from the redacted image
	// (e.g. from a caption or surrounding context). It enables the partial
	// detector; if empty that detector abstains.
	VisibleText string
}

// maxReadBytes caps how many bytes Scan reads from disk (~64 MiB).
const maxReadBytes = 64 << 20

// fileKind classifies a file by its leading magic bytes.
type fileKind uint8

const (
	kindUnknown fileKind = iota
	kindJPEG
	kindPDF
	kindZIP
	kindPNG
)

// sniff classifies raw file bytes by magic number.
// It inspects only the first few bytes of head.
func sniff(head []byte) fileKind {
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return kindJPEG
	case len(head) >= 5 && string(head[:5]) == "%PDF-":
		return kindPDF
	case len(head) >= 4 && head[0] == 'P' && head[1] == 'K' && head[2] == 0x03 && head[3] == 0x04:
		return kindZIP
	case len(head) >= 8 &&
		head[0] == 0x89 && head[1] == 'P' && head[2] == 'N' && head[3] == 'G' &&
		head[4] == 0x0D && head[5] == 0x0A && head[6] == 0x1A && head[7] == 0x0A:
		return kindPNG
	default:
		return kindUnknown
	}
}

// Scan reads the file at path, sniffs its content type, and runs the applicable
// detector. found=false means no leak was recovered and the caller should
// proceed with normal pixel solving. A non-nil error is returned only when path
// cannot be read.
func Scan(path string, opts Options) (Result, bool, error) {
	// #nosec G304 -- user-provided file path
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, false, fmt.Errorf("leak: read %s: %w", path, err)
	}
	// Cap to maxReadBytes; if the file is larger we still work on the prefix for
	// sniffing, but detectors that need the full file will abstain gracefully.
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
	}

	head := data[:min(16, len(data))]
	switch sniff(head) {
	case kindJPEG:
		res, found := exifThumbnail(data)
		return res, found, nil
	case kindPDF:
		res, found := pdfText(data)
		return res, found, nil
	case kindZIP:
		res, found := officeText(data)
		return res, found, nil
	case kindPNG:
		res, found := partial(data, opts.VisibleText)
		return res, found, nil
	default:
		return Result{}, false, nil
	}
}

// exifThumbnail attempts to recover an EXIF IFD1 embedded JPEG thumbnail.
// It is a stub; the real implementation lands in Task 2.
func exifThumbnail(data []byte) (Result, bool) {
	return Result{}, false
}

// pdfText attempts to recover text hidden beneath filled rectangles in a PDF.
// It is a stub; the real implementation lands in Task 3.
func pdfText(data []byte) (Result, bool) {
	return Result{}, false
}

// officeText attempts to extract body text from an OOXML document (docx/pptx).
// It is a stub; the real implementation lands in Task 4.
func officeText(data []byte) (Result, bool) {
	return Result{}, false
}

// partial surfaces caller-supplied visible text when the image contains a
// plausible solid redaction block.
// It is a stub; the real implementation lands in Task 5.
func partial(data []byte, visibleText string) (Result, bool) {
	return Result{}, false
}
