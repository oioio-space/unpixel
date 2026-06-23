// Command gensick renders the paper-parity (Hill-2016) sick-corpus reference
// images to an output directory alongside a manifest.json that records each
// image's filename, ground-truth text, charset, font, block size, kind ("sick"
// or "digits"), and a short annotation note. Run via `go generate`.
//
// Usage: go run ./internal/fixture/gensick -out testdata/sick
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/oioio-space/unpixel/fonts"
	"github.com/oioio-space/unpixel/internal/fixture"
)

func main() {
	out := flag.String("out", "testdata/sick", "output directory for images + manifest")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gensick:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", out, err)
	}

	// Build a name → font-data map from the bundled font catalog once, so we
	// pay the parse cost only once per unique face rather than per fixture.
	all := fonts.All()
	byName := make(map[string]fonts.Font, len(all))
	for _, f := range all {
		byName[f.Name] = f
	}

	specs := fixture.SickMatrix()
	for _, s := range specs {
		f, ok := byName[s.Font]
		if !ok {
			return fmt.Errorf("font %q not found in bundled catalog", s.Font)
		}
		img, err := fixture.RedactFont(s.Spec, f.Data, nil)
		if err != nil {
			return fmt.Errorf("redact %q: %w", s.Name, err)
		}
		if err := writePNG(filepath.Join(out, s.SickFile()), img); err != nil {
			return err
		}
	}

	if err := writeManifest(filepath.Join(out, "manifest.json"), specs); err != nil {
		return err
	}
	fmt.Printf("gensick: wrote %d images + manifest.json to %s\n", len(specs), out)
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

func writeManifest(path string, specs []fixture.SickSpec) (err error) {
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
