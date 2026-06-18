// Command gen renders the fixture.Matrix reference images to an output directory,
// alongside a manifest.json that records each image's filename and its original
// generation parameters (the image ↔ parameters link). Run via `go generate`.
//
// Usage: go run ./internal/fixture/gen -out testdata/fixtures
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel/internal/fixture"
)

func main() {
	out := flag.String("out", "testdata/fixtures", "output directory for images + manifest")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", out, err)
	}

	specs := fixture.Matrix()
	for _, s := range specs {
		img, err := fixture.Redact(s)
		if err != nil {
			return fmt.Errorf("redact %q: %w", s.Name, err)
		}
		if err := writePNG(filepath.Join(out, s.File()), img); err != nil {
			return err
		}
	}

	if err := writeManifest(filepath.Join(out, "manifest.json"), specs); err != nil {
		return err
	}
	fmt.Printf("gen: wrote %d images + manifest.json to %s\n", len(specs), out)
	return nil
}

func writePNG(path string, img *image.RGBA) (err error) {
	f, err := os.Create(path) // #nosec G304 -- generator writes to controlled fixture paths
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if err = png.Encode(f, img); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return nil
}

func writeManifest(path string, specs []fixture.Spec) (err error) {
	f, err := os.Create(path) // #nosec G304 -- generator writes to controlled fixture paths
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err = enc.Encode(specs); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}
