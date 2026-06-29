package mcpserver_test

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

// docxBytesWith builds a minimal valid .docx (OOXML zip) whose document.xml
// body contains text in a <w:t> run, plus the required [Content_Types].xml.
func docxBytesWith(t *testing.T, text string) []byte {
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

func TestLeakScan_office(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/d.docx"
	if err := os.WriteFile(p, docxBytesWith(t, "leaked-text-xyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := mcp.LeakScan(p, "")
	if err != nil {
		t.Fatalf("LeakScan: %v", err)
	}
	if !res.Found || res.Text == "" {
		t.Fatalf("Found=%v Text=%q, want a leak with text", res.Found, res.Text)
	}
}
