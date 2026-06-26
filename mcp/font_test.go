package mcpserver_test

// font_test.go — tests for LoadFontData (font_path / font_base64 loading and
// validation).

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// bundledTTFPath returns the path to a bundled TTF that exists in the repo.
// We use LiberationSans-Regular.ttf as the canonical "custom font" test fixture
// because it is always present and is a valid sfnt file.
func bundledTTFPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "fonts", "embed", "LiberationSans-Regular.ttf")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("bundled TTF not found at %s: %v", p, err)
	}
	return p
}

// TestLoadFontData_bothEmpty returns nil without error.
func TestLoadFontData_bothEmpty(t *testing.T) {
	data, err := mcpserver.LoadFontData("", "")
	if err != nil {
		t.Fatalf("LoadFontData(\"\",\"\"): unexpected error: %v", err)
	}
	if data != nil {
		t.Errorf("LoadFontData(\"\",\"\"): want nil, got %d bytes", len(data))
	}
}

// TestLoadFontData_bothSet returns an error.
func TestLoadFontData_bothSet(t *testing.T) {
	_, err := mcpserver.LoadFontData("/some/path.ttf", "dGVzdA==")
	if err == nil {
		t.Error("LoadFontData(path, base64): want error for mutually exclusive inputs, got nil")
	}
}

// TestLoadFontData_validPath loads a real TTF from disk.
func TestLoadFontData_validPath(t *testing.T) {
	p := bundledTTFPath(t)
	data, err := mcpserver.LoadFontData(p, "")
	if err != nil {
		t.Fatalf("LoadFontData(path=%q): %v", p, err)
	}
	if len(data) == 0 {
		t.Error("LoadFontData(path): returned empty bytes")
	}
}

// TestLoadFontData_nonexistentPath returns an error.
func TestLoadFontData_nonexistentPath(t *testing.T) {
	_, err := mcpserver.LoadFontData("/does/not/exist.ttf", "")
	if err == nil {
		t.Error("LoadFontData(nonexistent path): want error, got nil")
	}
}

// TestLoadFontData_badMagicPath returns an error when the file is not a font.
func TestLoadFontData_badMagicPath(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notafont*.bin")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err = f.Write([]byte("not a font at all")); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}

	_, loadErr := mcpserver.LoadFontData(f.Name(), "")
	if loadErr == nil {
		t.Error("LoadFontData(bad magic via path): want error, got nil")
	}
}

// TestLoadFontData_validBase64 loads a real TTF via base64.
func TestLoadFontData_validBase64(t *testing.T) {
	p := bundledTTFPath(t)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read TTF: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	data, err := mcpserver.LoadFontData("", b64)
	if err != nil {
		t.Fatalf("LoadFontData(base64): %v", err)
	}
	if len(data) != len(raw) {
		t.Errorf("LoadFontData(base64): got %d bytes, want %d", len(data), len(raw))
	}
}

// TestLoadFontData_invalidBase64 returns an error.
func TestLoadFontData_invalidBase64(t *testing.T) {
	_, err := mcpserver.LoadFontData("", "not-valid-base64!!!")
	if err == nil {
		t.Error("LoadFontData(invalid base64): want error, got nil")
	}
}

// TestLoadFontData_badMagicBase64 returns an error when base64 decodes to non-font bytes.
func TestLoadFontData_badMagicBase64(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("not a font"))
	_, err := mcpserver.LoadFontData("", b64)
	if err == nil {
		t.Error("LoadFontData(bad magic via base64): want error, got nil")
	}
}
