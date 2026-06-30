package fixture_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel/internal/fixture"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/varfont"
	vfembed "github.com/oioio-space/unpixel/internal/varfont/embed"
)

// bytesReader wraps a byte slice in a *bytes.Reader, satisfying the
// io.Reader/Seeker/ReaderAt interface that varfont.ParseFont requires.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// toRGBA converts any image.Image to *image.RGBA, returning (img, true) when
// the source is already *image.RGBA, or a freshly drawn copy otherwise.
func toRGBA(img image.Image) (*image.RGBA, bool) {
	if r, ok := img.(*image.RGBA); ok {
		return r, true
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst, true
}

// cropRGBA returns a fresh *image.RGBA containing the sub-rectangle described
// by r, clipped to img's bounds.
func cropRGBA(img *image.RGBA, r fixture.Rect) *image.RGBA {
	b := img.Bounds()
	x0 := max(b.Min.X, b.Min.X+r.X)
	y0 := max(b.Min.Y, b.Min.Y+r.Y)
	x1 := min(b.Max.X, x0+r.W)
	y1 := min(b.Max.Y, y0+r.H)
	dw := max(0, x1-x0)
	dh := max(0, y1-y0)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	if dw == 0 || dh == 0 {
		return dst
	}
	rowBytes := dw * 4
	for row := range dh {
		srcOff := img.PixOffset(x0, y0+row)
		copy(dst.Pix[row*dst.Stride:row*dst.Stride+rowBytes], img.Pix[srcOff:srcOff+rowBytes])
	}
	return dst
}

// manifestDir is the path to the generated context corpus relative to the repo root.
// Tests are run from the package directory (internal/fixture), so we walk up twice.
const manifestDir = "../../testdata/context"

// TestContextMatrix_SelfConsistent checks every spec has unique names, non-zero
// sizes, non-empty visible and secret text, and a valid layout value.
func TestContextMatrix_SelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range fixture.ContextMatrix() {
		if s.Name == "" || seen[s.Name] {
			t.Errorf("empty or duplicate spec name %q", s.Name)
		}
		seen[s.Name] = true

		if s.VisibleText == "" {
			t.Errorf("%s: empty visible_text", s.Name)
		}
		if s.Secret == "" {
			t.Errorf("%s: empty secret", s.Name)
		}
		if s.FontSize <= 0 {
			t.Errorf("%s: non-positive font_size %.0f", s.Name, s.FontSize)
		}
		if s.BlockSize <= 0 {
			t.Errorf("%s: non-positive block_size %d", s.Name, s.BlockSize)
		}
		switch s.Layout {
		case fixture.LayoutSameLine, fixture.LayoutLabelAbove:
			// valid
		default:
			t.Errorf("%s: unknown layout %q", s.Name, s.Layout)
		}
		if s.VarFont && s.VarWght <= 0 {
			t.Errorf("%s: var_font=true but var_wght=%.1f", s.Name, s.VarWght)
		}
	}
}

// TestContextFile_ReturnsPNGName verifies File() appends ".png" to the spec Name.
func TestContextFile_ReturnsPNGName(t *testing.T) {
	s := fixture.ContextSpec{Name: "ctx_sameline_user"}
	if got, want := s.File(), "ctx_sameline_user.png"; got != want {
		t.Errorf("File() = %q, want %q", got, want)
	}
}

// TestContextCorpus_ManifestAndPNGs loads the committed manifest and verifies:
//  1. The manifest parses cleanly.
//  2. Every PNG exists on disk.
//  3. Every PNG has non-zero dimensions.
//  4. Every visible_rect and redacted_rect is within the PNG bounds.
//
// This test is the primary CI guard that the committed corpus stays in sync with
// the generator.
func TestContextCorpus_ManifestAndPNGs(t *testing.T) {
	manifestPath := filepath.Join(manifestDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest %s: %v — run: go run ./internal/fixture/gencontext -out testdata/context", manifestPath, err)
	}

	var specs []fixture.ContextSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("manifest is empty")
	}
	t.Logf("manifest: %d specs", len(specs))

	for _, s := range specs {
		t.Run(s.Name, func(t *testing.T) {
			pngPath := filepath.Join(manifestDir, s.File())
			f, err := os.Open(pngPath) // #nosec G304 — test reads committed testdata
			if err != nil {
				t.Fatalf("open PNG %s: %v", pngPath, err)
			}
			t.Cleanup(func() { _ = f.Close() })

			img, err := png.Decode(f)
			if err != nil {
				t.Fatalf("decode PNG %s: %v", pngPath, err)
			}

			b := img.Bounds()
			if b.Dx() == 0 || b.Dy() == 0 {
				t.Fatalf("PNG %s has zero dimensions %v", pngPath, b)
			}

			// visible_rect must be within bounds.
			vr := s.VisibleRect
			if vr.X < 0 || vr.Y < 0 || vr.X+vr.W > b.Dx() || vr.Y+vr.H > b.Dy() {
				t.Errorf("visible_rect %+v out of PNG bounds %v", vr, b)
			}
			if vr.W == 0 || vr.H == 0 {
				t.Errorf("visible_rect has zero dimension: %+v", vr)
			}

			// redacted_rect must be within bounds.
			rr := s.RedactedRect
			if rr.X < 0 || rr.Y < 0 || rr.X+rr.W > b.Dx() || rr.Y+rr.H > b.Dy() {
				t.Errorf("redacted_rect %+v out of PNG bounds %v", rr, b)
			}
			if rr.W == 0 || rr.H == 0 {
				t.Errorf("redacted_rect has zero dimension: %+v", rr)
			}

			t.Logf("PNG %dx%d | visible_rect=%+v | redacted_rect=%+v",
				b.Dx(), b.Dy(), vr, rr)
		})
	}
}

// TestContextCorpus_CrossImageCalibration proves C1b end-to-end:
//
//  1. Load the separate font-sample PNG (fontsample_wght700.png).
//  2. Call varfont.CalibrateFromVisible with the sample text from that image.
//  3. Use the fitted axes as a warm-start to fit the SEPARATE redaction image
//     (ctx_crossimg_wght700.png) with the known secret text.
//  4. Assert the final distance is low (< 0.5), proving that calibrating from
//     a separate image successfully drives recovery of the redaction.
//
// The test is skipped (not failed) when the corpus has not been generated yet.
func TestContextCorpus_CrossImageCalibration(t *testing.T) {
	manifestPath := filepath.Join(manifestDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Skipf("manifest not found (%v) — run: go run ./internal/fixture/gencontext -out testdata/context", err)
	}

	var specs []fixture.ContextSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	// Find the C1b cross-image fixture.
	var crossSpec *fixture.ContextSpec
	for i := range specs {
		if specs[i].FontSample != nil {
			crossSpec = &specs[i]
			break
		}
	}
	if crossSpec == nil {
		t.Skip("no cross-image (font_sample) fixture in manifest — regenerate corpus")
		return // unreachable after Skip; satisfies staticcheck SA5011
	}
	t.Logf("cross-image fixture %q (wght=%.0f, sample=%q, secret=%q)",
		crossSpec.Name, crossSpec.VarWght, crossSpec.FontSample.SampleText, crossSpec.Secret)

	// Step 1: load the separate font-sample PNG.
	samplePath := filepath.Join(manifestDir, crossSpec.FontSample.File())
	sf, err := os.Open(samplePath) // #nosec G304 — test reads committed testdata
	if err != nil {
		t.Fatalf("open font-sample PNG %s: %v — regenerate corpus", samplePath, err)
	}
	t.Cleanup(func() { _ = sf.Close() })

	sampleImg, err := png.Decode(sf)
	if err != nil {
		t.Fatalf("decode font-sample PNG: %v", err)
	}
	sampleRGBA, _ := toRGBA(sampleImg)

	// Step 2: calibrate axes from the SEPARATE sample image.
	font, err := varfont.ParseFont(bytesReader(vfembed.NunitoVFWght))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}

	m := metric.NewPixelmatchFast(0.1)
	style := varfont.DefaultStyle()
	style.FontSize = crossSpec.FontSize

	axisSpecs := []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: 400}}

	calResult, err := varfont.CalibrateFromVisible(varfont.CalibrateConfig{
		Font:      font,
		Text:      crossSpec.FontSample.SampleText,
		Style:     style,
		Target:    sampleRGBA,
		Pixelator: nil, // sharp sample image — compare directly
		Metric:    m,
		Axes:      axisSpecs,
	})
	if err != nil {
		t.Fatalf("CalibrateFromVisible (separate sample): %v", err)
	}
	fittedWght := calResult.Axes[0].Value
	t.Logf("calibrated from separate sample: wght=%.1f (true %.1f) dist=%.4f evals=%d",
		fittedWght, crossSpec.VarWght, calResult.Distance, calResult.Evals)

	// Step 3: load the SEPARATE redaction image and fit with the calibrated axes.
	redactPath := filepath.Join(manifestDir, crossSpec.File())
	rf, err := os.Open(redactPath) // #nosec G304 — test reads committed testdata
	if err != nil {
		t.Fatalf("open redaction PNG %s: %v — regenerate corpus", redactPath, err)
	}
	t.Cleanup(func() { _ = rf.Close() })

	redactImg, err := png.Decode(rf)
	if err != nil {
		t.Fatalf("decode redaction PNG: %v", err)
	}
	redactRGBA, _ := toRGBA(redactImg)

	// Crop the mosaic region from the redaction image.
	redactCrop := cropRGBA(redactRGBA, crossSpec.RedactedRect)
	if redactCrop.Bounds().Empty() {
		t.Fatal("redacted_rect produces empty crop — regenerate corpus")
	}

	pix := pixelate.NewLinearBlockAverage(crossSpec.BlockSize)

	// Warm-start from the calibrated wght value.
	warmAxes := []varfont.AxisSpec{{Tag: "wght", Min: 200, Max: 900, Start: fittedWght}}
	fitResult, err := varfont.FitAxes(varfont.FitConfig{
		Font:      font,
		Text:      crossSpec.Secret,
		Style:     style,
		Target:    redactCrop,
		Pixelator: pix,
		Metric:    m,
		BlockSize: crossSpec.BlockSize,
		Axes:      warmAxes,
	})
	if err != nil {
		t.Fatalf("FitAxes on redaction: %v", err)
	}
	t.Logf("fit on redaction: wght=%.1f (true %.1f) dist=%.4f evals=%d",
		fitResult.Axes[0].Value, crossSpec.VarWght, fitResult.Distance, fitResult.Evals)

	// Step 4: assert the cross-image calibration achieved a meaningful fit.
	if got := fitResult.Distance; math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("distance: got %v, want finite value", got)
	}
	if got, want := fitResult.Distance, 0.5; got > want {
		t.Errorf("cross-image fit distance: got %.4f, want <= %.4f — calibration from separate image did not converge",
			got, want)
	}
}

// TestContextCorpus_CalibrateFromVisible exercises C1 end-to-end on the first
// Nunito variable-font fixture:
//
//  1. Load the PNG and manifest.
//  2. Crop the visible_rect to get the sharp known-text image.
//  3. Call varfont.CalibrateFromVisible with the known visible_text.
//  4. Assert the returned calibration distance is finite and less than 0.5
//     (a loose bound; the exact value is logged honestly).
//
// If the corpus has not been generated yet, the test is skipped (not failed),
// so CI does not break when testdata/context/ is absent.
func TestContextCorpus_CalibrateFromVisible(t *testing.T) {
	manifestPath := filepath.Join(manifestDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Skipf("manifest not found (%v) — run: go run ./internal/fixture/gencontext -out testdata/context", err)
	}

	var specs []fixture.ContextSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	// Find the first variable-font fixture for C2 calibration.
	var vfSpec *fixture.ContextSpec
	for i := range specs {
		if specs[i].VarFont {
			vfSpec = &specs[i]
			break
		}
	}
	if vfSpec == nil {
		t.Skip("no var_font fixture in manifest")
		return // unreachable after Skip; satisfies staticcheck SA5011
	}
	t.Logf("calibrating on fixture %q (wght=%.0f, visible=%q)", vfSpec.Name, vfSpec.VarWght, vfSpec.VisibleText)

	// Load the PNG.
	pngPath := filepath.Join(manifestDir, vfSpec.File())
	f, err := os.Open(pngPath) // #nosec G304 — test reads committed testdata
	if err != nil {
		t.Fatalf("open PNG: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	fullImg, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	full, ok := toRGBA(fullImg)
	if !ok {
		t.Fatal("PNG is not *image.RGBA-compatible")
	}

	// Crop the visible_rect.
	vr := vfSpec.VisibleRect
	visibleCrop := cropRGBA(full, vr)

	// Parse the Nunito variable font.
	font, err := varfont.ParseFont(bytesReader(vfembed.NunitoVFWght))
	if err != nil {
		t.Fatalf("ParseFont: %v", err)
	}

	m := metric.NewPixelmatchFast(0.1)
	pix := pixelate.NewLinearBlockAverage(vfSpec.BlockSize)
	_ = pix // available for future pixelated-visible tests

	style := varfont.DefaultStyle()
	style.FontSize = vfSpec.FontSize

	// Calibrate from the sharp visible crop (Pixelator=nil → sharp comparison).
	calResult, err := varfont.CalibrateFromVisible(varfont.CalibrateConfig{
		Font:      font,
		Text:      vfSpec.VisibleText,
		Style:     style,
		Target:    visibleCrop,
		Pixelator: nil, // sharp visible text
		Metric:    m,
		Axes: []varfont.AxisSpec{
			{Tag: "wght", Min: 200, Max: 900, Start: 400},
		},
	})
	if err != nil {
		t.Fatalf("CalibrateFromVisible: %v", err)
	}

	t.Logf("CalibrateFromVisible: wght=%.1f (true %.1f) distance=%.4f evals=%d",
		calResult.Axes[0].Value, vfSpec.VarWght, calResult.Distance, calResult.Evals)

	// got before want.
	if got := calResult.Distance; math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("distance: got %v, want finite value", got)
	}
	// Loose bound: calibration should resolve to a meaningful distance on sharp text.
	if got, want := calResult.Distance, 0.5; got > want {
		t.Errorf("distance: got %.4f, want <= %.4f — calibration did not converge", got, want)
	}
}
