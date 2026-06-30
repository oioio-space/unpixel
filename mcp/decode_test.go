package mcpserver_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestDecode_auto verifies that method=auto on the block08_go fixture
// returns a non-empty text and a non-negative distance.
func TestDecode_auto(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "auto", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(auto): %v", err)
	}
	if got.Text == "" {
		t.Error("Decode(auto): Text is empty")
	}
	if got.Distance < 0 {
		t.Errorf("Decode(auto): Distance = %.4f, want >= 0", got.Distance)
	}
	if got.MethodUsed == "" {
		t.Error("Decode(auto): MethodUsed is empty")
	}
}

// TestDecode_mosaic verifies that method=mosaic recovers "go" from block08_go.png.
func TestDecode_mosaic(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "mosaic", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(mosaic): %v", err)
	}
	if got.Text != "go" {
		t.Errorf("Decode(mosaic): Text = %q, want %q", got.Text, "go")
	}
	if got.MethodUsed != "mosaic" {
		t.Errorf("Decode(mosaic): MethodUsed = %q, want %q", got.MethodUsed, "mosaic")
	}
	if got.Distance < 0 {
		t.Errorf("Decode(mosaic): Distance = %.4f, want >= 0", got.Distance)
	}
}

// TestDecode_unknownMethod verifies that an unknown method returns an error.
func TestDecode_unknownMethod(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	_, err = mcpserver.Decode(ctx, img, "nonexistent-method", mcpserver.DecodeOptions{})
	if err == nil {
		t.Error("Decode(nonexistent-method): want error, got nil")
	}
}

// TestDecode_fidelity verifies that Fidelity = 1 - Distance for a mosaic decode.
func TestDecode_fidelity(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "mosaic", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(mosaic): %v", err)
	}
	want := 1 - got.Distance
	if want < 0 {
		want = 0
	}
	const tol = 1e-9
	if got.Fidelity < want-tol || got.Fidelity > want+tol {
		t.Errorf("Fidelity = %.6f, want 1-Distance = %.6f", got.Fidelity, want)
	}
}

// TestDecode_multiFrameAuto_fourFrames verifies that the multi-frame auto path
// (all offsets zero → DecodeMultiFrameAuto) completes without error on the
// 4-frame hello_b8 fixture set and returns a non-nil result with a finite
// distance. Exact text is not asserted — blind calibration does not guarantee
// a decode on every run.
func TestDecode_multiFrameAuto_fourFrames(t *testing.T) {
	const multiframeDir = "../testdata/multiframe"
	frameFiles := []string{
		"hello_b8_f0.png",
		"hello_b8_f1.png",
		"hello_b8_f2.png",
		"hello_b8_f3.png",
	}
	for _, f := range frameFiles {
		if _, err := os.Stat(filepath.Join(multiframeDir, f)); err != nil {
			t.Skipf("multiframe fixture %s not found — skipping", f)
		}
	}

	primary, err := loadImageFile(filepath.Join(multiframeDir, frameFiles[0]))
	if err != nil {
		t.Fatalf("load primary frame: %v", err)
	}

	frames := make([]mcpserver.FrameInput, len(frameFiles)-1)
	for i, f := range frameFiles[1:] {
		frames[i] = mcpserver.FrameInput{Path: filepath.Join(multiframeDir, f)}
		// OffsetX/OffsetY left zero → auto-detection path.
	}

	ctx := t.Context()
	got, err := mcpserver.Decode(ctx, primary, "multi-frame", mcpserver.DecodeOptions{
		Frames: frames,
	})
	if err != nil {
		t.Fatalf("Decode(multi-frame, auto, 4 frames): %v", err)
	}
	if got.MethodUsed != "multi-frame" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "multi-frame")
	}
	if math.IsInf(got.Distance, 0) || math.IsNaN(got.Distance) {
		t.Errorf("Distance = %v, want finite", got.Distance)
	}
	if got.Distance < 0 {
		t.Errorf("Distance = %.4f, want >= 0", got.Distance)
	}
	t.Logf("multi-frame auto: text=%q dist=%.4f fidelity=%.4f", got.Text, got.Distance, got.Fidelity)
}

// TestDecode_multiFrameAuto_singleFrame verifies that a single-frame
// multi-frame call (all offsets zero, no extra frames) returns the same
// MethodUsed as other decoders and a non-negative distance — the single-frame
// fast path in DecodeMultiFrameAuto must not panic or error on a real fixture.
func TestDecode_multiFrameAuto_singleFrame(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	ctx := t.Context()
	got, err := mcpserver.Decode(ctx, img, "multi-frame", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(multi-frame, single): %v", err)
	}
	if got.MethodUsed != "multi-frame" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "multi-frame")
	}
	if got.Distance < 0 {
		t.Errorf("Distance = %.4f, want >= 0", got.Distance)
	}
	t.Logf("multi-frame single: text=%q dist=%.4f", got.Text, got.Distance)
}
