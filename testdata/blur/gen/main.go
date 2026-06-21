//go:build ignore

// Command gen renders the blurfixture.Matrix reference images to testdata/blur,
// alongside a manifest.json that records each image's generation parameters.
// Fixtures are committed; regenerate when the pipeline or fixture set changes.
//
// Usage:
//
//	go run ./testdata/blur/gen [-out testdata/blur]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel/internal/fixture/blurfixture"
)

func main() {
	out := flag.String("out", "testdata/blur", "output directory for images + manifest")
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

	specs := append(blurfixture.Matrix(), blurfixture.ConnectSpecs()...)
	for _, s := range specs {
		img, err := blurfixture.Redact(s)
		if err != nil {
			return fmt.Errorf("redact %q: %w", s.Name, err)
		}
		if err := writePNG(filepath.Join(out, s.File), img); err != nil {
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

func writeManifest(path string, specs []blurfixture.Spec) (err error) {
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
