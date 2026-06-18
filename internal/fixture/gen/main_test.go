package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture"
)

// TestRun generates the fixtures into a temp dir and checks every spec produced
// an image and a matching manifest entry.
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
	if len(specs) != len(fixture.Matrix()) {
		t.Errorf("manifest has %d specs, want %d", len(specs), len(fixture.Matrix()))
	}
	for _, s := range specs {
		if _, err := os.Stat(filepath.Join(dir, s.File())); err != nil {
			t.Errorf("missing image for %s: %v", s.Name, err)
		}
	}
}
