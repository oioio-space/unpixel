package unpixel_test

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire default components
)

// TestLargeblockDecode is a regression guard for blind recovery at large mosaic
// block sizes (20–32 px). Each testdata/largeblock fixture is a short word redacted
// at a coarse block; with the fixture's own config (charset + font size + block) the
// engine recovers it exactly. It documents that a large block alone does not defeat
// recovery when few, information-rich blocks still span each glyph — the complement
// to the coarse-block information-starvation limit for long strings.
func TestLargeblockDecode(t *testing.T) {
	raw, err := os.ReadFile("testdata/largeblock/manifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var entries []struct {
		Name        string `json:"name"`
		Text        string `json:"text"`
		Charset     string `json:"charset"`
		FontSize    int    `json:"font_size"`
		BlockSize   int    `json:"block_size"`
		PaddingTop  int    `json:"padding_top"`
		PaddingLeft int    `json:"padding_left"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("largeblock manifest is empty")
	}

	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			img := loadPNGFile(t, filepath.Join("testdata/largeblock", e.Name+".png"))
			res, err := unpixel.Recover(t.Context(), img,
				unpixel.WithCharset(e.Charset),
				unpixel.WithBlockSize(e.BlockSize),
				unpixel.WithStyle(unpixel.Style{
					FontSize:    float64(e.FontSize),
					PaddingTop:  e.PaddingTop,
					PaddingLeft: e.PaddingLeft,
				}),
			)
			if err != nil {
				t.Fatalf("Recover: %v", err)
			}
			if res.BestGuess != e.Text {
				t.Errorf("BestGuess = %q (dist %.4f), want %q", res.BestGuess, res.BestTotal, e.Text)
			}
		})
	}
}

// loadPNGFile decodes a PNG fixture for the largeblock decode guard.
func loadPNGFile(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}
