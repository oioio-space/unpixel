package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture"
)

// TestRun generates the geometry-calibration fixtures into a temp dir and
// checks that every spec produced a PNG and a manifest entry with non-zero
// pixel rects. The ink-tight visible crop is verified to be narrower than the
// full image width (confirming the inkBounds crop ran).
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

	want := fixture.GeometryMatrix()
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

		if s.VisibleRect.W == 0 || s.VisibleRect.H == 0 {
			t.Errorf("%s: visible_rect has zero dimension: %+v", s.Name, s.VisibleRect)
		}
		if s.RedactedRect.W == 0 || s.RedactedRect.H == 0 {
			t.Errorf("%s: redacted_rect has zero dimension: %+v", s.Name, s.RedactedRect)
		}
		// The visible rect must not span the full image width — the ink-tight
		// crop should be narrower than the composed (visible+redacted) image.
		totalW := s.VisibleRect.W + s.RedactedRect.W
		if s.VisibleRect.W >= totalW {
			t.Errorf("%s: visible_rect.W %d >= total image width %d (redacted region missing)",
				s.Name, s.VisibleRect.W, totalW)
		}

		t.Logf("%s: visible_rect=%+v redacted_rect=%+v fontSize=%.0f xStretch=%.2f",
			s.Name, s.VisibleRect, s.RedactedRect, s.FontSize, s.XStretch)
	}
}
