package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture"
)

// TestRun generates the context-corpus fixtures into a temp dir and checks that
// every spec produced a PNG and a matching manifest entry with non-zero rects.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var specs []fixture.ContextSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	want := fixture.ContextMatrix()
	if got := len(specs); got != len(want) {
		t.Errorf("manifest len: got %d, want %d", got, len(want))
	}

	for _, s := range specs {
		// PNG must exist and be non-empty.
		imgPath := filepath.Join(dir, s.File())
		info, err := os.Stat(imgPath)
		if err != nil {
			t.Errorf("%s: missing PNG: %v", s.Name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s: PNG is empty", s.Name)
		}

		// Rects must be populated with non-zero dimensions.
		if s.VisibleRect.W == 0 || s.VisibleRect.H == 0 {
			t.Errorf("%s: visible_rect has zero dimension: %+v", s.Name, s.VisibleRect)
		}
		if s.RedactedRect.W == 0 || s.RedactedRect.H == 0 {
			t.Errorf("%s: redacted_rect has zero dimension: %+v", s.Name, s.RedactedRect)
		}

		t.Logf("%s: visible_rect=%+v redacted_rect=%+v", s.Name, s.VisibleRect, s.RedactedRect)
	}
}
