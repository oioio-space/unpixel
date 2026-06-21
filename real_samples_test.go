package unpixel_test

// real_samples_test.go exercises the geometry preprocessing pipeline
// (LocateRedaction → crop → InferBlockSize → segment.Lines) against every
// sample in testdata/real/manifest.json.
//
// Marx forward-model fidelity (98.4 %, linear distance 0.0163) is validated
// manually and recorded in PROGRESS.md — not in this suite — because the Noto
// Sans font used in that image is not bundled and a full blind decode is slow
// (> 600 s). Only hello-world is in the forward-model suite (real_mosaic_test.go)
// because Noto Sans Mono IS bundled.

import (
	"encoding/json"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/segment"
)

const realDir = "testdata/real"

// realSample mirrors the schema of testdata/real/manifest.json.
// Only the fields needed by the geometry tests are decoded; schema additions
// are silently ignored by json.Unmarshal so parsing stays stable.
type realSample struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Block   int    `json:"block"`
	Lines   int    `json:"lines"`
	OffsetX int    `json:"offset_x"`
	OffsetY int    `json:"offset_y"`
}

// loadRealManifest reads and parses testdata/real/manifest.json.
func loadRealManifest(tb testing.TB) []realSample {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(realDir, "manifest.json"))
	if err != nil {
		tb.Fatalf("read %s/manifest.json: %v", realDir, err)
	}
	var samples []realSample
	if err := json.Unmarshal(data, &samples); err != nil {
		tb.Fatalf("parse manifest: %v", err)
	}
	if len(samples) == 0 {
		tb.Fatal("real manifest is empty")
	}
	return samples
}

// openRealPNG decodes a PNG from testdata/real/ into an *image.RGBA.
func openRealPNG(tb testing.TB, file string) *image.RGBA {
	tb.Helper()
	path := filepath.Join(realDir, file)
	f, err := os.Open(path)
	if err != nil {
		tb.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	img, err := decodePNG(f) // decodePNG defined in real_mosaic_test.go
	if err != nil {
		tb.Fatalf("decode %s: %v", path, err)
	}
	return img
}

// TestRealSamples_geometry verifies the geometry preprocessing pipeline on
// every sample in testdata/real/manifest.json. It is always-on, hermetic and
// fast (no font rendering, no search).
//
// LocateRedaction design note: the detector uses blur-gradient heuristics, so
// its crop may not contain all text lines when those lines sit in different
// blur bands. For line counting we therefore run segment.Lines on the full
// image (which has the correct line topology) and use the LocateRedaction crop
// only for InferBlockSize (where it avoids the wide white margins that confuse
// the GCD-of-gaps estimator).
func TestRealSamples_geometry(t *testing.T) {
	for _, s := range loadRealManifest(t) {
		t.Run(s.Name, func(t *testing.T) {
			img := openRealPNG(t, s.File)

			// LocateRedaction must find a plausible band.
			rect, ok := unpixel.LocateRedaction(img)
			if !ok {
				t.Fatalf("%s: LocateRedaction returned ok=false, want true", s.Name)
			}
			if rect.Dx() < 200 || rect.Dy() < 10 {
				t.Errorf("%s: located band %v too small (dx=%d dy=%d)", s.Name, rect, rect.Dx(), rect.Dy())
			}

			// InferBlockSize on the crop (avoids the wide white margins).
			crop := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
			draw.Draw(crop, crop.Bounds(), img, rect.Min, draw.Src)
			gotBlock := unpixel.InferBlockSize(crop)
			if gotBlock <= 0 {
				t.Errorf("%s: InferBlockSize(crop) = %d, want > 0", s.Name, gotBlock)
			} else {
				// Both real samples have exact inference; allow ±33 % tolerance for
				// future samples where the GCD estimator is less precise.
				tol := max(1, s.Block/3)
				if diff := gotBlock - s.Block; diff > tol || diff < -tol {
					t.Errorf("%s: InferBlockSize(crop) = %d, want %d ± %d", s.Name, gotBlock, s.Block, tol)
				}
			}

			// segment.Lines on the full image (LocateRedaction's crop may only
			// contain part of a multi-line redaction; the full image preserves
			// the correct horizontal ink bands).
			gotLines := len(segment.Lines(img))
			if gotLines != s.Lines {
				t.Errorf("%s: segment.Lines = %d, want %d", s.Name, gotLines, s.Lines)
			}
		})
	}
}

// sinkInt defeats dead-code elimination for integer results in benchmarks.
var sinkInt int

// BenchmarkRealSamples_geometry measures the geometry preprocessing pipeline
// (LocateRedaction → crop → InferBlockSize → segment.Lines) per real sample.
// Image decoding is outside the measured loop; b.SetBytes reports throughput
// in terms of source image pixels (4 bytes each).
func BenchmarkRealSamples_geometry(b *testing.B) {
	for _, s := range loadRealManifest(b) {
		img := openRealPNG(b, s.File)
		bounds := img.Bounds()
		pixels := int64(bounds.Dx()) * int64(bounds.Dy())
		b.Run(s.Name, func(b *testing.B) {
			b.SetBytes(pixels * 4) // RGBA: 4 bytes per pixel
			b.ReportAllocs()
			for b.Loop() {
				rect, ok := unpixel.LocateRedaction(img)
				if !ok {
					b.Fatalf("%s: LocateRedaction returned ok=false", s.Name)
				}
				crop := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
				draw.Draw(crop, crop.Bounds(), img, rect.Min, draw.Src)
				sinkInt = unpixel.InferBlockSize(crop)
				sinkInt = len(segment.Lines(img))
			}
		})
	}
}
