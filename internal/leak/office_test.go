package leak

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// makeDocx builds a minimal valid .docx (OOXML zip) whose document.xml body
// contains the given text in a <w:t> run, plus the required [Content_Types].xml.
func makeDocx(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`)
	write("word/document.xml", `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>`+text+`</w:t></w:r></w:p></w:body></w:document>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestOfficeText_recoversDocx(t *testing.T) {
	data := makeDocx(t, "hidden-secret-123")
	res, found := officeText(data)
	if !found {
		t.Fatalf("officeText found=false, want true")
	}
	if !strings.Contains(res.Text, "hidden-secret-123") {
		t.Errorf("Text = %q, want it to contain the body text", res.Text)
	}
	if res.Source != SourceOfficeText {
		t.Errorf("Source = %q, want %q", res.Source, SourceOfficeText)
	}
}

func TestOfficeText_plainZipAbstains(t *testing.T) {
	// A zip with no [Content_Types].xml is not OOXML → abstain.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("hello.txt")
	_, _ = w.Write([]byte("not office"))
	_ = zw.Close()
	if _, found := officeText(buf.Bytes()); found {
		t.Errorf("found=true on non-OOXML zip, want false")
	}
}

// TestOfficeText_recoversPptx exercises the pptx slide path (a:t elements)
// and the isTargetEntry pptx branch.
func TestOfficeText_recoversPptx(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`)
	write("ppt/slides/slide1.xml", `<?xml version="1.0"?><p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>pptx-secret-456</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	res, found := officeText(buf.Bytes())
	if !found {
		t.Fatalf("officeText found=false on pptx, want true")
	}
	if !strings.Contains(res.Text, "pptx-secret-456") {
		t.Errorf("Text = %q, want it to contain pptx-secret-456", res.Text)
	}
	if res.Source != SourceOfficeText {
		t.Errorf("Source = %q, want %q", res.Source, SourceOfficeText)
	}
}

// TestIsTargetEntry covers the known patterns explicitly.
func TestIsTargetEntry(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"word/document.xml", true},
		{"ppt/slides/slide1.xml", true},
		{"ppt/slides/slide99.xml", true},
		{"[Content_Types].xml", false},
		{"word/styles.xml", false},
		{"ppt/slides/notaslide.txt", false},
	}
	for _, tc := range tests {
		if got := isTargetEntry(tc.name); got != tc.want {
			t.Errorf("isTargetEntry(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
