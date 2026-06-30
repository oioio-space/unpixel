package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRun generates multi-frame fixtures into a temp dir and verifies that
// every case produced its 4 frame PNGs and a well-formed manifest.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest []Case
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if got, want := len(manifest), len(cases()); got != want {
		t.Fatalf("manifest has %d cases, want %d", got, want)
	}
	for _, c := range manifest {
		if len(c.Frames) != len(phases) {
			t.Errorf("case %s: got %d frames, want %d", c.Name, len(c.Frames), len(phases))
		}
		for _, fe := range c.Frames {
			if _, err := os.Stat(filepath.Join(dir, fe.File)); err != nil {
				t.Errorf("case %s: missing frame file %s: %v", c.Name, fe.File, err)
			}
		}
	}
}
