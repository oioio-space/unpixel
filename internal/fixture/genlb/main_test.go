package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture"
)

// TestRun generates the large-block fixtures into a temp dir and checks that
// every spec produced a PNG and a matching manifest entry.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var specs []fixture.Spec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	want := fixture.LargeBlockMatrix()
	if got := len(specs); got != len(want) {
		t.Errorf("manifest len: got %d, want %d", got, len(want))
	}

	for _, s := range specs {
		imgPath := filepath.Join(dir, s.File())
		info, err := os.Stat(imgPath)
		if err != nil {
			t.Errorf("%s: missing PNG: %v", s.Name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s: PNG is empty", s.Name)
		}
		if s.BlockSize < 20 {
			t.Errorf("%s: block_size %d is below 20 (not a large-block spec)", s.Name, s.BlockSize)
		}
		t.Logf("%s: block=%d font=%.0f size=%d bytes", s.Name, s.BlockSize, s.FontSize, info.Size())
	}
}
