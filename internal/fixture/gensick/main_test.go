package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture"
)

// TestRun generates the sick-corpus fixtures into a temp dir and checks that
// every spec produced an image and a matching manifest entry.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var specs []fixture.SickSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	want := fixture.SickMatrix()
	if len(specs) != len(want) {
		t.Errorf("manifest has %d specs, want %d", len(specs), len(want))
	}
	for _, s := range specs {
		imgPath := filepath.Join(dir, s.SickFile())
		if _, statErr := os.Stat(imgPath); statErr != nil {
			t.Errorf("missing image for %s: %v", s.Name, statErr)
		}
	}
}
