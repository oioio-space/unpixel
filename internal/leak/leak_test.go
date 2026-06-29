package leak

import (
	"os"
	"testing"
)

func TestSniff(t *testing.T) {
	tests := []struct {
		name string
		head []byte
		want fileKind
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE1}, kindJPEG},
		{"pdf", []byte("%PDF-1.7\n"), kindPDF},
		{"zip", []byte{'P', 'K', 0x03, 0x04}, kindZIP},
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, kindPNG},
		{"unknown", []byte{0x00, 0x01, 0x02, 0x03}, kindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniff(tc.head); got != tc.want {
				t.Errorf("sniff(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestScan_unknownNoLeak(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/x.bin"
	if err := os.WriteFile(p, []byte{0, 1, 2, 3, 4, 5}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, found, err := Scan(p, Options{})
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if found {
		t.Errorf("found = true, want false for unknown file")
	}
}

// TestScan_dispatch exercises the full Scan dispatch path for JPEG, ZIP/docx,
// and PNG file types, asserting the correct Source for each.
func TestScan_dispatch(t *testing.T) {
	dir := t.TempDir()

	// JPEG with an EXIF thumbnail → SourceEXIFThumbnail.
	thumbBytes := makeThumbJPEG(t)
	jpegBytes := makeEXIFJPEG(t, thumbBytes)
	jpegPath := dir + "/sample.jpg"
	if err := os.WriteFile(jpegPath, jpegBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	res, found, err := Scan(jpegPath, Options{})
	if err != nil {
		t.Fatalf("Scan(jpeg): err = %v, want nil", err)
	}
	if !found {
		t.Fatalf("Scan(jpeg): found=false, want true (EXIF thumbnail)")
	}
	if res.Source != SourceEXIFThumbnail {
		t.Errorf("Scan(jpeg): Source = %q, want %q", res.Source, SourceEXIFThumbnail)
	}

	// OOXML docx (ZIP-based) → SourceOfficeText.
	docxBytes := makeDocx(t, "dispatch-test-secret")
	docxPath := dir + "/sample.docx"
	if err := os.WriteFile(docxPath, docxBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	res, found, err = Scan(docxPath, Options{})
	if err != nil {
		t.Fatalf("Scan(docx): err = %v, want nil", err)
	}
	if !found {
		t.Fatalf("Scan(docx): found=false, want true (office text)")
	}
	if res.Source != SourceOfficeText {
		t.Errorf("Scan(docx): Source = %q, want %q", res.Source, SourceOfficeText)
	}

	// PNG with a solid black block + VisibleText → SourcePartial.
	pngBytes := pngWithBlock(t)
	pngPath := dir + "/sample.png"
	if err := os.WriteFile(pngPath, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	res, found, err = Scan(pngPath, Options{VisibleText: "hint"})
	if err != nil {
		t.Fatalf("Scan(png): err = %v, want nil", err)
	}
	if !found {
		t.Fatalf("Scan(png): found=false, want true (partial redaction)")
	}
	if res.Source != SourcePartial {
		t.Errorf("Scan(png): Source = %q, want %q", res.Source, SourcePartial)
	}
}

// TestScan_unreadableError verifies that Scan returns a non-nil error when
// path cannot be read (non-existent file).
func TestScan_unreadableError(t *testing.T) {
	_, _, err := Scan("/nonexistent/path/to/file.jpg", Options{})
	if err == nil {
		t.Error("Scan on missing file: err = nil, want non-nil error")
	}
}

// TestScan_pdfDispatch exercises the PDF dispatch path through Scan.
func TestScan_pdfDispatch(t *testing.T) {
	dir := t.TempDir()
	pdfPath := dir + "/sample.pdf"
	if err := os.WriteFile(pdfPath, pdfFixtureBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	res, found, err := Scan(pdfPath, Options{})
	if err != nil {
		t.Fatalf("Scan(pdf): err = %v, want nil", err)
	}
	if !found {
		t.Fatalf("Scan(pdf): found=false, want true (text under rect)")
	}
	if res.Source != SourcePDFText {
		t.Errorf("Scan(pdf): Source = %q, want %q", res.Source, SourcePDFText)
	}
}
