# Leak pre-pass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A pure-Go, file-level pre-pass (`internal/leak`) that recovers the original content from metadata/format leaks — EXIF embedded thumbnail, PDF text-under-rectangle, Office body text, and visible-text-assisted partial redaction — before any pixel solving, wired into the CLI and MCP without touching the library core.

**Architecture:** `leak.Scan(path, Options)` sniffs the file by magic bytes and dispatches to one of four detector files. Each detector is independent and abstains (returns found=false) when its leak isn't present. The CLI and MCP call `Scan` at their file-path entry points and short-circuit on a confident hit; `Recover`/`New`/the panel never call it, so the decode invariant holds by construction.

**Tech Stack:** Go (pure, `CGO_ENABLED=0`), stdlib `image/jpeg`/`archive/zip`/`encoding/xml`/`encoding/binary`, new dep `rsc.io/pdf` (pure-Go, BSD-3-Clause).

## Global Constraints

Apply to **every** task.

- **Pure Go, no CGO** (`cgo:check` gate). `rsc.io/pdf` is pure-Go/BSD — the only new dependency.
- **Run tests caged**: `./scripts/gotest-caged.sh go test ./internal/leak/ -run <Name> -v`. Never bare `go test`.
- **Invariant**: panel fixtures 17/17 fidélité 1.000 + blur 13/14 unchanged. `leak` is never imported by `unpixel`/`New`/`Recover` or the panel — it is called only from `cmd/unpixel` and `mcp`. Verify no such import is added to the core.
- **All test fixtures forged IN-MEMORY** (bytes built in the test). NEVER depend on a gitignored or network-fetched file — that broke CI (testdata/wild is gitignored). Do not add files under testdata/.
- **Detectors abstain, never fabricate**: on any parse error or absent leak, return `(Result{}, false, nil)` (parse errors are not fatal) — except `Scan` returns a non-nil error only for unreadable paths.
- **Coverage** ≥ 85% for `internal/leak`; `mise run cover:check` stays green.
- **Security**: bound all reads from user files — cap thumbnail size, zip entry count/size (zip-bomb), and PDF page count; `#nosec G304` with a one-line reason on user-path `os.Open` (matches existing CLI/MCP usage).
- **Commit ritual (mandatory gate):** before each `git commit`, in a SEPARATE bash call:
  ```bash
  GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
  ```
  A post-commit hook appends a changelog line to `PROGRESS.md` and re-stages it; if `PROGRESS.md` shows up staged, `git restore --staged PROGRESS.md` before arming the marker. Stage only the task's files. Commit messages end with:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Branch:** all work on `feat/leak-prepass` (create before Task 1: `git checkout -b feat/leak-prepass`). Never commit on master.

**Existing patterns this plan consumes (verbatim):**
- MCP tool registration in `mcp/server.go NewServer`: `mcpsdk.AddTool(srv, toolX, handleX)`.
- MCP handler shape: `func handleX(ctx context.Context, _ *mcpsdk.CallToolRequest, in inputT) (*mcpsdk.CallToolResult, outT, error)`; error path uses `errResult(fmt.Errorf("unpixel_x: %w", err))`.
- MCP image output: see `handleRender` in `mcp/render.go` (returns PNG bytes as MCP image content).
- CLI flags: `&cli.BoolFlag{Name: "...", Value: ...}` / `&cli.StringFlag{...}` in the command's `Flags` slice (`cmd/unpixel/main.go` ~line 2015); read via `cmd.Bool("...")`.
- CLI image load: `loadImage(path)` (`cmd/unpixel/main.go:351`). MCP: `loadImage(in.ImagePath)`.

---

### Task 1: `internal/leak` skeleton — types, `Scan`, content sniff, dispatch

**Files:**
- Create: `internal/leak/leak.go`
- Test: `internal/leak/leak_test.go`

**Interfaces:**
- Produces:
  ```go
  package leak

  type Source string
  const (
      SourceEXIFThumbnail Source = "exif-thumbnail"
      SourcePDFText       Source = "pdf-text"
      SourceOfficeText    Source = "office-text"
      SourcePartial       Source = "partial-redaction"
  )
  type Result struct {
      Source     Source
      Text       string
      Image      image.Image
      Confidence float64
      Notes      []string
  }
  type Options struct { VisibleText string }

  // Scan reads path, sniffs its content type, and runs the applicable detector.
  // found=false means no leak was recovered (caller proceeds with normal decode).
  // A non-nil error is returned only when path cannot be read.
  func Scan(path string, opts Options) (Result, bool, error)

  // kind classifies raw file bytes by magic number.
  type fileKind uint8
  const (kindUnknown fileKind = iota; kindJPEG; kindPDF; kindZIP; kindPNG)
  func sniff(head []byte) fileKind
  ```
  `Scan` reads the file (cap at a sane max, e.g. 64 MiB), calls `sniff`, and dispatches: kindJPEG→`exifThumbnail`, kindPDF→`pdfText`, kindZIP→`officeText`, kindPNG→`partial` (with opts.VisibleText). In Task 1 the four detectors are stubs returning `(Result{}, false, nil)` (real bodies land in Tasks 2–5).

- [ ] **Step 1: Write the failing test (sniff classifies magic bytes)**

```go
package leak

import "testing"

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestSniff -v`
Expected: FAIL — package/`sniff` undefined.

- [ ] **Step 3: Implement leak.go (types, sniff, Scan with stub detectors)**

Create `internal/leak/leak.go` with the Interfaces types. `sniff`: JPEG `FFD8FF`; PDF prefix `%PDF-`; ZIP `PK\x03\x04`; PNG `\x89PNG\r\n\x1a\n`; else unknown. `Scan`: `data, err := os.ReadFile(path)` (`#nosec G304 -- user-provided file path`); on err return `(Result{}, false, fmt.Errorf("leak: read %s: %w", path, err))`; `switch sniff(data[:min(16,len(data))])` → call the matching stub. Stubs: `func exifThumbnail(data []byte) (Result, bool)`, `func pdfText(data []byte) (Result, bool)`, `func officeText(data []byte) (Result, bool)`, `func partial(data []byte, visibleText string) (Result, bool)` — each `return Result{}, false` for now.

- [ ] **Step 4: Run test to verify it passes**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestSniff -v`
Expected: PASS.

- [ ] **Step 5: Write a Scan dispatch test (unknown file → no leak, no error)**

```go
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
```
(add `"os"` import to the test.)

- [ ] **Step 6: Run it, verify pass**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -v`
Expected: PASS (both tests).

- [ ] **Step 7: Commit**

```bash
git add internal/leak/leak.go internal/leak/leak_test.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(leak): package skeleton — Scan, content sniff, dispatch

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: EXIF embedded-thumbnail detector (hand-rolled, no dep)

**Files:**
- Modify: `internal/leak/leak.go` (replace the `exifThumbnail` stub) — or Create `internal/leak/exifthumb.go`
- Test: `internal/leak/exifthumb_test.go`

**Interfaces:**
- Consumes: `Result`/`Source` (Task 1), stdlib `encoding/binary`, `image/jpeg`, `bytes`.
- Produces: real `func exifThumbnail(data []byte) (Result, bool)` returning `Result{Source: SourceEXIFThumbnail, Image: <decoded thumb>, Confidence: 1.0}` when an IFD1 JPEG thumbnail is present.

**Algorithm (EXIF/TIFF layout — implement exactly):**
1. Find the `APP1` segment: scan JPEG markers from offset 2 (after `FFD8`). Each marker is `FF <marker>` then 2-byte big-endian length (incl. the 2 length bytes). The APP1 marker is `FFE1`; its payload starts with `"Exif\x00\x00"` (6 bytes). Stop at `FFDA` (start of scan) — no APP1 ⇒ abstain.
2. After `"Exif\x00\x00"` begins the **TIFF header** (this offset is the TIFF origin, all IFD offsets are relative to it): 2 bytes byte-order (`II`=little, `MM`=big), 2 bytes magic `0x002A`, 4 bytes offset to IFD0. Use `binary.LittleEndian`/`BigEndian` accordingly.
3. Read IFD0 at that offset: 2-byte entry count, then `count`×12-byte entries, then a 4-byte **next-IFD offset**. That next offset (if non-zero) points to **IFD1** (the thumbnail IFD). Zero ⇒ abstain.
4. Read IFD1 entries (same 12-byte layout: tag(2), type(2), count(4), value/offset(4)). Find tag `0x0201` (`JPEGInterchangeFormat` = thumbnail offset, relative to TIFF origin) and `0x0202` (`JPEGInterchangeFormatLength`). Missing ⇒ abstain.
5. The thumbnail JPEG is `tiff[off : off+length]`. Cap `length` at `maxThumbBytes = 2<<20` (security). `jpeg.Decode(bytes.NewReader(thumb))` → the recovered image. Decode error ⇒ abstain.

- [ ] **Step 1: Write the failing test (forge a JPEG with an EXIF thumbnail, recover it)**

```go
package leak

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// makeThumbJPEG encodes a tiny solid-colour JPEG used as the embedded thumbnail.
func makeThumbJPEG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range im.Pix {
		im.Pix[i] = 0xAA
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// makeEXIFJPEG builds a minimal JPEG (FFD8 + APP1[Exif/TIFF/IFD0→IFD1] + FFD9)
// whose IFD1 carries thumb as JPEGInterchangeFormat. Little-endian TIFF.
func makeEXIFJPEG(t *testing.T, thumb []byte) []byte {
	t.Helper()
	// TIFF block (origin = start of this block).
	var tiff bytes.Buffer
	tiff.WriteString("II")                                   // little-endian
	binary.Write(&tiff, binary.LittleEndian, uint16(0x002A)) // magic
	binary.Write(&tiff, binary.LittleEndian, uint32(8))      // IFD0 at offset 8
	// IFD0: 0 entries, next-IFD offset → IFD1.
	binary.Write(&tiff, binary.LittleEndian, uint16(0)) // entry count 0
	ifd1Off := uint32(8 + 2 + 4)                        // after IFD0 (count+nextoff)
	binary.Write(&tiff, binary.LittleEndian, ifd1Off)   // next IFD = IFD1
	// IFD1: 2 entries (0x0201 offset, 0x0202 length), next-IFD 0, then thumb bytes.
	binary.Write(&tiff, binary.LittleEndian, uint16(2)) // 2 entries
	ifd1End := ifd1Off + 2 + 2*12 + 4                   // count + 2 entries + nextoff
	thumbOff := ifd1End
	writeEntry := func(tag, typ uint16, count, val uint32) {
		binary.Write(&tiff, binary.LittleEndian, tag)
		binary.Write(&tiff, binary.LittleEndian, typ)
		binary.Write(&tiff, binary.LittleEndian, count)
		binary.Write(&tiff, binary.LittleEndian, val)
	}
	writeEntry(0x0201, 4, 1, thumbOff)              // JPEGInterchangeFormat (LONG)
	writeEntry(0x0202, 4, 1, uint32(len(thumb)))    // JPEGInterchangeFormatLength
	binary.Write(&tiff, binary.LittleEndian, uint32(0)) // no next IFD
	tiff.Write(thumb)

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8})                                  // SOI
	out.Write([]byte{0xFF, 0xE1})                                  // APP1
	binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))   // segment length
	out.Write(payload)
	out.Write([]byte{0xFF, 0xD9}) // EOI
	return out.Bytes()
}

func TestExifThumbnail_recovers(t *testing.T) {
	thumb := makeThumbJPEG(t)
	data := makeEXIFJPEG(t, thumb)
	res, found := exifThumbnail(data)
	if !found {
		t.Fatalf("exifThumbnail found=false, want true")
	}
	if res.Source != SourceEXIFThumbnail {
		t.Errorf("Source = %q, want %q", res.Source, SourceEXIFThumbnail)
	}
	if res.Image == nil || res.Image.Bounds().Dx() != 8 {
		t.Errorf("recovered image = %v, want 8x8 thumbnail", res.Image)
	}
	_ = color.RGBA{} // keep import if unused after edits
}

func TestExifThumbnail_noExifAbstains(t *testing.T) {
	plain := makeThumbJPEG(t) // a JPEG with no APP1/EXIF
	if _, found := exifThumbnail(plain); found {
		t.Errorf("found=true on JPEG without EXIF thumbnail, want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestExifThumbnail -v`
Expected: FAIL (stub returns false → `found=false` on the positive case).

- [ ] **Step 3: Implement `exifThumbnail` per the Algorithm**

Replace the stub. Walk APP1, parse TIFF/IFD0→IFD1, read tags 0x0201/0x0202, slice+`jpeg.Decode` the thumbnail, cap at `maxThumbBytes`. Bounds-check every offset against `len(data)` (abstain on any out-of-range — hostile input safety). Abstain (return `false`) on any malformation; never panic.

- [ ] **Step 4: Run tests to verify they pass**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestExifThumbnail -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/leak/exifthumb.go internal/leak/exifthumb_test.go internal/leak/leak.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(leak): EXIF embedded-thumbnail detector (hand-rolled APP1/IFD1)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: PDF text-under-rectangle detector (`rsc.io/pdf`)

**Files:**
- Create: `internal/leak/pdf.go`
- Test: `internal/leak/pdf_test.go`
- Modify: `go.mod`, `go.sum` (add `rsc.io/pdf`)

**Interfaces:**
- Consumes: `Result`/`Source`; `rsc.io/pdf` (`pdf.NewReader(io.ReaderAt, int64)`, `r.NumPage()`, `r.Page(i).Content()` → `pdf.Content{Text []pdf.Text, Rect []pdf.Rect}`; `pdf.Text{X,Y,W,FontSize float64, S string}`; `pdf.Rect{Min,Max pdf.Point}`).
- Produces: real `func pdfText(data []byte) (Result, bool)`.

**Algorithm:** `pdf.NewReader(bytes.NewReader(data), int64(len(data)))`; for each page (cap `maxPDFPages = 50`), get `Content()`. For each `Rect` that is plausibly a redaction box (area ≥ a few text-heights), collect `Text` whose baseline point `(X,Y)` falls inside the Rect. Concatenate the inside-rect text (sorted by `pdf.TextHorizontal`) → that's the leaked text under the box. Return `Result{Source: SourcePDFText, Text: <joined>, Confidence: 0.9}` if any non-empty text was found under a rect; else abstain. Wrap rsc.io/pdf calls in a `defer recover()` (it can panic on malformed PDFs) → abstain on panic.

- [ ] **Step 1: Add the dependency**

Run: `go get rsc.io/pdf@latest` then `go mod tidy`. Verify pure-Go (no cgo pulled): `mise run cgo:check` → still green. Confirm license is BSD (it is). Expected: `go.mod` gains `rsc.io/pdf vX.Y.Z`.

- [ ] **Step 2: Write the failing test (forge a 1-page PDF with text inside a filled rect)**

```go
package leak

import (
	"strings"
	"testing"
)

// minimalRedactedPDF returns the bytes of a hand-written 1-page PDF whose
// content stream draws a filled black rectangle and places the text "SECRET"
// inside it (un-flattened — the text object is still present under the fill).
// Kept as a literal so the fixture needs no generator and no network.
func minimalRedactedPDF() []byte {
	// NOTE to implementer: build a valid minimal PDF (header %PDF-1.4, catalog,
	// pages, one page with a content stream:  "0 0 0 rg 100 600 120 20 re f"
	// (black fill rect) then "BT /F1 14 Tf 105 604 Td (SECRET) Tj ET", a Helvetica
	// font resource, and a correct xref+trailer). Verify it loads with rsc.io/pdf
	// before asserting. If hand-authoring the xref is error-prone, generate the
	// bytes once with a tiny helper and paste them as a []byte literal here.
	return pdfFixtureBytes // defined in this test file as a verified literal
}

func TestPdfText_recoversUnderRect(t *testing.T) {
	res, found := pdfText(minimalRedactedPDF())
	if !found {
		t.Fatalf("pdfText found=false, want true")
	}
	if !strings.Contains(res.Text, "SECRET") {
		t.Errorf("Text = %q, want it to contain SECRET", res.Text)
	}
	if res.Source != SourcePDFText {
		t.Errorf("Source = %q, want %q", res.Source, SourcePDFText)
	}
}

func TestPdfText_noRectAbstains(t *testing.T) {
	// A PDF with text but no filled rectangle → nothing "under a box" → abstain.
	if _, found := pdfText(plainTextPDFBytes); found {
		t.Errorf("found=true on PDF without a redaction rect, want false")
	}
}
```
IMPLEMENTER NOTE: author `pdfFixtureBytes` and `plainTextPDFBytes` as verified `[]byte` literals in the test file. Build each by writing a minimal PDF, loading it once with `pdf.NewReader` in a scratch `main` to confirm `Content()` returns the expected `Text`/`Rect`, then paste the confirmed bytes. Do NOT write the fixture to testdata/ (in-memory only).

- [ ] **Step 3: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestPdfText -v`
Expected: FAIL (stub).

- [ ] **Step 4: Implement `pdfText` per the Algorithm**

Create `internal/leak/pdf.go`. Use the panic-guard. Point-in-rect: `t.X >= rect.Min.X && t.X <= rect.Max.X && t.Y >= rect.Min.Y && t.Y <= rect.Max.Y`. Replace the Task-1 stub call site so `Scan` routes kindPDF here.

- [ ] **Step 5: Run tests to verify they pass**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestPdfText -v`
Expected: PASS (both). Then `mise run cgo:check` → still pure Go.

- [ ] **Step 6: Commit**

```bash
git add internal/leak/pdf.go internal/leak/pdf_test.go internal/leak/leak.go go.mod go.sum
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(leak): PDF text-under-rectangle detector (rsc.io/pdf)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Office (OOXML) body-text detector (stdlib only)

**Files:**
- Create: `internal/leak/office.go`
- Test: `internal/leak/office_test.go`

**Interfaces:**
- Consumes: `Result`/`Source`; stdlib `archive/zip`, `encoding/xml`, `bytes`, `strings`.
- Produces: real `func officeText(data []byte) (Result, bool)`.

**Algorithm:** `zip.NewReader(bytes.NewReader(data), int64(len(data)))`. Confirm it's OOXML: an entry named `[Content_Types].xml` exists (else abstain). Read `word/document.xml` (docx) and/or `ppt/slides/slide*.xml` (pptx); cap total entries at `maxZipEntries = 512` and each decompressed entry at `maxZipEntryBytes = 8<<20` (zip-bomb guard via `io.LimitReader`). Extract text from `<w:t>…</w:t>` (docx) and `<a:t>…</a:t>` (pptx) elements using `encoding/xml` token scanning (match local element name `t`). Join with spaces. Return `Result{Source: SourceOfficeText, Text: <joined>, Confidence: 0.85}` if non-empty; else abstain.

- [ ] **Step 1: Write the failing test (build a minimal .docx in-memory)**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestOfficeText -v`
Expected: FAIL (stub).

- [ ] **Step 3: Implement `officeText` per the Algorithm**

Create `internal/leak/office.go`. Token-scan with `xml.NewDecoder`; collect `CharData` while inside an element whose `Name.Local == "t"`. Route `Scan` kindZIP → here.

- [ ] **Step 4: Run tests to verify they pass**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestOfficeText -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/leak/office.go internal/leak/office_test.go internal/leak/leak.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(leak): OOXML body-text detector (archive/zip + encoding/xml)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Partial-redaction detector (visible-text-assisted)

**Files:**
- Create: `internal/leak/partial.go`
- Test: `internal/leak/partial_test.go`

**Interfaces:**
- Consumes: `Result`/`Source`, `Options.VisibleText`, stdlib `image`, `image/png`, `bytes`.
- Produces: real `func partial(data []byte, visibleText string) (Result, bool)`.

**Algorithm (honest, no OCR):** if `visibleText == ""` → abstain immediately (return false) — auto-OCR is out of scope (Tier-2). Otherwise decode the PNG; the detector's job is to *surface* the caller-supplied visible text as the likely redacted content when the image plausibly contains a redaction (a near-uniform dark/solid rectangular region exists). v1 implementation: decode image; detect presence of a solid rectangular block (a run of rows/cols whose pixels are near-constant and differ from the page background) via a simple scan; if such a block exists, return `Result{Source: SourcePartial, Text: visibleText, Confidence: 0.5, Notes: ["surfaced caller-supplied visible text; a solid redaction block is present"]}`. If no redaction-like block, abstain. (This keeps it honest: it only *confirms+surfaces*, and the confidence is modest.)

- [ ] **Step 1: Write the failing test (image with a solid block + visible text → surfaced; no hint → abstain)**

```go
package leak

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngWithBlock(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			im.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	for y := 10; y < 30; y++ { // solid black redaction block
		for x := 20; x < 60; x++ {
			im.Set(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPartial_surfacesWithHint(t *testing.T) {
	res, found := partial(pngWithBlock(t), "the-secret")
	if !found {
		t.Fatalf("partial found=false, want true (block present + visible hint)")
	}
	if res.Text != "the-secret" || res.Source != SourcePartial {
		t.Errorf("got {%q,%q}, want {the-secret, partial-redaction}", res.Text, res.Source)
	}
}

func TestPartial_abstainsWithoutHint(t *testing.T) {
	if _, found := partial(pngWithBlock(t), ""); found {
		t.Errorf("found=true without VisibleText, want false (needs OCR — out of scope)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestPartial -v`
Expected: FAIL (stub).

- [ ] **Step 3: Implement `partial` per the Algorithm**

Create `internal/leak/partial.go`. Reuse `internal/imutil.ToRGBA` if helpful (after decode). Solid-block scan: find a bounding region of near-constant dark pixels covering ≥ a small fraction of the image. Route `Scan` kindPNG → `partial(data, opts.VisibleText)`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `./scripts/gotest-caged.sh go test ./internal/leak/ -run TestPartial -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/leak/partial.go internal/leak/partial_test.go internal/leak/leak.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(leak): partial-redaction detector (visible-text-assisted, abstains without hint)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: CLI + MCP integration

**Files:**
- Modify: `cmd/unpixel/main.go` (add `--leak-scan` flag + pre-pass at the file-path entry)
- Create: `mcp/leakscan.go` (new `unpixel_leak_scan` tool)
- Modify: `mcp/server.go` (register the tool)
- Test: `mcp/leakscan_test.go`

**Interfaces:**
- Consumes: `leak.Scan`, `leak.Options`, `leak.Result`; MCP `AddTool`/handler pattern; `errResult`; `handleRender`'s image-output approach for the thumbnail case.
- Produces: MCP tool `unpixel_leak_scan(image_path string, visible_text string)` → JSON `{source, text, confidence, notes}` (and, for exif-thumbnail, the recovered image as MCP image content). CLI: when the input is a file path and `--leak-scan` (default true), run `leak.Scan` first; on `found`, print the recovered text/`[image]` + source and exit 0 without pixel solving.

- [ ] **Step 1: Write the failing MCP test**

```go
package mcpserver_test

import (
	"os"
	"testing"

	mcp "github.com/oioio-space/unpixel/mcp"
)

func TestLeakScan_office(t *testing.T) {
	// Build a docx in a temp file (reuse the leak package's approach inline).
	dir := t.TempDir()
	p := dir + "/d.docx"
	if err := os.WriteFile(p, docxBytesWith("leaked-text-xyz"), 0o600); err != nil {
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
```
IMPLEMENTER NOTE: expose a thin exported `mcp.LeakScan(path, visibleText) (LeakReport, error)` wrapping `leak.Scan`; `LeakReport{Found bool, Source, Text string, Confidence float64, Notes []string}`. Provide `docxBytesWith` in the test (copy the `makeDocx` builder from Task 4's test, or extract a shared in-test helper).

- [ ] **Step 2: Run test to verify it fails**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run TestLeakScan_office -v`
Expected: FAIL — `mcp.LeakScan` undefined.

- [ ] **Step 3: Implement the MCP tool + CLI flag**

`mcp/leakscan.go`: `LeakScan` wrapper + `toolLeakScan`/`handleLeakScan` (input `{image_path, visible_text}`; output the report; for `SourceEXIFThumbnail` encode `res.Image` to PNG and add image content like `handleRender`). Register in `server.go NewServer`. CLI: add `&cli.BoolFlag{Name: "leak-scan", Value: true}`; in the main run path, before `loadImage`, if `cmd.Bool("leak-scan")` and the arg is a real file, call `leak.Scan`; on found, print and return nil.

- [ ] **Step 4: Run tests + confirm core untouched**

Run: `./scripts/gotest-caged.sh go test ./mcp/ -run TestLeakScan -v` → PASS.
Run: `grep -rn '"github.com/oioio-space/unpixel/internal/leak"' unpixel.go defaults/ internal/` → expected: NO matches (core must not import leak).
Run: `mise run bench:panel` → fixtures 17/17, blur 13/14 (unchanged).

- [ ] **Step 5: Commit**

```bash
git add cmd/unpixel/main.go mcp/leakscan.go mcp/leakscan_test.go mcp/server.go
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "feat(cli,mcp): wire leak pre-pass — --leak-scan flag + unpixel_leak_scan tool

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Validate, coverage, docs

**Files:**
- Modify: `internal/leak/*_test.go` (coverage gaps), `mcp/resources.go` (document the tool if the methods/tools resource lists tools)

**Interfaces:** none (validation).

- [ ] **Step 1: Full Scan end-to-end test through dispatch**

Add a `TestScan_dispatch` that writes a forged EXIF JPEG, a docx, and a redacted-block PNG (with VisibleText) to temp files and asserts `Scan` returns the right `Source` for each. Caged run.

- [ ] **Step 2: Coverage**

Run: `./scripts/gotest-caged.sh go test -coverprofile=/tmp/c.out ./internal/leak/ && go tool cover -func=/tmp/c.out | tail -1`
Add direct tests for any under-covered branch (bounds-check abstain paths, panic-guard in pdf). Then `mise run cover:check` → ≥ 85% PASS. Paste the line.

- [ ] **Step 3: Full gate**

Run: `mise run ci > /tmp/ci.log 2>&1; echo "EXIT=$?"; grep -E "Total coverage|ERROR|FAIL|cover:check" /tmp/ci.log`
Expected: `EXIT=0`, coverage ≥ 85%, no ERROR/FAIL. (Capture `$?` directly — do not mask behind a pipe.)

- [ ] **Step 4: Document the new MCP tool**

If `mcp/resources.go` enumerates tools/methods, add a one-line entry for `unpixel_leak_scan`. Update the CLI `--help`/README only if a user-facing capability line exists (scribe-level; skip if purely internal).

- [ ] **Step 5: Commit**

```bash
git add -A
GIT_DIR=$(git rev-parse --git-dir); git diff --cached | sha1sum | cut -d' ' -f1 > "$GIT_DIR/claude-simplify-ok"
git commit -m "test(leak): end-to-end Scan dispatch + coverage; document leak-scan tool

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- §3 `internal/leak` + `Scan`/`Options`/`Result`/`Source` + sniff/dispatch → Task 1. ✅
- §3 exifThumbnail (hand-rolled APP1/IFD1) → Task 2. ✅
- §3 pdfText (rsc.io/pdf, text-in-rect) → Task 3. ✅
- §3 officeText (zip+xml w:t/a:t) → Task 4. ✅
- §3 partial (visible-text-assisted, abstain without hint) → Task 5. ✅
- §4 dependency rsc.io/pdf added → Task 3 Step 1. ✅
- §5 CLI `--leak-scan` + MCP `unpixel_leak_scan`; core untouched (verified by grep) → Task 6. ✅
- §6 in-memory fixtures, negatives/abstain, caged, ≥85% cover, no gitignored deps → Tasks 1–7 (each test forges bytes; Task 7 coverage). ✅
- §2 success criteria: per-detector positive (Tasks 2–5), zero-false-positive abstain tests (every task), core-untouched invariant (Task 6 Step 4 grep + bench:panel), pure-Go/caged/cover (Task 7). ✅
- §8 security bounds (maxThumbBytes, maxZip*, maxPDFPages, #nosec) → Global Constraints + Tasks 2/3/4. ✅

**Placeholder scan:** Two IMPLEMENTER NOTES (Task 3 PDF fixture literal; Task 6 docx test helper) describe forging concrete byte fixtures that must be verified before pasting — these are unavoidable (a valid PDF xref is data, not logic) and specify exactly what to build and how to verify. Not vague placeholders. All detector algorithms are spelled out with exact tags/offsets/bounds.

**Type consistency:** `Scan/Options/Result/Source/fileKind/sniff` (Task 1) consumed by every detector; detector funcs `exifThumbnail(data)`, `pdfText(data)`, `officeText(data)`, `partial(data,visibleText)` consistent Task 1 ↔ 2–5; `mcp.LeakScan`/`LeakReport` (Task 6) wraps `leak.Scan`. `rsc.io/pdf` types (`pdf.Text{X,Y,W,FontSize,S}`, `pdf.Rect`) match the grounded API.

**Known deferrals (in PROGRESS.md / spec §4):** HEIC/TIFF EXIF, encrypted/complex PDF, PPTX notes, true OCR for partial (Tier-2 #5).
