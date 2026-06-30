// Command genlb renders the large-block fixture corpus to an output directory
// alongside a manifest.json. Each image is produced by the faithful
// render → crop → white-pad → pixelate pipeline at block sizes 20, 24, and 32,
// which exercise the multi-frame scoring regime where phase diversity is the
// primary lever for sub-block information.
//
// These fixtures are separate from testdata/fixtures (the 17-entry panel) so
// the panel invariant is not disturbed.
//
// Usage:
//
//	go run ./internal/fixture/genlb -out testdata/largeblock
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
	out := flag.String("out", "testdata/largeblock", "output directory for images + manifest")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "genlb:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", out, err)
	}

	specs := fixture.LargeBlockMatrix()
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
	fmt.Printf("genlb: wrote %d images + manifest.json to %s\n", len(specs), out)
	return nil
}

// writePNG encodes img as a PNG at path.
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

// writeManifest encodes specs as indented JSON at path.
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
