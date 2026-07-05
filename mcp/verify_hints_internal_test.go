package mcpserver

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/fonts"
)

func TestBundledFontData(t *testing.T) {
	want := fonts.All()[0].Name // a name guaranteed to exist
	tests := []struct {
		name  string
		query string
		ok    bool
	}{
		{"exact", want, true},
		{"case-insensitive", "noto sans mono", true},
		{"trimmed", "  Noto Sans Mono  ", true},
		{"unknown", "Comic Sans MS", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, ok := bundledFontData(tc.query)
			if ok != tc.ok {
				t.Fatalf("bundledFontData(%q) ok=%v, want %v", tc.query, ok, tc.ok)
			}
			if ok && len(data) == 0 {
				t.Errorf("bundledFontData(%q) returned empty data with ok=true", tc.query)
			}
		})
	}
}

func TestCropRect(t *testing.T) {
	tests := []struct {
		name    string
		in      []int
		want    image.Rectangle
		wantErr bool
	}{
		{"nil", nil, image.Rectangle{}, false},
		{"empty", []int{}, image.Rectangle{}, false},
		{"valid corner bbox", []int{10, 20, 40, 60}, image.Rect(10, 20, 40, 60), false},
		{"wrong-length", []int{1, 2, 3}, image.Rectangle{}, true},
		{"zero-width", []int{5, 5, 5, 10}, image.Rectangle{}, true},
		{"zero-height", []int{5, 5, 10, 5}, image.Rectangle{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cropRect(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("cropRect(%v) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("cropRect(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCropForVerify(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 200, 100))

	t.Run("empty crop returns image unchanged", func(t *testing.T) {
		out, err := cropForVerify(src, image.Rectangle{})
		if err != nil {
			t.Fatalf("cropForVerify: %v", err)
		}
		if out.Bounds() != src.Bounds() {
			t.Errorf("bounds = %v, want %v", out.Bounds(), src.Bounds())
		}
	})

	t.Run("crop adds white margin", func(t *testing.T) {
		crop := image.Rect(10, 10, 60, 40) // 50×30
		out, err := cropForVerify(src, crop)
		if err != nil {
			t.Fatalf("cropForVerify: %v", err)
		}
		want := image.Rect(0, 0, crop.Dx()+verifyCropMargin.X, crop.Dy()+verifyCropMargin.Y)
		if out.Bounds() != want {
			t.Errorf("bounds = %v, want %v", out.Bounds(), want)
		}
	})

	t.Run("non-intersecting crop errors", func(t *testing.T) {
		if _, err := cropForVerify(src, image.Rect(500, 500, 520, 520)); err == nil {
			t.Error("cropForVerify with out-of-bounds crop: want error, got nil")
		}
	})
}

func TestHintOptions(t *testing.T) {
	t.Run("unknown bundled font errors", func(t *testing.T) {
		if _, err := hintOptions(VerifyHints{Font: "No Such Font"}); err == nil {
			t.Error("hintOptions with unknown font: want error, got nil")
		}
	})

	t.Run("zero hints yield no options", func(t *testing.T) {
		opts, err := hintOptions(VerifyHints{})
		if err != nil {
			t.Fatalf("hintOptions: %v", err)
		}
		if len(opts) != 0 {
			t.Errorf("len(opts) = %d, want 0 for zero hints", len(opts))
		}
	})

	t.Run("bundled font builds an option", func(t *testing.T) {
		opts, err := hintOptions(VerifyHints{Font: "Noto Sans Mono", BlockSize: 32, LinearLight: true, FontSize: 100, XScale: 1.05})
		if err != nil {
			t.Fatalf("hintOptions: %v", err)
		}
		// renderer + block + style + pixelator = 4 options.
		if len(opts) != 4 {
			t.Errorf("len(opts) = %d, want 4 (renderer, block, style, pixelator)", len(opts))
		}
	})

	t.Run("linear-light without block size is dropped", func(t *testing.T) {
		opts, err := hintOptions(VerifyHints{LinearLight: true})
		if err != nil {
			t.Fatalf("hintOptions: %v", err)
		}
		if len(opts) != 0 {
			t.Errorf("len(opts) = %d, want 0 (linear-light needs block_size)", len(opts))
		}
	})
}
