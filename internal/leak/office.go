package leak

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// maxZipEntries caps how many entries we inspect to guard against zip-bomb / DoS.
const maxZipEntries = 512

// maxZipEntryBytes caps each decompressed entry at 8 MiB.
const maxZipEntryBytes = 8 << 20

// officeText extracts body text from an OOXML document (docx or pptx).
//
// It confirms the file is OOXML by requiring a [Content_Types].xml entry, then
// reads word/document.xml (docx) and ppt/slides/slide*.xml (pptx). Text is
// extracted from any XML element whose local name is "t" (w:t for docx, a:t for
// pptx). Returns false on any parse error or when no text is found.
func officeText(data []byte) (Result, bool) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{}, false
	}

	// Confirm OOXML: must have [Content_Types].xml.
	isOOXML := false
	for _, f := range zr.File {
		if f.Name == "[Content_Types].xml" {
			isOOXML = true
			break
		}
	}
	if !isOOXML {
		return Result{}, false
	}

	var parts []string
	scanned := 0
	for _, f := range zr.File {
		if scanned >= maxZipEntries {
			break
		}
		if !isTargetEntry(f.Name) {
			continue
		}
		scanned++
		text := extractTextFromEntry(f)
		if text != "" {
			parts = append(parts, text)
		}
	}

	joined := strings.Join(parts, " ")
	if joined == "" {
		return Result{}, false
	}
	return Result{
		Source:     SourceOfficeText,
		Text:       joined,
		Confidence: 0.85,
	}, true
}

// isTargetEntry reports whether name is a document body entry we should read.
func isTargetEntry(name string) bool {
	if name == "word/document.xml" {
		return true
	}
	// pptx slides: ppt/slides/slide*.xml
	if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
		return true
	}
	return false
}

// extractTextFromEntry opens a zip entry, decodes its XML, and collects the
// character data of all elements whose local name is "t".
// Returns empty string on any error (abstain-on-error contract).
func extractTextFromEntry(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()

	var texts []string
	dec := xml.NewDecoder(io.LimitReader(rc, maxZipEntryBytes))
	inT := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			inT = el.Name.Local == "t"
		case xml.EndElement:
			if el.Name.Local == "t" {
				inT = false
			}
		case xml.CharData:
			if inT {
				s := strings.TrimSpace(string(el))
				if s != "" {
					texts = append(texts, s)
				}
			}
		}
	}
	return strings.Join(texts, " ")
}
